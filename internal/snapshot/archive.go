// Package snapshot は、取込スナップショットの受信と展開を扱う。
package snapshot

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unicode/utf8"

	"batchscope/internal/limits"
)

const (
	manifestName  = "manifest.json"
	nodesName     = "nodes.ndjson"
	relationsName = "relations.ndjson"
)

// ErrorKind は、アーカイブ処理が失敗した理由を表す。
type ErrorKind string

const (
	// ErrorIO は、受信元、context、またはファイル操作の失敗を表す。
	ErrorIO ErrorKind = "io"
	// ErrorCompressedLimit は、受信した圧縮アーカイブの上限超過を表す。
	ErrorCompressedLimit ErrorKind = "compressed_size_limit"
	// ErrorExtractedLimit は、gzipから読み出した展開後データの上限超過を表す。
	ErrorExtractedLimit ErrorKind = "extracted_size_limit"
	// ErrorInvalidArchive は、gzipまたはtarの形式不正を表す。
	ErrorInvalidArchive ErrorKind = "invalid_archive"
	// ErrorInvalidEntry は、許可されない名前、種別、または重複エントリを表す。
	ErrorInvalidEntry ErrorKind = "invalid_entry"
	// ErrorMissingEntry は、必須ファイルが存在しないことを表す。
	ErrorMissingEntry ErrorKind = "missing_entry"
	// ErrorNDJSONLineLimit は、NDJSON一行の上限超過を表す。
	ErrorNDJSONLineLimit ErrorKind = "ndjson_line_size_limit"
	// ErrorManifestSizeLimit は、manifest.jsonの上限超過を表す。
	ErrorManifestSizeLimit ErrorKind = "manifest_size_limit"
	// ErrorInvalidUTF8 は、入力ファイルに不正なUTF-8が含まれることを表す。
	ErrorInvalidUTF8 ErrorKind = "invalid_utf8"
	// ErrorInvalidJSON は、JSONとして解釈できない入力を表す。
	ErrorInvalidJSON ErrorKind = "invalid_json"
	// ErrorSchemaViolation は、JSON Schemaに違反する入力を表す。
	ErrorSchemaViolation ErrorKind = "schema_violation"
	// ErrorNodeCountMismatch は、マニフェストとノードの件数不一致を表す。
	ErrorNodeCountMismatch ErrorKind = "node_count_mismatch"
	// ErrorRelationCountMismatch は、マニフェストと依存関係の件数不一致を表す。
	ErrorRelationCountMismatch ErrorKind = "relation_count_mismatch"
	// ErrorDuplicateNode は、同じIDを持つノードの重複を表す。
	ErrorDuplicateNode ErrorKind = "duplicate_node"
	// ErrorDuplicateLimit は、一つのスナップショット内で同じIDを持つリミットの重複を表す。
	ErrorDuplicateLimit ErrorKind = "duplicate_limit"
	// ErrorMissingParent は、存在しない親の参照を表す。
	ErrorMissingParent ErrorKind = "missing_parent"
	// ErrorMissingNode は、依存関係から存在しないノードへの参照を表す。
	ErrorMissingNode ErrorKind = "missing_node"
	// ErrorInvalidParentType は、許可されない種別の親を表す。
	ErrorInvalidParentType ErrorKind = "invalid_parent_type"
	// ErrorMultipleParents は、同じノードIDに異なる親を設定した入力を表す。
	ErrorMultipleParents ErrorKind = "multiple_parents"
	// ErrorParentCycle は、親子関係の循環を表す。
	ErrorParentCycle ErrorKind = "parent_cycle"
	// ErrorInvalidLimitOwner は、job以外のノードに設定されたリミットを表す。
	ErrorInvalidLimitOwner ErrorKind = "invalid_limit_owner"
	// ErrorInvalidDuration は、固定秒数へ変換できないmax_elapsedを表す。
	ErrorInvalidDuration ErrorKind = "invalid_duration"
	// ErrorDuplicateRelation は、内容が同じ依存関係の重複を表す。
	ErrorDuplicateRelation ErrorKind = "duplicate_relation"
)

// Error は、失敗理由と該当するアーカイブ内ファイルを保持する。
// Errがある場合はerrors.Isおよびerrors.Asで元のエラーを調べられる。
type Error struct {
	Kind    ErrorKind
	File    string
	Line    int
	Pointer string
	Err     error
}

func (e *Error) Error() string {
	location := e.File
	if e.Line > 0 {
		location = fmt.Sprintf("%s:%d", location, e.Line)
	}
	if e.Pointer != "" {
		location += e.Pointer
	}
	if location == "" {
		location = "snapshot archive"
	}
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", location, e.Kind, e.Err)
	}
	return fmt.Sprintf("%s: %s", location, e.Kind)
}

func (e *Error) Unwrap() error {
	return e.Err
}

// Extracted は、展開に成功した三つのファイルを表す。
// 呼出側は使用後にDirectoryを削除する責任を持つ。
type Extracted struct {
	Directory string
	Manifest  string
	Nodes     string
	Relations string
}

type archiveLimits struct {
	compressed int64
	extracted  int64
	ndjsonLine int64
}

var defaultArchiveLimits = archiveLimits{
	compressed: limits.MaxCompressedArchiveBytes,
	extracted:  limits.MaxExtractedArchiveBytes,
	ndjsonLine: limits.MaxNDJSONLineBytes,
}

// Receive はsourceをdirectory内の一時ファイルへ保存する。
// 成功時のファイルは呼出側が所有し、使用後に削除する責任を持つ。
func Receive(ctx context.Context, directory string, source io.Reader) (string, error) {
	return receive(ctx, directory, source, defaultArchiveLimits)
}

func receive(ctx context.Context, directory string, source io.Reader, configured archiveLimits) (string, error) {
	archive, err := os.CreateTemp(directory, ".batchscope-snapshot-*.tar.gz")
	if err != nil {
		return "", &Error{Kind: ErrorIO, Err: err}
	}
	path := archive.Name()
	succeeded := false
	defer func() {
		if !succeeded {
			_ = os.Remove(path)
		}
	}()

	limited := &io.LimitedReader{R: contextReader{ctx: ctx, reader: source}, N: configured.compressed + 1}
	written, copyErr := io.Copy(archive, limited)
	if written > configured.compressed {
		_ = archive.Close()
		return "", &Error{Kind: ErrorCompressedLimit}
	}
	if copyErr != nil {
		_ = archive.Close()
		return "", &Error{Kind: ErrorIO, Err: copyErr}
	}
	if err := archive.Close(); err != nil {
		return "", &Error{Kind: ErrorIO, Err: err}
	}

	succeeded = true
	return path, nil
}

// Extract はarchivePathの内容をdirectory内の一時ディレクトリへ展開する。
// 成功時のDirectoryとその配下のファイルは呼出側が所有し、使用後に削除する責任を持つ。
func Extract(ctx context.Context, archivePath, directory string) (Extracted, error) {
	return extract(ctx, archivePath, directory, defaultArchiveLimits)
}

func extract(ctx context.Context, archivePath, directory string, configured archiveLimits) (result Extracted, err error) {
	archive, err := os.Open(archivePath)
	if err != nil {
		return Extracted{}, &Error{Kind: ErrorIO, Err: err}
	}
	defer archive.Close()

	gzipReader, err := gzip.NewReader(contextReader{ctx: ctx, reader: archive})
	if err != nil {
		return Extracted{}, archiveReadError(err)
	}
	defer gzipReader.Close()

	extractionDirectory, err := os.MkdirTemp(directory, ".batchscope-snapshot-*")
	if err != nil {
		return Extracted{}, &Error{Kind: ErrorIO, Err: err}
	}
	succeeded := false
	defer func() {
		if !succeeded {
			_ = os.RemoveAll(extractionDirectory)
		}
	}()

	// tarの申告サイズではなくgzipから実際に読み出したバイト数を制限し、
	// 偽ったヘッダーや末尾データでも展開後上限を迂回できないようにする。
	expanded := &maximumReader{reader: gzipReader, maximum: configured.extracted}
	tarReader := tar.NewReader(expanded)
	seen := make(map[string]struct{}, 3)

	for {
		if err := ctx.Err(); err != nil {
			return Extracted{}, &Error{Kind: ErrorIO, Err: err}
		}

		header, nextErr := tarReader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return Extracted{}, archiveReadError(nextErr)
		}
		if err := validateHeader(header, seen); err != nil {
			return Extracted{}, err
		}

		filePath := filepath.Join(extractionDirectory, header.Name)
		if err := extractEntry(ctx, tarReader, filePath, header.Name, configured.ndjsonLine); err != nil {
			return Extracted{}, err
		}
		seen[header.Name] = struct{}{}
	}

	// tar終端以降もgzipを最後まで読み、展開後サイズとgzipチェックサムを検査する。
	padding := &tarPaddingWriter{}
	if _, err := io.Copy(padding, expanded); err != nil {
		return Extracted{}, archiveReadError(err)
	}
	if padding.nonZero {
		return Extracted{}, &Error{Kind: ErrorInvalidArchive, Err: errors.New("non-zero data after tar end marker")}
	}
	for _, required := range []string{manifestName, nodesName, relationsName} {
		if _, ok := seen[required]; !ok {
			return Extracted{}, &Error{Kind: ErrorMissingEntry, File: required}
		}
	}

	result = Extracted{
		Directory: extractionDirectory,
		Manifest:  filepath.Join(extractionDirectory, manifestName),
		Nodes:     filepath.Join(extractionDirectory, nodesName),
		Relations: filepath.Join(extractionDirectory, relationsName),
	}
	succeeded = true
	return result, nil
}

func validateHeader(header *tar.Header, seen map[string]struct{}) error {
	if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
		return &Error{Kind: ErrorInvalidEntry, File: header.Name}
	}
	if header.Name != manifestName && header.Name != nodesName && header.Name != relationsName {
		return &Error{Kind: ErrorInvalidEntry, File: header.Name}
	}
	if _, duplicated := seen[header.Name]; duplicated {
		return &Error{Kind: ErrorInvalidEntry, File: header.Name}
	}
	return nil
}

func extractEntry(ctx context.Context, source io.Reader, destination, archiveName string, lineLimit int64) error {
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return &Error{Kind: ErrorIO, File: archiveName, Err: err}
	}

	if archiveName == nodesName || archiveName == relationsName {
		err = copyValidatedNDJSON(ctx, file, source, archiveName, lineLimit)
	} else {
		_, err = io.Copy(file, contextReader{ctx: ctx, reader: source})
		if err != nil {
			err = archiveReadErrorForFile(archiveName, err)
		}
	}
	if closeErr := file.Close(); err == nil && closeErr != nil {
		err = &Error{Kind: ErrorIO, File: archiveName, Err: closeErr}
	}
	return err
}

func copyValidatedNDJSON(ctx context.Context, destination io.Writer, source io.Reader, name string, maximum int64) error {
	reader := bufio.NewReader(contextReader{ctx: ctx, reader: source})
	lineNumber := 0
	for {
		lineNumber++
		line, err := readLine(reader, maximum)
		if errors.Is(err, errLineTooLong) {
			return &Error{Kind: ErrorNDJSONLineLimit, File: name, Line: lineNumber}
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return archiveReadErrorForFile(name, err)
		}
		if len(line) == 0 && errors.Is(err, io.EOF) {
			return nil
		}

		content := line
		if content[len(content)-1] == '\n' {
			content = content[:len(content)-1]
		}
		if !utf8.Valid(content) {
			return &Error{Kind: ErrorInvalidUTF8, File: name, Line: lineNumber}
		}
		if _, writeErr := destination.Write(line); writeErr != nil {
			return &Error{Kind: ErrorIO, File: name, Line: lineNumber, Err: writeErr}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
	}
}

var errLineTooLong = errors.New("NDJSON line exceeds limit")

func readLine(reader *bufio.Reader, maximum int64) ([]byte, error) {
	line := make([]byte, 0, min(int64(64<<10), maximum+1))
	for {
		fragment, err := reader.ReadSlice('\n')
		line = append(line, fragment...)
		contentLength := int64(len(line))
		if len(line) > 0 && line[len(line)-1] == '\n' {
			contentLength--
		}
		if contentLength > maximum {
			return nil, errLineTooLong
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return line, err
		}
	}
}

var errMaximumExceeded = errors.New("expanded archive exceeds limit")

type maximumReader struct {
	reader  io.Reader
	maximum int64
	read    int64
}

func (r *maximumReader) Read(buffer []byte) (int, error) {
	remaining := r.maximum - r.read
	if remaining == 0 {
		var probe [1]byte
		n, err := r.reader.Read(probe[:])
		if n > 0 {
			return 0, errMaximumExceeded
		}
		return 0, err
	}
	if int64(len(buffer)) > remaining {
		buffer = buffer[:remaining]
	}
	n, err := r.reader.Read(buffer)
	r.read += int64(n)
	return n, err
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

type tarPaddingWriter struct {
	nonZero bool
}

func (w *tarPaddingWriter) Write(buffer []byte) (int, error) {
	for _, value := range buffer {
		if value != 0 {
			w.nonZero = true
			break
		}
	}
	return len(buffer), nil
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

func archiveReadError(err error) *Error {
	return archiveReadErrorForFile("", err)
}

func archiveReadErrorForFile(name string, err error) *Error {
	if errors.Is(err, errMaximumExceeded) {
		return &Error{Kind: ErrorExtractedLimit, File: name}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &Error{Kind: ErrorIO, File: name, Err: err}
	}
	return &Error{Kind: ErrorInvalidArchive, File: name, Err: err}
}
