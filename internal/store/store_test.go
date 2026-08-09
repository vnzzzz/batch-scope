package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func TestNewStartsEmptyAndRemovesStaleDatabases(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{currentDatabaseName, importingDatabaseName, ".retired-9.db"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("stale"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	storage := newTestStore(t, directory)
	if got := storage.State(); got != StateEmpty {
		t.Fatalf("state = %q, want %q", got, StateEmpty)
	}
	if storage.Ready() {
		t.Fatal("Ready() = true, want false")
	}
	for _, name := range []string{currentDatabaseName, importingDatabaseName, ".retired-9.db"} {
		if _, err := os.Stat(filepath.Join(directory, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s still exists: %v", name, err)
		}
	}
	if _, _, err := storage.Acquire(); !errors.Is(err, ErrNoDatabase) {
		t.Fatalf("Acquire() error = %v, want %v", err, ErrNoDatabase)
	}
}

func TestImportCreatesDocumentedTablesAndIndexes(t *testing.T) {
	storage := newTestStore(t, t.TempDir())
	operation := beginTestImport(t, storage)

	wantColumns := map[string][]string{
		"node": {
			"node_id", "node_type", "name", "name_normalized", "path", "path_normalized",
			"parent_id", "locator_json", "attributes_json",
		},
		"limit_fact": {
			"limit_id", "node_id", "kind", "business_day_offset", "local_time_seconds",
			"time_zone", "finish_sort_seconds", "duration_seconds", "source_text", "origin", "certainty",
		},
		"relation": {
			"relation_id", "from_id", "to_id", "relation_kind", "origin", "certainty", "evidence_json",
		},
	}
	for table, want := range wantColumns {
		if got := tableColumns(t, operation.DB(), table); !reflect.DeepEqual(got, want) {
			t.Errorf("%s columns = %v, want %v", table, got, want)
		}
	}

	if err := operation.Complete(context.Background()); err != nil {
		t.Fatal(err)
	}
	db, release, err := storage.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	wantIndexes := map[string][]string{
		"idx_node_id":         {"node_id"},
		"idx_node_parent":     {"parent_id"},
		"idx_node_name_exact": {"node_type", "name_normalized"},
		"idx_node_path_exact": {"node_type", "path_normalized"},
		"idx_relation_from":   {"from_id"},
		"idx_relation_to":     {"to_id"},
		"idx_limit_node":      {"node_id"},
		"idx_limit_finish":    {"kind", "time_zone", "finish_sort_seconds"},
		"idx_limit_elapsed":   {"kind", "duration_seconds"},
	}
	if got := databaseIndexes(t, db); !reflect.DeepEqual(got, wantIndexes) {
		t.Fatalf("indexes = %v, want %v", got, wantIndexes)
	}
	if !indexIsUnique(t, db, "node", "idx_node_id") {
		t.Fatal("idx_node_id is not unique")
	}
}

func TestStateTransitionsAndSingleImport(t *testing.T) {
	storage := newTestStore(t, t.TempDir())
	first := beginTestImport(t, storage)
	if got := storage.State(); got != StateImporting {
		t.Fatalf("state during initial import = %q, want %q", got, StateImporting)
	}
	if storage.Ready() {
		t.Fatal("Ready() during initial import = true, want false")
	}
	if _, err := storage.BeginImport(context.Background()); !errors.Is(err, ErrImportInProgress) {
		t.Fatalf("second BeginImport() error = %v, want %v", err, ErrImportInProgress)
	}
	if err := first.Abort(); err != nil {
		t.Fatal(err)
	}
	if got := storage.State(); got != StateEmpty {
		t.Fatalf("state after initial failure = %q, want %q", got, StateEmpty)
	}

	activateValue(t, storage, "first")
	update := beginTestImport(t, storage)
	if got := storage.State(); got != StateImporting {
		t.Fatalf("state during update = %q, want %q", got, StateImporting)
	}
	if !storage.Ready() {
		t.Fatal("Ready() during update = false, want true")
	}
	if err := update.Abort(); err != nil {
		t.Fatal(err)
	}
	if got := storage.State(); got != StateReady {
		t.Fatalf("state after update failure = %q, want %q", got, StateReady)
	}
	if got := activeValue(t, storage); got != "first" {
		t.Fatalf("active value = %q, want first", got)
	}
}

func TestAcquireKeepsOldDatabaseUntilReleaseAfterSwitch(t *testing.T) {
	directory := t.TempDir()
	storage := newTestStore(t, directory)
	activateValue(t, storage, "old")

	oldDB, releaseOld, err := storage.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	activateValue(t, storage, "new")

	if got := queryValue(t, oldDB); got != "old" {
		t.Fatalf("old database value after switch = %q, want old", got)
	}
	if got := activeValue(t, storage); got != "new" {
		t.Fatalf("new database value = %q, want new", got)
	}
	retired, err := filepath.Glob(filepath.Join(directory, ".retired-*.db"))
	if err != nil {
		t.Fatal(err)
	}
	if len(retired) != 1 {
		t.Fatalf("retired databases before release = %v, want one", retired)
	}

	releaseOld()
	releaseOld()
	if err := oldDB.Ping(); err == nil {
		t.Fatal("old database is still open after release")
	}
	retired, err = filepath.Glob(filepath.Join(directory, ".retired-*.db"))
	if err != nil {
		t.Fatal(err)
	}
	if len(retired) != 0 {
		t.Fatalf("retired databases after release = %v, want none", retired)
	}
}

func TestAcquiredDatabaseKeepsSnapshotAcrossRepeatedQueriesAfterSwitch(t *testing.T) {
	storage := newTestStore(t, t.TempDir())
	activateValue(t, storage, "old")

	oldDB, releaseOld, err := storage.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer releaseOld()

	activateValue(t, storage, "new")
	for queryNumber := 1; queryNumber <= 5; queryNumber++ {
		if got := queryValue(t, oldDB); got != "old" {
			t.Fatalf("old database value after switch on query %d = %q, want old", queryNumber, got)
		}
	}
}

func TestSearchesCanRunWhileDatabaseSwitches(t *testing.T) {
	storage := newTestStore(t, t.TempDir())
	activateValue(t, storage, "old")

	operation := beginTestImport(t, storage)
	if _, err := operation.DB().Exec(`INSERT INTO node
        (node_id, node_type, name, name_normalized)
        VALUES ('new', 'job', 'new', 'new')`); err != nil {
		t.Fatal(err)
	}

	var stop atomic.Bool
	errorsFromSearches := make(chan error, 8)
	var searches sync.WaitGroup
	for range 8 {
		searches.Add(1)
		go func() {
			defer searches.Done()
			for !stop.Load() {
				db, release, err := storage.Acquire()
				if err != nil {
					errorsFromSearches <- err
					return
				}
				var value string
				err = db.QueryRow("SELECT node_id FROM node").Scan(&value)
				release()
				if err != nil {
					errorsFromSearches <- err
					return
				}
				if value != "old" && value != "new" {
					errorsFromSearches <- errors.New("search returned an unexpected database value")
					return
				}
			}
		}()
	}

	if err := operation.Complete(context.Background()); err != nil {
		t.Fatal(err)
	}
	stop.Store(true)
	searches.Wait()
	close(errorsFromSearches)
	for err := range errorsFromSearches {
		t.Errorf("concurrent search: %v", err)
	}
	if got := activeValue(t, storage); got != "new" {
		t.Fatalf("active value = %q, want new", got)
	}
}

func TestFailedUpdateDoesNotReplaceCurrentDatabase(t *testing.T) {
	directory := t.TempDir()
	storage := newTestStore(t, directory)
	activateValue(t, storage, "current")
	currentPath := filepath.Join(directory, currentDatabaseName)
	before, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatal(err)
	}

	operation := beginTestImport(t, storage)
	if _, err := operation.DB().Exec("DROP TABLE node"); err != nil {
		t.Fatal(err)
	}
	if err := operation.Complete(context.Background()); err == nil {
		t.Fatal("Complete() succeeded for an invalid importing database")
	}
	if got := storage.State(); got != StateReady {
		t.Fatalf("state = %q, want %q", got, StateReady)
	}
	if got := activeValue(t, storage); got != "current" {
		t.Fatalf("active value = %q, want current", got)
	}
	after, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatal("current.db changed after failed update")
	}
	if _, err := os.Stat(filepath.Join(directory, importingDatabaseName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("importing.db remains after failure: %v", err)
	}
}

func TestCompleteRejectsParentCycleAndKeepsCurrentDatabase(t *testing.T) {
	directory := t.TempDir()
	storage := newTestStore(t, directory)
	activateValue(t, storage, "current")
	operation := beginTestImport(t, storage)

	tx, err := operation.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("PRAGMA defer_foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO node
        (node_id, node_type, name, name_normalized, parent_id)
        VALUES ('A', 'management_unit', 'A', 'a', 'B'),
               ('B', 'management_unit', 'B', 'b', 'A')`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if err := operation.Complete(context.Background()); err == nil {
		t.Fatal("Complete() succeeded with a parent hierarchy cycle")
	}
	assertFailedUpdateKeepsCurrent(t, storage, directory, "current")
}

func TestCompleteRepresentativeNameSearchAllowsDuplicateNames(t *testing.T) {
	storage := newTestStore(t, t.TempDir())
	operation := beginTestImport(t, storage)
	if _, err := operation.DB().Exec(`INSERT INTO node
        (node_id, node_type, name, name_normalized, path, path_normalized)
        VALUES ('A', 'job', 'Same', 'same', '/z', '/z'),
               ('B', 'job', 'Same', 'same', '/a', '/a')`); err != nil {
		t.Fatal(err)
	}
	if err := operation.Complete(context.Background()); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if got := storage.State(); got != StateReady {
		t.Fatalf("state = %q, want %q", got, StateReady)
	}
}

func TestCompleteKeepsCurrentDatabaseWhenActivationRenameFails(t *testing.T) {
	directory := t.TempDir()
	storage := newTestStore(t, directory)
	activateValue(t, storage, "current")
	operation := beginTestImport(t, storage)
	insertValue(t, operation.DB(), "replacement")

	activationErr := errors.New("activation rename failed")
	currentPath := filepath.Join(directory, currentDatabaseName)
	importingPath := filepath.Join(directory, importingDatabaseName)
	originalRename := renameFile
	renameFile = func(oldPath, newPath string) error {
		if oldPath == importingPath && newPath == currentPath {
			return activationErr
		}
		return originalRename(oldPath, newPath)
	}
	t.Cleanup(func() {
		renameFile = originalRename
	})

	err := operation.Complete(context.Background())
	if !errors.Is(err, activationErr) {
		t.Fatalf("Complete() error = %v, want %v", err, activationErr)
	}
	assertFailedUpdateKeepsCurrent(t, storage, directory, "current")
}

func TestCompleteKeepsCurrentDatabaseWhenActivatedDatabaseCannotOpen(t *testing.T) {
	directory := t.TempDir()
	storage := newTestStore(t, directory)
	activateValue(t, storage, "current")
	operation := beginTestImport(t, storage)
	insertValue(t, operation.DB(), "replacement")

	openErr := errors.New("open activated database failed")
	currentPath := filepath.Join(directory, currentDatabaseName)
	originalOpen := openSQLite
	openSQLite = func(ctx context.Context, path string) (*sql.DB, error) {
		if path == currentPath {
			return nil, openErr
		}
		return originalOpen(ctx, path)
	}
	t.Cleanup(func() {
		openSQLite = originalOpen
	})

	err := operation.Complete(context.Background())
	if !errors.Is(err, openErr) {
		t.Fatalf("Complete() error = %v, want %v", err, openErr)
	}
	assertFailedUpdateKeepsCurrent(t, storage, directory, "current")
}

func TestCompleteBecomesEmptyWhenCurrentDatabaseCannotBeRestored(t *testing.T) {
	directory := t.TempDir()
	storage := newTestStore(t, directory)
	activateValue(t, storage, "current")
	oldDB, releaseOld, err := storage.Acquire()
	if err != nil {
		t.Fatal(err)
	}

	operation := beginTestImport(t, storage)
	insertValue(t, operation.DB(), "replacement")
	activationErr := errors.New("activation rename failed")
	restoreErr := errors.New("restore rename failed")
	currentPath := filepath.Join(directory, currentDatabaseName)
	importingPath := filepath.Join(directory, importingDatabaseName)
	originalRename := renameFile
	renameFile = func(oldPath, newPath string) error {
		switch {
		case oldPath == importingPath && newPath == currentPath:
			return activationErr
		case filepath.Base(oldPath) == ".retired-1.db" && newPath == currentPath:
			return restoreErr
		default:
			return originalRename(oldPath, newPath)
		}
	}
	t.Cleanup(func() {
		renameFile = originalRename
	})

	err = operation.Complete(context.Background())
	if !errors.Is(err, activationErr) {
		t.Errorf("Complete() error = %v, want activation error %v", err, activationErr)
	}
	if !errors.Is(err, restoreErr) {
		t.Errorf("Complete() error = %v, want restore error %v", err, restoreErr)
	}
	if got := storage.State(); got != StateEmpty {
		t.Fatalf("state = %q, want %q", got, StateEmpty)
	}
	if storage.Ready() {
		t.Fatal("Ready() = true, want false")
	}
	if _, _, err := storage.Acquire(); !errors.Is(err, ErrNoDatabase) {
		t.Fatalf("Acquire() error = %v, want %v", err, ErrNoDatabase)
	}
	if _, err := os.Stat(currentPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("current.db exists after failed restore: %v", err)
	}
	if got := queryValue(t, oldDB); got != "current" {
		t.Fatalf("acquired database value = %q, want current", got)
	}
	if _, err := os.Stat(importingPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("importing.db remains after failure: %v", err)
	}
	retiredPath := filepath.Join(directory, ".retired-1.db")
	if _, err := os.Stat(retiredPath); err != nil {
		t.Fatalf("retired database before release: %v", err)
	}

	releaseOld()
	if err := oldDB.Ping(); err == nil {
		t.Fatal("old database is still open after release")
	}
	if _, err := os.Stat(retiredPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retired database remains after release: %v", err)
	}
}

func TestCompleteReportsRetiredCleanupAfterSuccessfulSwitch(t *testing.T) {
	directory := t.TempDir()
	storage := newTestStore(t, directory)
	activateValue(t, storage, "old")
	operation := beginTestImport(t, storage)
	insertValue(t, operation.DB(), "new")

	cleanupErr := errors.New("retired database cleanup failed")
	originalCleanup := cleanupRetiredDatabase
	cleanupRetiredDatabase = func(active *database) error {
		return errors.Join(originalCleanup(active), cleanupErr)
	}
	t.Cleanup(func() {
		cleanupRetiredDatabase = originalCleanup
	})

	err := operation.Complete(context.Background())
	if !errors.Is(err, ErrRetiredCleanup) {
		t.Fatalf("Complete() error = %v, want %v", err, ErrRetiredCleanup)
	}
	if got := storage.State(); got != StateReady {
		t.Fatalf("state = %q, want %q", got, StateReady)
	}
	if got := activeValue(t, storage); got != "new" {
		t.Fatalf("active value = %q, want new", got)
	}
}

func newTestStore(t *testing.T, directory string) *Store {
	t.Helper()
	storage, err := New(directory)
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

func beginTestImport(t *testing.T, storage *Store) *Import {
	t.Helper()
	operation, err := storage.BeginImport(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return operation
}

func activateValue(t *testing.T, storage *Store, value string) {
	t.Helper()
	operation := beginTestImport(t, storage)
	insertValue(t, operation.DB(), value)
	if err := operation.Complete(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func insertValue(t *testing.T, db *sql.DB, value string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO node
        (node_id, node_type, name, name_normalized)
        VALUES (?, 'job', ?, ?)`, value, value, value); err != nil {
		t.Fatal(err)
	}
}

func assertFailedUpdateKeepsCurrent(t *testing.T, storage *Store, directory, wantValue string) {
	t.Helper()
	if got := storage.State(); got != StateReady {
		t.Fatalf("state = %q, want %q", got, StateReady)
	}
	if !storage.Ready() {
		t.Fatal("Ready() = false, want true")
	}
	if got := activeValue(t, storage); got != wantValue {
		t.Fatalf("active value = %q, want %q", got, wantValue)
	}
	if _, err := os.Stat(filepath.Join(directory, currentDatabaseName)); err != nil {
		t.Fatalf("current.db after failure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, importingDatabaseName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("importing.db remains after failure: %v", err)
	}
}

func activeValue(t *testing.T, storage *Store) string {
	t.Helper()
	db, release, err := storage.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	return queryValue(t, db)
}

func queryValue(t *testing.T, db *sql.DB) string {
	t.Helper()
	var value string
	if err := db.QueryRow("SELECT node_id FROM node").Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func tableColumns(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var position, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&position, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return columns
}

func databaseIndexes(t *testing.T, db *sql.DB) map[string][]string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master
        WHERE type = 'index' AND name NOT LIKE 'sqlite_%'
        ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}

	indexes := make(map[string][]string, len(names))
	for _, name := range names {
		columns, err := db.Query("PRAGMA index_info(" + name + ")")
		if err != nil {
			t.Fatal(err)
		}
		for columns.Next() {
			var sequence, columnID int
			var columnName string
			if err := columns.Scan(&sequence, &columnID, &columnName); err != nil {
				_ = columns.Close()
				t.Fatal(err)
			}
			indexes[name] = append(indexes[name], columnName)
		}
		if err := columns.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return indexes
}

func indexIsUnique(t *testing.T, db *sql.DB, table, index string) bool {
	t.Helper()
	rows, err := db.Query("PRAGMA index_list(" + table + ")")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var sequence, unique, partial int
		var name, origin string
		if err := rows.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
			t.Fatal(err)
		}
		if name == index {
			return unique == 1
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return false
}
