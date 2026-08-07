package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealth(t *testing.T) {
	a, err := New(Config{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	a.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
}

func TestReadyWithoutSnapshot(t *testing.T) {
	a, err := New(Config{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	a.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
}

func TestStatus(t *testing.T) {
	a, err := New(Config{Version: "test", Commit: "abc"})
	if err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	a.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/status", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["state"] != "empty" {
		t.Fatalf("state = %v, want empty", body["state"])
	}
}
