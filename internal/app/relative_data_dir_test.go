package app

import (
	"bytes"
	"io"
	"net/http"
	"path/filepath"
	"testing"
)

func TestRelativeDataDirectorySupportsSnapshotImport(t *testing.T) {
	archive := demoSnapshotArchive(t)
	workingDirectory := t.TempDir()
	t.Chdir(workingDirectory)

	a, err := New(Config{Version: "test", Commit: "test", DataDir: "./batchscope-data"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Errorf("Close(): %v", err)
		}
	})

	wantDataDirectory := filepath.Join(workingDirectory, "batchscope-data")
	if got := a.DataDirectory(); got != wantDataDirectory {
		t.Fatalf("data directory = %q, want %q", got, wantDataDirectory)
	}

	accepted := postSnapshot(t, a, io.NopCloser(bytes.NewReader(archive)), snapshotMediaType)
	assertStatus(t, accepted, http.StatusAccepted)
	waitForImportState(t, a, accepted.Header().Get("Location"), importStateSucceeded)

	ready := serveRequest(a, "/readyz")
	assertStatus(t, ready, http.StatusOK)

	targets := serveRequest(a, "/v1/targets?query=JOB-A&type=job")
	assertStatus(t, targets, http.StatusOK)
	targetBody := decodeObject(t, targets)
	items, ok := targetBody["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("target items = %#v, want one item", targetBody["items"])
	}
	item, ok := items[0].(map[string]any)
	if !ok || item["id"] != "JOB-A" {
		t.Fatalf("target item = %#v, want JOB-A", items[0])
	}

	generations, err := filepath.Glob(filepath.Join(wantDataDirectory, "generation-*.db"))
	if err != nil {
		t.Fatal(err)
	}
	if len(generations) != 1 {
		t.Fatalf("generation databases = %v, want one", generations)
	}
}
