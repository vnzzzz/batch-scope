package target

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"
	"time"

	"batchscope/internal/normalize"
	"batchscope/internal/store"
)

type testNode struct {
	id       string
	typeName string
	name     string
	path     *string
	parentID *string
}

func TestSearchExactMatchNormalization(t *testing.T) {
	nodes := []testNode{
		{id: "ROOT", typeName: "management_unit", name: "Root"},
		{id: "NET", typeName: "job_network", name: "Network", parentID: pointer("ROOT")},
		{id: "Job-Ａ", typeName: "job", name: "ＡＢＣ", path: pointer(" /Root/ＡＢＣ "), parentID: pointer("NET")},
	}
	db := openSearchDB(t, "normalization", nodes, nil)

	tests := []struct {
		name      string
		query     string
		wantID    string
		wantMatch string
	}{
		{name: "ID exact", query: "Job-Ａ", wantID: "Job-Ａ", wantMatch: "id"},
		{name: "name width", query: "ABC", wantID: "Job-Ａ", wantMatch: "name"},
		{name: "name case", query: "abc", wantID: "Job-Ａ", wantMatch: "name"},
		{name: "name surrounding whitespace", query: "  ＡＢＣ\t", wantID: "Job-Ａ", wantMatch: "name"},
		{name: "path width and whitespace", query: "  /Root/ABC ", wantID: "Job-Ａ", wantMatch: "path"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Search(context.Background(), db, test.query, []string{"job"})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Items) != 1 || result.Items[0].ID != test.wantID {
				t.Fatalf("items = %#v, want %s", result.Items, test.wantID)
			}
			if !reflect.DeepEqual(result.Items[0].MatchedBy, []string{test.wantMatch}) {
				t.Fatalf("matchedBy = %v, want %s", result.Items[0].MatchedBy, test.wantMatch)
			}
		})
	}

	for _, query := range []string{"job-Ａ", "Job-A", "/root/ABC"} {
		result, err := Search(context.Background(), db, query, []string{"job"})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Items) != 0 {
			t.Errorf("query %q returned %#v, want no match", query, result.Items)
		}
	}
}

func TestSearchPreservesMatchesAndOrder(t *testing.T) {
	nodes := []testNode{
		{id: "q", typeName: "job", name: "q", path: pointer("q")},
		{id: "NAME-B", typeName: "job", name: "Q", path: pointer("/b")},
		{id: "NAME-A-Z", typeName: "job", name: "q", path: pointer("/a")},
		{id: "NAME-A-A", typeName: "job_network", name: "Q", path: pointer("/a")},
		{id: "NAME-NO-PATH", typeName: "job", name: "q"},
		{id: "PATH", typeName: "job", name: "other", path: pointer("q")},
	}
	db := openSearchDB(t, "ordering", nodes, nil)

	var previous []SearchItem
	for iteration := 0; iteration < 10; iteration++ {
		result, err := Search(context.Background(), db, "q", []string{"job", "job_network"})
		if err != nil {
			t.Fatal(err)
		}
		ids := itemIDs(result.Items)
		want := []string{"q", "NAME-A-A", "NAME-A-Z", "NAME-B", "NAME-NO-PATH", "PATH"}
		if !reflect.DeepEqual(ids, want) {
			t.Fatalf("IDs = %v, want %v", ids, want)
		}
		if !reflect.DeepEqual(result.Items[0].MatchedBy, []string{"id", "name", "path"}) {
			t.Fatalf("first matchedBy = %v", result.Items[0].MatchedBy)
		}
		if iteration > 0 && !reflect.DeepEqual(result.Items, previous) {
			t.Fatal("result order or matchedBy changed between identical searches")
		}
		previous = result.Items
	}
}

func TestLoadOneBuildsAncestorPathFromParentsOnly(t *testing.T) {
	nodes := []testNode{
		{id: "ROOT", typeName: "management_unit", name: "Root"},
		{id: "UNIT", typeName: "management_unit", name: "Unit", parentID: pointer("ROOT")},
		{id: "NET", typeName: "job_network", name: "Network", parentID: pointer("UNIT")},
		{id: "JOB", typeName: "job", name: "Job", path: pointer("/ROOT/UNIT/NET/JOB"), parentID: pointer("NET")},
		{id: "DEPENDENCY", typeName: "job", name: "Dependency"},
	}
	relations := []testRelation{{from: "DEPENDENCY", to: "JOB"}}
	db := openSearchDB(t, "ancestors", nodes, relations)

	detail, err := LoadOne(context.Background(), db, "JOB")
	if err != nil {
		t.Fatal(err)
	}
	want := []Ancestor{
		{ID: "ROOT", Namespace: "default", LocalID: "ROOT", Type: "management_unit", Name: "Root"},
		{ID: "UNIT", Namespace: "default", LocalID: "UNIT", Type: "management_unit", Name: "Unit"},
		{ID: "NET", Namespace: "default", LocalID: "NET", Type: "job_network", Name: "Network"},
	}
	if !reflect.DeepEqual(detail.AncestorPath, want) {
		t.Fatalf("ancestorPath = %#v, want %#v", detail.AncestorPath, want)
	}
}

func TestSearchTruncatesAtOneThousand(t *testing.T) {
	nodes := make([]testNode, 0, MaxSearchResults+1)
	for index := 0; index <= MaxSearchResults; index++ {
		id := fmt.Sprintf("JOB-%04d", index)
		path := fmt.Sprintf("/%04d", index)
		nodes = append(nodes, testNode{id: id, typeName: "job", name: "bulk", path: &path})
	}
	db := openSearchDB(t, "truncated", nodes, nil)

	result, err := Search(context.Background(), db, "bulk", []string{"job"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != MaxSearchResults || !result.Truncated {
		t.Fatalf("len(items) = %d, truncated = %v", len(result.Items), result.Truncated)
	}
	if result.Items[0].ID != "JOB-0000" || result.Items[MaxSearchResults-1].ID != "JOB-0999" {
		t.Fatalf("boundary IDs = %s, %s", result.Items[0].ID, result.Items[MaxSearchResults-1].ID)
	}
}

type testRelation struct {
	from string
	to   string
}

func openSearchDB(t *testing.T, snapshotID string, nodes []testNode, relations []testRelation) *sql.DB {
	t.Helper()
	storage, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := storage.Close(); err != nil {
			t.Errorf("Close(): %v", err)
		}
	})
	operation, err := storage.BeginImport(context.Background())
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
	for index, relation := range relations {
		if _, err := operation.DB().Exec(`INSERT INTO relation (
			relation_id, from_id, to_id, relation_kind, origin, certainty
		) VALUES (?, ?, ?, 'precedes', 'scheduler', 'declared')`, fmt.Sprintf("relation-%d", index), relation.from, relation.to); err != nil {
			t.Fatal(err)
		}
	}
	if err := operation.Complete(context.Background(), store.Generation{
		SnapshotID: snapshotID, GeneratedAt: time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC),
		SchemaVersion: "0.5", NodeCount: len(nodes), RelationCount: len(relations), Fingerprint: snapshotID,
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

func pointer(value string) *string {
	return &value
}

func itemIDs(items []SearchItem) []string {
	ids := make([]string, len(items))
	for index, item := range items {
		ids[index] = item.ID
	}
	return ids
}
