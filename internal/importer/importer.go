// Package importer は、受信したスナップショットを検索用SQLiteへ切り替える工程をまとめる。
package importer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"batchscope/internal/snapshot"
	"batchscope/internal/store"
)

// ErrSnapshotIDConflictは、現在世代と同じSnapshotIDに異なる展開後内容が指定されたことを表す。
var ErrSnapshotIDConflict = errors.New("snapshot ID is already active with different content")

// Stageは、受信後の取込工程で外部へ通知する状態を表す。
type Stage string

const (
	StageValidating Stage = "validating"
	StageBuilding   Stage = "building"
	StageActivating Stage = "activating"
)

// Progressは段階変更時点までに安全に確定した取込情報を保持する。
type Progress struct {
	Stage         Stage
	SnapshotID    string
	NodeCount     int
	RelationCount int
	LimitCount    int
}

// ProgressFuncは段階が変わるたびに同期的に呼ばれる。nilを指定できる。
type ProgressFunc func(Progress)

// completeImportは、切替成功後の警告を取込の境界で再現するテスト専用注入点である。
var completeImport = func(ctx context.Context, operation *store.Import, generation store.Generation) error {
	return operation.Complete(ctx, generation)
}

// loadSnapshotは、検証後のSQLite登録失敗を取込境界のテストで再現する注入点である。
var loadSnapshot = snapshot.Load

// Resultは、検証後に判明した取込情報と成功時の付随結果を表す。
// 検証後の工程が失敗した場合も、errorとともに判明済みの件数を返す。
type Result struct {
	// CleanupWarning は、切替後に不要となったSQLiteの後始末だけが失敗した場合に設定される。
	// この値が設定されていても切替は成功しており、Runのerrorはnilで新しいSQLiteを検索に使用できる。
	CleanupWarning error
	// Reusedは、現在の検索世代と内容が同じためSQLiteを再構築せずに成功したことを表す。
	Reused bool
	// SnapshotIDと各件数は、Validateが成功した後に設定される。
	SnapshotID    string
	NodeCount     int
	RelationCount int
	LimitCount    int
}

// Run はスナップショットを受信、展開、検査、登録し、全ての検査に通ったSQLiteだけを検索先へ切り替える。
// temporaryDirectoryは受信アーカイブと展開ファイルを置ける既存ディレクトリでなければならない。
// 検索先の切替後に旧SQLiteの後始末だけが失敗した場合は、成功を返してResult.CleanupWarningで通知する。
func Run(ctx context.Context, temporaryDirectory string, source io.Reader, storage *store.Store) (result Result, err error) {
	operation, err := storage.BeginImport(ctx)
	if err != nil {
		return Result{}, err
	}

	archivePath, err := snapshot.Receive(ctx, temporaryDirectory, source)
	if err != nil {
		return Result{}, errors.Join(err, operation.Abort())
	}
	return ProcessReceived(ctx, temporaryDirectory, archivePath, operation, storage, nil)
}

// ProcessReceivedは、予約済みの取込と受信済みアーカイブを引き継ぎ、検査済みSQLiteだけを検索先へ切り替える。
// archivePathとoperationは成功、失敗を問わずこの関数が後始末するため、呼出側は再利用してはならない。
func ProcessReceived(
	ctx context.Context,
	temporaryDirectory string,
	archivePath string,
	operation *store.Import,
	storage *store.Store,
	notify ProgressFunc,
) (result Result, err error) {
	abortRequired := true
	defer func() {
		if abortRequired {
			err = errors.Join(err, operation.Abort())
		}
	}()
	archiveOwned := true
	defer func() {
		if archiveOwned {
			err = errors.Join(err, removeFile(archivePath))
		}
	}()

	notifyProgress(notify, Progress{Stage: StageValidating})
	extracted, err := snapshot.Extract(ctx, archivePath, temporaryDirectory)
	if err != nil {
		return Result{}, err
	}
	extractedOwned := true
	defer func() {
		if extractedOwned {
			err = errors.Join(err, removeDirectory(extracted.Directory))
		}
	}()

	if err := removeFile(archivePath); err != nil {
		return Result{}, err
	}
	archiveOwned = false

	validated, err := snapshot.Validate(ctx, extracted)
	if err != nil {
		return Result{}, err
	}
	result = Result{
		SnapshotID: validated.SnapshotID, NodeCount: validated.NodeCount,
		RelationCount: validated.RelationCount, LimitCount: validated.LimitCount,
	}

	if current, ok := storage.CurrentGeneration(); ok && current.SnapshotID == validated.SnapshotID {
		if current.Fingerprint != validated.Fingerprint {
			return result, ErrSnapshotIDConflict
		}
		// 取込リソースの状態遷移は通常取込と同じ順序を保つが、SQLiteの登録と切替は行わない。
		notifyProgress(notify, progressFromValidation(StageBuilding, validated))
		notifyProgress(notify, progressFromValidation(StageActivating, validated))
		if err := operation.Abort(); err != nil {
			return result, err
		}
		abortRequired = false
		result.Reused = true
		return result, nil
	}

	notifyProgress(notify, progressFromValidation(StageBuilding, validated))
	if err := loadSnapshot(ctx, operation.DB(), extracted, validated); err != nil {
		return result, err
	}
	if err := removeDirectory(extracted.Directory); err != nil {
		return result, err
	}
	extractedOwned = false

	// Complete開始後の索引作成、検査、切替、失敗時の破棄はStoreが一括して所有する。
	// ここでAbortの責務を外し、Complete失敗時に同じSQLiteを二重に閉じない。
	abortRequired = false
	notifyProgress(notify, progressFromValidation(StageActivating, validated))
	generation := store.Generation{
		SnapshotID:         validated.SnapshotID,
		GeneratedAt:        validated.GeneratedAt,
		SchemaVersion:      validated.SchemaVersion,
		NodeCount:          validated.NodeCount,
		RelationCount:      validated.RelationCount,
		LimitCount:         validated.LimitCount,
		MaxSCCNodes:        validated.MaxSCCNodes,
		MaxJobNetworkDepth: validated.MaxJobNetworkDepth,
		Fingerprint:        validated.Fingerprint,
	}
	if err := completeImport(ctx, operation, generation); err != nil {
		if errors.Is(err, store.ErrRetiredCleanup) {
			result.CleanupWarning = err
			return result, nil
		}
		return result, err
	}
	return result, nil
}

func notifyProgress(notify ProgressFunc, progress Progress) {
	if notify != nil {
		notify(progress)
	}
}

func progressFromValidation(stage Stage, validated snapshot.ValidationResult) Progress {
	return Progress{
		Stage: stage, SnapshotID: validated.SnapshotID, NodeCount: validated.NodeCount,
		RelationCount: validated.RelationCount, LimitCount: validated.LimitCount,
	}
}

func removeFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove received snapshot: %w", err)
	}
	return nil
}

func removeDirectory(path string) error {
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove extracted snapshot: %w", err)
	}
	return nil
}
