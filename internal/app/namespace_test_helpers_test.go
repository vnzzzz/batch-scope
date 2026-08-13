package app

import (
	"context"
	"database/sql"
	"testing"

	"batchscope/internal/identity"
)

func completeNamespacedAppGeneration(t *testing.T, a *App, snapshotID string, nodes []appTestNode) {
	t.Helper()
	operation := beginAppGeneration(t, a, nodes)
	ids := make([]string, len(nodes))
	for index, node := range nodes {
		ids[index] = node.id
	}
	insertNamespacedTestIdentities(t, operation.DB(), ids)
	if err := operation.Complete(context.Background(), generation(snapshotID, len(nodes))); err != nil {
		t.Fatal(err)
	}
}

func completeNamespacedAnalysisGeneration(t *testing.T, a *App, snapshotID string, data analysisTestData) {
	t.Helper()
	operation := beginAnalysisGeneration(t, a, data)
	ids := make([]string, len(data.nodes))
	for index, node := range data.nodes {
		ids[index] = node.id
	}
	insertNamespacedTestIdentities(t, operation.DB(), ids)
	if err := operation.Complete(context.Background(), analysisGeneration(snapshotID, len(data.nodes), len(data.relations), len(data.facts))); err != nil {
		t.Fatal(err)
	}
}

func insertNamespacedTestIdentities(t *testing.T, db *sql.DB, ids []string) {
	t.Helper()
	if _, err := db.Exec(`CREATE TABLE node_identity (
		node_id TEXT PRIMARY KEY REFERENCES node(node_id),
		namespace TEXT NOT NULL,
		local_id TEXT NOT NULL,
		UNIQUE(namespace, local_id)
	); CREATE INDEX idx_node_identity_local ON node_identity(local_id, namespace, node_id);`); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		namespace, localID, err := identity.Decode(id)
		if err != nil {
			t.Fatalf("namespaced test node %q is not canonical: %v", id, err)
		}
		if _, err := db.Exec(`INSERT INTO node_identity (node_id, namespace, local_id) VALUES (?, ?, ?)`, id, namespace, localID); err != nil {
			t.Fatal(err)
		}
	}
}
