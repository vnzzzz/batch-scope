// Command perf-measure measures snapshot import and downstream analysis without
// making the long-running datasets part of the ordinary verification target.
// Standard Make targets use datasets accepted by the current product limits;
// oversized generators remain available only to preserve historical measurement inputs.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"batchscope/internal/testsupport/graphgen"
)

const reportSchemaVersion = "1"

type config struct {
	Mode              string
	Profile           string
	PathologicalCases string
	Nodes             int
	Relations         int
	Runs              int
	Concurrencies     []int
}

type report struct {
	SchemaVersion string          `json:"schema_version"`
	GeneratedAt   time.Time       `json:"generated_at"`
	Configuration reportConfig    `json:"configuration"`
	Environment   reportEnv       `json:"environment"`
	Methodology   methodology     `json:"methodology"`
	Datasets      []datasetReport `json:"datasets"`
}

type reportConfig struct {
	Mode          string `json:"mode"`
	Profile       string `json:"profile"`
	Nodes         int    `json:"nodes,omitempty"`
	Relations     int    `json:"relations,omitempty"`
	Runs          int    `json:"runs"`
	Concurrencies []int  `json:"concurrencies,omitempty"`
}

type reportEnv struct {
	GOOS       string `json:"goos"`
	GOARCH     string `json:"goarch"`
	GoVersion  string `json:"go_version"`
	CPUs       int    `json:"cpus"`
	GOMAXPROCS int    `json:"gomaxprocs"`
}

type methodology struct {
	TemporaryRoot           string `json:"temporary_root"`
	FixtureDiskAccounting   string `json:"fixture_disk_accounting"`
	ColdCacheDefinition     string `json:"cold_cache_definition"`
	WarmCacheDefinition     string `json:"warm_cache_definition"`
	MemoryPeakDefinition    string `json:"memory_peak_definition"`
	TemporaryPeakDefinition string `json:"temporary_peak_definition"`
	SearchTotalDefinition   string `json:"search_total_definition"`
	DigestDefinition        string `json:"digest_definition"`
}

type datasetSpec struct {
	name  string
	build func() graphgen.Dataset
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "perf-measure:", err)
		os.Exit(1)
	}
}

func run() error {
	configured, err := parseConfig(os.Args[1:])
	if err != nil {
		return err
	}
	if configured.Mode == "target-search" {
		return runTargetSearch(configured, os.Stdout)
	}
	if configured.Mode == "limit-analysis" {
		return runLimitAnalysis(configured, os.Stdout)
	}
	specs, err := selectDatasets(configured)
	if err != nil {
		return err
	}

	var reportedConcurrencies []int
	if configured.Mode == "concurrent" || configured.Mode == "connection-comparison" {
		reportedConcurrencies = configured.Concurrencies
	}
	result := report{
		SchemaVersion: reportSchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		Configuration: reportConfig{
			Mode: configured.Mode, Profile: configured.Profile, Nodes: configured.Nodes,
			Relations: configured.Relations, Runs: configured.Runs,
			Concurrencies: reportedConcurrencies,
		},
		Environment: reportEnv{
			GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, GoVersion: runtime.Version(),
			CPUs: runtime.NumCPU(), GOMAXPROCS: runtime.GOMAXPROCS(0),
		},
		Methodology: methodology{
			TemporaryRoot:           "/tmp",
			FixtureDiskAccounting:   "peak temporary disk covers the per-run working directory; the immutable source archive is reported separately",
			ColdCacheDefinition:     "PRAGMA shrink_memory resets the SQLite connection page cache before each cold run; the operating-system page cache is retained",
			WarmCacheDefinition:     "the warm run immediately follows the cold run on the same SQLite connection without resetting its page cache",
			MemoryPeakDefinition:    "heap allocation and Linux resident memory are sampled every 5 ms and at phase boundaries; deltas use the sample immediately before the operation",
			TemporaryPeakDefinition: "regular-file sizes under the per-run /tmp working directory are sampled every 5 ms and at import phase boundaries",
			SearchTotalDefinition:   "search total starts immediately before Traverse and ends immediately after Build; deterministic serialization is timed separately",
			DigestDefinition:        "SHA-256 of deterministic JSON encodings of Traverse, Scan, and Build results; all repeated digests for a target must match",
		},
		Datasets: make([]datasetReport, 0, len(specs)),
	}

	for _, spec := range specs {
		fixture, err := prepareFixture(spec)
		if err != nil {
			return fmt.Errorf("prepare %s: %w", spec.name, err)
		}
		datasetResult, measureErr := measureDataset(configured, fixture)
		cleanupErr := fixture.cleanup()
		if measureErr != nil {
			return fmt.Errorf("measure %s: %w", spec.name, errors.Join(measureErr, cleanupErr))
		}
		if cleanupErr != nil {
			return fmt.Errorf("clean up %s fixture: %w", spec.name, cleanupErr)
		}
		result.Datasets = append(result.Datasets, datasetResult)
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func parseConfig(arguments []string) (config, error) {
	flags := flag.NewFlagSet("perf-measure", flag.ContinueOnError)
	mode := flags.String("mode", "pipeline", "measurement mode: pipeline, import, concurrent, connection-comparison, target-search, or limit-analysis")
	profile := flags.String("profile", "small", "dataset profile: small, operational, pathological, or a historical oversized generator (medium, scale, custom)")
	pathological := flags.String("pathological-cases", "all", "comma-separated pathological cases, or all")
	nodes := flags.Int("nodes", 0, "custom profile node count")
	relations := flags.Int("relations", 0, "custom profile relation count")
	runs := flags.Int("runs", 5, "number of repeated runs or concurrent rounds")
	concurrencyText := flags.String("concurrencies", "1,2,4,8", "comma-separated simultaneous search counts")
	if err := flags.Parse(arguments); err != nil {
		return config{}, err
	}
	if flags.NArg() != 0 {
		return config{}, fmt.Errorf("unexpected positional arguments: %s", strings.Join(flags.Args(), " "))
	}
	if *mode != "pipeline" && *mode != "import" && *mode != "concurrent" && *mode != "connection-comparison" && *mode != "target-search" && *mode != "limit-analysis" {
		return config{}, fmt.Errorf("unsupported mode %q", *mode)
	}
	if *runs < 2 {
		return config{}, errors.New("runs must be at least 2 to report a distribution")
	}
	if err := validateCustomSize(*profile, *nodes, *relations); err != nil {
		return config{}, err
	}
	concurrencyValue := *concurrencyText
	concurrencyWasSet := false
	flags.Visit(func(current *flag.Flag) {
		if current.Name == "concurrencies" {
			concurrencyWasSet = true
		}
	})
	if (*mode == "target-search" || *mode == "limit-analysis") && !concurrencyWasSet {
		concurrencyValue = "1,4"
	}
	concurrencies, err := parsePositiveInts(concurrencyValue)
	if err != nil {
		return config{}, fmt.Errorf("parse concurrencies: %w", err)
	}
	return config{
		Mode: *mode, Profile: *profile, PathologicalCases: *pathological,
		Nodes: *nodes, Relations: *relations,
		Runs: *runs, Concurrencies: concurrencies,
	}, nil
}

func validateCustomSize(profile string, nodes, relations int) error {
	if profile != "custom" {
		if nodes != 0 || relations != 0 {
			return errors.New("nodes and relations require profile custom")
		}
		return nil
	}
	if nodes < graphgen.SmallNodeCount {
		return fmt.Errorf("custom nodes must be at least %d", graphgen.SmallNodeCount)
	}
	if nodes > 10_000_000 {
		return errors.New("custom nodes must not exceed 10000000")
	}
	minimumRelations := (nodes*5 + 1) / 2
	maximumRelations := nodes * 3
	if relations < minimumRelations || relations > maximumRelations {
		return fmt.Errorf("custom relations must be between %d and %d for %d nodes", minimumRelations, maximumRelations, nodes)
	}
	return nil
}

func parsePositiveInts(value string) ([]int, error) {
	parts := strings.Split(value, ",")
	result := make([]int, 0, len(parts))
	seen := make(map[int]struct{}, len(parts))
	for _, part := range parts {
		parsed, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || parsed < 1 {
			return nil, fmt.Errorf("%q is not a positive integer", part)
		}
		if _, duplicate := seen[parsed]; duplicate {
			return nil, fmt.Errorf("concurrency %d is repeated", parsed)
		}
		seen[parsed] = struct{}{}
		result = append(result, parsed)
	}
	return result, nil
}

func selectDatasets(configured config) ([]datasetSpec, error) {
	switch configured.Profile {
	case "small":
		return []datasetSpec{{name: "small", build: graphgen.Small}}, nil
	case "operational":
		return []datasetSpec{{name: "operational-400k", build: graphgen.Operational400K}}, nil
	case "medium":
		// Medium and Scale are kept so historical measurement inputs remain identifiable.
		// Current product validation still applies, so standard Make targets do not expose them.
		return []datasetSpec{{name: "medium", build: graphgen.Medium}}, nil
	case "scale":
		return []datasetSpec{{name: "scale", build: graphgen.Scale}}, nil
	case "pathological":
		return selectPathological(configured.PathologicalCases)
	case "custom":
		// Custom preserves the old growth-profile generator; it does not bypass current capacity checks.
		name := fmt.Sprintf("custom-%d-%d", configured.Nodes, configured.Relations)
		return []datasetSpec{{name: name, build: func() graphgen.Dataset {
			return graphgen.Custom(configured.Nodes, configured.Relations)
		}}}, nil
	default:
		return nil, fmt.Errorf("unsupported profile %q", configured.Profile)
	}
}

func selectPathological(value string) ([]datasetSpec, error) {
	known := make(map[string]graphgen.PathologicalCase)
	for _, kind := range graphgen.PathologicalCases() {
		known[string(kind)] = kind
	}
	names := strings.Split(value, ",")
	if value == "all" {
		names = make([]string, 0, len(known))
		for _, kind := range graphgen.PathologicalCases() {
			names = append(names, string(kind))
		}
	}
	result := make([]datasetSpec, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, rawName := range names {
		name := strings.TrimSpace(rawName)
		kind, ok := known[name]
		if !ok {
			return nil, fmt.Errorf("unknown pathological case %q", name)
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, fmt.Errorf("pathological case %q is repeated", name)
		}
		seen[name] = struct{}{}
		selected := kind
		result = append(result, datasetSpec{name: name, build: func() graphgen.Dataset {
			return graphgen.Pathological(selected)
		}})
	}
	return result, nil
}
