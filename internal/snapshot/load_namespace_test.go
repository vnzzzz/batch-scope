package snapshot

import (
	"context"
	"testing"

	"batchscope/internal/identity"
	"batchscope/internal/store"
)

func TestLoadStoresNamespaceIdentityIndex(t *testing.T) {
	mainID := identity.Canonical("main", "JOB-A")
	drID := identity.Canonical("dr", "JOB-A")
	nodes := []string{
		`{"type":"job","id":"` + mainID + `","namespace":"main","localId":"JOB-A","name":"Main A"}`,
		`{"type":"job","id":"` + drID + `","namespace":"dr","localId":"JOB-A","name":"DR A"}`,
		`{"type":"job","id":"LEGACY","name":"Legacy"}`,
	}
	extracted := writeExtractedSnapshot(t, nodes, nil)
	validated, err := Validate(context.Background(), extracted)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	storage, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	operation, err := storage.BeginImport(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = operation.Abort() })
	if err := Load(context.Background(), operation.DB(), extracted, validated); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	rows, err := operation.DB().Query(`SELECT node_id, namespace, local_id FROM node_identity ORDER BY namespace, local_id, node_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got [][3]string
	for rows.Next() {
		var row [3]string
		if err := rows.Scan(&row[0], &row[1], &row[2]); err != nil {
			t.Fatal(err)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := [][3]string{
		{"LEGACY", "default", "LEGACY"},
		{drID, "dr", "JOB-A"},
		{mainID, "main", "JOB-A"},
	}
	if len(got) != len(want) {
		t.Fatalf("identity rows = %v", got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("identity row %d = %v, want %v", index, got[index], want[index])
		}
	}

	var indexCount int
	if err := operation.DB().QueryRow(`SELECT COUNT(*) FROM pragma_index_list('node_identity') WHERE name = 'idx_node_identity_local'`).Scan(&indexCount); err != nil {
		t.Fatal(err)
	}
	if indexCount != 1 {
		t.Fatalf("idx_node_identity_local count = %d", indexCount)
	}
}
