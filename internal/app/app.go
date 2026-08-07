package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Config struct {
	Version string
	Commit  string
}

type App struct {
	config  Config
	bootID  string
	started time.Time
	handler http.Handler
}

func New(config Config) (*App, error) {
	bootID, err := newBootID()
	if err != nil {
		return nil, fmt.Errorf("generate boot ID: %w", err)
	}

	a := &App{
		config:  config,
		bootID:  bootID,
		started: time.Now().UTC(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("GET /readyz", a.ready)
	mux.HandleFunc("GET /v1/status", a.status)
	a.handler = mux
	return a, nil
}

func (a *App) Handler() http.Handler {
	return a.handler
}

func (a *App) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *App) ready(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{
		"status": "not_ready",
		"reason": "snapshot_not_loaded",
	})
}

func (a *App) status(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"state":     "empty",
		"bootId":    a.bootID,
		"startedAt": a.started,
		"snapshot":  nil,
		"build": map[string]string{
			"version": a.config.Version,
			"commit":  a.config.Commit,
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func newBootID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}
