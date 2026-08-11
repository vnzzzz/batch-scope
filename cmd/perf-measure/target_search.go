package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	batchapp "batchscope/internal/app"
	"batchscope/internal/store"
	"batchscope/internal/testsupport/graphgen"
)

const (
	targetSearchSingleName    = "target search unique name"
	targetSearchSinglePath    = "/PERF/TARGET-SEARCH/UNIQUE"
	targetSearchManyName      = "target search shared name"
	targetSearchManyPath      = "/PERF/TARGET-SEARCH/SHARED"
	targetSearchTruncatedName = "target search truncated name"
)

type targetSearchReport struct {
	SchemaVersion string                    `json:"schema_version"`
	GeneratedAt   time.Time                 `json:"generated_at"`
	Configuration reportConfig              `json:"configuration"`
	Environment   reportEnv                 `json:"environment"`
	Methodology   targetSearchMethodology   `json:"methodology"`
	Dataset       targetSearchDatasetReport `json:"dataset"`
}

type targetSearchMethodology struct {
	TemporaryRoot         string `json:"temporary_root"`
	LatencyDefinition     string `json:"latency_definition"`
	ColdCacheDefinition   string `json:"cold_cache_definition"`
	WarmCacheDefinition   string `json:"warm_cache_definition"`
	ConcurrencyDefinition string `json:"concurrency_definition"`
	PercentileDefinition  string `json:"percentile_definition"`
}

type targetSearchDatasetReport struct {
	Name          string                   `json:"name"`
	Nodes         int                      `json:"nodes"`
	Relations     int                      `json:"relations"`
	ArchiveBytes  int64                    `json:"archive_bytes"`
	ArchiveSHA256 string                   `json:"archive_sha256"`
	Cases         []targetSearchCaseReport `json:"cases"`
}

type targetSearchCase struct {
	Name            string
	MatchKind       string
	Query           string
	ReturnedTargets int
	Truncated       bool
}

type targetSearchCaseReport struct {
	Name                string                    `json:"name"`
	MatchKind           string                    `json:"match_kind"`
	ReturnedTargets     int                       `json:"returned_targets"`
	Truncated           bool                      `json:"truncated"`
	Deterministic       bool                      `json:"deterministic"`
	DeterministicDigest string                    `json:"deterministic_digest"`
	Measurements        []targetSearchMeasurement `json:"measurements"`
}

type targetSearchMeasurement struct {
	Concurrency int                 `json:"concurrency"`
	CacheState  string              `json:"cache_state"`
	Runs        int                 `json:"runs"`
	Requests    int                 `json:"requests"`
	Rounds      []targetSearchRound `json:"rounds"`
	LatencyNS   distribution        `json:"latency_ns"`
}

type targetSearchRound struct {
	Run         int          `json:"run"`
	LatenciesNS []int64      `json:"latencies_ns"`
	LatencyNS   distribution `json:"latency_ns"`
}

type targetSearchResponse struct {
	SnapshotID string `json:"snapshotId"`
	Items      []struct {
		MatchedBy []string `json:"matchedBy"`
	} `json:"items"`
	Truncated bool `json:"truncated"`
}

type targetSearchOutcome struct {
	latencyNS int64
	body      []byte
	status    int
}

func runTargetSearch(configured config, output io.Writer) error {
	if configured.Profile != "small" {
		return errors.New("target-search mode requires profile small")
	}
	spec := datasetSpec{name: "small", build: targetSearchDataset}
	reported, err := measureTargetSearch(configured, spec, defaultTargetSearchCases())
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(reported)
}

func measureTargetSearch(configured config, spec datasetSpec, cases []targetSearchCase) (targetSearchReport, error) {
	fixture, err := prepareFixture(spec)
	if err != nil {
		return targetSearchReport{}, fmt.Errorf("prepare %s: %w", spec.name, err)
	}
	reported, measureErr := measureTargetSearchFixture(configured, fixture, cases)
	cleanupErr := fixture.cleanup()
	if measureErr != nil || cleanupErr != nil {
		return targetSearchReport{}, errors.Join(measureErr, cleanupErr)
	}
	return reported, nil
}

func measureTargetSearchFixture(configured config, fixture *fixture, cases []targetSearchCase) (targetSearchReport, error) {
	setup, err := importOnce(fixture, 1)
	if err != nil {
		return targetSearchReport{}, fmt.Errorf("import %s: %w", fixture.Name, err)
	}
	application, err := batchapp.NewWithStore(batchapp.Config{
		Version: "perf-measure", Commit: "working-tree", DataDir: setup.active.workspace,
	}, setup.active.storage)
	if err != nil {
		return targetSearchReport{}, errors.Join(err, setup.active.close())
	}

	caseReports, measureErr := measureTargetSearchCases(
		application.Handler(), setup.active.storage, cases, configured.Concurrencies, configured.Runs,
	)
	closeErr := application.Close()
	removeErr := os.RemoveAll(setup.active.workspace)
	if measureErr != nil || closeErr != nil || removeErr != nil {
		return targetSearchReport{}, errors.Join(measureErr, closeErr, removeErr)
	}

	return targetSearchReport{
		SchemaVersion: reportSchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		Configuration: reportConfig{
			Mode: configured.Mode, Profile: configured.Profile, Runs: configured.Runs,
			Concurrencies: slices.Clone(configured.Concurrencies),
		},
		Environment: reportEnv{
			GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, GoVersion: runtime.Version(),
			CPUs: runtime.NumCPU(), GOMAXPROCS: runtime.GOMAXPROCS(0),
		},
		Methodology: targetSearchMethodology{
			TemporaryRoot:         "/tmp",
			LatencyDefinition:     "latency starts immediately before the product HTTP handler ServeHTTP call and ends when it returns; routing, parameter parsing, search, DTO assembly, JSON encoding, logging, and response writes are included; request construction and response validation are excluded",
			ColdCacheDefinition:   "before every cold round, PRAGMA shrink_memory resets every SQLite connection page cache in the product search pool; the operating-system page cache is retained",
			WarmCacheDefinition:   "the warm round immediately follows its cold round on the same product HTTP handler and SQLite connection pool without resetting connection page caches",
			ConcurrencyDefinition: "all workers construct independent httptest requests and recorders before a barrier, then call the same product HTTP handler simultaneously",
			PercentileDefinition:  "p95 uses the nearest-rank method over every request latency for the case, cache state, and concurrency",
		},
		Dataset: targetSearchDatasetReport{
			Name: fixture.Name, Nodes: fixture.NodeCount, Relations: fixture.RelationCount,
			ArchiveBytes: fixture.ArchiveBytes, ArchiveSHA256: fixture.ArchiveSHA256,
			Cases: caseReports,
		},
	}, nil
}

func measureTargetSearchCases(handler http.Handler, storage *store.Store, cases []targetSearchCase, concurrencies []int, runs int) ([]targetSearchCaseReport, error) {
	reports := make([]targetSearchCaseReport, 0, len(cases))
	for _, searchCase := range cases {
		report := targetSearchCaseReport{
			Name: searchCase.Name, MatchKind: searchCase.MatchKind,
			ReturnedTargets: searchCase.ReturnedTargets, Truncated: searchCase.Truncated,
			Deterministic: true,
			Measurements:  make([]targetSearchMeasurement, 0, len(concurrencies)*2),
		}
		var expectedDigest string
		for _, concurrency := range concurrencies {
			coldRounds := make([]targetSearchRound, 0, runs)
			warmRounds := make([]targetSearchRound, 0, runs)
			coldLatencies := make([]int64, 0, runs*concurrency)
			warmLatencies := make([]int64, 0, runs*concurrency)
			for runNumber := 1; runNumber <= runs; runNumber++ {
				if err := resetTargetSearchPageCaches(context.Background(), storage); err != nil {
					return nil, fmt.Errorf("reset page caches for %s: %w", searchCase.Name, err)
				}
				runtime.GC()
				cold, digest, err := runTargetSearchRound(handler, searchCase, concurrency, runNumber, expectedDigest)
				if err != nil {
					return nil, err
				}
				if expectedDigest == "" {
					expectedDigest = digest
				}
				coldRounds = append(coldRounds, cold)
				coldLatencies = append(coldLatencies, cold.LatenciesNS...)

				runtime.GC()
				warm, _, err := runTargetSearchRound(handler, searchCase, concurrency, runNumber, expectedDigest)
				if err != nil {
					return nil, err
				}
				warmRounds = append(warmRounds, warm)
				warmLatencies = append(warmLatencies, warm.LatenciesNS...)
			}
			report.Measurements = append(report.Measurements,
				newTargetSearchMeasurement(concurrency, "sqlite_page_caches_reset", runs, coldRounds, coldLatencies),
				newTargetSearchMeasurement(concurrency, "reused_sqlite_page_caches", runs, warmRounds, warmLatencies),
			)
		}
		report.DeterministicDigest = expectedDigest
		reports = append(reports, report)
	}
	return reports, nil
}

func newTargetSearchMeasurement(concurrency int, cacheState string, runs int, rounds []targetSearchRound, latencies []int64) targetSearchMeasurement {
	return targetSearchMeasurement{
		Concurrency: concurrency, CacheState: cacheState, Runs: runs,
		Requests: len(latencies), Rounds: rounds, LatencyNS: summarize(latencies),
	}
}

func runTargetSearchRound(handler http.Handler, searchCase targetSearchCase, concurrency, runNumber int, expectedDigest string) (targetSearchRound, string, error) {
	requestPath := "/v1/targets?" + url.Values{
		"query": []string{searchCase.Query}, "type": []string{"job"},
	}.Encode()
	start := make(chan struct{})
	ready := make(chan struct{}, concurrency)
	outcomes := make(chan targetSearchOutcome, concurrency)
	var workers sync.WaitGroup
	for range concurrency {
		request := httptest.NewRequest(http.MethodGet, requestPath, nil)
		request.Header.Set("X-Request-Id", "perf-target-search")
		recorder := httptest.NewRecorder()
		workers.Add(1)
		go func() {
			defer workers.Done()
			ready <- struct{}{}
			<-start
			started := time.Now()
			handler.ServeHTTP(recorder, request)
			latency := time.Since(started)
			outcomes <- targetSearchOutcome{
				latencyNS: latency.Nanoseconds(), body: slices.Clone(recorder.Body.Bytes()), status: recorder.Code,
			}
		}()
	}
	for range concurrency {
		<-ready
	}
	// 要求とrecorderの準備後に全workerを解放し、クライアント側の準備時間をHTTPレイテンシへ含めない。
	close(start)
	workers.Wait()
	close(outcomes)

	latencies := make([]int64, 0, concurrency)
	digest := expectedDigest
	for outcome := range outcomes {
		currentDigest, err := validateTargetSearchOutcome(searchCase, outcome)
		if err != nil {
			return targetSearchRound{}, "", fmt.Errorf("measure %s at concurrency %d: %w", searchCase.Name, concurrency, err)
		}
		if digest == "" {
			digest = currentDigest
		}
		if currentDigest != digest {
			return targetSearchRound{}, "", fmt.Errorf("case %s produced non-deterministic HTTP responses", searchCase.Name)
		}
		latencies = append(latencies, outcome.latencyNS)
	}
	slices.Sort(latencies)
	return targetSearchRound{
		Run: runNumber, LatenciesNS: latencies, LatencyNS: summarize(latencies),
	}, digest, nil
}

func validateTargetSearchOutcome(searchCase targetSearchCase, outcome targetSearchOutcome) (string, error) {
	if outcome.status != http.StatusOK {
		return "", fmt.Errorf("HTTP status = %d, want %d: %s", outcome.status, http.StatusOK, outcome.body)
	}
	var response targetSearchResponse
	if err := json.Unmarshal(outcome.body, &response); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if response.SnapshotID == "" {
		return "", errors.New("response snapshotId is empty")
	}
	if len(response.Items) != searchCase.ReturnedTargets || response.Truncated != searchCase.Truncated {
		return "", fmt.Errorf("returned %d targets with truncated=%v, want %d and %v", len(response.Items), response.Truncated, searchCase.ReturnedTargets, searchCase.Truncated)
	}
	for _, item := range response.Items {
		if !slices.Contains(item.MatchedBy, searchCase.MatchKind) {
			return "", fmt.Errorf("matchedBy = %v, want %q", item.MatchedBy, searchCase.MatchKind)
		}
	}
	digest := sha256.Sum256(outcome.body)
	return hex.EncodeToString(digest[:]), nil
}

func resetTargetSearchPageCaches(ctx context.Context, storage *store.Store) error {
	database, _, release, err := storage.Acquire()
	if err != nil {
		return err
	}
	defer release()
	connections := database.Stats().MaxOpenConnections
	return forEachComparisonConnection(ctx, database, connections, func(ctx context.Context, connection *sql.Conn) error {
		_, err := connection.ExecContext(ctx, "PRAGMA shrink_memory")
		return err
	})
}

func defaultTargetSearchCases() []targetSearchCase {
	return []targetSearchCase{
		{Name: "id-single", MatchKind: "id", Query: "JOB-TARGET", ReturnedTargets: 1},
		{Name: "name-single", MatchKind: "name", Query: targetSearchSingleName, ReturnedTargets: 1},
		{Name: "path-single", MatchKind: "path", Query: targetSearchSinglePath, ReturnedTargets: 1},
		{Name: "name-many", MatchKind: "name", Query: targetSearchManyName, ReturnedTargets: 100},
		{Name: "path-many", MatchKind: "path", Query: targetSearchManyPath, ReturnedTargets: 100},
		{Name: "name-truncated", MatchKind: "name", Query: targetSearchTruncatedName, ReturnedTargets: 1_000, Truncated: true},
	}
}

func targetSearchDataset() graphgen.Dataset {
	// graphgen.Smallは呼出しごとに新しく生成されるため、このモード専用のコピーだけに検索条件を加える。
	// 既存モードが使うSmallのアーカイブと測定済みSHA-256は変更しない。
	dataset := graphgen.Small()
	regularJobs := make([]*graphgen.Node, 0, len(dataset.Nodes))
	for index := range dataset.Nodes {
		node := &dataset.Nodes[index]
		if node.ID == "JOB-TARGET" {
			node.Name = targetSearchSingleName
			node.Path = targetSearchString(targetSearchSinglePath)
		}
		if node.Type == "job" && strings.HasPrefix(node.ID, "JOB-") && node.ID != "JOB-TARGET" {
			regularJobs = append(regularJobs, node)
		}
	}
	const requiredJobs = 100 + 100 + 1_001
	if len(regularJobs) < requiredJobs {
		panic(fmt.Sprintf("target search fixture has %d regular jobs, want at least %d", len(regularJobs), requiredJobs))
	}
	for _, node := range regularJobs[:100] {
		node.Name = targetSearchManyName
	}
	for _, node := range regularJobs[100:200] {
		node.Path = targetSearchString(targetSearchManyPath)
	}
	for _, node := range regularJobs[200:requiredJobs] {
		node.Name = targetSearchTruncatedName
	}
	return dataset
}

func targetSearchString(value string) *string {
	return &value
}
