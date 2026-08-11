package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"batchscope/internal/importer"
)

const demoBootID = "demo-boot-01"

func TestDemoDownstreamLimitAnalysisResponseMatchesHandler(t *testing.T) {
	a := newTestApp(t)
	workspace := t.TempDir()
	archive := demoSnapshotArchive(t)
	if _, err := importer.Run(context.Background(), workspace, bytes.NewReader(archive), a.store); err != nil {
		t.Fatalf("import demo snapshot: %v", err)
	}

	recorder := serveRequest(a, "/v1/downstream-limit-analysis?targetId=JOB-A")
	assertStatus(t, recorder, http.StatusOK)
	actual := decodeObject(t, recorder)
	// bootIdだけはプロセス起動ごとに変わるため、デモで定めた値へ置き換えて残りの公開DTO全体を比較する。
	actual["bootId"] = demoBootID

	responsePath := filepath.Join("..", "..", "examples", "demo", "responses", "downstream-limit-analysis.json")
	expectedContents, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatal(err)
	}
	var expected map[string]any
	if err := json.Unmarshal(expectedContents, &expected); err != nil {
		t.Fatalf("decode demo response: %v", err)
	}
	if !reflect.DeepEqual(actual, expected) {
		formatted, marshalErr := json.MarshalIndent(actual, "", "  ")
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		t.Fatalf("demo response differs from handler output; generated response follows:\n%s", formatted)
	}
}

func demoSnapshotArchive(t *testing.T) []byte {
	t.Helper()
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	names := []string{"manifest.json", "nodes.ndjson", "relations.ndjson"}
	sort.Strings(names)
	for _, name := range names {
		contents, err := os.ReadFile(filepath.Join("..", "..", "examples", "demo", "snapshot", name))
		if err != nil {
			t.Fatal(err)
		}
		header := &tar.Header{Name: name, Mode: 0o600, Size: int64(len(contents)), Typeflag: tar.TypeReg}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(tarWriter, bytes.NewReader(contents)); err != nil {
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
