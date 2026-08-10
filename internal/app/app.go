package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"batchscope/internal/store"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

const (
	apiTitle   = "BatchScope API"
	apiVersion = "v1"
)

type Config struct {
	Version string
	Commit  string
	DataDir string
}

type App struct {
	config  Config
	bootID  string
	started time.Time
	store   *store.Store
	handler http.Handler
}

type HealthResponse struct {
	Status string `json:"status"`
}

type ReadyResponse struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type StatusResponse struct {
	State     string    `json:"state"`
	BootID    string    `json:"bootId"`
	StartedAt time.Time `json:"startedAt"`
	// Snapshotは、使用中のスナップショット情報を保持する。
	// 取込を実装するまでは常にnullを返すため、任意の値を許す型にしている。
	Snapshot any       `json:"snapshot"`
	Build    BuildInfo `json:"build"`
}

type BuildInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

type healthOutput struct {
	Body HealthResponse
}

type readyOutput struct {
	Status int
	Body   ReadyResponse
}

type statusOutput struct {
	Body StatusResponse
}

func New(config Config) (*App, error) {
	storage, err := store.New(config.DataDir)
	if err != nil {
		return nil, fmt.Errorf("open SQLite store: %w", err)
	}

	bootID, err := newBootID()
	if err != nil {
		_ = storage.Close()
		return nil, fmt.Errorf("generate boot ID: %w", err)
	}

	a := &App{
		config:  config,
		bootID:  bootID,
		started: time.Now().UTC(),
		store:   storage,
	}

	mux := http.NewServeMux()
	buildAPI(mux, a)
	a.handler = mux
	return a, nil
}

// OpenAPISpecは、サーバーと同じConfigおよびルート登録からOpenAPIを組み立てる。
// 返す内容はApp設定に依存せず、常に同じになる。
func OpenAPISpec() *huma.OpenAPI {
	mux := http.NewServeMux()
	api := buildAPI(mux, &App{})
	return api.OpenAPI()
}

func (a *App) Handler() http.Handler {
	return a.handler
}

// BootIDは、プロセス寿命をまたぐログと応答を対応付ける識別子を返す。
func (a *App) BootID() string {
	return a.bootID
}

// Close は、Appが所有するSQLiteストアを閉じる。
func (a *App) Close() error {
	return a.store.Close()
}

func buildAPI(mux *http.ServeMux, a *App) huma.API {
	api := humago.New(mux, humaConfig())
	registerRoutes(api, a)
	return api
}

func humaConfig() huma.Config {
	return huma.Config{
		OpenAPI: &huma.OpenAPI{
			OpenAPI: "3.1.0",
			Info: &huma.Info{
				Title:   apiTitle,
				Version: apiVersion,
			},
			Components: &huma.Components{
				Schemas: huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer),
			},
		},
		OpenAPIPath:  "/openapi",
		DocsPath:     "/docs",
		DocsRenderer: huma.DocsRendererStoplightElements,
		SchemasPath:  "",
		Formats: map[string]huma.Format{
			"application/json": huma.DefaultJSONFormat,
			"json":             huma.DefaultJSONFormat,
		},
		DefaultFormat: "application/json",
	}
}

func registerRoutes(api huma.API, a *App) {
	huma.Register(api, huma.Operation{
		OperationID: "health",
		Method:      http.MethodGet,
		Path:        "/healthz",
		Summary:     "Check process health",
	}, a.health)

	huma.Register(api, huma.Operation{
		OperationID:   "readiness",
		Method:        http.MethodGet,
		Path:          "/readyz",
		Summary:       "Check snapshot readiness",
		DefaultStatus: http.StatusServiceUnavailable,
	}, a.ready)
	// Humaは動的なStatusフィールドを実行時には使うが、既定値以外の応答は自動生成しない。
	// 既存データがある場合の200にも、503と同じレスポンス形式を明示する。
	readinessResponses := api.OpenAPI().Paths["/readyz"].Get.Responses
	readinessResponses["200"] = &huma.Response{
		Description: http.StatusText(http.StatusOK),
		Content:     readinessResponses["503"].Content,
	}

	huma.Register(api, huma.Operation{
		OperationID: "status",
		Method:      http.MethodGet,
		Path:        "/v1/status",
		Summary:     "Get service status",
	}, a.status)
}

func (a *App) health(context.Context, *struct{}) (*healthOutput, error) {
	return &healthOutput{
		Body: HealthResponse{Status: "ok"},
	}, nil
}

func (a *App) ready(context.Context, *struct{}) (*readyOutput, error) {
	if a.store.Ready() {
		return &readyOutput{
			Status: http.StatusOK,
			Body: ReadyResponse{
				Status: "ready",
				Reason: "snapshot_loaded",
			},
		}, nil
	}
	return &readyOutput{
		Status: http.StatusServiceUnavailable,
		Body: ReadyResponse{
			Status: "not_ready",
			Reason: "snapshot_not_loaded",
		},
	}, nil
}

func (a *App) status(context.Context, *struct{}) (*statusOutput, error) {
	return &statusOutput{
		Body: StatusResponse{
			State:     string(a.store.State()),
			BootID:    a.bootID,
			StartedAt: a.started,
			Snapshot:  nil,
			Build: BuildInfo{
				Version: a.config.Version,
				Commit:  a.config.Commit,
			},
		},
	}, nil
}

func newBootID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}
