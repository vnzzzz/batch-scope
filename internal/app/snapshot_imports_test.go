package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"batchscope/internal/importer"
	"batchscope/internal/limits"
	"batchscope/internal/store"
)

func TestSnapshotImportAPIReceivesBodyBeforeReturningAndActivatesSnapshot(t *testing.T) {
	a := newTestApp(t)
	var logs synchronizedBuffer
	a.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	body := &eofTrackingBody{reader: bytes.NewReader(demoSnapshotArchive(t))}
	recorder := postSnapshot(t, a, body, snapshotMediaType+"; version=0.5")

	assertStatus(t, recorder, http.StatusAccepted)
	if !body.reachedEOF {
		t.Fatal("request body was not completely received before the handler returned")
	}
	if got := recorder.Header().Get("Retry-After"); got != "2" {
		t.Fatalf("Retry-After = %q, want 2", got)
	}
	location := recorder.Header().Get("Location")
	if !strings.HasPrefix(location, "/v1/snapshot-imports/imp_") || len(strings.TrimPrefix(location, "/v1/snapshot-imports/")) != 36 {
		t.Fatalf("Location = %q", location)
	}
	resource := waitForImportState(t, a, location, importStateSucceeded)
	if resource["snapshotId"] != "demo-2026-08-07T00:00:00Z" {
		t.Fatalf("import resource = %#v", resource)
	}

	current := serveRequest(a, "/v1/snapshots/current")
	assertStatus(t, current, http.StatusOK)
	currentBody := decodeObject(t, current)
	status := serveRequest(a, "/v1/status")
	assertStatus(t, status, http.StatusOK)
	statusBody := decodeObject(t, status)
	if !equalJSONValue(currentBody, statusBody["snapshot"]) {
		t.Fatalf("current snapshot = %#v, status snapshot = %#v", currentBody, statusBody["snapshot"])
	}
	if currentBody["nodeCount"] != float64(19) || currentBody["relationCount"] != float64(19) || currentBody["limitCount"] != float64(5) {
		t.Fatalf("current snapshot counts = %#v", currentBody)
	}
	targets := serveRequest(a, "/v1/targets?query=JOB-A&type=job")
	assertStatus(t, targets, http.StatusOK)

	logEntry := waitForImportLog(t, &logs)
	for _, key := range []string{"import_id", "snapshot_id", "duration_ms", "import_state"} {
		if _, ok := logEntry[key]; !ok {
			t.Errorf("import log omits %s: %#v", key, logEntry)
		}
	}
	if strings.Contains(logs.String(), "売上集計") || strings.Contains(logs.String(), "/DAILY/SALES") {
		t.Fatalf("import log leaked snapshot input: %s", logs.String())
	}
}

func TestConcurrentSnapshotImportRejectsBeforeReadingBodyAndKeepsCurrentReady(t *testing.T) {
	a := newTestApp(t)
	initial := postSnapshot(t, a, io.NopCloser(bytes.NewReader(demoSnapshotArchive(t))), snapshotMediaType)
	assertStatus(t, initial, http.StatusAccepted)
	initialLocation := initial.Header().Get("Location")
	waitForImportState(t, a, initialLocation, importStateSucceeded)

	originalProcess := processReceived
	started := make(chan struct{})
	resume := make(chan struct{})
	var once sync.Once
	processReceived = func(
		ctx context.Context,
		temporaryDirectory string,
		archivePath string,
		operation *store.Import,
		storage *store.Store,
		notify importer.ProgressFunc,
	) (importer.Result, error) {
		once.Do(func() { close(started) })
		<-resume
		return originalProcess(ctx, temporaryDirectory, archivePath, operation, storage, notify)
	}
	t.Cleanup(func() { processReceived = originalProcess })

	updateArchive := demoSnapshotArchiveWithID(t, "demo-update")
	update := postSnapshot(t, a, io.NopCloser(bytes.NewReader(updateArchive)), snapshotMediaType)
	assertStatus(t, update, http.StatusAccepted)
	<-started

	ready := serveRequest(a, "/readyz")
	assertStatus(t, ready, http.StatusOK)
	status := decodeObject(t, serveRequest(a, "/v1/status"))
	if status["state"] != "importing" {
		t.Fatalf("status during update = %#v", status)
	}
	active := status["snapshot"].(map[string]any)
	if active["snapshotId"] != "demo-2026-08-07T00:00:00Z" {
		t.Fatalf("active snapshot during update = %#v", active)
	}

	secondBody := &countingRequestBody{reader: bytes.NewReader(updateArchive)}
	second := postSnapshot(t, a, secondBody, snapshotMediaType)
	assertStatus(t, second, http.StatusConflict)
	assertContentType(t, second, "application/problem+json")
	if secondBody.reads != 0 {
		t.Fatalf("second request body reads = %d, want 0", secondBody.reads)
	}
	problem := decodeObject(t, second)
	if problem["type"] != "/problems/snapshot-import-in-progress" {
		t.Fatalf("problem = %#v", problem)
	}

	close(resume)
	waitForImportState(t, a, update.Header().Get("Location"), importStateSucceeded)
}

func TestSnapshotImportFailureMappingsAreSafe(t *testing.T) {
	t.Run("compressed size", func(t *testing.T) {
		a := newTestApp(t)
		body := &countingRequestBody{reader: bytes.NewReader([]byte("not read"))}
		request := httptest.NewRequest(http.MethodPost, "/v1/snapshot-imports", body)
		request.Header.Set("Content-Type", snapshotMediaType)
		request.ContentLength = limits.MaxCompressedArchiveBytes + 1
		response := httptest.NewRecorder()
		a.Handler().ServeHTTP(response, request)
		assertStatus(t, response, http.StatusRequestEntityTooLarge)
		assertContentType(t, response, "application/problem+json")
		if body.reads != 0 {
			t.Fatalf("oversized request body reads = %d, want 0", body.reads)
		}
		problem := decodeObject(t, response)
		if problem["type"] != "/problems/snapshot-too-large" {
			t.Fatalf("problem = %#v", problem)
		}
	})

	t.Run("capacity", func(t *testing.T) {
		a := newTestApp(t)
		archive := makeAppSnapshotArchive(t, map[string]string{
			"manifest.json":    `{"schemaVersion":"0.5","snapshotId":"too-large","generatedAt":"2026-08-11T00:00:00Z","nodeCount":10001,"relationCount":0,"producer":{"name":"secret-input","version":"1"}}`,
			"nodes.ndjson":     "secret input that must not be returned\n",
			"relations.ndjson": "",
		})
		accepted := postSnapshot(t, a, io.NopCloser(bytes.NewReader(archive)), snapshotMediaType)
		assertStatus(t, accepted, http.StatusAccepted)
		resource := waitForImportState(t, a, accepted.Header().Get("Location"), importStateFailed)
		failure := resource["error"].(map[string]any)
		if failure["type"] != "/problems/snapshot-capacity-exceeded" || failure["status"] != float64(http.StatusUnprocessableEntity) ||
			failure["file"] != "manifest.json" || failure["pointer"] != "/nodeCount" || failure["reason"] != "capacity_exceeded" {
			t.Fatalf("capacity failure = %#v", failure)
		}
		encoded, _ := json.Marshal(failure)
		if bytes.Contains(encoded, []byte("secret")) {
			t.Fatalf("failure leaked input: %s", encoded)
		}
	})

	t.Run("interrupted receive", func(t *testing.T) {
		a := newTestApp(t)
		body := &erroringRequestBody{reader: bytes.NewReader([]byte("partial")), err: errors.New("connection interrupted")}
		response := postSnapshot(t, a, body, snapshotMediaType)
		assertStatus(t, response, http.StatusBadRequest)
		assertContentType(t, response, "application/problem+json")
		problem := decodeObject(t, response)
		if problem["type"] != "/problems/invalid-request" || problem["title"] == nil || problem["status"] == nil || problem["detail"] == nil {
			t.Fatalf("problem = %#v", problem)
		}
		assertNoImportTemporaryFiles(t, a)
	})
}

func TestSnapshotUploadDeadlineInterruptsSocketReadAndReleasesImport(t *testing.T) {
	a := newTestApp(t)
	restoreSnapshotUploadDeadline(t, 250*time.Millisecond)
	server := newTCPTestServer(t, a.Handler())
	t.Cleanup(server.Close)

	pipeReader, pipeWriter := io.Pipe()
	t.Cleanup(func() { _ = pipeWriter.Close() })
	request, err := http.NewRequest(http.MethodPost, server.URL+"/v1/snapshot-imports", pipeReader)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", snapshotMediaType)
	result := make(chan httpClientResult, 1)
	go func() {
		response, requestErr := server.Client().Do(request)
		result <- httpClientResult{response: response, err: requestErr}
	}()
	if _, err := pipeWriter.Write([]byte("partial archive")); err != nil {
		t.Fatal(err)
	}
	waitForStoreState(t, a.store, store.StateImporting)

	var upload httpClientResult
	select {
	case upload = <-result:
	case <-time.After(2 * time.Second):
		t.Fatal("snapshot upload handler remained blocked after its deadline")
	}
	if upload.err != nil {
		t.Fatalf("POST error = %v", upload.err)
	}
	defer upload.response.Body.Close()
	if upload.response.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST status = %d, want %d", upload.response.StatusCode, http.StatusBadRequest)
	}
	var problem map[string]any
	if err := json.NewDecoder(upload.response.Body).Decode(&problem); err != nil {
		t.Fatal(err)
	}
	if problem["type"] != "/problems/invalid-request" || problem["status"] != float64(http.StatusBadRequest) {
		t.Fatalf("deadline problem = %#v", problem)
	}
	assertNoImportTemporaryFiles(t, a)

	nextRequest, err := http.NewRequest(
		http.MethodPost,
		server.URL+"/v1/snapshot-imports",
		bytes.NewReader(demoSnapshotArchive(t)),
	)
	if err != nil {
		t.Fatal(err)
	}
	nextRequest.Header.Set("Content-Type", snapshotMediaType)
	next, err := server.Client().Do(nextRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Body.Close()
	if next.StatusCode != http.StatusAccepted {
		t.Fatalf("next POST status = %d, want %d", next.StatusCode, http.StatusAccepted)
	}
	waitForImportState(t, a, next.Header.Get("Location"), importStateSucceeded)
}

func TestSnapshotUploadDeadlineAllowsAppCloseToFinish(t *testing.T) {
	a, err := New(Config{Version: "test", Commit: "abc", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	restoreSnapshotUploadDeadline(t, 250*time.Millisecond)
	server := newTCPTestServer(t, a.Handler())
	t.Cleanup(server.Close)

	pipeReader, pipeWriter := io.Pipe()
	t.Cleanup(func() { _ = pipeWriter.Close() })
	request, err := http.NewRequest(http.MethodPost, server.URL+"/v1/snapshot-imports", pipeReader)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", snapshotMediaType)
	requestResult := make(chan httpClientResult, 1)
	go func() {
		response, requestErr := server.Client().Do(request)
		requestResult <- httpClientResult{response: response, err: requestErr}
	}()
	if _, err := pipeWriter.Write([]byte("partial archive")); err != nil {
		t.Fatal(err)
	}
	waitForStoreState(t, a.store, store.StateImporting)

	closeResult := make(chan error, 1)
	go func() { closeResult <- a.Close() }()
	select {
	case err := <-closeResult:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close() remained blocked by the synchronous request body read")
	}
	var upload httpClientResult
	select {
	case upload = <-requestResult:
	case <-time.After(time.Second):
		t.Fatal("POST did not return after Close() completed")
	}
	if upload.err != nil {
		t.Fatalf("POST error = %v", upload.err)
	}
	_ = upload.response.Body.Close()
}

func TestSnapshotImportNotFoundUsesProblemDetails(t *testing.T) {
	a := newTestApp(t)
	response := serveRequest(a, "/v1/snapshot-imports/imp_00000000000000000000000000000000")
	assertStatus(t, response, http.StatusNotFound)
	assertContentType(t, response, "application/problem+json")
	problem := decodeObject(t, response)
	if problem["type"] != "/problems/import-not-found" || problem["title"] == nil || problem["status"] == nil || problem["detail"] == nil {
		t.Fatalf("problem = %#v", problem)
	}
}

func TestCurrentSnapshotRequiresSearchableGeneration(t *testing.T) {
	a := newTestApp(t)
	response := serveRequest(a, "/v1/snapshots/current")
	assertStatus(t, response, http.StatusServiceUnavailable)
	assertContentType(t, response, "application/problem+json")
	problem := decodeObject(t, response)
	if problem["type"] != "/problems/snapshot-not-loaded" {
		t.Fatalf("problem = %#v", problem)
	}
}

func TestSnapshotImportReusesSameContentAndReportsSnapshotIDConflict(t *testing.T) {
	a := newTestApp(t)
	firstArchive := singleNodeAppArchive(t, "shared", "FIRST")
	first := postSnapshot(t, a, io.NopCloser(bytes.NewReader(firstArchive)), snapshotMediaType)
	assertStatus(t, first, http.StatusAccepted)
	waitForImportState(t, a, first.Header().Get("Location"), importStateSucceeded)
	before, err := filepath.Glob(filepath.Join(a.config.DataDir, "generation-*.db"))
	if err != nil {
		t.Fatal(err)
	}

	repeated := postSnapshot(t, a, io.NopCloser(bytes.NewReader(firstArchive)), snapshotMediaType)
	assertStatus(t, repeated, http.StatusAccepted)
	waitForImportState(t, a, repeated.Header().Get("Location"), importStateSucceeded)
	after, err := filepath.Glob(filepath.Join(a.config.DataDir, "generation-*.db"))
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 1 || len(after) != 1 || before[0] != after[0] {
		t.Fatalf("same content rebuilt the search generation: before=%v after=%v", before, after)
	}

	conflictingArchive := singleNodeAppArchive(t, "shared", "SECOND")
	conflicting := postSnapshot(t, a, io.NopCloser(bytes.NewReader(conflictingArchive)), snapshotMediaType)
	assertStatus(t, conflicting, http.StatusAccepted)
	resource := waitForImportState(t, a, conflicting.Header().Get("Location"), importStateFailed)
	failure := resource["error"].(map[string]any)
	if failure["type"] != "/problems/snapshot-id-conflict" || failure["status"] != float64(http.StatusConflict) {
		t.Fatalf("conflict failure = %#v", failure)
	}
	current := decodeObject(t, serveRequest(a, "/v1/snapshots/current"))
	if current["snapshotId"] != "shared" || current["nodeCount"] != float64(1) {
		t.Fatalf("current snapshot changed after conflict: %#v", current)
	}
}

func TestAppCloseWaitsForRunningSnapshotImport(t *testing.T) {
	a, err := New(Config{Version: "test", Commit: "abc", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	originalProcess := processReceived
	started := make(chan struct{})
	resume := make(chan struct{})
	processReceived = func(
		ctx context.Context,
		temporaryDirectory string,
		archivePath string,
		operation *store.Import,
		storage *store.Store,
		notify importer.ProgressFunc,
	) (importer.Result, error) {
		close(started)
		<-resume
		return originalProcess(ctx, temporaryDirectory, archivePath, operation, storage, notify)
	}
	t.Cleanup(func() { processReceived = originalProcess })

	accepted := postSnapshot(t, a, io.NopCloser(bytes.NewReader(demoSnapshotArchive(t))), snapshotMediaType)
	assertStatus(t, accepted, http.StatusAccepted)
	<-started
	closeResult := make(chan error, 1)
	go func() { closeResult <- a.Close() }()
	select {
	case err := <-closeResult:
		t.Fatalf("Close() returned before import completion: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(resume)
	if err := <-closeResult; err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

type eofTrackingBody struct {
	reader     io.Reader
	reachedEOF bool
}

func (body *eofTrackingBody) Read(buffer []byte) (int, error) {
	n, err := body.reader.Read(buffer)
	if errors.Is(err, io.EOF) {
		body.reachedEOF = true
	}
	return n, err
}

func (*eofTrackingBody) Close() error { return nil }

type countingRequestBody struct {
	reader io.Reader
	reads  int
}

func (body *countingRequestBody) Read(buffer []byte) (int, error) {
	body.reads++
	return body.reader.Read(buffer)
}

func (*countingRequestBody) Close() error { return nil }

type erroringRequestBody struct {
	reader io.Reader
	err    error
}

func (body *erroringRequestBody) Read(buffer []byte) (int, error) {
	n, err := body.reader.Read(buffer)
	if errors.Is(err, io.EOF) {
		return 0, body.err
	}
	return n, err
}

func (*erroringRequestBody) Close() error { return nil }

type httpClientResult struct {
	response *http.Response
	err      error
}

func restoreSnapshotUploadDeadline(t *testing.T, deadline time.Duration) {
	t.Helper()
	original := snapshotUploadDeadline
	snapshotUploadDeadline = deadline
	t.Cleanup(func() { snapshotUploadDeadline = original })
}

func newTCPTestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	// socket readの中断はhttptestの直接呼出しでは再現できないため、実TCPを使える環境だけで検査する。
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if errors.Is(err, syscall.EPERM) {
		t.Skip("sandbox does not permit TCP listeners")
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(handler)
}

func waitForStoreState(t *testing.T, storage *store.Store, want store.State) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if storage.State() == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("Store did not reach %s", want)
}

func assertNoImportTemporaryFiles(t *testing.T, a *App) {
	t.Helper()
	for _, pattern := range []string{".batchscope-snapshot-*", "importing.db*"} {
		files, err := filepath.Glob(filepath.Join(a.config.DataDir, pattern))
		if err != nil || len(files) != 0 {
			t.Fatalf("temporary import files matching %s = %v, error = %v", pattern, files, err)
		}
	}
}

func postSnapshot(t *testing.T, a *App, body io.ReadCloser, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/snapshot-imports", body)
	request.Header.Set("Content-Type", contentType)
	recorder := httptest.NewRecorder()
	a.Handler().ServeHTTP(recorder, request)
	return recorder
}

func waitForImportState(t *testing.T, a *App, location string, want importState) map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response := serveRequest(a, location)
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, body = %s", location, response.Code, response.Body.String())
		}
		resource := decodeObject(t, response)
		if resource["state"] == string(want) {
			return resource
		}
		if resource["state"] == string(importStateFailed) || resource["state"] == string(importStateSucceeded) {
			t.Fatalf("import reached state %v, want %s: %#v", resource["state"], want, resource)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("import did not reach %s", want)
	return nil
}

func waitForImportLog(t *testing.T, logs *synchronizedBuffer) map[string]any {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
		for _, line := range lines {
			if line == "" {
				continue
			}
			var entry map[string]any
			if err := json.Unmarshal([]byte(line), &entry); err == nil && entry["operation"] == snapshotImportOperation {
				return entry
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("snapshot import log was not written: %s", logs.String())
	return nil
}

type synchronizedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *synchronizedBuffer) Write(contents []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(contents)
}

func (buffer *synchronizedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

func equalJSONValue(left, right any) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return bytes.Equal(leftJSON, rightJSON)
}

func demoSnapshotArchiveWithID(t *testing.T, snapshotID string) []byte {
	t.Helper()
	files := make(map[string]string, 3)
	for _, name := range []string{"manifest.json", "nodes.ndjson", "relations.ndjson"} {
		contents, err := os.ReadFile(filepath.Join("..", "..", "examples", "demo", "snapshot", name))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = string(contents)
	}
	files["manifest.json"] = strings.Replace(files["manifest.json"], "demo-2026-08-07T00:00:00Z", snapshotID, 1)
	return makeAppSnapshotArchive(t, files)
}

func singleNodeAppArchive(t *testing.T, snapshotID, nodeID string) []byte {
	t.Helper()
	return makeAppSnapshotArchive(t, map[string]string{
		"manifest.json":    `{"schemaVersion":"0.5","snapshotId":"` + snapshotID + `","generatedAt":"2026-08-11T00:00:00Z","nodeCount":1,"relationCount":0,"producer":{"name":"test","version":"1"}}`,
		"nodes.ndjson":     `{"type":"job","id":"` + nodeID + `","name":"` + nodeID + `","limitFacts":[]}` + "\n",
		"relations.ndjson": "",
	})
}

func makeAppSnapshotArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		contents := files[name]
		header := &tar.Header{Name: name, Mode: 0o600, Size: int64(len(contents)), Typeflag: tar.TypeReg}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tarWriter, contents); err != nil {
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
