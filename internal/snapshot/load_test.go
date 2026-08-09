package snapshot

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"batchscope/internal/store"
)

func TestLoadNormalizesAndConvertsSnapshotValues(t *testing.T) {
	network := `{"type":"job_network","id":"NET","name":"Network","parentId":null,"limitFacts":[]}`
	upperPathJob := `{"type":"job","id":"JOB-UPPER","name":"　Ｓｔｒａße　","path":"　/ＤＡＩＬＹ/Job　","parentId":"NET","limitFacts":[` +
		`{"id":"FINISH","kind":"finish_by","businessDayOffset":-1,"localTime":"01:02:03","timeZone":"Asia/Tokyo","sourceText":"前日01:02:03","origin":"scheduler","certainty":"declared"},` +
		`{"id":"ELAPSED","kind":"max_elapsed","duration":"PT30M","origin":"manual","certainty":"confirmed"},` +
		`{"id":"RAW","kind":"raw","sourceText":"運用日の翌朝","origin":"manual","certainty":"candidate"}` +
		`],"locator":{"line":1},"attributes":{"owner":"ops"}}`
	lowerPathJob := `{"type":"job","id":"JOB-LOWER","name":"Other","path":"/DAILY/job","parentId":"NET","limitFacts":[]}`
	relationLine := `{"fromId":"JOB-UPPER","toId":"JOB-LOWER","kind":"precedes","origin":"scheduler","certainty":"declared","evidence":[{"source":"definition"}]}`
	extracted := writeExtractedSnapshot(t, []string{upperPathJob, lowerPathJob, network}, []string{relationLine})
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
	if err := Load(context.Background(), operation.DB(), extracted, validated); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	var nameNormalized, pathNormalized, locatorJSON, attributesJSON string
	if err := operation.DB().QueryRow(`SELECT name_normalized, path_normalized, locator_json, attributes_json
        FROM node WHERE node_id = 'JOB-UPPER'`).Scan(&nameNormalized, &pathNormalized, &locatorJSON, &attributesJSON); err != nil {
		t.Fatal(err)
	}
	if nameNormalized != "strasse" {
		t.Errorf("name_normalized = %q, want strasse", nameNormalized)
	}
	if pathNormalized != "/DAILY/Job" {
		t.Errorf("path_normalized = %q, want /DAILY/Job", pathNormalized)
	}
	if locatorJSON != `{"line":1}` || attributesJSON != `{"owner":"ops"}` {
		t.Errorf("optional JSON = (%q, %q)", locatorJSON, attributesJSON)
	}
	var distinctPaths int
	if err := operation.DB().QueryRow(`SELECT COUNT(DISTINCT path_normalized) FROM node WHERE node_type = 'job'`).Scan(&distinctPaths); err != nil {
		t.Fatal(err)
	}
	if distinctPaths != 2 {
		t.Fatalf("case-distinct normalized paths = %d, want 2", distinctPaths)
	}

	assertFinishLimit(t, operation.DB())
	assertElapsedLimit(t, operation.DB())
	assertRawLimit(t, operation.DB())

	var relationIDValue, evidenceJSON string
	if err := operation.DB().QueryRow(`SELECT relation_id, evidence_json FROM relation`).Scan(&relationIDValue, &evidenceJSON); err != nil {
		t.Fatal(err)
	}
	if len(relationIDValue) != 64 || evidenceJSON != `[{"source":"definition"}]` {
		t.Errorf("stored relation = (%q, %q)", relationIDValue, evidenceJSON)
	}
	if err := operation.Abort(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRollsBackAllTablesOnFailure(t *testing.T) {
	network := testNode("job_network", "NET", nil, nil)
	firstLimit := map[string]any{"id": "FIRST", "kind": "raw", "sourceText": "raw", "origin": "manual", "certainty": "declared"}
	secondLimit := map[string]any{"id": "SECOND", "kind": "raw", "sourceText": "raw", "origin": "manual", "certainty": "declared"}
	nodes := []string{
		network,
		testNode("job", "JOB-A", stringPointer("NET"), []any{firstLimit}),
		testNode("job", "JOB-B", stringPointer("NET"), []any{secondLimit}),
	}
	extracted := writeExtractedSnapshot(t, nodes, nil)
	validated, err := Validate(context.Background(), extracted)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	// LoadはValidateと同じファイルを受け取る前提だが、再読込中の制約違反でも全行をrollbackする。
	writeTestFile(t, extracted.Nodes, strings.ReplaceAll(strings.Join(nodes, "\n")+"\n", `"SECOND"`, `"FIRST"`))

	storage, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	operation, err := storage.BeginImport(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := Load(context.Background(), operation.DB(), extracted, validated); err == nil {
		t.Fatal("Load() succeeded with duplicate limit IDs")
	}
	for _, table := range []string{"node", "limit_fact", "relation"} {
		var count int
		if err := operation.DB().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("%s rows after rollback = %d, want 0", table, count)
		}
	}
	if err := operation.Abort(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadAcceptsEquivalentIntegerRepresentations(t *testing.T) {
	for _, representation := range []string{"1", "1.0", "1e0"} {
		t.Run(representation, func(t *testing.T) {
			fact := fmt.Sprintf(`{"id":"LIMIT","kind":"finish_by","businessDayOffset":%s,"localTime":"00:00:00","timeZone":"UTC","origin":"scheduler","certainty":"declared"}`, representation)
			extracted := writeExtractedSnapshot(t, []string{
				testNode("job", "JOB", nil, []any{json.RawMessage(fact)}),
			}, nil)
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
			var offset int64
			if err := operation.DB().QueryRow(`SELECT business_day_offset FROM limit_fact WHERE limit_id = 'LIMIT'`).Scan(&offset); err != nil {
				t.Fatal(err)
			}
			if offset != 1 {
				t.Fatalf("business_day_offset = %d, want 1", offset)
			}
		})
	}
}

func assertFinishLimit(t *testing.T, db *sql.DB) {
	t.Helper()
	var offset, local, sort int64
	var zone, source string
	var duration sql.NullInt64
	if err := db.QueryRow(`SELECT business_day_offset, local_time_seconds, time_zone,
        finish_sort_seconds, duration_seconds, source_text FROM limit_fact WHERE limit_id = 'FINISH'`).
		Scan(&offset, &local, &zone, &sort, &duration, &source); err != nil {
		t.Fatal(err)
	}
	if offset != -1 || local != 3723 || zone != "Asia/Tokyo" || sort != -82677 || duration.Valid || source != "前日01:02:03" {
		t.Errorf("finish_by values = (%d, %d, %q, %d, %v, %q)", offset, local, zone, sort, duration, source)
	}
}

func assertElapsedLimit(t *testing.T, db *sql.DB) {
	t.Helper()
	var duration int64
	var offset, local, sort sql.NullInt64
	var zone, source sql.NullString
	if err := db.QueryRow(`SELECT business_day_offset, local_time_seconds, time_zone,
        finish_sort_seconds, duration_seconds, source_text FROM limit_fact WHERE limit_id = 'ELAPSED'`).
		Scan(&offset, &local, &zone, &sort, &duration, &source); err != nil {
		t.Fatal(err)
	}
	if offset.Valid || local.Valid || zone.Valid || sort.Valid || duration != 1800 || source.Valid {
		t.Errorf("max_elapsed values = (%v, %v, %v, %v, %d, %v)", offset, local, zone, sort, duration, source)
	}
}

func assertRawLimit(t *testing.T, db *sql.DB) {
	t.Helper()
	var offset, local, sort, duration sql.NullInt64
	var zone sql.NullString
	var source string
	if err := db.QueryRow(`SELECT business_day_offset, local_time_seconds, time_zone,
        finish_sort_seconds, duration_seconds, source_text FROM limit_fact WHERE limit_id = 'RAW'`).
		Scan(&offset, &local, &zone, &sort, &duration, &source); err != nil {
		t.Fatal(err)
	}
	if offset.Valid || local.Valid || zone.Valid || sort.Valid || duration.Valid || source != "運用日の翌朝" {
		t.Errorf("raw values = (%v, %v, %v, %v, %v, %q)", offset, local, zone, sort, duration, source)
	}
}
