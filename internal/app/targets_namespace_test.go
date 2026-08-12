package app

import (
	"context"
	"net/http"
	"testing"
	"time"

	"batchscope/internal/identity"
	"batchscope/internal/normalize"
	"batchscope/internal/store"
)

func TestTargetsSearchLocalIDAcrossNamespaces(t *testing.T) {
	a := newTestApp(t)
	operation, err := a.store.BeginImport(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operation.DB().Exec(`
CREATE TABLE node_identity (
    node_id TEXT PRIMARY KEY REFERENCES node(node_id),
    namespace TEXT NOT NULL,
    local_id TEXT NOT NULL,
    UNIQUE(namespace, local_id)
);
CREATE INDEX idx_node_identity_local ON node_identity(local_id, namespace, node_id);`); err != nil {
		t.Fatal(err)
	}
	for _, namespace := range []string{"main", "dr"} {
		id := identity.Encode(namespace, "JOB-A")
		name := namespace + " JOB-A"
		path := "/" + namespace + "/JOB-A"
		if _, err := operation.DB().Exec(`INSERT INTO node (
			node_id, node_type, name, name_normalized, path, path_normalized
		) VALUES (?, 'job', ?, ?, ?, ?)`, id, name, normalize.Name(name), path, normalize.Path(path)); err != nil {
			t.Fatal(err)
		}
		if _, err := operation.DB().Exec(`INSERT INTO node_identity (node_id, namespace, local_id) VALUES (?, ?, 'JOB-A')`, id, namespace); err != nil {
			t.Fatal(err)
		}
	}
	if err := operation.Complete(context.Background(), store.Generation{
		SnapshotID: "namespaced-targets", GeneratedAt: time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC),
		SchemaVersion: "0.6", NodeCount: 2, Fingerprint: "namespaced-targets",
	}); err != nil {
		t.Fatal(err)
	}

	all := serveRequest(a, "/v1/targets?jobId=JOB-A&type=job")
	assertStatus(t, all, http.StatusOK)
	items := decodeObject(t, all)["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %v", items)
	}
	if items[0].(map[string]any)["namespace"] != "dr" || items[1].(map[string]any)["namespace"] != "main" {
		t.Fatalf("namespace order = %v", items)
	}
	for _, value := range items {
		item := value.(map[string]any)
		if item["localId"] != "JOB-A" {
			t.Fatalf("localId = %v", item)
		}
		matched := item["matchedBy"].([]any)
		if len(matched) != 1 || matched[0] != "localId" {
			t.Fatalf("matchedBy = %v", matched)
		}
	}

	mainOnly := serveRequest(a, "/v1/targets?jobId=JOB-A&namespace=main&type=job")
	assertStatus(t, mainOnly, http.StatusOK)
	mainItems := decodeObject(t, mainOnly)["items"].([]any)
	if len(mainItems) != 1 || mainItems[0].(map[string]any)["namespace"] != "main" {
		t.Fatalf("main items = %v", mainItems)
	}
}

func TestTargetsRejectAmbiguousSelectorParameters(t *testing.T) {
	a := newTestApp(t)
	paths := []string{
		"/v1/targets?jobId=",
		"/v1/targets?namespace=main",
		"/v1/targets?query=JOB&jobId=JOB",
		"/v1/targets?query=JOB&namespace=main",
		"/v1/targets?jobId=JOB&jobId=JOB",
		"/v1/targets?jobId=JOB&namespace=main&namespace=dr",
	}
	for _, path := range paths {
		recorder := serveRequest(a, path)
		assertStatus(t, recorder, http.StatusBadRequest)
		if body := decodeObject(t, recorder); body["type"] != "/problems/invalid-request" {
			t.Fatalf("%s problem = %v", path, body)
		}
	}
}
