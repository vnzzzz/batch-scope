package main

import (
	"encoding/json"
	"testing"

	"batchscope/internal/testsupport/graphgen"
)

func TestMeasureTargetSearchCompletesHTTPRunsAndBuildsJSON(t *testing.T) {
	unitID := "UNIT"
	networkID := "NET"
	uniquePath := "/TEST/UNIQUE"
	dataset := graphgen.Dataset{
		Name: "target-search-test",
		Nodes: []graphgen.Node{
			{Type: "management_unit", ID: unitID, Name: "Unit", LimitFacts: []graphgen.Limit{}},
			{Type: "job_network", ID: networkID, Name: "Network", ParentID: &unitID, LimitFacts: []graphgen.Limit{}},
			{Type: "job", ID: "JOB-ID", Name: "ID only", ParentID: &networkID, LimitFacts: []graphgen.Limit{}},
			{Type: "job", ID: "JOB-NAME", Name: "Unique Name", ParentID: &networkID, LimitFacts: []graphgen.Limit{}},
			{Type: "job", ID: "JOB-PATH", Name: "Path only", Path: &uniquePath, ParentID: &networkID, LimitFacts: []graphgen.Limit{}},
		},
		Relations: []graphgen.Relation{
			{FromID: "JOB-ID", ToID: "JOB-NAME", Kind: "precedes", Origin: "scheduler", Certainty: "declared"},
			{FromID: "JOB-NAME", ToID: "JOB-PATH", Kind: "precedes", Origin: "scheduler", Certainty: "declared"},
		},
	}
	cases := []targetSearchCase{
		{Name: "id-single", MatchKind: "id", Query: "JOB-ID", ReturnedTargets: 1},
		{Name: "name-single", MatchKind: "name", Query: "unique name", ReturnedTargets: 1},
		{Name: "path-single", MatchKind: "path", Query: uniquePath, ReturnedTargets: 1},
	}
	configured := config{Mode: "target-search", Profile: "test", Runs: 2, Concurrencies: []int{1}}

	reported, err := measureTargetSearch(configured, datasetSpec{
		name: dataset.Name,
		build: func() graphgen.Dataset {
			return dataset
		},
	}, cases)
	if err != nil {
		t.Fatal(err)
	}
	if len(reported.Dataset.Cases) != len(cases) {
		t.Fatalf("cases = %d, want %d", len(reported.Dataset.Cases), len(cases))
	}
	for _, searchCase := range reported.Dataset.Cases {
		if !searchCase.Deterministic || searchCase.DeterministicDigest == "" {
			t.Errorf("case %s deterministic = %v, digest = %q", searchCase.Name, searchCase.Deterministic, searchCase.DeterministicDigest)
		}
		if len(searchCase.Measurements) != 2 {
			t.Fatalf("case %s measurements = %d, want cold and warm", searchCase.Name, len(searchCase.Measurements))
		}
		for _, measurement := range searchCase.Measurements {
			if measurement.Runs != 2 || measurement.Requests != 2 || measurement.LatencyNS.Min <= 0 {
				t.Errorf("case %s measurement = %#v", searchCase.Name, measurement)
			}
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
	if decoded["configuration"].(map[string]any)["mode"] != "target-search" {
		t.Fatalf("JSON configuration = %v", decoded["configuration"])
	}
}

func TestTargetSearchDatasetKeepsDefaultSmallUnchanged(t *testing.T) {
	baseline := graphgen.Small()
	measured := targetSearchDataset()
	if len(baseline.Nodes) != graphgen.SmallNodeCount || len(measured.Nodes) != graphgen.SmallNodeCount {
		t.Fatalf("node counts = %d and %d", len(baseline.Nodes), len(measured.Nodes))
	}
	for index := range baseline.Nodes {
		if baseline.Nodes[index].Path != nil {
			t.Fatalf("baseline node %s unexpectedly has a path", baseline.Nodes[index].ID)
		}
		if baseline.Nodes[index].ID == "JOB-TARGET" {
			if baseline.Nodes[index].Name != "JOB-TARGET" {
				t.Fatalf("baseline JOB-TARGET name = %q", baseline.Nodes[index].Name)
			}
			if measured.Nodes[index].Name != targetSearchSingleName || measured.Nodes[index].Path == nil {
				t.Fatalf("measured JOB-TARGET = %#v", measured.Nodes[index])
			}
		}
	}
}
