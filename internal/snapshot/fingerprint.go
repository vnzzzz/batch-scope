package snapshot

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// Fingerprintは、展開後の三ファイルだけからスナップショット内容を識別するSHA-256を返す。
// tarとgzipの表現は同一視し、ファイル名と内容長を固定幅で境界付けして連結の曖昧さを残さない。
func Fingerprint(ctx context.Context, extracted Extracted) (string, error) {
	hash := sha256.New()
	files := []struct {
		name string
		path string
	}{
		{name: manifestName, path: extracted.Manifest},
		{name: nodesName, path: extracted.Nodes},
		{name: relationsName, path: extracted.Relations},
	}
	for _, current := range files {
		if err := hashFingerprintFile(ctx, hash, current.name, current.path); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func hashFingerprintFile(ctx context.Context, destination io.Writer, name, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return &Error{Kind: ErrorIO, File: name, Err: err}
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return &Error{Kind: ErrorIO, File: name, Err: err}
	}
	// uint64のbig-endian長、名前、uint64のbig-endian長、内容という形式を固定し、
	// どのファイル境界でも異なる分割が同じバイト列にならないようにする。
	if err := binary.Write(destination, binary.BigEndian, uint64(len(name))); err != nil {
		return fmt.Errorf("hash %s name length: %w", name, err)
	}
	if _, err := io.WriteString(destination, name); err != nil {
		return fmt.Errorf("hash %s name: %w", name, err)
	}
	if err := binary.Write(destination, binary.BigEndian, uint64(info.Size())); err != nil {
		return fmt.Errorf("hash %s content length: %w", name, err)
	}
	if _, err := io.Copy(destination, contextReader{ctx: ctx, reader: file}); err != nil {
		return &Error{Kind: ErrorIO, File: name, Err: err}
	}
	return nil
}
