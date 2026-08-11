package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"batchscope/internal/observability"
	"batchscope/internal/store"
	"batchscope/internal/target"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

const (
	apiTitle   = "BatchScope API"
	apiVersion = "v1"
)

type Config struct {
	Version string
	Commit  string
	DataDir string
}

type App struct {
	config  Config
	bootID  string
	started time.Time
	store   *store.Store
	logger  *slog.Logger
	handler http.Handler
	imports *importRegistry
	workMu  sync.Mutex
	work    sync.WaitGroup
	closing bool
}

type HealthResponse struct {
	Status string `json:"status"`
}

type ReadyResponse struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type StatusResponse struct {
	State     string    `json:"state"`
	BootID    string    `json:"bootId"`
	StartedAt time.Time `json:"startedAt"`
	// Snapshotは検索に使用中の世代を表し、取込中も切替前の世代を維持する。
	Snapshot *SnapshotInfo `json:"snapshot"`
	Build    BuildInfo     `json:"build"`
}

type SnapshotInfo struct {
	// Humaへオブジェクト全体がnullになり得ることを伝え、未取込時のStatusResponseとSchemaを一致させる。
	_             struct{}  `nullable:"true"`
	SnapshotID    string    `json:"snapshotId"`
	GeneratedAt   time.Time `json:"generatedAt"`
	SchemaVersion string    `json:"schemaVersion"`
	NodeCount     int       `json:"nodeCount"`
	RelationCount int       `json:"relationCount"`
	LimitCount    int       `json:"limitCount"`
}

type BuildInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

type healthOutput struct {
	Body HealthResponse
}

type readyOutput struct {
	Status int
	Body   ReadyResponse
}

type statusOutput struct {
	Body StatusResponse
}

type targetsInput struct {
	Query         string   `query:"query" doc:"Exact ID, name, or full path to search for"`
	Types         []string `query:"type,explode" doc:"Target type filter; may be repeated" enum:"job,job_network"`
	RequestID     string   `header:"X-Request-Id" doc:"Request identifier used in structured logs"`
	queryProvided bool
}

// Resolveは、Humaのパラメーター束縛では区別できないqueryの未指定と指定された空文字を区別する。
func (input *targetsInput) Resolve(ctx huma.Context) []error {
	requestURL := ctx.URL()
	input.queryProvided = requestURL.Query().Has("query")
	return nil
}

type targetsResponse struct {
	SnapshotID string              `json:"snapshotId"`
	Items      []target.SearchItem `json:"items" nullable:"false"`
	Truncated  bool                `json:"truncated"`
}

type targetsOutput struct {
	Body targetsResponse
}

func New(config Config) (*App, error) {
	storage, err := store.New(config.DataDir)
	if err != nil {
		return nil, fmt.Errorf("open SQLite store: %w", err)
	}
	a, err := NewWithStore(config, storage)
	if err != nil {
		_ = storage.Close()
		return nil, err
	}
	return a, nil
}

// NewWithStoreは、準備済みのStoreを使って製品と同じHTTPルートを組み立てる。
// 返したAppがStoreを所有するため、呼出側はApp.Closeで両方を閉じる。
func NewWithStore(config Config, storage *store.Store) (*App, error) {
	if storage == nil {
		return nil, errors.New("store is nil")
	}
	bootID, err := newBootID()
	if err != nil {
		return nil, fmt.Errorf("generate boot ID: %w", err)
	}

	a := &App{
		config:  config,
		bootID:  bootID,
		started: time.Now().UTC(),
		store:   storage,
		logger:  slog.Default(),
		imports: newImportRegistry(),
	}

	mux := http.NewServeMux()
	buildAPI(mux, a)
	a.handler = mux
	return a, nil
}

// OpenAPISpecは、サーバーと同じConfigおよびルート登録からOpenAPIを組み立てる。
// 返す内容はApp設定に依存せず、常に同じになる。
func OpenAPISpec() *huma.OpenAPI {
	mux := http.NewServeMux()
	api := buildAPI(mux, &App{})
	return api.OpenAPI()
}

func (a *App) Handler() http.Handler {
	return a.handler
}

// BootIDは、プロセス寿命をまたぐログと応答を対応付ける識別子を返す。
func (a *App) BootID() string {
	return a.bootID
}

// Closeは非同期取込の終了を待ってから、Appが所有するSQLiteストアを閉じる。
func (a *App) Close() error {
	a.workMu.Lock()
	a.closing = true
	a.workMu.Unlock()
	a.work.Wait()
	return a.store.Close()
}

func buildAPI(mux *http.ServeMux, a *App) huma.API {
	api := humago.New(mux, humaConfig())
	registerRoutes(api, a)
	return api
}

func humaConfig() huma.Config {
	return huma.Config{
		OpenAPI: &huma.OpenAPI{
			OpenAPI: "3.1.0",
			Info: &huma.Info{
				Title:   apiTitle,
				Version: apiVersion,
			},
			Components: &huma.Components{
				Schemas: huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer),
			},
		},
		OpenAPIPath:  "/openapi",
		DocsPath:     "/docs",
		DocsRenderer: huma.DocsRendererStoplightElements,
		SchemasPath:  "",
		Formats: map[string]huma.Format{
			"application/json": huma.DefaultJSONFormat,
			"json":             huma.DefaultJSONFormat,
		},
		DefaultFormat: "application/json",
	}
}

func registerRoutes(api huma.API, a *App) {
	huma.Register(api, huma.Operation{
		OperationID: "health",
		Method:      http.MethodGet,
		Path:        "/healthz",
		Summary:     "Check process health",
	}, a.health)

	huma.Register(api, huma.Operation{
		OperationID:   "readiness",
		Method:        http.MethodGet,
		Path:          "/readyz",
		Summary:       "Check snapshot readiness",
		DefaultStatus: http.StatusServiceUnavailable,
	}, a.ready)
	// Humaは動的なStatusフィールドを実行時には使うが、既定値以外の応答は自動生成しない。
	// 既存データがある場合の200にも、503と同じレスポンス形式を明示する。
	readinessResponses := api.OpenAPI().Paths["/readyz"].Get.Responses
	readinessResponses["200"] = &huma.Response{
		Description: http.StatusText(http.StatusOK),
		Content:     readinessResponses["503"].Content,
	}

	huma.Register(api, huma.Operation{
		OperationID: "status",
		Method:      http.MethodGet,
		Path:        "/v1/status",
		Summary:     "Get service status",
	}, a.status)

	huma.Register(api, huma.Operation{
		OperationID: "search-targets",
		Method:      http.MethodGet,
		Path:        "/v1/targets",
		Summary:     "Search jobs and job networks by exact match",
		Errors:      []int{http.StatusBadRequest, http.StatusInternalServerError, http.StatusServiceUnavailable},
		// パラメーター不正はAPI固有のinvalid-requestへ写像するため、Humaの422検査を使わない。
		SkipValidateParams: true,
	}, a.targets)
	for _, parameter := range api.OpenAPI().Paths["/v1/targets"].Get.Parameters {
		if parameter.Name == "query" {
			parameter.Required = true
		}
	}
	delete(api.OpenAPI().Paths["/v1/targets"].Get.Responses, "422")

	registerAnalysisRoute(api, a)
	registerSnapshotRoutes(api, a)
}

func (a *App) health(context.Context, *struct{}) (*healthOutput, error) {
	return &healthOutput{
		Body: HealthResponse{Status: "ok"},
	}, nil
}

func (a *App) ready(context.Context, *struct{}) (*readyOutput, error) {
	if a.store.Ready() {
		return &readyOutput{
			Status: http.StatusOK,
			Body: ReadyResponse{
				Status: "ready",
				Reason: "snapshot_loaded",
			},
		}, nil
	}
	return &readyOutput{
		Status: http.StatusServiceUnavailable,
		Body: ReadyResponse{
			Status: "not_ready",
			Reason: "snapshot_not_loaded",
		},
	}, nil
}

func (a *App) status(context.Context, *struct{}) (*statusOutput, error) {
	return &statusOutput{
		Body: StatusResponse{
			State:     string(a.store.State()),
			BootID:    a.bootID,
			StartedAt: a.started,
			Snapshot:  a.currentSnapshotInfo(),
			Build: BuildInfo{
				Version: a.config.Version,
				Commit:  a.config.Commit,
			},
		},
	}, nil
}

func (a *App) currentSnapshotInfo() *SnapshotInfo {
	generation, ok := a.store.CurrentGeneration()
	if !ok {
		return nil
	}
	return &SnapshotInfo{
		SnapshotID: generation.SnapshotID, GeneratedAt: generation.GeneratedAt,
		SchemaVersion: generation.SchemaVersion, NodeCount: generation.NodeCount,
		RelationCount: generation.RelationCount, LimitCount: generation.LimitCount,
	}
}

func (a *App) beginImportWork() bool {
	a.workMu.Lock()
	defer a.workMu.Unlock()
	if a.closing {
		return false
	}
	a.work.Add(1)
	return true
}

func (a *App) targets(ctx context.Context, input *targetsInput) (*targetsOutput, error) {
	started := time.Now()
	requestID := input.RequestID
	var snapshotID, errorType string
	returnedTargets := 0
	defer func() {
		attrs := observability.Attrs(observability.Fields{
			RequestID: requestID, Operation: "target_search", Duration: time.Since(started),
			BootID: a.bootID, SnapshotID: snapshotID, ReturnedTargets: returnedTargets,
			ErrorType: errorType,
		})
		a.log().LogAttrs(ctx, slog.LevelInfo, "target search completed", attrs...)
	}()
	if requestID == "" {
		var err error
		requestID, err = newBootID()
		if err != nil {
			errorType = "internal-error"
			return nil, InternalErrorProblem("failed to generate request ID")
		}
	}

	if !input.queryProvided {
		errorType = "invalid-request"
		return nil, InvalidRequestProblem("query is required")
	}

	types, problem := targetTypes(input.Types)
	if problem != nil {
		errorType = "invalid-request"
		return nil, problem
	}

	db, generation, release, err := a.store.Acquire()
	if errors.Is(err, store.ErrNoDatabase) {
		errorType = "snapshot-not-loaded"
		return nil, SnapshotNotLoadedProblem("searchable snapshot is not loaded")
	}
	if err != nil {
		errorType = "internal-error"
		return nil, InternalErrorProblem("failed to acquire search snapshot")
	}
	defer release()
	snapshotID = generation.SnapshotID

	result, err := target.Search(ctx, db, input.Query, types)
	if err != nil {
		errorType = "internal-error"
		return nil, InternalErrorProblem("target search failed")
	}
	returnedTargets = len(result.Items)
	return &targetsOutput{Body: targetsResponse{
		SnapshotID: snapshotID,
		Items:      result.Items,
		Truncated:  result.Truncated,
	}}, nil
}

func targetTypes(requested []string) ([]string, *huma.ErrorModel) {
	if len(requested) == 0 {
		return []string{"job", "job_network"}, nil
	}

	seen := make(map[string]bool, len(requested))
	types := make([]string, 0, len(requested))
	for _, nodeType := range requested {
		if nodeType != "job" && nodeType != "job_network" {
			return nil, InvalidRequestProblem("type must be job or job_network")
		}
		if !seen[nodeType] {
			seen[nodeType] = true
			types = append(types, nodeType)
		}
	}
	return types, nil
}

func (a *App) log() *slog.Logger {
	if a.logger != nil {
		return a.logger
	}
	return slog.Default()
}

func newBootID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}
