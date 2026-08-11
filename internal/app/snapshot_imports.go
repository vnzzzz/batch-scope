package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"batchscope/internal/importer"
	"batchscope/internal/limits"
	"batchscope/internal/observability"
	"batchscope/internal/snapshot"
	"batchscope/internal/store"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

const (
	snapshotMediaType       = "application/vnd.batchscope.snapshot+gzip"
	snapshotImportOperation = "snapshot_import"
)

// processReceivedは、HTTP層と非同期取込の境界をテストで停止できる注入点である。
var processReceived = importer.ProcessReceived

type importStatusInput struct {
	ImportID string `path:"importId" doc:"Snapshot import identifier"`
}

type importStatusOutput struct {
	Body importResource
}

type currentSnapshotOutput struct {
	Body SnapshotInfo
}

func registerSnapshotRoutes(api huma.API, a *App) {
	registerSnapshotImportRoute(api, a)

	huma.Register(api, huma.Operation{
		OperationID: "get-snapshot-import",
		Method:      http.MethodGet,
		Path:        "/v1/snapshot-imports/{importId}",
		Summary:     "Get snapshot import status",
		Errors:      []int{http.StatusNotFound, http.StatusInternalServerError},
	}, a.snapshotImportStatus)

	huma.Register(api, huma.Operation{
		OperationID: "get-current-snapshot",
		Method:      http.MethodGet,
		Path:        "/v1/snapshots/current",
		Summary:     "Get the current searchable snapshot",
		Errors:      []int{http.StatusServiceUnavailable},
	}, a.currentSnapshot)
}

func registerSnapshotImportRoute(api huma.API, a *App) {
	registry := api.OpenAPI().Components.Schemas
	problemSchema := registry.Schema(reflect.TypeFor[huma.ErrorModel](), true, "")
	operation := &huma.Operation{
		OperationID: "create-snapshot-import",
		Method:      http.MethodPost,
		Path:        "/v1/snapshot-imports",
		Summary:     "Upload a snapshot and start an asynchronous import",
		RequestBody: &huma.RequestBody{
			Required: true,
			Content: map[string]*huma.MediaType{
				snapshotMediaType: {Schema: &huma.Schema{Type: huma.TypeString, Format: "binary"}},
			},
		},
		Parameters: []*huma.Param{{
			Name: "X-Request-Id", In: "header", Description: "Request identifier used in structured logs",
			Schema: &huma.Schema{Type: huma.TypeString},
		}},
		Responses: map[string]*huma.Response{
			"202": {
				Description: http.StatusText(http.StatusAccepted),
				Headers: map[string]*huma.Param{
					"Location":    {Schema: &huma.Schema{Type: huma.TypeString}},
					"Retry-After": {Schema: &huma.Schema{Type: huma.TypeString}},
				},
			},
		},
	}
	for _, status := range []int{
		http.StatusBadRequest, http.StatusConflict, http.StatusRequestEntityTooLarge,
		http.StatusInternalServerError,
	} {
		operation.Responses[httpStatusKey(status)] = &huma.Response{
			Description: http.StatusText(status),
			Content: map[string]*huma.MediaType{
				"application/problem+json": {Schema: &huma.Schema{Ref: problemSchema.Ref}},
			},
		}
	}
	api.OpenAPI().AddOperation(operation)
	api.Adapter().Handle(operation, func(ctx huma.Context) {
		a.receiveSnapshot(api, ctx)
	})
}

func (a *App) receiveSnapshot(api huma.API, ctx huma.Context) {
	started := time.Now()
	mediaType, _, err := mime.ParseMediaType(ctx.Header("Content-Type"))
	if err != nil || mediaType != snapshotMediaType {
		writeProblem(api, ctx, problemFromModel(InvalidRequestProblem("Content-Type must be "+snapshotMediaType)))
		return
	}
	if !a.beginImportWork() {
		writeProblem(api, ctx, problemFromModel(InternalErrorProblem("application is shutting down")))
		return
	}
	workOwned := true
	defer func() {
		if workOwned {
			a.work.Done()
		}
	}()

	requestID := ctx.Header("X-Request-Id")
	if requestID == "" {
		requestID, err = newBootID()
		if err != nil {
			writeProblem(api, ctx, problemFromModel(InternalErrorProblem("failed to generate request ID")))
			return
		}
	}
	importID, err := newImportID()
	if err != nil {
		writeProblem(api, ctx, problemFromModel(InternalErrorProblem("failed to generate import ID")))
		return
	}

	operation, err := a.store.BeginImport(ctx.Context())
	if err != nil {
		writeProblem(api, ctx, mappedImportProblem(err))
		return
	}
	request, _ := humago.Unwrap(ctx)
	if request.ContentLength > limits.MaxCompressedArchiveBytes {
		_ = operation.Abort()
		writeProblem(api, ctx, problemFromModel(SnapshotTooLargeProblem("snapshot exceeds the supported size")))
		return
	}
	body := &requestBodyTracker{reader: ctx.BodyReader()}
	archivePath, receiveErr := snapshot.Receive(ctx.Context(), a.config.DataDir, body)
	if receiveErr != nil {
		receiveErr = errors.Join(receiveErr, operation.Abort())
		writeProblem(api, ctx, mapReceiveProblem(receiveErr, body, ctx.Context()))
		return
	}

	acceptedAt := time.Now().UTC()
	a.imports.accept(importID, acceptedAt)
	go func() {
		defer a.work.Done()
		a.runSnapshotImport(requestID, importID, archivePath, operation, started)
	}()
	workOwned = false

	ctx.SetHeader("Location", "/v1/snapshot-imports/"+importID)
	ctx.SetHeader("Retry-After", "2")
	ctx.SetStatus(http.StatusAccepted)
}

func (a *App) runSnapshotImport(
	requestID string,
	importID string,
	archivePath string,
	operation *store.Import,
	started time.Time,
) {
	ctx := context.Background()
	result, err := processReceived(
		ctx, a.config.DataDir, archivePath, operation, a.store,
		func(progress importer.Progress) {
			a.imports.progress(importID, progress, time.Now().UTC())
		},
	)
	finalState := importStateSucceeded
	problem := problemDetails{}
	if err != nil {
		finalState = importStateFailed
		problem = mappedImportProblem(err)
		a.imports.fail(importID, result.SnapshotID, problem, time.Now().UTC())
	} else {
		a.imports.succeed(importID, result.SnapshotID, time.Now().UTC())
	}

	attrs := observability.Attrs(observability.Fields{
		RequestID: requestID, Operation: snapshotImportOperation, Duration: time.Since(started),
		BootID: a.bootID, ImportID: importID, SnapshotID: result.SnapshotID,
		NodeCount: result.NodeCount, RelationCount: result.RelationCount, LimitCount: result.LimitCount,
		ImportState: string(finalState), ErrorType: strings.TrimPrefix(problem.Type, "/problems/"),
	})
	a.log().LogAttrs(ctx, slog.LevelInfo, "snapshot import completed", attrs...)
}

func (a *App) snapshotImportStatus(_ context.Context, input *importStatusInput) (*importStatusOutput, error) {
	resource, ok := a.imports.get(input.ImportID)
	if !ok {
		return nil, ImportNotFoundProblem("snapshot import was not found")
	}
	return &importStatusOutput{Body: resource}, nil
}

func (a *App) currentSnapshot(context.Context, *struct{}) (*currentSnapshotOutput, error) {
	snapshotInfo := a.currentSnapshotInfo()
	if snapshotInfo == nil {
		return nil, SnapshotNotLoadedProblem("searchable snapshot is not loaded")
	}
	return &currentSnapshotOutput{Body: *snapshotInfo}, nil
}

type requestBodyTracker struct {
	reader  io.Reader
	readErr error
}

func (reader *requestBodyTracker) Read(buffer []byte) (int, error) {
	n, err := reader.reader.Read(buffer)
	if err != nil && !errors.Is(err, io.EOF) {
		reader.readErr = err
	}
	return n, err
}

func mapReceiveProblem(err error, body *requestBodyTracker, ctx context.Context) problemDetails {
	problem := mappedImportProblem(err)
	var snapshotErr *snapshot.Error
	if errors.As(err, &snapshotErr) && snapshotErr.Kind == snapshot.ErrorIO && (body.readErr != nil || ctx.Err() != nil) {
		return problemFromModel(InvalidRequestProblem("snapshot request body could not be read completely"))
	}
	return problem
}

func newImportID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return "imp_" + hex.EncodeToString(bytes[:]), nil
}

func problemFromModel(model *huma.ErrorModel) problemDetails {
	return problemDetails{
		Type: model.Type, Title: model.Title, Status: model.Status, Detail: model.Detail,
	}
}

func writeProblem(api huma.API, ctx huma.Context, problem problemDetails) {
	writeJSON(api, ctx, problem.Status, "application/problem+json", problem)
}

func writeJSON(api huma.API, ctx huma.Context, status int, contentType string, body any) {
	ctx.SetHeader("Content-Type", contentType)
	ctx.SetStatus(status)
	if body == nil {
		return
	}
	if err := api.Marshal(ctx.BodyWriter(), contentType, body); err != nil {
		slog.Default().Error("write HTTP response", "error", err)
	}
}

func httpStatusKey(status int) string {
	return strconv.Itoa(status)
}
