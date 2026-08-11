package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"batchscope/internal/normalize"
	"batchscope/internal/store"
)

type appTestNode struct {
	id       string
	typeName string
	name     string
	path     *string
	parentID *string
}

func TestTargetsWithoutSnapshot(t *testing.T) {
	a := newTestApp(t)
	recorder := serveRequest(a, "/v1/targets?query=JOB")

	assertStatus(t, recorder, http.StatusServiceUnavailable)
	assertContentType(t, recorder, "application/problem+json")
	body := decodeObject(t, recorder)
	if body["type"] != "/problems/snapshot-not-loaded" || body["status"] != float64(http.StatusServiceUnavailable) {
		t.Fatalf("problem = %v", body)
	}
}

func TestTargetsRejectInvalidParameters(t *testing.T) {
	a := newTestApp(t)
	for _, path := range []string{
		"/v1/targets",
		"/v1/targets?query=",
		"/v1/targets?query=%20%20%09",
		"/v1/targets?query=JOB&type=management_unit",
	} {
		recorder := serveRequest(a, path)
		assertStatus(t, recorder, http.StatusBadRequest)
		assertContentType(t, recorder, "application/problem+json")
		body := decodeObject(t, recorder)
		if body["type"] != "/problems/invalid-request" {
			t.Errorf("%s type = %v", path, body["type"])
		}
	}
}

func TestTargetsMapsStoreFailureToInternalError(t *testing.T) {
	a := newTestApp(t)
	if err := a.store.Close(); err != nil {
		t.Fatal(err)
	}
	recorder := serveRequest(a, "/v1/targets?query=JOB")

	assertStatus(t, recorder, http.StatusInternalServerError)
	assertContentType(t, recorder, "application/problem+json")
	if body := decodeObject(t, recorder); body["type"] != "/problems/internal-error" {
		t.Fatalf("problem = %v", body)
	}
}

func TestTargetsDefaultAndRepeatedTypes(t *testing.T) {
	a := newTestApp(t)
	completeAppGeneration(t, a, "snapshot-types", []appTestNode{
		{id: "UNIT", typeName: "management_unit", name: "shared"},
		{id: "NET", typeName: "job_network", name: "shared"},
		{id: "JOB", typeName: "job", name: "shared"},
	})

	defaultResponse := serveRequest(a, "/v1/targets?query=shared")
	assertStatus(t, defaultResponse, http.StatusOK)
	if ids := responseTargetIDs(t, defaultResponse); strings.Join(ids, ",") != "JOB,NET" {
		t.Fatalf("default IDs = %v, want JOB,NET", ids)
	}
	for _, value := range decodeObject(t, defaultResponse)["items"].([]any) {
		if _, ok := value.(map[string]any)["path"]; ok {
			t.Errorf("path is present for node without a path: %v", value)
		}
	}

	repeated := serveRequest(a, "/v1/targets?query=shared&type=job_network&type=job")
	assertStatus(t, repeated, http.StatusOK)
	if ids := responseTargetIDs(t, repeated); strings.Join(ids, ",") != "JOB,NET" {
		t.Fatalf("repeated type IDs = %v, want JOB,NET", ids)
	}

	jobOnly := serveRequest(a, "/v1/targets?query=shared&type=job")
	assertStatus(t, jobOnly, http.StatusOK)
	if ids := responseTargetIDs(t, jobOnly); strings.Join(ids, ",") != "JOB" {
		t.Fatalf("job IDs = %v, want JOB", ids)
	}
}

func TestTargetsResponseUsesAcquiredSnapshot(t *testing.T) {
	a := newTestApp(t)
	completeAppGeneration(t, a, "snapshot-response", []appTestNode{
		{id: "ROOT", typeName: "management_unit", name: "Root"},
		{id: "NET", typeName: "job_network", name: "Network", parentID: appString("ROOT")},
		{id: "JOB", typeName: "job", name: "Job", path: appString("/ROOT/NET/JOB"), parentID: appString("NET")},
	})

	recorder := serveRequest(a, "/v1/targets?query=JOB")
	assertStatus(t, recorder, http.StatusOK)
	body := decodeObject(t, recorder)
	if body["snapshotId"] != "snapshot-response" || body["truncated"] != false {
		t.Fatalf("response = %v", body)
	}
	items := body["items"].([]any)
	item := items[0].(map[string]any)
	if item["id"] != "JOB" || item["path"] != "/ROOT/NET/JOB" {
		t.Fatalf("item = %v", item)
	}
	ancestors := item["ancestorPath"].([]any)
	if ancestors[0].(map[string]any)["id"] != "ROOT" || ancestors[1].(map[string]any)["id"] != "NET" {
		t.Fatalf("ancestorPath = %v", ancestors)
	}
}

func TestTargetSearchStructuredLogOmitsSensitiveValues(t *testing.T) {
	a := newTestApp(t)
	completeAppGeneration(t, a, "snapshot-log", []appTestNode{
		{id: "JOB", typeName: "job", name: "Sensitive Job Name", path: appString("/sensitive/full/path")},
	})
	var buffer bytes.Buffer
	a.logger = slog.New(slog.NewJSONHandler(&buffer, nil))

	query := url.QueryEscape("  sensitive job name  ")
	request := httptest.NewRequest(http.MethodGet, "/v1/targets?query="+query, nil)
	request.Header.Set("X-Request-Id", "request-from-client")
	recorder := httptest.NewRecorder()
	a.Handler().ServeHTTP(recorder, request)
	assertStatus(t, recorder, http.StatusOK)

	var entry map[string]any
	if err := json.Unmarshal(buffer.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"request_id": "request-from-client", "operation": "target_search", "boot_id": a.bootID,
		"snapshot_id": "snapshot-log", "returned_targets": float64(1),
	} {
		if entry[key] != want {
			t.Errorf("%s = %v, want %v", key, entry[key], want)
		}
	}
	if _, ok := entry["duration_ms"]; !ok {
		t.Error("duration_ms is missing")
	}
	logged := buffer.String()
	for _, secret := range []string{"Sensitive Job Name", "/sensitive/full/path", "sensitive+job+name", "  sensitive job name  "} {
		if strings.Contains(logged, secret) {
			t.Errorf("log contains sensitive value %q: %s", secret, logged)
		}
	}
}

func TestTargetSearchKeepsSnapshotAndResultsInSameGeneration(t *testing.T) {
	a := newTestApp(t)
	oldNodes := generationNodes("OLD")
	completeAppGeneration(t, a, "old-snapshot", oldNodes)

	responses := []*httptest.ResponseRecorder{serveRequest(a, "/v1/targets?query=common")}
	operation := beginAppGeneration(t, a, generationNodes("NEW"))

	const concurrentRequests = 12
	concurrent := make([]*httptest.ResponseRecorder, concurrentRequests)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index := range concurrent {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			concurrent[index] = serveRequest(a, "/v1/targets?query=common")
		}(index)
	}
	close(start)
	if err := operation.Complete(context.Background(), generation("new-snapshot", len(oldNodes))); err != nil {
		t.Fatal(err)
	}
	wait.Wait()
	responses = append(responses, concurrent...)
	responses = append(responses, serveRequest(a, "/v1/targets?query=common"))

	for _, recorder := range responses {
		assertStatus(t, recorder, http.StatusOK)
		body := decodeObject(t, recorder)
		snapshotID := body["snapshotId"].(string)
		prefix := ""
		switch snapshotID {
		case "old-snapshot":
			prefix = "OLD-"
		case "new-snapshot":
			prefix = "NEW-"
		default:
			t.Fatalf("snapshotId = %q", snapshotID)
		}
		for _, item := range body["items"].([]any) {
			id := item.(map[string]any)["id"].(string)
			if !strings.HasPrefix(id, prefix) {
				t.Fatalf("snapshot %q contains target %q", snapshotID, id)
			}
		}
	}
}

func completeAppGeneration(t *testing.T, a *App, snapshotID string, nodes []appTestNode) {
	t.Helper()
	operation := beginAppGeneration(t, a, nodes)
	if err := operation.Complete(context.Background(), generation(snapshotID, len(nodes))); err != nil {
		t.Fatal(err)
	}
}

func beginAppGeneration(t *testing.T, a *App, nodes []appTestNode) *store.Import {
	t.Helper()
	operation, err := a.store.BeginImport(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range nodes {
		var normalizedPath any
		if node.path != nil {
			normalizedPath = normalize.Path(*node.path)
		}
		if _, err := operation.DB().Exec(`INSERT INTO node (
			node_id, node_type, name, name_normalized, path, path_normalized, parent_id
		) VALUES (?, ?, ?, ?, ?, ?, ?)`, node.id, node.typeName, node.name, normalize.Name(node.name), node.path, normalizedPath, node.parentID); err != nil {
			t.Fatal(err)
		}
	}
	return operation
}

func generation(snapshotID string, nodeCount int) store.Generation {
	return store.Generation{
		SnapshotID: snapshotID, GeneratedAt: time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC),
		SchemaVersion: "0.5", NodeCount: nodeCount, Fingerprint: snapshotID,
	}
}

func generationNodes(prefix string) []appTestNode {
	nodes := make([]appTestNode, 1_000)
	for index := range nodes {
		id := fmt.Sprintf("%s-%04d", prefix, index)
		path := fmt.Sprintf("/%s/%04d", prefix, index)
		nodes[index] = appTestNode{id: id, typeName: "job", name: "common", path: &path}
	}
	return nodes
}

func responseTargetIDs(t *testing.T, recorder *httptest.ResponseRecorder) []string {
	t.Helper()
	body := decodeObject(t, recorder)
	items := body["items"].([]any)
	ids := make([]string, len(items))
	for index, value := range items {
		ids[index] = value.(map[string]any)["id"].(string)
	}
	return ids
}

func appString(value string) *string {
	return &value
}
