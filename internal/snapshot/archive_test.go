package snapshot

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type testEntry struct {
	name     string
	body     []byte
	typeflag byte
	linkname string
}

func TestReceiveAcceptsCompressedSizeBoundary(t *testing.T) {
	directory := t.TempDir()
	contents := []byte("12345678")
	path, err := receive(context.Background(), directory, bytes.NewReader(contents), archiveLimits{compressed: int64(len(contents))})
	if err != nil {
		t.Fatalf("receive() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read received file: %v", err)
	}
	if !bytes.Equal(got, contents) {
		t.Fatalf("received contents = %q, want %q", got, contents)
	}
}

func TestReceiveRejectsCompressedSizeOverLimitAndRemovesTemporaryFile(t *testing.T) {
	directory := t.TempDir()
	_, err := receive(context.Background(), directory, bytes.NewReader([]byte("123456789")), archiveLimits{compressed: 8})
	assertErrorKind(t, err, ErrorCompressedLimit)
	assertDirectoryEmpty(t, directory)
}

func TestReceivePreservesReaderErrorAndRemovesTemporaryFile(t *testing.T) {
	directory := t.TempDir()
	readErr := errors.New("reader interrupted")
	_, err := receive(context.Background(), directory, &interruptedReader{err: readErr}, archiveLimits{compressed: 64})
	assertErrorKind(t, err, ErrorIO)
	if !errors.Is(err, readErr) {
		t.Fatalf("receive() error = %v, want wrapped reader error", err)
	}
	assertDirectoryEmpty(t, directory)
}

func TestReceivePreservesContextCancellationAndRemovesTemporaryFile(t *testing.T) {
	directory := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := receive(ctx, directory, bytes.NewReader([]byte("archive")), archiveLimits{compressed: 64})
	assertErrorKind(t, err, ErrorIO)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("receive() error = %v, want context.Canceled", err)
	}
	assertDirectoryEmpty(t, directory)
}

func TestExtractValidArchiveAtExpandedSizeBoundary(t *testing.T) {
	archive, expandedSize := makeArchive(t, validEntries(nil, nil))
	archivePath := writeArchive(t, archive)
	outputParent := t.TempDir()

	result, err := extract(context.Background(), archivePath, outputParent, archiveLimits{
		extracted:  expandedSize,
		ndjsonLine: 16,
	})
	if err != nil {
		t.Fatalf("extract() error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(result.Directory) })

	assertFileContents(t, result.Manifest, []byte("{}"))
	assertFileContents(t, result.Nodes, []byte("{}\n"))
	assertFileContents(t, result.Relations, []byte("{}\n"))
	if filepath.Dir(result.Manifest) != result.Directory || filepath.Dir(result.Nodes) != result.Directory || filepath.Dir(result.Relations) != result.Directory {
		t.Fatalf("result paths are not direct children of %q: %#v", result.Directory, result)
	}
}

func TestExtractRejectsActualExpandedSizeOverLimitAndRemovesOutput(t *testing.T) {
	archive, expandedSize := makeArchive(t, validEntries(bytes.Repeat([]byte("n"), 2048), nil))
	archivePath := writeArchive(t, archive)
	outputParent := t.TempDir()

	_, err := extract(context.Background(), archivePath, outputParent, archiveLimits{
		extracted:  expandedSize - 1,
		ndjsonLine: 4096,
	})
	assertErrorKind(t, err, ErrorExtractedLimit)
	assertDirectoryEmpty(t, outputParent)
}

func TestExtractCountsGzipBytesBeyondDeclaredTarEntries(t *testing.T) {
	archive, declaredArchiveSize := makeArchive(t, validEntries(nil, nil))
	gzipReader, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("open test gzip: %v", err)
	}
	tarBytes, err := io.ReadAll(gzipReader)
	if err != nil {
		t.Fatalf("read test gzip: %v", err)
	}
	if err := gzipReader.Close(); err != nil {
		t.Fatalf("close test gzip: %v", err)
	}
	tarBytes = append(tarBytes, bytes.Repeat([]byte{0}, 1024)...)
	archive = compressBytes(t, tarBytes)

	archivePath := writeArchive(t, archive)
	outputParent := t.TempDir()
	_, err = extract(context.Background(), archivePath, outputParent, archiveLimits{
		extracted:  declaredArchiveSize,
		ndjsonLine: 64,
	})
	assertErrorKind(t, err, ErrorExtractedLimit)
	assertDirectoryEmpty(t, outputParent)
}

func TestExtractRejectsNonZeroDataAfterTarEndMarker(t *testing.T) {
	archive, _ := makeArchive(t, validEntries(nil, nil))
	gzipReader, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("open test gzip: %v", err)
	}
	tarBytes, err := io.ReadAll(gzipReader)
	if err != nil {
		t.Fatalf("read test gzip: %v", err)
	}
	if err := gzipReader.Close(); err != nil {
		t.Fatalf("close test gzip: %v", err)
	}
	tarBytes = append(tarBytes, 1)
	assertExtractFailure(t, compressBytes(t, tarBytes), ErrorInvalidArchive, 64)
}

func TestExtractRejectsInvalidEntryTypes(t *testing.T) {
	tests := []struct {
		name     string
		typeflag byte
		linkname string
	}{
		{name: "directory", typeflag: tar.TypeDir},
		{name: "symbolic link", typeflag: tar.TypeSymlink, linkname: nodesName},
		{name: "hard link", typeflag: tar.TypeLink, linkname: nodesName},
		{name: "undefined file type", typeflag: tar.TypeFifo},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries := validEntries(nil, nil)
			entries[0].typeflag = tt.typeflag
			entries[0].linkname = tt.linkname
			if tt.typeflag == tar.TypeDir {
				entries[0].name = manifestName + "/"
			}
			archive, _ := makeArchive(t, entries)
			assertExtractFailure(t, archive, ErrorInvalidEntry, 64)
		})
	}
}

func TestExtractRejectsPathsOutsideDefinedRootFiles(t *testing.T) {
	for _, name := range []string{
		"../" + manifestName,
		"nested/" + manifestName,
		"./" + manifestName,
		"/" + manifestName,
		"extra.json",
	} {
		t.Run(name, func(t *testing.T) {
			entries := validEntries(nil, nil)
			entries[0].name = name
			archive, _ := makeArchive(t, entries)
			assertExtractFailure(t, archive, ErrorInvalidEntry, 64)
		})
	}
}

func TestExtractRejectsDuplicateEntry(t *testing.T) {
	entries := validEntries(nil, nil)
	entries = append(entries, testEntry{name: nodesName, body: []byte("{}\n"), typeflag: tar.TypeReg})
	archive, _ := makeArchive(t, entries)
	assertExtractFailure(t, archive, ErrorInvalidEntry, 64)
}

func TestExtractRejectsMissingEntry(t *testing.T) {
	archive, _ := makeArchive(t, validEntries(nil, nil)[:2])
	assertExtractFailure(t, archive, ErrorMissingEntry, 64)
}

func TestExtractChecksNDJSONLineSizeWithoutCountingNewline(t *testing.T) {
	tests := []struct {
		name string
		body []byte
		kind ErrorKind
	}{
		{name: "boundary with newline", body: []byte("12345678\n")},
		{name: "boundary without newline", body: []byte("12345678")},
		{name: "over limit", body: []byte("123456789\n"), kind: ErrorNDJSONLineLimit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			archive, expandedSize := makeArchive(t, validEntries(tt.body, nil))
			archivePath := writeArchive(t, archive)
			outputParent := t.TempDir()
			result, err := extract(context.Background(), archivePath, outputParent, archiveLimits{
				extracted:  expandedSize,
				ndjsonLine: 8,
			})
			if tt.kind != "" {
				assertErrorKind(t, err, tt.kind)
				assertDirectoryEmpty(t, outputParent)
				return
			}
			if err != nil {
				t.Fatalf("extract() error = %v", err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(result.Directory) })
			assertFileContents(t, result.Nodes, tt.body)
		})
	}
}

func TestExtractChecksEveryNDJSONFileForLineSize(t *testing.T) {
	for _, file := range []string{nodesName, relationsName} {
		t.Run(file, func(t *testing.T) {
			nodes := []byte("{}\n")
			relations := []byte("{}\n")
			if file == nodesName {
				nodes = []byte("12345\n")
			} else {
				relations = []byte("12345\n")
			}
			archive, _ := makeArchive(t, validEntries(nodes, relations))
			assertExtractFailure(t, archive, ErrorNDJSONLineLimit, 4)
		})
	}
}

func TestExtractRejectsInvalidUTF8InEveryNDJSONFile(t *testing.T) {
	invalid := []byte{'{', 0xff, '}', '\n'}
	for _, file := range []string{nodesName, relationsName} {
		t.Run(file, func(t *testing.T) {
			nodes := []byte("{}\n")
			relations := []byte("{}\n")
			if file == nodesName {
				nodes = invalid
			} else {
				relations = invalid
			}
			archive, _ := makeArchive(t, validEntries(nodes, relations))
			assertExtractFailure(t, archive, ErrorInvalidUTF8, 64)
		})
	}
}

func TestExtractRejectsCorruptGzipAndRemovesOutput(t *testing.T) {
	archive, _ := makeArchive(t, validEntries(nil, nil))
	archive[len(archive)-1] ^= 0xff
	assertExtractFailure(t, archive, ErrorInvalidArchive, 64)
}

func TestExtractPreservesContextCancellationAndRemovesOutput(t *testing.T) {
	archive, expandedSize := makeArchive(t, validEntries(nil, nil))
	archivePath := writeArchive(t, archive)
	outputParent := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := extract(ctx, archivePath, outputParent, archiveLimits{extracted: expandedSize, ndjsonLine: 64})
	assertErrorKind(t, err, ErrorIO)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("extract() error = %v, want context.Canceled", err)
	}
	assertDirectoryEmpty(t, outputParent)
}

func validEntries(nodes, relations []byte) []testEntry {
	if nodes == nil {
		nodes = []byte("{}\n")
	}
	if relations == nil {
		relations = []byte("{}\n")
	}
	return []testEntry{
		{name: manifestName, body: []byte("{}"), typeflag: tar.TypeReg},
		{name: nodesName, body: nodes, typeflag: tar.TypeReg},
		{name: relationsName, body: relations, typeflag: tar.TypeReg},
	}
}

func makeArchive(t *testing.T, entries []testEntry) ([]byte, int64) {
	t.Helper()
	var expanded bytes.Buffer
	tarWriter := tar.NewWriter(&expanded)
	for _, entry := range entries {
		header := &tar.Header{
			Name:     entry.name,
			Mode:     0o600,
			Size:     int64(len(entry.body)),
			Typeflag: entry.typeflag,
			Linkname: entry.linkname,
		}
		if entry.typeflag != tar.TypeReg && entry.typeflag != tar.TypeRegA {
			header.Size = 0
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatalf("write tar header %q: %v", entry.name, err)
		}
		if header.Size > 0 {
			if _, err := tarWriter.Write(entry.body); err != nil {
				t.Fatalf("write tar contents %q: %v", entry.name, err)
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}

	return compressBytes(t, expanded.Bytes()), int64(expanded.Len())
}

func compressBytes(t *testing.T, contents []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	if _, err := gzipWriter.Write(contents); err != nil {
		t.Fatalf("write gzip contents: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	return compressed.Bytes()
}

func writeArchive(t *testing.T, contents []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "snapshot.tar.gz")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	return path
}

func assertExtractFailure(t *testing.T, archive []byte, kind ErrorKind, lineLimit int64) {
	t.Helper()
	archivePath := writeArchive(t, archive)
	outputParent := t.TempDir()
	_, err := extract(context.Background(), archivePath, outputParent, archiveLimits{
		extracted:  1 << 20,
		ndjsonLine: lineLimit,
	})
	assertErrorKind(t, err, kind)
	assertDirectoryEmpty(t, outputParent)
}

func assertErrorKind(t *testing.T, err error, want ErrorKind) {
	t.Helper()
	var snapshotErr *Error
	if !errors.As(err, &snapshotErr) {
		t.Fatalf("error = %v, want *Error", err)
	}
	if snapshotErr.Kind != want {
		t.Fatalf("error kind = %q, want %q (error: %v)", snapshotErr.Kind, want, err)
	}
}

func assertDirectoryEmpty(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("directory %q contains %v, want empty", directory, entries)
	}
}

func assertFileContents(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("contents of %q = %q, want %q", path, got, want)
	}
}

type interruptedReader struct {
	done bool
	err  error
}

func (r *interruptedReader) Read(buffer []byte) (int, error) {
	if r.done {
		return 0, r.err
	}
	r.done = true
	return copy(buffer, "partial"), nil
}

var _ io.Reader = (*interruptedReader)(nil)
