package main

import (
	"context"
	"crypto/sha256"
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
	"sync"
	"time"

	batchapp "batchscope/internal/app"
	"batchscope/internal/store"
)

type limitAnalysisReport struct {
	SchemaVersion string                       `json:"schema_version"`
	GeneratedAt   time.Time                    `json:"generated_at"`
	Configuration reportConfig                 `json:"configuration"`
	Environment   reportEnv                    `json:"environment"`
	Methodology   limitAnalysisMethodology     `json:"methodology"`
	Datasets      []limitAnalysisDatasetReport `json:"datasets"`
}

type limitAnalysisMethodology struct {
	TemporaryRoot         string `json:"temporary_root"`
	LatencyDefinition     string `json:"latency_definition"`
	ColdCacheDefinition   string `json:"cold_cache_definition"`
	WarmCacheDefinition   string `json:"warm_cache_definition"`
	ConcurrencyDefinition string `json:"concurrency_definition"`
	PercentileDefinition  string `json:"percentile_definition"`
}

type limitAnalysisDatasetReport struct {
	Name          string                    `json:"name"`
	Nodes         int                       `json:"nodes"`
	Relations     int                       `json:"relations"`
	ArchiveBytes  int64                     `json:"archive_bytes"`
	ArchiveSHA256 string                    `json:"archive_sha256"`
	Target        limitAnalysisTargetReport `json:"target"`
}

type limitAnalysisTargetReport struct {
	TargetID            string                     `json:"target_id"`
	ReturnedLimits      int                        `json:"returned_limits"`
	ReturnedTreeNodes   int                        `json:"returned_tree_nodes"`
	UncoveredRoutes     int                        `json:"uncovered_routes"`
	Cycles              int                        `json:"cycles"`
	Deterministic       bool                       `json:"deterministic"`
	DeterministicDigest string                     `json:"deterministic_digest"`
	Measurements        []limitAnalysisMeasurement `json:"measurements"`
}

type limitAnalysisMeasurement struct {
	Concurrency int                  `json:"concurrency"`
	CacheState  string               `json:"cache_state"`
	Runs        int                  `json:"runs"`
	Requests    int                  `json:"requests"`
	Rounds      []limitAnalysisRound `json:"rounds"`
	LatencyNS   distribution         `json:"latency_ns"`
}

type limitAnalysisRound struct {
	Run         int          `json:"run"`
	LatenciesNS []int64      `json:"latencies_ns"`
	LatencyNS   distribution `json:"latency_ns"`
}

type limitAnalysisResponse struct {
	SnapshotID string `json:"snapshotId"`
	Target     struct {
		ID string `json:"id"`
	} `json:"target"`
	Limits struct {
		Target     limitAnalysisSections `json:"target"`
		Contained  limitAnalysisSections `json:"contained"`
		Downstream limitAnalysisSections `json:"downstream"`
	} `json:"limits"`
	Tree            limitAnalysisTreeNode `json:"tree"`
	UncoveredRoutes []json.RawMessage     `json:"uncoveredRoutes"`
	Cycles          []json.RawMessage     `json:"cycles"`
}

type limitAnalysisSections struct {
	FinishByGroups []struct {
		Total int `json:"total"`
	} `json:"finishByGroups"`
	MaxElapsed struct {
		Total int `json:"total"`
	} `json:"maxElapsed"`
	Raw struct {
		Total int `json:"total"`
	} `json:"raw"`
}

type limitAnalysisTreeNode struct {
	TreeNodeID string                  `json:"treeNodeId"`
	Children   []limitAnalysisTreeNode `json:"children"`
}

type limitAnalysisOutcome struct {
	latencyNS int64
	body      []byte
	status    int
}

type limitAnalysisResultCounts struct {
	limits, treeNodes, uncoveredRoutes, cycles int
}

func runLimitAnalysis(configured config, output io.Writer) error {
	specs, err := selectDatasets(configured)
	if err != nil {
		return err
	}
	reported := limitAnalysisReport{
		SchemaVersion: reportSchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		Configuration: reportConfig{
			Mode: configured.Mode, Profile: configured.Profile, Nodes: configured.Nodes,
			Relations: configured.Relations, Target: configured.Target, Runs: configured.Runs,
			Concurrencies: slices.Clone(configured.Concurrencies),
		},
		Environment: reportEnv{
			GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, GoVersion: runtime.Version(),
			CPUs: runtime.NumCPU(), GOMAXPROCS: runtime.GOMAXPROCS(0),
		},
		Methodology: limitAnalysisMethodology{
			TemporaryRoot:         "/tmp",
			LatencyDefinition:     "latency starts immediately before the product HTTP handler ServeHTTP call and ends when it returns; generation acquisition, Traverse, Scan, Build, public DTO mapping, JSON encoding, logging, and response writes are included; request construction and response validation are excluded",
			ColdCacheDefinition:   "before every cold round, PRAGMA shrink_memory resets every SQLite connection page cache in the product search pool; the operating-system page cache is retained",
			WarmCacheDefinition:   "the warm round immediately follows its cold round on the same product HTTP handler and SQLite connection pool without resetting connection page caches",
			ConcurrencyDefinition: "all workers construct independent httptest requests and recorders before a barrier, then call the same product HTTP handler simultaneously",
			PercentileDefinition:  "p95 uses the nearest-rank method over every request latency for the dataset, cache state, and concurrency",
		},
		Datasets: make([]limitAnalysisDatasetReport, 0, len(specs)),
	}

	for _, spec := range specs {
		fixture, err := prepareFixture(spec)
		if err != nil {
			return fmt.Errorf("prepare %s: %w", spec.name, err)
		}
		datasetReport, measureErr := measureLimitAnalysisFixture(configured, fixture)
		cleanupErr := fixture.cleanup()
		if measureErr != nil || cleanupErr != nil {
			return errors.Join(measureErr, cleanupErr)
		}
		reported.Datasets = append(reported.Datasets, datasetReport)
	}

	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(reported)
}

func measureLimitAnalysisFixture(configured config, fixture *fixture) (limitAnalysisDatasetReport, error) {
	targetID, err := fixture.selectTarget(configured.Target)
	if err != nil {
		return limitAnalysisDatasetReport{}, err
	}
	setup, err := importOnce(fixture, 1)
	if err != nil {
		return limitAnalysisDatasetReport{}, fmt.Errorf("import %s: %w", fixture.Name, err)
	}
	application, err := batchapp.NewWithStore(batchapp.Config{
		Version: "perf-measure", Commit: "working-tree", DataDir: setup.active.workspace,
	}, setup.active.storage)
	if err != nil {
		return limitAnalysisDatasetReport{}, errors.Join(err, setup.active.close())
	}

	targetReport, measureErr := measureLimitAnalysisTarget(
		application.Handler(), setup.active.storage, targetID, configured.Concurrencies, configured.Runs,
	)
	closeErr := application.Close()
	removeErr := os.RemoveAll(setup.active.workspace)
	if measureErr != nil || closeErr != nil || removeErr != nil {
		return limitAnalysisDatasetReport{}, errors.Join(measureErr, closeErr, removeErr)
	}
	return limitAnalysisDatasetReport{
		Name: fixture.Name, Nodes: fixture.NodeCount, Relations: fixture.RelationCount,
		ArchiveBytes: fixture.ArchiveBytes, ArchiveSHA256: fixture.ArchiveSHA256, Target: targetReport,
	}, nil
}

func measureLimitAnalysisTarget(handler http.Handler, storage *store.Store, targetID string, concurrencies []int, runs int) (limitAnalysisTargetReport, error) {
	report := limitAnalysisTargetReport{TargetID: targetID, Deterministic: true, Measurements: make([]limitAnalysisMeasurement, 0, len(concurrencies)*2)}
	var expectedDigest string
	var expectedCounts limitAnalysisResultCounts
	for _, concurrency := range concurrencies {
		coldRounds := make([]limitAnalysisRound, 0, runs)
		warmRounds := make([]limitAnalysisRound, 0, runs)
		coldLatencies := make([]int64, 0, runs*concurrency)
		warmLatencies := make([]int64, 0, runs*concurrency)
		for runNumber := 1; runNumber <= runs; runNumber++ {
			if err := resetHTTPPageCaches(context.Background(), storage); err != nil {
				return limitAnalysisTargetReport{}, fmt.Errorf("reset page caches for %s: %w", targetID, err)
			}
			runtime.GC()
			cold, digest, counts, err := runLimitAnalysisRound(handler, targetID, concurrency, runNumber, expectedDigest)
			if err != nil {
				return limitAnalysisTargetReport{}, err
			}
			if expectedDigest == "" {
				expectedDigest, expectedCounts = digest, counts
			}
			coldRounds = append(coldRounds, cold)
			coldLatencies = append(coldLatencies, cold.LatenciesNS...)

			runtime.GC()
			warm, _, _, err := runLimitAnalysisRound(handler, targetID, concurrency, runNumber, expectedDigest)
			if err != nil {
				return limitAnalysisTargetReport{}, err
			}
			warmRounds = append(warmRounds, warm)
			warmLatencies = append(warmLatencies, warm.LatenciesNS...)
		}
		report.Measurements = append(report.Measurements,
			newLimitAnalysisMeasurement(concurrency, "sqlite_page_caches_reset", runs, coldRounds, coldLatencies),
			newLimitAnalysisMeasurement(concurrency, "reused_sqlite_page_caches", runs, warmRounds, warmLatencies),
		)
	}
	report.DeterministicDigest = expectedDigest
	report.ReturnedLimits = expectedCounts.limits
	report.ReturnedTreeNodes = expectedCounts.treeNodes
	report.UncoveredRoutes = expectedCounts.uncoveredRoutes
	report.Cycles = expectedCounts.cycles
	return report, nil
}

func newLimitAnalysisMeasurement(concurrency int, cacheState string, runs int, rounds []limitAnalysisRound, latencies []int64) limitAnalysisMeasurement {
	return limitAnalysisMeasurement{
		Concurrency: concurrency, CacheState: cacheState, Runs: runs,
		Requests: len(latencies), Rounds: rounds, LatencyNS: summarize(latencies),
	}
}

func runLimitAnalysisRound(handler http.Handler, targetID string, concurrency, runNumber int, expectedDigest string) (limitAnalysisRound, string, limitAnalysisResultCounts, error) {
	requestPath := "/v1/downstream-limit-analysis?" + url.Values{"targetId": []string{targetID}}.Encode()
	start := make(chan struct{})
	ready := make(chan struct{}, concurrency)
	outcomes := make(chan limitAnalysisOutcome, concurrency)
	var workers sync.WaitGroup
	for range concurrency {
		request := httptest.NewRequest(http.MethodGet, requestPath, nil)
		request.Header.Set("X-Request-Id", "perf-limit-analysis")
		recorder := httptest.NewRecorder()
		workers.Add(1)
		go func() {
			defer workers.Done()
			ready <- struct{}{}
			<-start
			started := time.Now()
			handler.ServeHTTP(recorder, request)
			outcomes <- limitAnalysisOutcome{
				latencyNS: time.Since(started).Nanoseconds(), body: slices.Clone(recorder.Body.Bytes()), status: recorder.Code,
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
	var counts limitAnalysisResultCounts
	for outcome := range outcomes {
		currentDigest, currentCounts, err := validateLimitAnalysisOutcome(targetID, outcome)
		if err != nil {
			return limitAnalysisRound{}, "", limitAnalysisResultCounts{}, fmt.Errorf("measure %s at concurrency %d: %w", targetID, concurrency, err)
		}
		if digest == "" {
			digest, counts = currentDigest, currentCounts
		}
		if currentDigest != digest {
			return limitAnalysisRound{}, "", limitAnalysisResultCounts{}, fmt.Errorf("target %s produced non-deterministic HTTP responses", targetID)
		}
		latencies = append(latencies, outcome.latencyNS)
	}
	slices.Sort(latencies)
	return limitAnalysisRound{Run: runNumber, LatenciesNS: latencies, LatencyNS: summarize(latencies)}, digest, counts, nil
}

func validateLimitAnalysisOutcome(targetID string, outcome limitAnalysisOutcome) (string, limitAnalysisResultCounts, error) {
	if outcome.status != http.StatusOK {
		return "", limitAnalysisResultCounts{}, fmt.Errorf("HTTP status = %d, want %d: %s", outcome.status, http.StatusOK, outcome.body)
	}
	var response limitAnalysisResponse
	if err := json.Unmarshal(outcome.body, &response); err != nil {
		return "", limitAnalysisResultCounts{}, fmt.Errorf("decode response: %w", err)
	}
	if response.SnapshotID == "" || response.Target.ID != targetID || response.Tree.TreeNodeID == "" {
		return "", limitAnalysisResultCounts{}, fmt.Errorf("response snapshotId=%q target=%q treeNodeId=%q", response.SnapshotID, response.Target.ID, response.Tree.TreeNodeID)
	}
	counts := limitAnalysisResultCounts{
		limits:    countLimitAnalysisSections(response.Limits.Target) + countLimitAnalysisSections(response.Limits.Contained) + countLimitAnalysisSections(response.Limits.Downstream),
		treeNodes: countLimitAnalysisTreeNodes(response.Tree), uncoveredRoutes: len(response.UncoveredRoutes), cycles: len(response.Cycles),
	}
	digest := sha256.Sum256(outcome.body)
	return hex.EncodeToString(digest[:]), counts, nil
}

func countLimitAnalysisSections(section limitAnalysisSections) int {
	total := section.MaxElapsed.Total + section.Raw.Total
	for _, group := range section.FinishByGroups {
		total += group.Total
	}
	return total
}

func countLimitAnalysisTreeNodes(root limitAnalysisTreeNode) int {
	count := 0
	stack := []limitAnalysisTreeNode{root}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		count++
		stack = append(stack, current.Children...)
	}
	return count
}
