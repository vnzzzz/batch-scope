package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"sync"
	"time"

	"batchscope/internal/limitscan"
	"batchscope/internal/pathtree"
	"batchscope/internal/snapshot"
	"batchscope/internal/store"
	"batchscope/internal/testsupport/graphgen"
	"batchscope/internal/traversal"
)

type fixture struct {
	Name          string
	ArchivePath   string
	ArchiveBytes  int64
	ArchiveSHA256 string
	NodeCount     int
	RelationCount int
	TargetIDs     []string
	Directory     string
}

type datasetReport struct {
	Name                 string                      `json:"name"`
	Input                inputDescription            `json:"input"`
	Import               *importReport               `json:"import,omitempty"`
	Searches             []searchReport              `json:"searches,omitempty"`
	Concurrency          []concurrencyReport         `json:"concurrency,omitempty"`
	ConnectionComparison *connectionComparisonReport `json:"connection_comparison,omitempty"`
}

type inputDescription struct {
	Nodes         int      `json:"nodes"`
	Relations     int      `json:"relations"`
	Targets       []string `json:"targets"`
	ArchiveBytes  int64    `json:"archive_bytes"`
	ArchiveSHA256 string   `json:"archive_sha256"`
}

type importReport struct {
	Runs    []importRun             `json:"runs"`
	Summary map[string]distribution `json:"summary"`
}

type importRun struct {
	Run                    int   `json:"run"`
	TotalNS                int64 `json:"total_ns"`
	ReceiveNS              int64 `json:"receive_ns"`
	ExtractNS              int64 `json:"extract_ns"`
	ValidateNS             int64 `json:"validate_ns"`
	LoadNS                 int64 `json:"load_ns"`
	CompleteNS             int64 `json:"complete_ns"`
	BaselineHeapBytes      int64 `json:"baseline_heap_bytes"`
	PeakHeapBytes          int64 `json:"peak_heap_bytes"`
	PeakHeapDeltaBytes     int64 `json:"peak_heap_delta_bytes"`
	BaselineRSSBytes       int64 `json:"baseline_rss_bytes"`
	PeakRSSBytes           int64 `json:"peak_rss_bytes"`
	PeakRSSDeltaBytes      int64 `json:"peak_rss_delta_bytes"`
	PeakTemporaryDiskBytes int64 `json:"peak_temporary_disk_bytes"`
	SQLiteBytes            int64 `json:"sqlite_bytes"`
}

type searchReport struct {
	TargetID            string                  `json:"target_id"`
	CacheState          string                  `json:"cache_state"`
	Runs                []searchRun             `json:"runs"`
	Summary             map[string]distribution `json:"summary"`
	Deterministic       bool                    `json:"deterministic"`
	DeterministicDigest string                  `json:"deterministic_digest"`
}

type searchRun struct {
	Run                 int            `json:"run"`
	TraverseNS          int64          `json:"traverse_ns"`
	ScanNS              int64          `json:"scan_ns"`
	BuildNS             int64          `json:"build_ns"`
	TotalNS             int64          `json:"total_ns"`
	SerializeDigestNS   int64          `json:"serialize_digest_ns,omitempty"`
	BaselineHeapBytes   int64          `json:"baseline_heap_bytes"`
	PeakHeapBytes       int64          `json:"peak_heap_bytes"`
	PeakHeapDeltaBytes  int64          `json:"peak_heap_delta_bytes"`
	BaselineRSSBytes    int64          `json:"baseline_rss_bytes"`
	PeakRSSBytes        int64          `json:"peak_rss_bytes"`
	PeakRSSDeltaBytes   int64          `json:"peak_rss_delta_bytes"`
	Digest              string         `json:"digest,omitempty"`
	TraversalStats      traversalStats `json:"traversal_stats"`
	ReachedNodes        int            `json:"reached_nodes"`
	RelationConnections int            `json:"relation_connections"`
	RelationRows        int            `json:"relation_rows"`
	ScopeEdges          int            `json:"scope_edges"`
	SCCCount            int            `json:"scc_count"`
	SCCMaxSize          int            `json:"scc_max_size"`
	Limits              limitCounts    `json:"limits"`
	TreeNodes           int            `json:"tree_nodes"`
	HiddenConnections   int            `json:"hidden_connections"`
	UncoveredRoutes     int            `json:"uncovered_routes"`
}

type limitCounts struct {
	Target     int `json:"target"`
	Contained  int `json:"contained"`
	Downstream int `json:"downstream"`
	FinishBy   int `json:"finish_by"`
	MaxElapsed int `json:"max_elapsed"`
	Raw        int `json:"raw"`
	Total      int `json:"total"`
}

type traversalStats struct {
	ExpandedNodes   int `json:"expanded_nodes"`
	RelationQueries int `json:"relation_queries"`
	RelationRows    int `json:"relation_rows"`
	ScopeQueries    int `json:"scope_queries"`
	ScopeRows       int `json:"scope_rows"`
}

type concurrencyReport struct {
	TargetID         string             `json:"target_id"`
	Concurrency      int                `json:"concurrency"`
	CacheState       string             `json:"cache_state"`
	Rounds           []concurrencyRound `json:"rounds"`
	LatencyNS        distribution       `json:"latency_ns"`
	ThroughputPerSec floatDistribution  `json:"throughput_per_second"`
	WaitCount        distribution       `json:"db_wait_count"`
	WaitDurationNS   distribution       `json:"db_wait_duration_ns"`
	PeakHeapDelta    distribution       `json:"peak_heap_delta_bytes"`
	PeakRSSDelta     distribution       `json:"peak_rss_delta_bytes"`
}

type concurrencyRound struct {
	Round               int          `json:"round"`
	WallNS              int64        `json:"wall_ns"`
	ThroughputPerSecond float64      `json:"throughput_per_second"`
	LatenciesNS         []int64      `json:"latencies_ns"`
	Latency             distribution `json:"latency_ns"`
	DBBefore            dbStats      `json:"db_before"`
	DBAfter             dbStats      `json:"db_after"`
	WaitCountDelta      int64        `json:"wait_count_delta"`
	WaitDurationDeltaNS int64        `json:"wait_duration_delta_ns"`
	BaselineHeapBytes   int64        `json:"baseline_heap_bytes"`
	PeakHeapBytes       int64        `json:"peak_heap_bytes"`
	PeakHeapDeltaBytes  int64        `json:"peak_heap_delta_bytes"`
	BaselineRSSBytes    int64        `json:"baseline_rss_bytes"`
	PeakRSSBytes        int64        `json:"peak_rss_bytes"`
	PeakRSSDeltaBytes   int64        `json:"peak_rss_delta_bytes"`
}

type connectionComparisonReport struct {
	TargetID                     string                     `json:"target_id"`
	CacheState                   string                     `json:"cache_state"`
	DatabaseGenerationPathPolicy string                     `json:"database_generation_path_policy"`
	ConnectionInitialization     string                     `json:"connection_initialization"`
	ExecutionOrder               string                     `json:"execution_order"`
	Results                      []connectionStrategyReport `json:"results"`
}

type connectionStrategyReport struct {
	ConnectionStrategy           string            `json:"connection_strategy"`
	ConfiguredMaxOpenConnections int               `json:"configured_max_open_connections"`
	Measurement                  concurrencyReport `json:"measurement"`
}

type dbStats struct {
	MaxOpenConnections int   `json:"max_open_connections"`
	OpenConnections    int   `json:"open_connections"`
	InUse              int   `json:"in_use"`
	Idle               int   `json:"idle"`
	WaitCount          int64 `json:"wait_count"`
	WaitDurationNS     int64 `json:"wait_duration_ns"`
}

type activeDataset struct {
	storage   *store.Store
	workspace string
}

func prepareFixture(spec datasetSpec) (*fixture, error) {
	dataset := spec.build()
	archive, err := dataset.Archive(false)
	if err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp("/tmp", "batchscope-perf-input-")
	if err != nil {
		return nil, err
	}
	archivePath := filepath.Join(directory, "snapshot.tar.gz")
	if err := os.WriteFile(archivePath, archive, 0o600); err != nil {
		_ = os.RemoveAll(directory)
		return nil, err
	}
	digest := sha256.Sum256(archive)
	targetIDs := make([]string, len(dataset.Expectations))
	for index, expectation := range dataset.Expectations {
		targetIDs[index] = expectation.TargetID
	}
	result := &fixture{
		Name: spec.name, ArchivePath: archivePath, ArchiveBytes: int64(len(archive)),
		ArchiveSHA256: hex.EncodeToString(digest[:]), NodeCount: len(dataset.Nodes),
		RelationCount: len(dataset.Relations), TargetIDs: targetIDs, Directory: directory,
	}
	// graphgen deliberately materializes expectations. Release them before measuring so
	// heap deltas describe the import and search pipelines instead of fixture construction.
	archive = nil
	dataset = graphgen.Dataset{}
	runtime.GC()
	debug.FreeOSMemory()
	return result, nil
}

func (fixture *fixture) cleanup() error {
	return os.RemoveAll(fixture.Directory)
}

func measureDataset(configured config, fixture *fixture) (datasetReport, error) {
	result := datasetReport{
		Name: fixture.Name,
		Input: inputDescription{
			Nodes: fixture.NodeCount, Relations: fixture.RelationCount, Targets: fixture.TargetIDs,
			ArchiveBytes: fixture.ArchiveBytes, ArchiveSHA256: fixture.ArchiveSHA256,
		},
	}
	if configured.Mode != "concurrent" && configured.Mode != "connection-comparison" {
		imports, active, err := measureImports(fixture, configured.Runs)
		if err != nil {
			return datasetReport{}, err
		}
		result.Import = &importReport{Runs: imports, Summary: summarizeImports(imports)}
		if configured.Mode == "import" {
			if err := active.close(); err != nil {
				return datasetReport{}, err
			}
			return result, nil
		}
		searches, searchErr := measureSearches(active.storage, fixture.TargetIDs, configured.Runs)
		closeErr := active.close()
		if searchErr != nil || closeErr != nil {
			return datasetReport{}, errors.Join(searchErr, closeErr)
		}
		result.Searches = searches
		return result, nil
	}

	setup, err := importOnce(fixture, 1)
	if err != nil {
		return datasetReport{}, err
	}
	result.Import = &importReport{Runs: []importRun{setup.measurement}, Summary: summarizeImports([]importRun{setup.measurement})}
	if configured.Mode == "connection-comparison" {
		comparison, measureErr := measureConnectionComparison(&setup.active, fixture.TargetIDs[0], configured.Concurrencies, configured.Runs)
		closeErr := setup.active.close()
		if measureErr != nil || closeErr != nil {
			return datasetReport{}, errors.Join(measureErr, closeErr)
		}
		result.ConnectionComparison = comparison
		return result, nil
	}
	concurrency, measureErr := measureConcurrency(setup.active.storage, fixture.TargetIDs[0], configured.Concurrencies, configured.Runs)
	closeErr := setup.active.close()
	if measureErr != nil || closeErr != nil {
		return datasetReport{}, errors.Join(measureErr, closeErr)
	}
	result.Concurrency = concurrency
	return result, nil
}

func (active activeDataset) close() error {
	if active.storage == nil {
		return nil
	}
	return errors.Join(active.storage.Close(), os.RemoveAll(active.workspace))
}

type importedRun struct {
	measurement importRun
	active      activeDataset
}

func measureImports(fixture *fixture, runs int) ([]importRun, activeDataset, error) {
	measurements := make([]importRun, 0, runs)
	var retained activeDataset
	for runNumber := 1; runNumber <= runs; runNumber++ {
		if retained.storage != nil {
			if err := retained.close(); err != nil {
				return nil, activeDataset{}, err
			}
			retained = activeDataset{}
			// Each run is an independent import sample. Return the previous run's heap to
			// the OS before taking the next baseline so two snapshot generations do not overlap.
			runtime.GC()
			debug.FreeOSMemory()
		}
		current, err := importOnce(fixture, runNumber)
		if err != nil {
			return nil, activeDataset{}, err
		}
		measurements = append(measurements, current.measurement)
		retained = current.active
	}
	return measurements, retained, nil
}

func importOnce(fixture *fixture, runNumber int) (result importedRun, err error) {
	workspace, err := os.MkdirTemp("/tmp", "batchscope-perf-run-")
	if err != nil {
		return importedRun{}, err
	}
	dataDirectory := filepath.Join(workspace, "data")
	storage, err := store.New(dataDirectory)
	if err != nil {
		_ = os.RemoveAll(workspace)
		return importedRun{}, err
	}
	keep := false
	defer func() {
		if !keep {
			err = errors.Join(err, storage.Close(), os.RemoveAll(workspace))
		}
	}()

	archive, err := os.Open(fixture.ArchivePath)
	if err != nil {
		return importedRun{}, err
	}
	defer archive.Close()
	runtime.GC()
	monitor := startPeakMonitor(workspace, true)
	defer monitor.stop()
	totalStart := time.Now()
	operation, err := storage.BeginImport(context.Background())
	if err != nil {
		return importedRun{}, err
	}
	abort := true
	defer func() {
		if abort {
			err = errors.Join(err, operation.Abort())
		}
	}()

	phaseStart := time.Now()
	archivePath, err := snapshot.Receive(context.Background(), workspace, archive)
	result.measurement.ReceiveNS = time.Since(phaseStart).Nanoseconds()
	monitor.mark()
	if err != nil {
		return importedRun{}, err
	}
	defer os.Remove(archivePath)

	phaseStart = time.Now()
	extracted, err := snapshot.Extract(context.Background(), archivePath, workspace)
	result.measurement.ExtractNS = time.Since(phaseStart).Nanoseconds()
	monitor.mark()
	if err != nil {
		return importedRun{}, err
	}
	defer os.RemoveAll(extracted.Directory)
	if err := os.Remove(archivePath); err != nil {
		return importedRun{}, err
	}

	phaseStart = time.Now()
	validated, err := snapshot.Validate(context.Background(), extracted)
	result.measurement.ValidateNS = time.Since(phaseStart).Nanoseconds()
	monitor.mark()
	if err != nil {
		return importedRun{}, err
	}

	phaseStart = time.Now()
	err = snapshot.Load(context.Background(), operation.DB(), extracted, validated)
	result.measurement.LoadNS = time.Since(phaseStart).Nanoseconds()
	monitor.mark()
	if err != nil {
		return importedRun{}, err
	}
	if err := os.RemoveAll(extracted.Directory); err != nil {
		return importedRun{}, err
	}

	phaseStart = time.Now()
	abort = false
	err = operation.Complete(context.Background())
	result.measurement.CompleteNS = time.Since(phaseStart).Nanoseconds()
	result.measurement.TotalNS = time.Since(totalStart).Nanoseconds()
	monitor.mark()
	if err != nil && !errors.Is(err, store.ErrRetiredCleanup) {
		return importedRun{}, err
	}
	peaks := monitor.stop()
	result.measurement.Run = runNumber
	result.measurement.BaselineHeapBytes = peaks.BaselineHeap
	result.measurement.PeakHeapBytes = peaks.PeakHeap
	result.measurement.PeakHeapDeltaBytes = nonNegativeDelta(peaks.PeakHeap, peaks.BaselineHeap)
	result.measurement.BaselineRSSBytes = peaks.BaselineRSS
	result.measurement.PeakRSSBytes = peaks.PeakRSS
	result.measurement.PeakRSSDeltaBytes = nonNegativeDelta(peaks.PeakRSS, peaks.BaselineRSS)
	result.measurement.PeakTemporaryDiskBytes = peaks.PeakDisk
	info, err := os.Stat(filepath.Join(dataDirectory, "current.db"))
	if err != nil {
		return importedRun{}, err
	}
	result.measurement.SQLiteBytes = info.Size()
	result.active = activeDataset{storage: storage, workspace: workspace}
	keep = true
	return result, nil
}

func measureSearches(storage *store.Store, targetIDs []string, runs int) ([]searchReport, error) {
	db, release, err := storage.Acquire()
	if err != nil {
		return nil, err
	}
	defer release()
	result := make([]searchReport, 0, len(targetIDs)*2)
	for _, targetID := range targetIDs {
		cold := make([]searchRun, 0, runs)
		warm := make([]searchRun, 0, runs)
		for runNumber := 1; runNumber <= runs; runNumber++ {
			if _, err := db.Exec("PRAGMA shrink_memory"); err != nil {
				return nil, fmt.Errorf("reset SQLite page cache: %w", err)
			}
			runtime.GC()
			coldRun, err := runSearch(context.Background(), db, targetID, runNumber)
			if err != nil {
				return nil, err
			}
			cold = append(cold, coldRun)
			runtime.GC()
			warmRun, err := runSearch(context.Background(), db, targetID, runNumber)
			if err != nil {
				return nil, err
			}
			warm = append(warm, warmRun)
		}
		coldReport, err := newSearchReport(targetID, "sqlite_page_cache_reset", cold)
		if err != nil {
			return nil, err
		}
		warmReport, err := newSearchReport(targetID, "reused_sqlite_page_cache", warm)
		if err != nil {
			return nil, err
		}
		if coldReport.DeterministicDigest != warmReport.DeterministicDigest {
			return nil, fmt.Errorf("target %q changed digest between cold and warm runs", targetID)
		}
		result = append(result, coldReport, warmReport)
	}
	return result, nil
}

func runSearch(ctx context.Context, db *sql.DB, targetID string, runNumber int) (searchRun, error) {
	monitor := startPeakMonitor("", false)
	defer monitor.stop()
	totalStart := time.Now()
	phaseStart := time.Now()
	reached, err := traversal.Traverse(ctx, db, targetID)
	traverseDuration := time.Since(phaseStart)
	monitor.mark()
	if err != nil {
		return searchRun{}, fmt.Errorf("Traverse(%q): %w", targetID, err)
	}
	phaseStart = time.Now()
	limits, err := limitscan.Scan(ctx, db, reached)
	scanDuration := time.Since(phaseStart)
	monitor.mark()
	if err != nil {
		return searchRun{}, fmt.Errorf("Scan(%q): %w", targetID, err)
	}
	phaseStart = time.Now()
	tree, err := pathtree.Build(ctx, reached, limits)
	buildDuration := time.Since(phaseStart)
	totalDuration := time.Since(totalStart)
	monitor.mark()
	if err != nil {
		return searchRun{}, fmt.Errorf("Build(%q): %w", targetID, err)
	}
	measuredPeaks := monitor.stop()
	result := searchRun{
		Run: runNumber, TraverseNS: traverseDuration.Nanoseconds(), ScanNS: scanDuration.Nanoseconds(),
		BuildNS: buildDuration.Nanoseconds(), TotalNS: totalDuration.Nanoseconds(),
		BaselineHeapBytes: measuredPeaks.BaselineHeap, PeakHeapBytes: measuredPeaks.PeakHeap,
		PeakHeapDeltaBytes: nonNegativeDelta(measuredPeaks.PeakHeap, measuredPeaks.BaselineHeap),
		BaselineRSSBytes:   measuredPeaks.BaselineRSS, PeakRSSBytes: measuredPeaks.PeakRSS,
		PeakRSSDeltaBytes: nonNegativeDelta(measuredPeaks.PeakRSS, measuredPeaks.BaselineRSS),
		TraversalStats: traversalStats{
			ExpandedNodes: reached.Stats.ExpandedNodes, RelationQueries: reached.Stats.RelationQueries,
			RelationRows: reached.Stats.RelationRows, ScopeQueries: reached.Stats.ScopeQueries,
			ScopeRows: reached.Stats.ScopeRows,
		},
		ReachedNodes:        len(reached.Nodes),
		RelationConnections: len(reached.Connections), RelationRows: reached.Stats.RelationRows,
		ScopeEdges: len(reached.ScopeEdges), SCCCount: len(reached.Cycles), SCCMaxSize: maximumSCCSize(reached.Cycles),
		Limits: countLimits(limits), TreeNodes: countTreeNodes(&tree.Tree),
		HiddenConnections: countHiddenConnections(&tree.Tree), UncoveredRoutes: len(tree.UncoveredRoutes),
	}
	digestStart := time.Now()
	digest, err := pipelineDigest(reached, limits, tree)
	if err != nil {
		return searchRun{}, err
	}
	result.SerializeDigestNS = time.Since(digestStart).Nanoseconds()
	result.Digest = digest
	return result, nil
}

func pipelineDigest(reached traversal.Result, limits limitscan.Result, tree pathtree.Result) (string, error) {
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	for _, value := range []any{reached, limits, tree} {
		if err := encoder.Encode(value); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func measureConcurrency(storage *store.Store, targetID string, concurrencies []int, rounds int) ([]concurrencyReport, error) {
	db, release, err := storage.Acquire()
	if err != nil {
		return nil, err
	}
	defer release()
	result := make([]concurrencyReport, 0, len(concurrencies))
	for _, concurrency := range concurrencies {
		roundResults := make([]concurrencyRound, 0, rounds)
		allLatencies := make([]int64, 0, rounds*concurrency)
		throughputs := make([]float64, 0, rounds)
		waitCounts := make([]int64, 0, rounds)
		waitDurations := make([]int64, 0, rounds)
		heapDeltas := make([]int64, 0, rounds)
		rssDeltas := make([]int64, 0, rounds)
		for round := 1; round <= rounds; round++ {
			if _, err := db.Exec("PRAGMA shrink_memory"); err != nil {
				return nil, fmt.Errorf("reset SQLite page cache: %w", err)
			}
			runtime.GC()
			measured, err := runConcurrentRound(db, targetID, concurrency, round)
			if err != nil {
				return nil, err
			}
			roundResults = append(roundResults, measured)
			allLatencies = append(allLatencies, measured.LatenciesNS...)
			throughputs = append(throughputs, measured.ThroughputPerSecond)
			waitCounts = append(waitCounts, measured.WaitCountDelta)
			waitDurations = append(waitDurations, measured.WaitDurationDeltaNS)
			heapDeltas = append(heapDeltas, measured.PeakHeapDeltaBytes)
			rssDeltas = append(rssDeltas, measured.PeakRSSDeltaBytes)
		}
		result = append(result, concurrencyReport{
			TargetID: targetID, Concurrency: concurrency, CacheState: "sqlite_page_cache_reset_before_round",
			Rounds: roundResults, LatencyNS: summarize(allLatencies), ThroughputPerSec: summarizeFloat(throughputs),
			WaitCount: summarize(waitCounts), WaitDurationNS: summarize(waitDurations),
			PeakHeapDelta: summarize(heapDeltas), PeakRSSDelta: summarize(rssDeltas),
		})
	}
	return result, nil
}

func runConcurrentRound(db *sql.DB, targetID string, concurrency, round int) (concurrencyRound, error) {
	start := make(chan struct{})
	ready := make(chan struct{}, concurrency)
	type outcome struct {
		latency int64
		err     error
	}
	outcomes := make(chan outcome, concurrency)
	var workers sync.WaitGroup
	for worker := 0; worker < concurrency; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			ready <- struct{}{}
			<-start
			latency, err := runConcurrentSearch(context.Background(), db, targetID)
			outcomes <- outcome{latency: latency, err: err}
		}()
	}
	for range concurrency {
		<-ready
	}
	// 全workerが開始線へ到達してから同時に解放し、goroutine作成時間を検索待ち時間へ混ぜない。
	before := convertDBStats(db.Stats())
	monitor := startPeakMonitor("", false)
	wallStart := time.Now()
	close(start)
	workers.Wait()
	wallDuration := time.Since(wallStart)
	peaks := monitor.stop()
	close(outcomes)
	after := convertDBStats(db.Stats())
	latencies := make([]int64, 0, concurrency)
	for measured := range outcomes {
		if measured.err != nil {
			return concurrencyRound{}, measured.err
		}
		latencies = append(latencies, measured.latency)
	}
	slices.Sort(latencies)
	return concurrencyRound{
		Round: round, WallNS: wallDuration.Nanoseconds(),
		ThroughputPerSecond: float64(concurrency) / wallDuration.Seconds(),
		LatenciesNS:         latencies, Latency: summarize(latencies), DBBefore: before, DBAfter: after,
		WaitCountDelta:      after.WaitCount - before.WaitCount,
		WaitDurationDeltaNS: after.WaitDurationNS - before.WaitDurationNS,
		BaselineHeapBytes:   peaks.BaselineHeap, PeakHeapBytes: peaks.PeakHeap,
		PeakHeapDeltaBytes: nonNegativeDelta(peaks.PeakHeap, peaks.BaselineHeap),
		BaselineRSSBytes:   peaks.BaselineRSS, PeakRSSBytes: peaks.PeakRSS,
		PeakRSSDeltaBytes: nonNegativeDelta(peaks.PeakRSS, peaks.BaselineRSS),
	}, nil
}

func runConcurrentSearch(ctx context.Context, db *sql.DB, targetID string) (int64, error) {
	started := time.Now()
	reached, err := traversal.Traverse(ctx, db, targetID)
	if err != nil {
		return 0, fmt.Errorf("Traverse(%q): %w", targetID, err)
	}
	limits, err := limitscan.Scan(ctx, db, reached)
	if err != nil {
		return 0, fmt.Errorf("Scan(%q): %w", targetID, err)
	}
	if _, err := pathtree.Build(ctx, reached, limits); err != nil {
		return 0, fmt.Errorf("Build(%q): %w", targetID, err)
	}
	return time.Since(started).Nanoseconds(), nil
}

func convertDBStats(stats sql.DBStats) dbStats {
	return dbStats{
		MaxOpenConnections: stats.MaxOpenConnections, OpenConnections: stats.OpenConnections,
		InUse: stats.InUse, Idle: stats.Idle, WaitCount: stats.WaitCount,
		WaitDurationNS: stats.WaitDuration.Nanoseconds(),
	}
}

func newSearchReport(targetID, cacheState string, runs []searchRun) (searchReport, error) {
	if len(runs) == 0 {
		return searchReport{}, errors.New("search report has no runs")
	}
	digest := runs[0].Digest
	for _, run := range runs[1:] {
		if run.Digest != digest {
			return searchReport{}, fmt.Errorf("target %q produced non-deterministic digests", targetID)
		}
	}
	return searchReport{
		TargetID: targetID, CacheState: cacheState, Runs: runs,
		Summary: summarizeSearches(runs), Deterministic: true, DeterministicDigest: digest,
	}, nil
}

func countLimits(result limitscan.Result) limitCounts {
	counts := limitCounts{}
	count := func(limits limitscan.Limits) int {
		total := limits.MaxElapsed.Total + limits.Raw.Total
		counts.MaxElapsed += limits.MaxElapsed.Total
		counts.Raw += limits.Raw.Total
		for _, group := range limits.FinishByGroups {
			total += group.Total
			counts.FinishBy += group.Total
		}
		return total
	}
	counts.Target = count(result.Target)
	counts.Contained = count(result.Contained)
	counts.Downstream = count(result.Downstream)
	counts.Total = counts.Target + counts.Contained + counts.Downstream
	return counts
}

func maximumSCCSize(cycles []traversal.Cycle) int {
	maximum := 0
	for _, cycle := range cycles {
		if len(cycle.NodeIDs) > maximum {
			maximum = len(cycle.NodeIDs)
		}
	}
	return maximum
}

func countTreeNodes(root *pathtree.TreeNode) int {
	count := 0
	stack := []*pathtree.TreeNode{root}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		count++
		stack = append(stack, current.Children...)
	}
	return count
}

func countHiddenConnections(root *pathtree.TreeNode) int {
	count := 0
	stack := []*pathtree.TreeNode{root}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		count += len(current.HiddenConnections)
		stack = append(stack, current.Children...)
	}
	return count
}
