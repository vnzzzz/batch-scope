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

// completeImportは、切替成功後の警告を取込の境界で再現するテスト専用注入点である。
var completeImport = func(ctx context.Context, operation *store.Import) error {
	return operation.Complete(ctx)
}

// Result は、検索先の切替に成功した取込の付随結果を表す。
// 呼出側は、Resultに含まれる警告を取込失敗として扱ってはならない。
type Result struct {
	// CleanupWarning は、切替後に不要となったSQLiteの後始末だけが失敗した場合に設定される。
	// この値が設定されていても切替は成功しており、Runのerrorはnilで新しいSQLiteを検索に使用できる。
	CleanupWarning error
}

// Run はスナップショットを受信、展開、検査、登録し、全ての検査に通ったSQLiteだけを検索先へ切り替える。
// temporaryDirectoryは受信アーカイブと展開ファイルを置ける既存ディレクトリでなければならない。
// 検索先の切替後に旧SQLiteの後始末だけが失敗した場合は、成功を返してResult.CleanupWarningで通知する。
func Run(ctx context.Context, temporaryDirectory string, source io.Reader, storage *store.Store) (result Result, err error) {
	operation, err := storage.BeginImport(ctx)
	if err != nil {
		return Result{}, err
	}
	abortRequired := true
	defer func() {
		if abortRequired {
			err = errors.Join(err, operation.Abort())
		}
	}()

	archivePath, err := snapshot.Receive(ctx, temporaryDirectory, source)
	if err != nil {
		return Result{}, err
	}
	archiveOwned := true
	defer func() {
		if archiveOwned {
			err = errors.Join(err, removeFile(archivePath))
		}
	}()

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

	if err := snapshot.Load(ctx, operation.DB(), extracted, validated); err != nil {
		return Result{}, err
	}
	if err := removeDirectory(extracted.Directory); err != nil {
		return Result{}, err
	}
	extractedOwned = false

	// Complete開始後の索引作成、検査、切替、失敗時の破棄はStoreが一括して所有する。
	// ここでAbortの責務を外し、Complete失敗時に同じSQLiteを二重に閉じない。
	abortRequired = false
	if err := completeImport(ctx, operation); err != nil {
		if errors.Is(err, store.ErrRetiredCleanup) {
			return Result{CleanupWarning: err}, nil
		}
		return Result{}, err
	}
	return Result{}, nil
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
