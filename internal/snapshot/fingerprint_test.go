package snapshot

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFingerprintIgnoresArchiveContainerRepresentation(t *testing.T) {
	files := map[string][]byte{
		manifestName:  []byte(`{"schemaVersion":"0.5"}`),
		nodesName:     []byte("node\n"),
		relationsName: []byte("relation\n"),
	}
	first := extractFingerprintArchive(t, files, []string{manifestName, nodesName, relationsName}, 0o600, time.Unix(1, 0), gzip.BestSpeed)
	second := extractFingerprintArchive(t, files, []string{relationsName, manifestName, nodesName}, 0o640, time.Unix(2, 0), gzip.BestCompression)

	firstFingerprint, err := Fingerprint(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	secondFingerprint, err := Fingerprint(context.Background(), second)
	if err != nil {
		t.Fatal(err)
	}
	if firstFingerprint != secondFingerprint {
		t.Fatalf("fingerprints differ by archive representation: %q != %q", firstFingerprint, secondFingerprint)
	}
}

func TestFingerprintChangesForOneByteOfExpandedContent(t *testing.T) {
	first := writeFingerprintFiles(t, []byte("node\n"))
	second := writeFingerprintFiles(t, []byte("nodf\n"))
	firstFingerprint, err := Fingerprint(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	secondFingerprint, err := Fingerprint(context.Background(), second)
	if err != nil {
		t.Fatal(err)
	}
	if firstFingerprint == secondFingerprint {
		t.Fatalf("fingerprint did not change: %q", firstFingerprint)
	}
}

func extractFingerprintArchive(t *testing.T, files map[string][]byte, order []string, mode int64, modified time.Time, level int) Extracted {
	t.Helper()
	var archive bytes.Buffer
	gzipWriter, err := gzip.NewWriterLevel(&archive, level)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter.ModTime = modified
	gzipWriter.Name = "container-name"
	tarWriter := tar.NewWriter(gzipWriter)
	for _, name := range order {
		contents := files[name]
		header := &tar.Header{Name: name, Mode: mode, Size: int64(len(contents)), ModTime: modified, Uid: int(modified.Unix()), Gid: 99}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(contents); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "snapshot.tar.gz")
	if err := os.WriteFile(path, archive.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	extracted, err := Extract(context.Background(), path, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(extracted.Directory) })
	return extracted
}

func writeFingerprintFiles(t *testing.T, nodes []byte) Extracted {
	t.Helper()
	directory := t.TempDir()
	extracted := Extracted{
		Directory: directory,
		Manifest:  filepath.Join(directory, manifestName),
		Nodes:     filepath.Join(directory, nodesName),
		Relations: filepath.Join(directory, relationsName),
	}
	for path, contents := range map[string][]byte{
		extracted.Manifest: []byte("manifest"), extracted.Nodes: nodes, extracted.Relations: []byte("relation\n"),
	} {
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(file, bytes.NewReader(contents)); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return extracted
}
