package main

import (
	"encoding/json"
	"testing"

	"batchscope/internal/testsupport/graphgen"
)

func TestMeasureLimitAnalysisCompletesHTTPRunsAndBuildsJSON(t *testing.T) {
	dataset := graphgen.Pathological(graphgen.ParallelRelations)
	fixture, err := prepareFixture(datasetSpec{name: dataset.Name, build: func() graphgen.Dataset { return dataset }})
	if err != nil {
		t.Fatal(err)
	}
	defer fixture.cleanup()
	reported, err := measureLimitAnalysisFixture(config{
		Mode: "limit-analysis", Profile: "pathological", Runs: 2, Concurrencies: []int{1},
	}, fixture)
	if err != nil {
		t.Fatal(err)
	}
	if reported.Target.TargetID == "" || !reported.Target.Deterministic || reported.Target.DeterministicDigest == "" {
		t.Fatalf("target report = %#v", reported.Target)
	}
	if len(reported.Target.Measurements) != 2 {
		t.Fatalf("measurements = %d, want cold and warm", len(reported.Target.Measurements))
	}
	for _, measurement := range reported.Target.Measurements {
		if measurement.Runs != 2 || measurement.Requests != 2 || measurement.LatencyNS.Min <= 0 {
			t.Errorf("measurement = %#v", measurement)
		}
	}
	encoded, err := json.Marshal(reported)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["name"] != dataset.Name {
		t.Fatalf("JSON dataset = %v", decoded)
	}
}
