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

// Run はスナップショットを受信、展開、検査、登録し、全ての検査に通ったSQLiteだけを検索先へ切り替える。
// temporaryDirectoryは受信アーカイブと展開ファイルを置ける既存ディレクトリでなければならない。
func Run(ctx context.Context, temporaryDirectory string, source io.Reader, storage *store.Store) (err error) {
	archivePath, err := snapshot.Receive(ctx, temporaryDirectory, source)
	if err != nil {
		return err
	}
	archiveOwned := true
	defer func() {
		if archiveOwned {
			err = errors.Join(err, removeFile(archivePath))
		}
	}()

	extracted, err := snapshot.Extract(ctx, archivePath, temporaryDirectory)
	if err != nil {
		return err
	}
	extractedOwned := true
	defer func() {
		if extractedOwned {
			err = errors.Join(err, removeDirectory(extracted.Directory))
		}
	}()

	if err := removeFile(archivePath); err != nil {
		return err
	}
	archiveOwned = false

	validated, err := snapshot.Validate(ctx, extracted)
	if err != nil {
		return err
	}

	operation, err := storage.BeginImport(ctx)
	if err != nil {
		return err
	}
	abortRequired := true
	defer func() {
		if abortRequired {
			err = errors.Join(err, operation.Abort())
		}
	}()

	if err := snapshot.Load(ctx, operation.DB(), extracted, validated); err != nil {
		return err
	}
	if err := removeDirectory(extracted.Directory); err != nil {
		return err
	}
	extractedOwned = false

	// Complete開始後の索引作成、検査、切替、失敗時の破棄はStoreが一括して所有する。
	// ここでAbortの責務を外し、Complete失敗時に同じSQLiteを二重に閉じない。
	abortRequired = false
	return operation.Complete(ctx)
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
