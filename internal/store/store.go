// Package store は、検索用SQLiteの作成、切替、参照寿命を管理する。
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

const (
	currentDatabaseName   = "current.db"
	importingDatabaseName = "importing.db"
)

// State は、検索用SQLiteと取込処理の状態を表す。
type State string

const (
	StateEmpty     State = "empty"
	StateImporting State = "importing"
	StateReady     State = "ready"
)

var (
	ErrNoDatabase       = errors.New("search database is not ready")
	ErrImportInProgress = errors.New("snapshot import is already in progress")
	ErrImportFinished   = errors.New("snapshot import is already finished")
	ErrClosed           = errors.New("store is closed")
	// ErrRetiredCleanup は、検索先の切替成功後に退避済みSQLiteの後始末が失敗したことを示す。
	ErrRetiredCleanup = errors.New("retired database cleanup failed")

	// renameFileは、SQLite切替時のrename失敗をテストから再現するため差し替え可能にする。
	renameFile = os.Rename
	// openSQLiteは、有効化後のSQLiteを開けない場合をテストから再現するため差し替え可能にする。
	openSQLite = openDatabase
	// cleanupRetiredDatabaseは、退避済みSQLiteの後始末失敗を再現するテスト専用注入点である。
	cleanupRetiredDatabase = closeAndRemove
)

// Store は、現在の検索先と進行中の取込を所有する。
type Store struct {
	mu        sync.Mutex
	directory string
	current   *database
	importing *Import
	retiredID uint64
	closed    bool
}

type database struct {
	db      *sql.DB
	path    string
	refs    int
	retired bool
}

// Import は、一件のimporting.db作成処理を表す。
// DBを使う処理を終えてからCompleteまたはAbortを一度だけ呼び出す。
type Import struct {
	store    *Store
	db       *sql.DB
	finished bool
}

// New はデータディレクトリを準備し、以前の一時SQLiteを破棄してemptyから開始する。
func New(directory string) (*Store, error) {
	if directory == "" {
		return nil, errors.New("data directory is empty")
	}
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	if err := removeStartupDatabases(directory); err != nil {
		return nil, err
	}
	return &Store{directory: directory}, nil
}

// State は現在のサービス状態を返す。
func (s *Store) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stateLocked()
}

// Ready は検索可能なSQLiteがあるかを返す。
// importing中も、切替前のSQLiteがあればtrueを返す。
func (s *Store) Ready() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current != nil
}

// BeginImport はimporting.dbを新規作成し、テーブルを準備する。
// 同時に開始できる取込は一件だけである。
func (s *Store) BeginImport(ctx context.Context) (*Import, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, ErrClosed
	}
	if s.importing != nil {
		return nil, ErrImportInProgress
	}

	path := filepath.Join(s.directory, importingDatabaseName)
	if err := removeDatabaseFiles(path); err != nil {
		return nil, fmt.Errorf("remove previous importing database: %w", err)
	}
	db, err := openSQLite(ctx, path)
	if err != nil {
		_ = removeDatabaseFiles(path)
		return nil, fmt.Errorf("open importing database: %w", err)
	}
	if err := createTables(ctx, db); err != nil {
		_ = db.Close()
		_ = removeDatabaseFiles(path)
		return nil, fmt.Errorf("create importing database tables: %w", err)
	}

	operation := &Import{store: s, db: db}
	s.importing = operation
	return operation, nil
}

// Acquire は呼出時点の検索用SQLiteと、冪等な解放関数を返す。
// 切替後も、解放されるまでは返したDBを閉じない。
func (s *Store) Acquire() (*sql.DB, func(), error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, nil, ErrClosed
	}
	active := s.current
	if active == nil {
		s.mu.Unlock()
		return nil, nil, ErrNoDatabase
	}
	active.refs++
	s.mu.Unlock()

	var once sync.Once
	release := func() {
		once.Do(func() {
			s.release(active)
		})
	}
	return active.db, release, nil
}

// Close は新しい処理の開始を拒否し、未使用のSQLiteを閉じる。
// 使用中の検索用SQLiteは、最後のAcquire参照が解放された時点で閉じる。
func (s *Store) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	operation := s.importing
	s.importing = nil
	if operation != nil {
		operation.finished = true
	}
	active := s.current
	s.current = nil
	var closable *database
	if active != nil {
		active.retired = true
		if active.refs == 0 {
			closable = active
		}
	}
	s.mu.Unlock()

	var result error
	if operation != nil {
		if err := operation.db.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("close importing database: %w", err))
		}
		if err := removeDatabaseFiles(filepath.Join(s.directory, importingDatabaseName)); err != nil {
			result = errors.Join(result, fmt.Errorf("remove importing database: %w", err))
		}
	}
	if closable != nil {
		result = errors.Join(result, closeAndRemove(closable))
	}
	return result
}

// DB は取込先SQLiteを返す。
// CompleteまたはAbortの開始後は使用してはならない。
func (i *Import) DB() *sql.DB {
	return i.db
}

// Complete は索引とSQLiteの整合性を検査し、成功したDBだけを検索先へ切り替える。
// errors.Is(err, ErrRetiredCleanup)が真の場合、検索先の切替は成功しているため、
// 呼出側は取込失敗として扱ってはならない。
func (i *Import) Complete(ctx context.Context) error {
	s := i.store
	s.mu.Lock()
	if i.finished || s.importing != i {
		s.mu.Unlock()
		return ErrImportFinished
	}
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	s.mu.Unlock()

	if err := createIndexes(ctx, i.db); err != nil {
		return i.fail(fmt.Errorf("create search indexes: %w", err))
	}
	if err := checkDatabase(ctx, i.db); err != nil {
		return i.fail(fmt.Errorf("check importing database: %w", err))
	}
	if err := i.db.Close(); err != nil {
		return i.failAfterClose(fmt.Errorf("close importing database: %w", err))
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if i.finished || s.importing != i {
		return ErrImportFinished
	}
	if s.closed {
		i.finished = true
		s.importing = nil
		removeErr := removeDatabaseFiles(filepath.Join(s.directory, importingDatabaseName))
		return errors.Join(ErrClosed, removeErr)
	}

	old := s.current
	var retiredPath string
	if old != nil {
		s.retiredID++
		retiredPath = filepath.Join(s.directory, fmt.Sprintf(".retired-%d.db", s.retiredID))
		if err := renameFile(old.path, retiredPath); err != nil {
			return i.failAfterCloseLocked(fmt.Errorf("retire current database: %w", err))
		}
	}

	importingPath := filepath.Join(s.directory, importingDatabaseName)
	currentPath := filepath.Join(s.directory, currentDatabaseName)
	if err := renameFile(importingPath, currentPath); err != nil {
		cause := fmt.Errorf("activate importing database: %w", err)
		if old != nil {
			cause = errors.Join(cause, i.restoreCurrentLocked(old, retiredPath))
		}
		return i.failAfterCloseLocked(cause)
	}

	newDB, err := openSQLite(ctx, currentPath)
	if err != nil {
		cause := errors.Join(
			fmt.Errorf("open activated database: %w", err),
			removeDatabaseFiles(currentPath),
		)
		if old != nil {
			cause = errors.Join(cause, i.restoreCurrentLocked(old, retiredPath))
		}
		return i.failAfterCloseLocked(cause)
	}

	next := &database{db: newDB, path: currentPath}
	s.current = next
	s.importing = nil
	i.finished = true

	var result error
	if old != nil {
		old.path = retiredPath
		old.retired = true
		if old.refs == 0 {
			result = errors.Join(result, cleanupRetiredDatabase(old))
		}
	}
	if result != nil {
		return errors.Join(ErrRetiredCleanup, result)
	}
	return result
}

// Abort はimporting.dbを破棄する。
// 既存の検索用SQLiteとready状態は変更しない。
func (i *Import) Abort() error {
	s := i.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if i.finished || s.importing != i {
		return ErrImportFinished
	}
	i.finished = true
	s.importing = nil

	closeErr := i.db.Close()
	removeErr := removeDatabaseFiles(filepath.Join(s.directory, importingDatabaseName))
	return errors.Join(closeErr, removeErr)
}

func (i *Import) fail(cause error) error {
	closeErr := i.db.Close()
	return i.failAfterClose(errors.Join(cause, closeErr))
}

func (i *Import) failAfterClose(cause error) error {
	s := i.store
	s.mu.Lock()
	defer s.mu.Unlock()
	return i.failAfterCloseLocked(cause)
}

func (i *Import) failAfterCloseLocked(cause error) error {
	s := i.store
	if !i.finished && s.importing == i {
		i.finished = true
		s.importing = nil
	}
	removeErr := removeDatabaseFiles(filepath.Join(s.directory, importingDatabaseName))
	return errors.Join(cause, removeErr)
}

func (i *Import) restoreCurrentLocked(old *database, retiredPath string) error {
	if err := renameFile(retiredPath, old.path); err != nil {
		// 復元失敗後は内部のpathと実際の配置が一致しないため、旧DBを検索先として公開しない。
		i.store.current = nil
		old.path = retiredPath
		old.retired = true
		var cleanupErr error
		if old.refs == 0 {
			cleanupErr = closeAndRemove(old)
		}
		return errors.Join(fmt.Errorf("restore current database: %w", err), cleanupErr)
	}
	return nil
}

func (s *Store) release(active *database) {
	s.mu.Lock()
	active.refs--
	shouldClose := active.retired && active.refs == 0
	s.mu.Unlock()
	if shouldClose {
		_ = closeAndRemove(active)
	}
}

func (s *Store) stateLocked() State {
	if s.importing != nil {
		return StateImporting
	}
	if s.current != nil {
		return StateReady
	}
	return StateEmpty
}

func openDatabase(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// foreign_keysは接続単位であり、current.dbのパスは切替後に次のスナップショットへ再利用される。
	// 退避済みハンドルが同じDSNへ再接続して別データを読まないよう、単一の接続を期限なしで保持する。
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func createTables(ctx context.Context, db *sql.DB) error {
	const statements = `
CREATE TABLE node (
    node_id TEXT PRIMARY KEY,
    node_type TEXT NOT NULL,
    name TEXT NOT NULL,
    name_normalized TEXT NOT NULL,
    path TEXT,
    path_normalized TEXT,
    parent_id TEXT REFERENCES node(node_id),
    locator_json TEXT,
    attributes_json TEXT
);
CREATE TABLE limit_fact (
    limit_id TEXT PRIMARY KEY,
    node_id TEXT NOT NULL REFERENCES node(node_id),
    kind TEXT NOT NULL,
    business_day_offset INTEGER,
    local_time_seconds INTEGER,
    time_zone TEXT,
    finish_sort_seconds INTEGER,
    duration_seconds INTEGER,
    source_text TEXT,
    origin TEXT NOT NULL,
    certainty TEXT NOT NULL
);
CREATE TABLE relation (
    relation_id TEXT PRIMARY KEY,
    from_id TEXT NOT NULL REFERENCES node(node_id),
    to_id TEXT NOT NULL REFERENCES node(node_id),
    relation_kind TEXT NOT NULL,
    origin TEXT NOT NULL,
    certainty TEXT NOT NULL,
    evidence_json TEXT
);`
	_, err := db.ExecContext(ctx, statements)
	return err
}

func createIndexes(ctx context.Context, db *sql.DB) error {
	const statements = `
CREATE UNIQUE INDEX idx_node_id ON node(node_id);
CREATE INDEX idx_node_parent ON node(parent_id);
CREATE INDEX idx_node_name_exact ON node(node_type, name_normalized);
CREATE INDEX idx_node_path_exact ON node(node_type, path_normalized);
CREATE INDEX idx_relation_from ON relation(from_id);
CREATE INDEX idx_relation_to ON relation(to_id);
CREATE INDEX idx_limit_node ON limit_fact(node_id);
CREATE INDEX idx_limit_finish ON limit_fact(kind, time_zone, finish_sort_seconds);
CREATE INDEX idx_limit_elapsed ON limit_fact(kind, duration_seconds);`
	_, err := db.ExecContext(ctx, statements)
	return err
}

func checkDatabase(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	if rows.Next() {
		_ = rows.Close()
		return errors.New("foreign key check failed")
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	var result string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("integrity check returned %q", result)
	}
	if err := checkParentHierarchy(ctx, db); err != nil {
		return err
	}
	if err := checkRepresentativeSearches(ctx, db); err != nil {
		return err
	}
	return nil
}

func checkParentHierarchy(ctx context.Context, db *sql.DB) error {
	// foreign_key_check後は、親なしのノードから到達できないノードがあれば親子関係に循環がある。
	// UNIONで到達済みノードを除外するため、循環を含む入力でも再帰CTEは停止する。
	const query = `
WITH RECURSIVE reachable(node_id) AS (
	SELECT node_id
	FROM node
	WHERE parent_id IS NULL
	UNION
	SELECT child.node_id
	FROM node AS child
	JOIN reachable ON child.parent_id = reachable.node_id
)
SELECT EXISTS(
	SELECT 1
	FROM node
	LEFT JOIN reachable USING (node_id)
	WHERE reachable.node_id IS NULL
)`
	var cyclic bool
	if err := db.QueryRowContext(ctx, query).Scan(&cyclic); err != nil {
		return fmt.Errorf("check parent hierarchy: %w", err)
	}
	if cyclic {
		return errors.New("parent hierarchy contains a cycle")
	}
	return nil
}

func checkRepresentativeSearches(ctx context.Context, db *sql.DB) error {
	var nodeID, nodeType, normalizedName string
	var normalizedPath sql.NullString
	err := db.QueryRowContext(ctx, `
SELECT node_id, node_type, name_normalized, path_normalized
FROM node
WHERE node_type IN ('job', 'job_network')
ORDER BY node_id
LIMIT 1`).Scan(&nodeID, &nodeType, &normalizedName, &normalizedPath)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("select representative node: %w", err)
	}

	checks := []struct {
		name  string
		query string
		args  []any
	}{
		{
			name:  "ID",
			query: `SELECT EXISTS(SELECT 1 FROM node WHERE node_id = ?)`,
			args:  []any{nodeID},
		},
		{
			name:  "name",
			query: `SELECT EXISTS(SELECT 1 FROM node WHERE node_type = ? AND name_normalized = ? AND node_id = ?)`,
			args:  []any{nodeType, normalizedName, nodeID},
		},
	}
	if normalizedPath.Valid {
		checks = append(checks, struct {
			name  string
			query string
			args  []any
		}{
			name:  "path",
			query: `SELECT EXISTS(SELECT 1 FROM node WHERE node_type = ? AND path_normalized = ? AND node_id = ?)`,
			args:  []any{nodeType, normalizedPath.String, nodeID},
		})
	}

	for _, check := range checks {
		var found bool
		if err := db.QueryRowContext(ctx, check.query, check.args...).Scan(&found); err != nil {
			return fmt.Errorf("run representative %s search: %w", check.name, err)
		}
		// 同名または同じパスの別ノードがあっても、代表ノード自身が検索条件を満たせば正常である。
		if !found {
			return fmt.Errorf("representative %s search omitted node %q", check.name, nodeID)
		}
	}
	return nil
}

func removeStartupDatabases(directory string) error {
	for _, name := range []string{currentDatabaseName, importingDatabaseName} {
		if err := removeDatabaseFiles(filepath.Join(directory, name)); err != nil {
			return fmt.Errorf("remove stale %s: %w", name, err)
		}
	}
	retired, err := filepath.Glob(filepath.Join(directory, ".retired-*.db*"))
	if err != nil {
		return fmt.Errorf("find stale retired databases: %w", err)
	}
	for _, path := range retired {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove stale retired database: %w", err)
		}
	}
	return nil
}

func removeDatabaseFiles(path string) error {
	var result error
	for _, candidate := range []string{path, path + "-journal", path + "-shm", path + "-wal"} {
		if err := os.Remove(candidate); err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, err)
		}
	}
	return result
}

func closeAndRemove(active *database) error {
	closeErr := active.db.Close()
	removeErr := removeDatabaseFiles(active.path)
	return errors.Join(closeErr, removeErr)
}
