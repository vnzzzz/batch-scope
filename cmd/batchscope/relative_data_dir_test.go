package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"batchscope/internal/app"
)

func TestRelativeDataDirectorySupportsSnapshotImport(t *testing.T) {
	archive := demoSnapshotArchive(t)
	workingDirectory := t.TempDir()
	t.Chdir(workingDirectory)

	dataDirectory, err := resolveDataDirectory("./batchscope-data")
	if err != nil {
		t.Fatal(err)
	}
	wantDataDirectory := filepath.Join(workingDirectory, "batchscope-data")
	if dataDirectory != wantDataDirectory {
		t.Fatalf("data directory = %q, want %q", dataDirectory, wantDataDirectory)
	}

	application, err := app.New(app.Config{Version: "test", Commit: "test", DataDir: dataDirectory})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := application.Close(); err != nil {
			t.Errorf("Close(): %v", err)
		}
	})

	request := httptest.NewRequest(http.MethodPost, "/v1/snapshot-imports", bytes.NewReader(archive))
	request.Header.Set("Content-Type", "application/vnd.batchscope.snapshot+gzip")
	response := httptest.NewRecorder()
	application.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("snapshot import status = %d, want %d: %s", response.Code, http.StatusAccepted, response.Body.String())
	}

	location := response.Header().Get("Location")
	if location == "" {
		t.Fatal("snapshot import response omitted Location")
	}
	waitForSnapshotImportSucceeded(t, application, location)

	ready := httptest.NewRecorder()
	application.Handler().ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusOK {
		t.Fatalf("ready status = %d, want %d: %s", ready.Code, http.StatusOK, ready.Body.String())
	}

	targets := httptest.NewRecorder()
	application.Handler().ServeHTTP(targets, httptest.NewRequest(http.MethodGet, "/v1/targets?query=JOB-A&type=job", nil))
	if targets.Code != http.StatusOK {
		t.Fatalf("target search status = %d, want %d: %s", targets.Code, http.StatusOK, targets.Body.String())
	}
	var targetBody struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(targets.Body.Bytes(), &targetBody); err != nil {
		t.Fatal(err)
	}
	if len(targetBody.Items) != 1 || targetBody.Items[0].ID != "JOB-A" {
		t.Fatalf("target search items = %#v, want JOB-A", targetBody.Items)
	}

	generations, err := filepath.Glob(filepath.Join(dataDirectory, "generation-*.db"))
	if err != nil {
		t.Fatal(err)
	}
	if len(generations) != 1 {
		t.Fatalf("generation databases = %v, want one", generations)
	}
}

func TestRelativeDataDirectoryFromEnvironmentIsResolved(t *testing.T) {
	workingDirectory := t.TempDir()
	t.Chdir(workingDirectory)
	t.Setenv("BATCHSCOPE_DATA_DIR", "./env-data")

	dataDirectory, err := resolveDataDirectory(defaultDataDirectory())
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(workingDirectory, "env-data")
	if dataDirectory != want {
		t.Fatalf("data directory = %q, want %q", dataDirectory, want)
	}
}

func waitForSnapshotImportSucceeded(t *testing.T, application *app.App, location string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response := httptest.NewRecorder()
		application.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, location, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("import status response = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
		}
		var resource struct {
			State string `json:"state"`
			Error any    `json:"error"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &resource); err != nil {
			t.Fatal(err)
		}
		switch resource.State {
		case "succeeded":
			return
		case "failed":
			t.Fatalf("snapshot import failed: %#v", resource.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("snapshot import did not succeed within 5 seconds")
}

func demoSnapshotArchive(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, name := range []string{"manifest.json", "nodes.ndjson", "relations.ndjson"} {
		content, err := os.ReadFile(filepath.Join("..", "..", "examples", "demo", "snapshot", name))
		if err != nil {
			t.Fatal(err)
		}
		header := &tar.Header{Name: name, Mode: 0o600, Size: int64(len(content))}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
