package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"
)

const peakSampleInterval = 5 * time.Millisecond

type distribution struct {
	Min    int64 `json:"min"`
	Median int64 `json:"median"`
	P95    int64 `json:"p95"`
	Max    int64 `json:"max"`
}

type floatDistribution struct {
	Min    float64 `json:"min"`
	Median float64 `json:"median"`
	P95    float64 `json:"p95"`
	Max    float64 `json:"max"`
}

type peaks struct {
	BaselineHeap int64
	PeakHeap     int64
	BaselineRSS  int64
	PeakRSS      int64
	PeakDisk     int64
}

type peakMonitor struct {
	root        string
	includeDisk bool
	stopSignal  chan struct{}
	done        chan struct{}
	mu          sync.Mutex
	stopOnce    sync.Once
	result      peaks
}

func startPeakMonitor(root string, includeDisk bool) *peakMonitor {
	monitor := &peakMonitor{
		root: root, includeDisk: includeDisk, stopSignal: make(chan struct{}), done: make(chan struct{}),
	}
	monitor.sample(true)
	go func() {
		defer close(monitor.done)
		ticker := time.NewTicker(peakSampleInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				monitor.sample(false)
			case <-monitor.stopSignal:
				return
			}
		}
	}()
	return monitor
}

func (monitor *peakMonitor) mark() {
	monitor.sample(false)
}

func (monitor *peakMonitor) stop() peaks {
	monitor.stopOnce.Do(func() {
		monitor.sample(false)
		close(monitor.stopSignal)
	})
	<-monitor.done
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	return monitor.result
}

func (monitor *peakMonitor) sample(baseline bool) {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	heap := int64(memory.HeapAlloc)
	rss := residentBytes()
	disk := int64(0)
	if monitor.includeDisk {
		disk = regularFileBytes(monitor.root)
	}
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	if baseline {
		monitor.result.BaselineHeap = heap
		monitor.result.BaselineRSS = rss
	}
	if heap > monitor.result.PeakHeap {
		monitor.result.PeakHeap = heap
	}
	if rss > monitor.result.PeakRSS {
		monitor.result.PeakRSS = rss
	}
	if disk > monitor.result.PeakDisk {
		monitor.result.PeakDisk = disk
	}
}

func residentBytes() int64 {
	file, err := os.Open("/proc/self/statm")
	if err != nil {
		return 0
	}
	defer file.Close()
	var totalPages, residentPages int64
	if _, err := fmt.Fscan(file, &totalPages, &residentPages); err != nil {
		return 0
	}
	return residentPages * int64(os.Getpagesize())
}

func regularFileBytes(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

func nonNegativeDelta(peak, baseline int64) int64 {
	if peak <= baseline {
		return 0
	}
	return peak - baseline
}

func summarize(values []int64) distribution {
	if len(values) == 0 {
		return distribution{}
	}
	ordered := append([]int64(nil), values...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	median := ordered[len(ordered)/2]
	if len(ordered)%2 == 0 {
		median = ordered[len(ordered)/2-1] + (ordered[len(ordered)/2]-ordered[len(ordered)/2-1])/2
	}
	p95Index := (95*len(ordered)+99)/100 - 1
	return distribution{Min: ordered[0], Median: median, P95: ordered[p95Index], Max: ordered[len(ordered)-1]}
}

func summarizeFloat(values []float64) floatDistribution {
	if len(values) == 0 {
		return floatDistribution{}
	}
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	median := ordered[len(ordered)/2]
	if len(ordered)%2 == 0 {
		median = (ordered[len(ordered)/2-1] + ordered[len(ordered)/2]) / 2
	}
	p95Index := (95*len(ordered)+99)/100 - 1
	return floatDistribution{Min: ordered[0], Median: median, P95: ordered[p95Index], Max: ordered[len(ordered)-1]}
}

func summarizeImports(runs []importRun) map[string]distribution {
	return summarizeFields(len(runs), map[string]func(int) int64{
		"total_ns":                  func(index int) int64 { return runs[index].TotalNS },
		"receive_ns":                func(index int) int64 { return runs[index].ReceiveNS },
		"extract_ns":                func(index int) int64 { return runs[index].ExtractNS },
		"validate_ns":               func(index int) int64 { return runs[index].ValidateNS },
		"load_ns":                   func(index int) int64 { return runs[index].LoadNS },
		"complete_ns":               func(index int) int64 { return runs[index].CompleteNS },
		"peak_heap_delta_bytes":     func(index int) int64 { return runs[index].PeakHeapDeltaBytes },
		"peak_rss_delta_bytes":      func(index int) int64 { return runs[index].PeakRSSDeltaBytes },
		"peak_temporary_disk_bytes": func(index int) int64 { return runs[index].PeakTemporaryDiskBytes },
		"sqlite_bytes":              func(index int) int64 { return runs[index].SQLiteBytes },
	})
}

func summarizeSearches(runs []searchRun) map[string]distribution {
	return summarizeFields(len(runs), map[string]func(int) int64{
		"traverse_ns":           func(index int) int64 { return runs[index].TraverseNS },
		"scan_ns":               func(index int) int64 { return runs[index].ScanNS },
		"build_ns":              func(index int) int64 { return runs[index].BuildNS },
		"total_ns":              func(index int) int64 { return runs[index].TotalNS },
		"serialize_digest_ns":   func(index int) int64 { return runs[index].SerializeDigestNS },
		"peak_heap_delta_bytes": func(index int) int64 { return runs[index].PeakHeapDeltaBytes },
		"peak_rss_delta_bytes":  func(index int) int64 { return runs[index].PeakRSSDeltaBytes },
		"expanded_nodes":        func(index int) int64 { return int64(runs[index].TraversalStats.ExpandedNodes) },
		"relation_queries":      func(index int) int64 { return int64(runs[index].TraversalStats.RelationQueries) },
		"relation_rows":         func(index int) int64 { return int64(runs[index].TraversalStats.RelationRows) },
		"scope_queries":         func(index int) int64 { return int64(runs[index].TraversalStats.ScopeQueries) },
		"scope_rows":            func(index int) int64 { return int64(runs[index].TraversalStats.ScopeRows) },
		"reached_nodes":         func(index int) int64 { return int64(runs[index].ReachedNodes) },
		"relation_connections":  func(index int) int64 { return int64(runs[index].RelationConnections) },
		"scope_edges":           func(index int) int64 { return int64(runs[index].ScopeEdges) },
		"scc_count":             func(index int) int64 { return int64(runs[index].SCCCount) },
		"scc_max_size":          func(index int) int64 { return int64(runs[index].SCCMaxSize) },
		"limits_total":          func(index int) int64 { return int64(runs[index].Limits.Total) },
		"limits_finish_by":      func(index int) int64 { return int64(runs[index].Limits.FinishBy) },
		"limits_max_elapsed":    func(index int) int64 { return int64(runs[index].Limits.MaxElapsed) },
		"limits_raw":            func(index int) int64 { return int64(runs[index].Limits.Raw) },
		"tree_nodes":            func(index int) int64 { return int64(runs[index].TreeNodes) },
		"hidden_connections":    func(index int) int64 { return int64(runs[index].HiddenConnections) },
		"uncovered_routes":      func(index int) int64 { return int64(runs[index].UncoveredRoutes) },
	})
}

func summarizeFields(count int, fields map[string]func(int) int64) map[string]distribution {
	result := make(map[string]distribution, len(fields))
	for name, valueAt := range fields {
		values := make([]int64, count)
		for index := range count {
			values[index] = valueAt(index)
		}
		result[name] = summarize(values)
	}
	return result
}
