package target

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"batchscope/internal/identity"
	"batchscope/internal/normalize"
	"batchscope/internal/store"
)

type namespacedSearchNode struct {
	namespace string
	localID   string
	typeName  string
	name      string
}

func TestSearchByLocalIDReturnsEveryNamespace(t *testing.T) {
	db := openNamespacedSearchDB(t, "namespaces", []namespacedSearchNode{
		{namespace: "main", localID: "JOB-A", typeName: "job", name: "Main A"},
		{namespace: "dr", localID: "JOB-A", typeName: "job", name: "DR A"},
		{namespace: "main", localID: "JOB-B", typeName: "job", name: "Main B"},
	})

	result, err := SearchByLocalID(context.Background(), db, "JOB-A", "", []string{"job"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Truncated {
		t.Fatal("unexpected truncated result")
	}
	if len(result.Items) != 2 {
		t.Fatalf("items = %#v", result.Items)
	}
	if got := []string{result.Items[0].Namespace, result.Items[1].Namespace}; !reflect.DeepEqual(got, []string{"dr", "main"}) {
		t.Fatalf("namespaces = %v", got)
	}
	for _, item := range result.Items {
		if item.LocalID != "JOB-A" || !reflect.DeepEqual(item.MatchedBy, []string{"localId"}) {
			t.Fatalf("item = %#v", item)
		}
	}
}

func TestSearchByLocalIDCanSelectNamespace(t *testing.T) {
	db := openNamespacedSearchDB(t, "namespace-filter", []namespacedSearchNode{
		{namespace: "main", localID: "JOB-A", typeName: "job", name: "Main A"},
		{namespace: "dr", localID: "JOB-A", typeName: "job", name: "DR A"},
	})
	mainID := identity.Canonical("main", "JOB-A")

	result, err := SearchByLocalID(context.Background(), db, "JOB-A", "main", []string{"job"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].ID != mainID || result.Items[0].Namespace != "main" {
		t.Fatalf("items = %#v", result.Items)
	}
}

func TestSearchByLocalIDKeepsLegacyDefaultNamespace(t *testing.T) {
	db := openSearchDB(t, "legacy-local-id", []testNode{{id: "JOB-A", typeName: "job", name: "A"}}, nil)
	result, err := SearchByLocalID(context.Background(), db, "JOB-A", "", []string{"job"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].Namespace != "default" || result.Items[0].LocalID != "JOB-A" {
		t.Fatalf("items = %#v", result.Items)
	}
}

func TestSearchByLocalIDDoesNotDecodeCanonicalLookingLegacyID(t *testing.T) {
	legacyID := identity.Canonical("main", "JOB-A")
	db := openSearchDB(t, "legacy-canonical-looking", []testNode{{id: legacyID, typeName: "job", name: "Legacy"}}, nil)

	result, err := SearchByLocalID(context.Background(), db, "JOB-A", "", []string{"job"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("canonical-looking legacy ID was decoded: %#v", result.Items)
	}
	result, err = SearchByLocalID(context.Background(), db, legacyID, "", []string{"job"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].Namespace != "default" || result.Items[0].LocalID != legacyID {
		t.Fatalf("legacy item = %#v", result.Items)
	}
}

func openNamespacedSearchDB(t *testing.T, snapshotID string, nodes []namespacedSearchNode) *sql.DB {
	t.Helper()
	storage, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	operation, err := storage.BeginImport(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operation.DB().Exec(`CREATE TABLE node_identity (
		node_id TEXT PRIMARY KEY REFERENCES node(node_id),
		namespace TEXT NOT NULL,
		local_id TEXT NOT NULL,
		UNIQUE(namespace, local_id)
	); CREATE INDEX idx_node_identity_local ON node_identity(local_id, namespace, node_id);`); err != nil {
		t.Fatal(err)
	}
	for _, node := range nodes {
		id := identity.Canonical(node.namespace, node.localID)
		if _, err := operation.DB().Exec(`INSERT INTO node (
			node_id, node_type, name, name_normalized
		) VALUES (?, ?, ?, ?)`, id, node.typeName, node.name, normalize.Name(node.name)); err != nil {
			t.Fatal(err)
		}
		if _, err := operation.DB().Exec(`INSERT INTO node_identity (node_id, namespace, local_id) VALUES (?, ?, ?)`, id, node.namespace, node.localID); err != nil {
			t.Fatal(err)
		}
	}
	if err := operation.Complete(context.Background(), store.Generation{
		SnapshotID: snapshotID, GeneratedAt: time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC),
		SchemaVersion: "0.5", NodeCount: len(nodes), Fingerprint: snapshotID,
	}); err != nil {
		t.Fatal(err)
	}
	db, _, release, err := storage.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	return db
}
