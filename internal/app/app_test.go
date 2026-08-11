package app

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"batchscope/internal/store"

	"github.com/danielgtaylor/huma/v2"
)

func TestHealth(t *testing.T) {
	a := newTestApp(t)
	recorder := serveRequest(a, "/healthz")

	assertStatus(t, recorder, http.StatusOK)
	assertContentType(t, recorder, "application/json")
	body := decodeObject(t, recorder)
	assertKeys(t, body, "status")
	if body["status"] != "ok" {
		t.Fatalf("status = %v, want ok", body["status"])
	}
}

func TestReadyWithoutSnapshot(t *testing.T) {
	a := newTestApp(t)
	recorder := serveRequest(a, "/readyz")

	assertStatus(t, recorder, http.StatusServiceUnavailable)
	assertContentType(t, recorder, "application/json")
	body := decodeObject(t, recorder)
	assertKeys(t, body, "status", "reason")
	if body["status"] != "not_ready" {
		t.Fatalf("status = %v, want not_ready", body["status"])
	}
	if body["reason"] != "snapshot_not_loaded" {
		t.Fatalf("reason = %v, want snapshot_not_loaded", body["reason"])
	}
}

func TestReadyAndStatusFollowStoreLifecycle(t *testing.T) {
	a := newTestApp(t)

	initialImport, err := a.store.BeginImport(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertServiceState(t, a, "importing", http.StatusServiceUnavailable)
	if err := initialImport.Complete(context.Background(), store.Generation{
		SnapshotID: "test", GeneratedAt: time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
		SchemaVersion: "0.5", Fingerprint: "test",
	}); err != nil {
		t.Fatal(err)
	}
	assertServiceState(t, a, "ready", http.StatusOK)

	updateImport, err := a.store.BeginImport(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertServiceState(t, a, "importing", http.StatusOK)
	if err := updateImport.Abort(); err != nil {
		t.Fatal(err)
	}
	assertServiceState(t, a, "ready", http.StatusOK)
}

func TestStatus(t *testing.T) {
	a := newTestApp(t)
	recorder := serveRequest(a, "/v1/status")

	assertStatus(t, recorder, http.StatusOK)
	assertContentType(t, recorder, "application/json")
	body := decodeObject(t, recorder)
	assertKeys(t, body, "state", "bootId", "startedAt", "snapshot", "build")

	if body["state"] != "empty" {
		t.Fatalf("state = %v, want empty", body["state"])
	}

	bootID, ok := body["bootId"].(string)
	if !ok {
		t.Fatalf("bootId = %T, want string", body["bootId"])
	}
	bootBytes, err := hex.DecodeString(bootID)
	if err != nil || len(bootID) != 32 || len(bootBytes) != 16 {
		t.Fatalf("bootId = %q, want 32 hexadecimal characters", bootID)
	}

	startedAt, ok := body["startedAt"].(string)
	if !ok {
		t.Fatalf("startedAt = %T, want string", body["startedAt"])
	}
	if _, err := time.Parse(time.RFC3339, startedAt); err != nil {
		t.Fatalf("startedAt = %q, want RFC 3339: %v", startedAt, err)
	}

	snapshot, ok := body["snapshot"]
	if !ok {
		t.Fatal("snapshot is missing")
	}
	if snapshot != nil {
		t.Fatalf("snapshot = %v, want null", snapshot)
	}

	build, ok := body["build"].(map[string]any)
	if !ok {
		t.Fatalf("build = %T, want object", body["build"])
	}
	assertKeys(t, build, "version", "commit")
	if build["version"] != "test" {
		t.Fatalf("build.version = %v, want test", build["version"])
	}
	if build["commit"] != "abc" {
		t.Fatalf("build.commit = %v, want abc", build["commit"])
	}
}

func TestOpenAPISpec(t *testing.T) {
	spec := OpenAPISpec()
	if spec.Info.Title != "BatchScope API" {
		t.Fatalf("info.title = %q, want BatchScope API", spec.Info.Title)
	}
	if spec.Info.Version != "v1" {
		t.Fatalf("info.version = %q, want v1", spec.Info.Version)
	}
	for _, path := range []string{
		"/healthz", "/readyz", "/v1/status", "/v1/targets", "/v1/downstream-limit-analysis",
		"/v1/snapshot-imports/{importId}", "/v1/snapshots/current",
	} {
		item := spec.Paths[path]
		if item == nil || item.Get == nil {
			t.Errorf("GET %s is missing from OpenAPI", path)
		}
	}
	if item := spec.Paths["/v1/snapshot-imports"]; item == nil || item.Post == nil {
		t.Error("POST /v1/snapshot-imports is missing from OpenAPI")
	} else if item.Post.RequestBody == nil || item.Post.RequestBody.Content[snapshotMediaType] == nil {
		t.Errorf("POST /v1/snapshot-imports request body does not expose %s", snapshotMediaType)
	} else {
		for _, status := range []string{"400", "409", "413", "500"} {
			response := item.Post.Responses[status]
			if response == nil {
				t.Errorf("POST /v1/snapshot-imports response %s is missing", status)
				continue
			}
			media := response.Content["application/problem+json"]
			if media == nil || media.Schema == nil || media.Schema.Ref != "#/components/schemas/ErrorModel" {
				t.Errorf("POST /v1/snapshot-imports response %s does not use ErrorModel", status)
			}
		}
	}
	schemas := spec.Components.Schemas.Map()
	if schema := schemas["SnapshotInfo"]; schema == nil || schema.Type != huma.TypeObject || schema.Nullable {
		t.Errorf("SnapshotInfo schema = %#v, want non-null object", schema)
	}
	statusSnapshot := schemas["StatusResponse"].Properties["snapshot"]
	if !schemaAllowsRefOrNull(statusSnapshot, "#/components/schemas/SnapshotInfo") {
		t.Errorf("StatusResponse.snapshot schema = %#v, want SnapshotInfo or null", statusSnapshot)
	}
	currentSnapshot := spec.Paths["/v1/snapshots/current"].Get.Responses["200"].Content["application/json"].Schema
	if currentSnapshot.Ref != "#/components/schemas/SnapshotInfo" || currentSnapshot.Nullable || len(currentSnapshot.AnyOf) != 0 {
		t.Errorf("GET /v1/snapshots/current 200 schema = %#v, want non-null SnapshotInfo reference", currentSnapshot)
	}
	if _, ok := schemas["ProblemDetails"]; ok {
		t.Error("OpenAPI unexpectedly contains ProblemDetails")
	}
	targets := spec.Paths["/v1/targets"].Get
	queryRequired := false
	for _, parameter := range targets.Parameters {
		if parameter.Name == "query" {
			queryRequired = parameter.Required
		}
	}
	if !queryRequired {
		t.Error("GET /v1/targets query parameter is not required")
	}
	if targets.Responses["422"] != nil {
		t.Error("GET /v1/targets exposes an unmapped 422 response")
	}
	if responses := spec.Paths["/v1/snapshot-imports/{importId}"].Get.Responses; responses["422"] != nil {
		t.Error("GET /v1/snapshot-imports/{importId} exposes an unmapped 422 response")
	}
	for _, status := range []string{"200", "503"} {
		if spec.Paths["/readyz"].Get.Responses[status] == nil {
			t.Errorf("GET /readyz response %s is missing from OpenAPI", status)
		}
	}
}

func TestStatusReturnsOneStoreStateDuringConcurrentGenerationSwitch(t *testing.T) {
	a := newTestApp(t)
	activateStatusGeneration(t, a, "snapshot-0")

	for iteration := 1; iteration <= 10; iteration++ {
		oldSnapshotID := fmt.Sprintf("snapshot-%d", iteration-1)
		newSnapshotID := fmt.Sprintf("snapshot-%d", iteration)
		operation, err := a.store.BeginImport(context.Background())
		if err != nil {
			t.Fatal(err)
		}

		stop := make(chan struct{})
		badCombination := make(chan string, 1)
		var readers sync.WaitGroup
		for range 8 {
			readers.Add(1)
			go func() {
				defer readers.Done()
				for {
					select {
					case <-stop:
						return
					default:
					}
					response := serveRequest(a, "/v1/status")
					var body map[string]any
					if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil {
						recordBadCombination(badCombination, "invalid status response")
						return
					}
					snapshot, ok := body["snapshot"].(map[string]any)
					if !ok {
						recordBadCombination(badCombination, fmt.Sprintf("%v + null", body["state"]))
						return
					}
					state, snapshotID := body["state"], snapshot["snapshotId"]
					if (state != "importing" || snapshotID != oldSnapshotID) &&
						(state != "ready" || snapshotID != newSnapshotID) {
						recordBadCombination(badCombination, fmt.Sprintf("%v + %v", state, snapshotID))
						return
					}
				}
			}()
		}

		if err := operation.Complete(context.Background(), statusGeneration(newSnapshotID)); err != nil {
			close(stop)
			readers.Wait()
			t.Fatal(err)
		}
		close(stop)
		readers.Wait()
		select {
		case combination := <-badCombination:
			t.Fatalf("status returned a Store state that never existed: %s", combination)
		default:
		}
	}
}

func schemaAllowsRefOrNull(schema *huma.Schema, ref string) bool {
	if schema == nil || len(schema.AnyOf) != 2 {
		return false
	}
	foundRef, foundNull := false, false
	for _, alternative := range schema.AnyOf {
		foundRef = foundRef || alternative.Ref == ref
		foundNull = foundNull || alternative.Type == "null"
	}
	return foundRef && foundNull
}

func activateStatusGeneration(t *testing.T, a *App, snapshotID string) {
	t.Helper()
	operation, err := a.store.BeginImport(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := operation.Complete(context.Background(), statusGeneration(snapshotID)); err != nil {
		t.Fatal(err)
	}
}

func statusGeneration(snapshotID string) store.Generation {
	return store.Generation{
		SnapshotID: snapshotID, GeneratedAt: time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC),
		SchemaVersion: "0.5", Fingerprint: "status-" + snapshotID,
	}
}

func recordBadCombination(results chan<- string, combination string) {
	select {
	case results <- combination:
	default:
	}
}

func TestProblemDetails(t *testing.T) {
	tests := []struct {
		name       string
		problem    *huma.ErrorModel
		wantType   string
		wantStatus int
	}{
		{
			name:       "invalid request",
			problem:    InvalidRequestProblem("invalid input"),
			wantType:   "/problems/invalid-request",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "target not found",
			problem:    TargetNotFoundProblem("target does not exist"),
			wantType:   "/problems/target-not-found",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "internal error",
			problem:    InternalErrorProblem("operation failed"),
			wantType:   "/problems/internal-error",
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "snapshot not loaded",
			problem:    SnapshotNotLoadedProblem("snapshot is not loaded"),
			wantType:   "/problems/snapshot-not-loaded",
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "snapshot import in progress",
			problem:    SnapshotImportInProgressProblem("import is in progress"),
			wantType:   "/problems/snapshot-import-in-progress",
			wantStatus: http.StatusConflict,
		},
		{
			name:       "snapshot ID conflict",
			problem:    SnapshotIDConflictProblem("snapshot ID conflicts"),
			wantType:   "/problems/snapshot-id-conflict",
			wantStatus: http.StatusConflict,
		},
		{
			name:       "snapshot too large",
			problem:    SnapshotTooLargeProblem("snapshot is too large"),
			wantType:   "/problems/snapshot-too-large",
			wantStatus: http.StatusRequestEntityTooLarge,
		},
		{
			name:       "snapshot capacity exceeded",
			problem:    SnapshotCapacityExceededProblem("capacity exceeded"),
			wantType:   "/problems/snapshot-capacity-exceeded",
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "invalid snapshot",
			problem:    InvalidSnapshotProblem("snapshot is invalid"),
			wantType:   "/problems/invalid-snapshot",
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "import not found",
			problem:    ImportNotFoundProblem("import does not exist"),
			wantType:   "/problems/import-not-found",
			wantStatus: http.StatusNotFound,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mux := http.NewServeMux()
			api := buildAPI(mux, &App{})
			huma.Register(api, huma.Operation{
				OperationID: "problem-test",
				Method:      http.MethodGet,
				Path:        "/problem-test",
				Hidden:      true,
			}, func(context.Context, *struct{}) (*healthOutput, error) {
				return nil, test.problem
			})

			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/problem-test", nil))
			assertStatus(t, recorder, test.wantStatus)
			assertContentType(t, recorder, "application/problem+json")

			body := decodeObject(t, recorder)
			if body["type"] != test.wantType {
				t.Fatalf("response type = %v, want %q", body["type"], test.wantType)
			}
			if body["status"] != float64(test.wantStatus) {
				t.Fatalf("response status = %v, want %d", body["status"], test.wantStatus)
			}
		})
	}
}

func newTestApp(t *testing.T) *App {
	t.Helper()
	a, err := New(Config{Version: "test", Commit: "abc", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Errorf("Close(): %v", err)
		}
	})
	return a
}

func assertServiceState(t *testing.T, a *App, wantState string, wantReadyStatus int) {
	t.Helper()
	ready := serveRequest(a, "/readyz")
	assertStatus(t, ready, wantReadyStatus)
	readyBody := decodeObject(t, ready)
	if wantReadyStatus == http.StatusOK {
		if readyBody["status"] != "ready" || readyBody["reason"] != "snapshot_loaded" {
			t.Fatalf("ready body = %v, want ready snapshot_loaded", readyBody)
		}
	} else if readyBody["status"] != "not_ready" || readyBody["reason"] != "snapshot_not_loaded" {
		t.Fatalf("ready body = %v, want not_ready snapshot_not_loaded", readyBody)
	}

	status := serveRequest(a, "/v1/status")
	assertStatus(t, status, http.StatusOK)
	statusBody := decodeObject(t, status)
	if statusBody["state"] != wantState {
		t.Fatalf("state = %v, want %s", statusBody["state"], wantState)
	}
}

func serveRequest(a *App, path string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	a.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	return recorder
}

func assertStatus(t *testing.T, recorder *httptest.ResponseRecorder, want int) {
	t.Helper()
	if recorder.Code != want {
		t.Fatalf("status = %d, want %d", recorder.Code, want)
	}
}

func assertContentType(t *testing.T, recorder *httptest.ResponseRecorder, want string) {
	t.Helper()
	got := recorder.Header().Get("Content-Type")
	if got != want {
		t.Fatalf("Content-Type = %q, want %q", got, want)
	}
	if link := recorder.Header().Get("Link"); link != "" {
		t.Fatalf("Link = %q, want empty", link)
	}
}

func decodeObject(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func assertKeys(t *testing.T, object map[string]any, keys ...string) {
	t.Helper()
	if len(object) != len(keys) {
		t.Fatalf("keys = %v, want %v", object, keys)
	}
	for _, key := range keys {
		if _, ok := object[key]; !ok {
			t.Errorf("key %q is missing", key)
		}
	}
}
