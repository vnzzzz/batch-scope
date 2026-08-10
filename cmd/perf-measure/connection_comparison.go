package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
)

const (
	singleConnectionStrategy = "single_connection"
	multipleReadersStrategy  = "multiple_read_connections"
)

type comparisonStrategy struct {
	name         string
	maxOpenConns int
	database     *sql.DB
	roundResults []concurrencyRound
}

func measureConnectionComparison(active *activeDataset, targetID string, concurrencies []int, rounds int) (*connectionComparisonReport, error) {
	generationPath, err := preserveComparisonGeneration(active)
	if err != nil {
		return nil, err
	}
	result := &connectionComparisonReport{
		TargetID:                     targetID,
		CacheState:                   "all_connection_page_caches_reset_before_round",
		DatabaseGenerationPathPolicy: "one immutable SQLite file path unique to the imported generation and never reused",
		ConnectionInitialization:     "read-only immutable DSN applies foreign_keys=on and query_only=on to every connection",
		ExecutionOrder:               "strategies run consecutively in one process; first strategy alternates by round and concurrency",
	}
	for concurrencyIndex, concurrency := range concurrencies {
		strategies, err := openComparisonStrategies(generationPath, concurrency, rounds)
		if err != nil {
			return nil, err
		}
		measureErr := measurePairedStrategies(strategies, targetID, concurrency, concurrencyIndex, rounds)
		closeErr := closeComparisonStrategies(strategies)
		if measureErr != nil || closeErr != nil {
			return nil, errors.Join(measureErr, closeErr)
		}
		for _, strategy := range strategies {
			result.Results = append(result.Results, summarizeComparisonStrategy(strategy, targetID, concurrency))
		}
	}
	return result, nil
}

func preserveComparisonGeneration(active *activeDataset) (string, error) {
	if active.storage == nil {
		return "", errors.New("comparison source storage is not available")
	}
	directory := filepath.Join(active.workspace, "comparison-generations")
	if err := os.Mkdir(directory, 0o750); err != nil {
		return "", fmt.Errorf("create comparison generation directory: %w", err)
	}
	sourcePath := filepath.Join(active.workspace, "data", "current.db")
	generationPath := filepath.Join(directory, "snapshot-generation-1.db")
	if err := copySQLiteFile(sourcePath, generationPath); err != nil {
		return "", fmt.Errorf("preserve comparison generation: %w", err)
	}
	// 製品storeが再利用するcurrent.dbから切り離した後だけ、比較用ハンドルを開く。
	// 世代固有パスは測定終了まで置換せず、古い接続が別世代へ再接続する余地をなくす。
	closeErr := active.storage.Close()
	if closeErr != nil {
		return "", fmt.Errorf("close imported product store: %w", closeErr)
	}
	return generationPath, nil
}

func copySQLiteFile(sourcePath, destinationPath string) (err error) {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	destination, err := os.OpenFile(destinationPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		closeErr := destination.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(destinationPath)
		}
	}()
	_, err = io.Copy(destination, source)
	return err
}

func openComparisonStrategies(path string, concurrency, rounds int) ([]*comparisonStrategy, error) {
	strategies := []*comparisonStrategy{
		{name: singleConnectionStrategy, maxOpenConns: 1, roundResults: make([]concurrencyRound, 0, rounds)},
		{name: multipleReadersStrategy, maxOpenConns: concurrency, roundResults: make([]concurrencyRound, 0, rounds)},
	}
	for _, strategy := range strategies {
		database, err := openComparisonDatabase(context.Background(), path, strategy.maxOpenConns)
		if err != nil {
			_ = closeComparisonStrategies(strategies)
			return nil, fmt.Errorf("open %s: %w", strategy.name, err)
		}
		strategy.database = database
	}
	return strategies, nil
}

func openComparisonDatabase(ctx context.Context, path string, maxOpenConnections int) (*sql.DB, error) {
	if maxOpenConnections < 1 {
		return nil, errors.New("max open connections must be positive")
	}
	dsn := url.URL{Scheme: "file", Path: path}
	query := url.Values{}
	query.Set("mode", "ro")
	query.Set("immutable", "1")
	query.Set("_foreign_keys", "on")
	query.Set("_query_only", "1")
	dsn.RawQuery = query.Encode()
	database, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(maxOpenConnections)
	database.SetMaxIdleConns(maxOpenConnections)
	database.SetConnMaxLifetime(0)
	database.SetConnMaxIdleTime(0)
	if err := forEachComparisonConnection(ctx, database, maxOpenConnections, verifyComparisonConnection); err != nil {
		_ = database.Close()
		return nil, err
	}
	return database, nil
}

func verifyComparisonConnection(ctx context.Context, connection *sql.Conn) error {
	for _, pragma := range []string{"foreign_keys", "query_only"} {
		var enabled int
		if err := connection.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&enabled); err != nil {
			return fmt.Errorf("read PRAGMA %s: %w", pragma, err)
		}
		if enabled != 1 {
			return fmt.Errorf("PRAGMA %s = %d, want 1", pragma, enabled)
		}
	}
	return nil
}

func measurePairedStrategies(strategies []*comparisonStrategy, targetID string, concurrency, concurrencyIndex, rounds int) error {
	for round := 1; round <= rounds; round++ {
		first, second := 0, 1
		if (round+concurrencyIndex)%2 == 0 {
			first, second = second, first
		}
		for _, strategyIndex := range []int{first, second} {
			strategy := strategies[strategyIndex]
			if err := resetComparisonPageCaches(context.Background(), strategy.database, strategy.maxOpenConns); err != nil {
				return fmt.Errorf("reset %s page caches: %w", strategy.name, err)
			}
			runtime.GC()
			measured, err := runConcurrentRound(strategy.database, targetID, concurrency, round)
			if err != nil {
				return fmt.Errorf("measure %s: %w", strategy.name, err)
			}
			strategy.roundResults = append(strategy.roundResults, measured)
		}
	}
	return nil
}

func resetComparisonPageCaches(ctx context.Context, database *sql.DB, connections int) error {
	return forEachComparisonConnection(ctx, database, connections, func(ctx context.Context, connection *sql.Conn) error {
		_, err := connection.ExecContext(ctx, "PRAGMA shrink_memory")
		return err
	})
}

func forEachComparisonConnection(ctx context.Context, database *sql.DB, count int, apply func(context.Context, *sql.Conn) error) (err error) {
	connections := make([]*sql.Conn, 0, count)
	defer func() {
		for _, connection := range connections {
			err = errors.Join(err, connection.Close())
		}
	}()
	// 接続を同時に保持し、接続プールの同じ接続を繰り返し取得せず全接続へPRAGMAを適用する。
	for range count {
		connection, acquireErr := database.Conn(ctx)
		if acquireErr != nil {
			return acquireErr
		}
		connections = append(connections, connection)
	}
	for _, connection := range connections {
		if applyErr := apply(ctx, connection); applyErr != nil {
			return applyErr
		}
	}
	return nil
}

func summarizeComparisonStrategy(strategy *comparisonStrategy, targetID string, concurrency int) connectionStrategyReport {
	allLatencies := make([]int64, 0, len(strategy.roundResults)*concurrency)
	throughputs := make([]float64, 0, len(strategy.roundResults))
	waitCounts := make([]int64, 0, len(strategy.roundResults))
	waitDurations := make([]int64, 0, len(strategy.roundResults))
	heapDeltas := make([]int64, 0, len(strategy.roundResults))
	rssDeltas := make([]int64, 0, len(strategy.roundResults))
	for _, measured := range strategy.roundResults {
		allLatencies = append(allLatencies, measured.LatenciesNS...)
		throughputs = append(throughputs, measured.ThroughputPerSecond)
		waitCounts = append(waitCounts, measured.WaitCountDelta)
		waitDurations = append(waitDurations, measured.WaitDurationDeltaNS)
		heapDeltas = append(heapDeltas, measured.PeakHeapDeltaBytes)
		rssDeltas = append(rssDeltas, measured.PeakRSSDeltaBytes)
	}
	return connectionStrategyReport{
		ConnectionStrategy: strategy.name, ConfiguredMaxOpenConnections: strategy.maxOpenConns,
		Measurement: concurrencyReport{
			TargetID: targetID, Concurrency: concurrency, CacheState: "all_connection_page_caches_reset_before_round",
			Rounds: strategy.roundResults, LatencyNS: summarize(allLatencies), ThroughputPerSec: summarizeFloat(throughputs),
			WaitCount: summarize(waitCounts), WaitDurationNS: summarize(waitDurations),
			PeakHeapDelta: summarize(heapDeltas), PeakRSSDelta: summarize(rssDeltas),
		},
	}
}

func closeComparisonStrategies(strategies []*comparisonStrategy) error {
	var result error
	for _, strategy := range strategies {
		if strategy.database != nil {
			result = errors.Join(result, strategy.database.Close())
		}
	}
	return result
}
