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
	"time"

	"batchscope/internal/limitscan"
	"batchscope/internal/pathtree"
	"batchscope/internal/snapshot"
	"batchscope/internal/store"
	"batchscope/internal/traversal"
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

	db, generation, release, err := storage.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	assertCount(t, db, "node", 9)
	assertCount(t, db, "limit_fact", 3)
	assertCount(t, db, "relation", 8)
	if generation.SnapshotID == "" || generation.SchemaVersion != "0.5" || generation.GeneratedAt.IsZero() ||
		generation.NodeCount != 9 || generation.RelationCount != 8 || generation.LimitCount != 3 || len(generation.Fingerprint) != 64 {
		t.Fatalf("generation metadata = %#v", generation)
	}

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

func TestRunDemoSnapshotSupportsDownstreamLimitScan(t *testing.T) {
	ctx := context.Background()
	archive := demoArchive(t)
	workspace := t.TempDir()
	storage := newImporterTestStore(t, filepath.Join(workspace, "data"))
	if _, err := Run(ctx, workspace, bytes.NewReader(archive), storage); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	db, _, release, err := storage.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	reached, err := traversal.Traverse(ctx, db, "JOB-A")
	if err != nil {
		t.Fatalf("Traverse() error = %v", err)
	}
	got, err := limitscan.Scan(ctx, db, reached)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	if len(got.Downstream.FinishByGroups) != 1 {
		t.Fatalf("finish_by groups = %#v, want one group", got.Downstream.FinishByGroups)
	}
	finishGroup := got.Downstream.FinishByGroups[0]
	finishItems := finishGroup.Items
	if finishGroup.Total != 2 || len(finishItems) != 2 {
		t.Fatalf("finish_by total = %d, items = %d, want both 2", finishGroup.Total, len(finishItems))
	}
	if ids := limitFactIDs(finishItems); !reflect.DeepEqual(ids, []string{
		"LIMIT-JOB-C-FINISH",
		"LIMIT-JOB-B-FINISH",
	}) {
		t.Errorf("finish_by IDs = %v", ids)
	}
	if got.Downstream.MaxElapsed.Total != 1 || got.Downstream.MaxElapsed.Items[0].Fact.ID != "LIMIT-JOB-B-ELAPSED" {
		t.Errorf("max_elapsed = %#v, want LIMIT-JOB-B-ELAPSED", got.Downstream.MaxElapsed)
	}
	if total := len(finishItems) + got.Downstream.MaxElapsed.Total + got.Downstream.Raw.Total; total != 3 {
		t.Errorf("downstream limit total = %d, want 3", total)
	}
	if finishItems[0].ScopeRoot == nil || finishItems[0].ScopeRoot.ID != "NET-CLOSE" {
		t.Errorf("JOB-C ScopeRoot = %#v, want NET-CLOSE", finishItems[0].ScopeRoot)
	}
	if finishItems[1].ScopeRoot != nil || got.Downstream.MaxElapsed.Items[0].ScopeRoot != nil {
		t.Errorf("JOB-B ScopeRoots = finish %#v, elapsed %#v, want nil", finishItems[1].ScopeRoot, got.Downstream.MaxElapsed.Items[0].ScopeRoot)
	}
}

func TestSearchKeepsOneSnapshotGenerationAcrossConcurrentSwitch(t *testing.T) {
	startedAt := time.Now()
	ctx := context.Background()
	workspace := t.TempDir()
	storage := newImporterTestStore(t, filepath.Join(workspace, "data"))
	if _, err := Run(ctx, workspace, bytes.NewReader(generationArchive(t, "OLD")), storage); err != nil {
		t.Fatalf("initial Run() error = %v", err)
	}

	oldDB, oldGeneration, releaseOld, err := storage.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	traversed := make(chan struct{})
	resumeOld := make(chan struct{})
	resumedOld := false
	defer func() {
		if !resumedOld {
			close(resumeOld)
		}
	}()
	oldResult := make(chan generationSearch, 1)
	oldError := make(chan error, 1)
	go func() {
		defer releaseOld()
		reached, err := traversal.Traverse(ctx, oldDB, "TARGET")
		if err != nil {
			oldError <- err
			return
		}
		close(traversed)
		<-resumeOld
		limits, err := limitscan.Scan(ctx, oldDB, reached)
		if err != nil {
			oldError <- err
			return
		}
		tree, err := pathtree.Build(ctx, reached, limits)
		if err != nil {
			oldError <- err
			return
		}
		oldResult <- generationSearch{traversal: reached, limits: limits, tree: tree}
	}()

	select {
	case <-traversed:
	case err := <-oldError:
		t.Fatalf("old generation Traverse: %v", err)
	}
	if _, err := Run(ctx, workspace, bytes.NewReader(generationArchive(t, "NEW")), storage); err != nil {
		t.Fatalf("replacement Run() error = %v", err)
	}
	newDB, newGeneration, releaseNew, err := storage.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	newSearch, err := searchGeneration(ctx, newDB)
	releaseNew()
	if err != nil {
		t.Fatal(err)
	}
	close(resumeOld)
	resumedOld = true

	select {
	case err := <-oldError:
		t.Fatalf("old generation search: %v", err)
	case oldSearch := <-oldResult:
		assertSearchGeneration(t, oldSearch, "OLD", "NEW")
	}
	assertSearchGeneration(t, newSearch, "NEW", "OLD")
	if oldGeneration.SnapshotID != "OLD" || newGeneration.SnapshotID != "NEW" {
		t.Fatalf("generation metadata mixed across switch: old=%q new=%q", oldGeneration.SnapshotID, newGeneration.SnapshotID)
	}
	t.Logf("concurrent snapshot generation switch runtime: %s", time.Since(startedAt))
}

type generationSearch struct {
	traversal traversal.Result
	limits    limitscan.Result
	tree      pathtree.Result
}

func searchGeneration(ctx context.Context, db *sql.DB) (generationSearch, error) {
	reached, err := traversal.Traverse(ctx, db, "TARGET")
	if err != nil {
		return generationSearch{}, err
	}
	limits, err := limitscan.Scan(ctx, db, reached)
	if err != nil {
		return generationSearch{}, err
	}
	tree, err := pathtree.Build(ctx, reached, limits)
	if err != nil {
		return generationSearch{}, err
	}
	return generationSearch{traversal: reached, limits: limits, tree: tree}, nil
}

func assertSearchGeneration(t *testing.T, result generationSearch, want, unwanted string) {
	t.Helper()
	reached := make(map[string]struct{}, len(result.traversal.Nodes))
	for _, node := range result.traversal.Nodes {
		reached[node.Node.ID] = struct{}{}
	}
	if _, ok := reached[want]; !ok {
		t.Errorf("reached nodes omit generation %q: %v", want, reached)
	}
	if _, mixed := reached[unwanted]; mixed {
		t.Errorf("reached nodes mix generation %q: %v", unwanted, reached)
	}
	if got := limitFactIDs(result.limits.Downstream.Raw.Items); !reflect.DeepEqual(got, []string{"LIMIT-" + want}) {
		t.Errorf("downstream raw limits = %v, want generation %q only", got, want)
	}
	if _, ok := result.tree.LimitReferences[want]; !ok {
		t.Errorf("path tree omits limit owner %q", want)
	}
	if _, mixed := result.tree.LimitReferences[unwanted]; mixed {
		t.Errorf("path tree mixes limit owner %q", unwanted)
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
	db, _, release, err := storage.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	assertCount(t, db, "node", 9)
	release()
	generations, err := filepath.Glob(filepath.Join(dataDirectory, "generation-*.db"))
	if err != nil || len(generations) != 1 {
		t.Fatalf("generation databases after failure = %v, error %v", generations, err)
	}
	if _, err := os.Stat(filepath.Join(dataDirectory, "importing.db")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("importing.db after failure: %v", err)
	}
}

func TestCapacityFailureKeepsCurrentGeneration(t *testing.T) {
	workspace := t.TempDir()
	dataDirectory := filepath.Join(workspace, "data")
	storage := newImporterTestStore(t, dataDirectory)
	if _, err := Run(context.Background(), workspace, bytes.NewReader(singleNodeArchive(t, "CURRENT")), storage); err != nil {
		t.Fatal(err)
	}

	manifest := `{"schemaVersion":"0.5","snapshotId":"too-large","generatedAt":"2026-08-10T00:00:00Z","nodeCount":10001,"relationCount":0,"producer":{"name":"test","version":"1"}}`
	overCapacity := makeArchive(t, map[string]string{
		"manifest.json": manifest, "nodes.ndjson": "not-json\n", "relations.ndjson": "",
	})
	_, err := Run(context.Background(), workspace, bytes.NewReader(overCapacity), storage)
	var snapshotErr *snapshot.Error
	if !errors.As(err, &snapshotErr) || snapshotErr.Kind != snapshot.ErrorCapacityExceeded {
		t.Fatalf("Run() error = %v, want capacity error", err)
	}
	db, generation, release, err := storage.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if got := queryValueForImporterTest(t, db); got != "CURRENT" || generation.SnapshotID != "CURRENT" {
		t.Fatalf("active generation after capacity failure: value=%q metadata=%#v", got, generation)
	}
	if _, err := os.Stat(filepath.Join(dataDirectory, "importing.db")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("importing.db after capacity failure: %v", err)
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
	completeImport = func(ctx context.Context, operation *store.Import, generation store.Generation) error {
		if err := originalComplete(ctx, operation, generation); err != nil {
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
	db, _, release, err := storage.Acquire()
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
	db, _, release, err := storage.Acquire()
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

func generationArchive(t *testing.T, generation string) []byte {
	t.Helper()
	nodes := strings.Join([]string{
		`{"type":"job","id":"TARGET","name":"TARGET","limitFacts":[]}`,
		fmt.Sprintf(`{"type":"job","id":%q,"name":%q,"limitFacts":[{"id":%q,"kind":"raw","sourceText":%q,"origin":"scheduler","certainty":"declared"}]}`,
			generation, generation, "LIMIT-"+generation, generation),
	}, "\n") + "\n"
	relations := fmt.Sprintf(`{"fromId":"TARGET","toId":%q,"kind":"precedes","origin":"scheduler","certainty":"declared"}`+"\n", generation)
	manifest := fmt.Sprintf(`{"schemaVersion":"0.5","snapshotId":%q,"generatedAt":"2026-08-10T00:00:00Z","nodeCount":2,"relationCount":1,"producer":{"name":"test","version":"1"}}`, generation)
	return makeArchive(t, map[string]string{
		"manifest.json": manifest, "nodes.ndjson": nodes, "relations.ndjson": relations,
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

func queryValueForImporterTest(t *testing.T, db *sql.DB) string {
	t.Helper()
	var value string
	if err := db.QueryRow("SELECT node_id FROM node").Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func limitFactIDs(items []limitscan.Item) []string {
	ids := make([]string, len(items))
	for index, item := range items {
		ids[index] = item.Fact.ID
	}
	return ids
}
