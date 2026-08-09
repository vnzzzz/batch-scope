package importer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"batchscope/internal/store"
)

func TestRunImportsDemoSnapshotWithIndexes(t *testing.T) {
	archive := demoArchive(t)
	workspace := t.TempDir()
	storage := newImporterTestStore(t, filepath.Join(workspace, "data"))
	if _, err := Run(context.Background(), workspace, bytes.NewReader(archive), storage); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := storage.State(); got != store.StateReady {
		t.Fatalf("state = %q, want %q", got, store.StateReady)
	}

	db, release, err := storage.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	assertCount(t, db, "node", 9)
	assertCount(t, db, "limit_fact", 3)
	assertCount(t, db, "relation", 8)

	rows, err := db.Query(`SELECT name FROM sqlite_master
        WHERE type = 'index' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var indexes []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		indexes = append(indexes, name)
	}
	wantIndexes := []string{
		"idx_limit_elapsed", "idx_limit_finish", "idx_limit_node", "idx_node_id", "idx_node_name_exact",
		"idx_node_parent", "idx_node_path_exact", "idx_relation_from", "idx_relation_to",
	}
	if !reflect.DeepEqual(indexes, wantIndexes) {
		t.Fatalf("indexes = %v, want %v", indexes, wantIndexes)
	}
}

func TestRunFailureKeepsCurrentAndRemovesImportingDatabase(t *testing.T) {
	workspace := t.TempDir()
	dataDirectory := filepath.Join(workspace, "data")
	storage := newImporterTestStore(t, dataDirectory)
	if _, err := Run(context.Background(), workspace, bytes.NewReader(demoArchive(t)), storage); err != nil {
		t.Fatalf("initial Run() error = %v", err)
	}

	invalid := duplicateLimitArchive(t)
	if _, err := Run(context.Background(), workspace, bytes.NewReader(invalid), storage); err == nil {
		t.Fatal("Run() succeeded with duplicate limit IDs")
	}
	if got := storage.State(); got != store.StateReady {
		t.Fatalf("state after failure = %q, want %q", got, store.StateReady)
	}
	db, release, err := storage.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	assertCount(t, db, "node", 9)
	release()
	if _, err := os.Stat(filepath.Join(dataDirectory, "current.db")); err != nil {
		t.Fatalf("current.db after failure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDirectory, "importing.db")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("importing.db after failure: %v", err)
	}
}

func TestRunProducesSameTableContentsFromSameInput(t *testing.T) {
	archive := demoArchive(t)
	want := importAndDump(t, archive)
	got := importAndDump(t, archive)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("table contents differ for identical input\nfirst: %#v\nsecond: %#v", want, got)
	}
}

func TestRunReservesImportBeforeReadingArchive(t *testing.T) {
	workspace := t.TempDir()
	storage := newImporterTestStore(t, filepath.Join(workspace, "data"))
	firstReader := &blockingReader{
		reader:  bytes.NewReader(demoArchive(t)),
		started: make(chan struct{}),
		resume:  make(chan struct{}),
	}
	firstDone := make(chan error, 1)
	go func() {
		_, err := Run(context.Background(), workspace, firstReader, storage)
		firstDone <- err
	}()

	<-firstReader.started
	if got := storage.State(); got != store.StateImporting {
		t.Fatalf("state during receive = %q, want %q", got, store.StateImporting)
	}
	secondReader := &countingReader{reader: bytes.NewReader(demoArchive(t))}
	if _, err := Run(context.Background(), workspace, secondReader, storage); !errors.Is(err, store.ErrImportInProgress) {
		t.Fatalf("concurrent Run() error = %v, want %v", err, store.ErrImportInProgress)
	}
	if secondReader.reads != 0 {
		t.Fatalf("concurrent reader calls = %d, want 0", secondReader.reads)
	}

	close(firstReader.resume)
	if err := <-firstDone; err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
}

func TestRunAbortsReservationWhenReceiveFails(t *testing.T) {
	workspace := t.TempDir()
	dataDirectory := filepath.Join(workspace, "data")
	storage := newImporterTestStore(t, dataDirectory)
	receiveErr := errors.New("receive failed")

	if _, err := Run(context.Background(), workspace, errorReader{err: receiveErr}, storage); !errors.Is(err, receiveErr) {
		t.Fatalf("Run() error = %v, want %v", err, receiveErr)
	}
	if got := storage.State(); got != store.StateEmpty {
		t.Fatalf("state = %q, want %q", got, store.StateEmpty)
	}
	if _, err := os.Stat(filepath.Join(dataDirectory, "importing.db")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("importing.db after receive failure: %v", err)
	}
}

func TestRunReportsRetiredCleanupAsSuccessfulWarning(t *testing.T) {
	workspace := t.TempDir()
	storage := newImporterTestStore(t, filepath.Join(workspace, "data"))
	if _, err := Run(context.Background(), workspace, bytes.NewReader(singleNodeArchive(t, "OLD")), storage); err != nil {
		t.Fatalf("initial Run() error = %v", err)
	}

	cleanupErr := errors.New("cleanup failed")
	originalComplete := completeImport
	completeImport = func(ctx context.Context, operation *store.Import) error {
		if err := originalComplete(ctx, operation); err != nil {
			return err
		}
		return errors.Join(store.ErrRetiredCleanup, cleanupErr)
	}
	t.Cleanup(func() { completeImport = originalComplete })

	result, err := Run(context.Background(), workspace, bytes.NewReader(singleNodeArchive(t, "NEW")), storage)
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if !errors.Is(result.CleanupWarning, store.ErrRetiredCleanup) || !errors.Is(result.CleanupWarning, cleanupErr) {
		t.Fatalf("CleanupWarning = %v", result.CleanupWarning)
	}
	if got := storage.State(); got != store.StateReady {
		t.Fatalf("state = %q, want %q", got, store.StateReady)
	}
	db, release, err := storage.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	var newCount, oldCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM node WHERE node_id = 'NEW'`).Scan(&newCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM node WHERE node_id = 'OLD'`).Scan(&oldCount); err != nil {
		t.Fatal(err)
	}
	if newCount != 1 || oldCount != 0 {
		t.Fatalf("searchable nodes NEW=%d OLD=%d, want NEW=1 OLD=0", newCount, oldCount)
	}
}

type blockingReader struct {
	reader  io.Reader
	started chan struct{}
	resume  chan struct{}
	once    sync.Once
}

func (r *blockingReader) Read(contents []byte) (int, error) {
	r.once.Do(func() {
		close(r.started)
		<-r.resume
	})
	return r.reader.Read(contents)
}

type countingReader struct {
	reader io.Reader
	reads  int
}

func (r *countingReader) Read(contents []byte) (int, error) {
	r.reads++
	return r.reader.Read(contents)
}

type errorReader struct {
	err error
}

func (r errorReader) Read([]byte) (int, error) {
	return 0, r.err
}

func importAndDump(t *testing.T, archive []byte) map[string][][]any {
	t.Helper()
	workspace := t.TempDir()
	storage := newImporterTestStore(t, filepath.Join(workspace, "data"))
	if _, err := Run(context.Background(), workspace, bytes.NewReader(archive), storage); err != nil {
		t.Fatal(err)
	}
	db, release, err := storage.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	return map[string][][]any{
		"node": dumpQuery(t, db, `SELECT node_id, node_type, name, name_normalized, path, path_normalized,
            parent_id, locator_json, attributes_json FROM node ORDER BY node_id`),
		"limit_fact": dumpQuery(t, db, `SELECT limit_id, node_id, kind, business_day_offset,
            local_time_seconds, time_zone, finish_sort_seconds, duration_seconds, source_text, origin, certainty
            FROM limit_fact ORDER BY limit_id`),
		"relation": dumpQuery(t, db, `SELECT relation_id, from_id, to_id, relation_kind, origin, certainty,
            evidence_json FROM relation ORDER BY relation_id`),
	}
}

func dumpQuery(t *testing.T, db *sql.DB, query string) [][]any {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var result [][]any
	for rows.Next() {
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for index := range values {
			destinations[index] = &values[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			t.Fatal(err)
		}
		result = append(result, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func demoArchive(t *testing.T) []byte {
	t.Helper()
	files := make(map[string]string, 3)
	for _, name := range []string{"manifest.json", "nodes.ndjson", "relations.ndjson"} {
		contents, err := os.ReadFile(filepath.Join("..", "..", "examples", "demo", "snapshot", name))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = string(contents)
	}
	return makeArchive(t, files)
}

func duplicateLimitArchive(t *testing.T) []byte {
	t.Helper()
	limit := `{"id":"DUPLICATE","kind":"raw","sourceText":"raw","origin":"manual","certainty":"declared"}`
	nodes := []string{
		`{"type":"job_network","id":"NET","name":"Network","parentId":null,"limitFacts":[]}`,
		`{"type":"job","id":"JOB-A","name":"A","parentId":"NET","limitFacts":[` + limit + `]}`,
		`{"type":"job","id":"JOB-B","name":"B","parentId":"NET","limitFacts":[` + limit + `]}`,
	}
	manifest := map[string]any{
		"schemaVersion": "0.5", "snapshotId": "duplicate-limit", "generatedAt": "2026-08-08T00:00:00Z",
		"nodeCount": len(nodes), "relationCount": 0,
		"producer": map[string]string{"name": "test", "version": "1"},
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return makeArchive(t, map[string]string{
		"manifest.json":    string(manifestJSON),
		"nodes.ndjson":     strings.Join(nodes, "\n") + "\n",
		"relations.ndjson": "",
	})
}

func singleNodeArchive(t *testing.T, nodeID string) []byte {
	t.Helper()
	nodes := fmt.Sprintf("{\"type\":\"job\",\"id\":%q,\"name\":%q,\"limitFacts\":[]}\n", nodeID, nodeID)
	manifest := fmt.Sprintf(`{"schemaVersion":"0.5","snapshotId":%q,"generatedAt":"2026-08-08T00:00:00Z","nodeCount":1,"relationCount":0,"producer":{"name":"test","version":"1"}}`, nodeID)
	return makeArchive(t, map[string]string{
		"manifest.json": manifest, "nodes.ndjson": nodes, "relations.ndjson": "",
	})
}

func makeArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		contents := files[name]
		header := &tar.Header{Name: name, Mode: 0o600, Size: int64(len(contents)), Typeflag: tar.TypeReg}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tarWriter, contents); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}

func newImporterTestStore(t *testing.T, directory string) *store.Store {
	t.Helper()
	storage, err := store.New(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := storage.Close(); err != nil {
			t.Errorf("Close(): %v", err)
		}
	})
	return storage
}

func assertCount(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("%s rows = %d, want %d", table, got, want)
	}
}
