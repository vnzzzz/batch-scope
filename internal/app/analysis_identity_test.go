package app

import (
	"encoding/json"
	"testing"

	"batchscope/internal/identity"
)

func TestAnalysisNodeJSONIncludesNamespaceIdentity(t *testing.T) {
	node := analysisNode{ID: identity.Encode("main", "JOB-A"), Type: "job", Name: "Main Job"}
	contents, err := json.Marshal(node)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(contents, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["namespace"] != "main" || decoded["localId"] != "JOB-A" || decoded["id"] != node.ID {
		t.Fatalf("analysis node = %v", decoded)
	}
}

func TestAnalysisNodeJSONKeepsLegacyShape(t *testing.T) {
	node := analysisNode{ID: "LEGACY-JOB", Type: "job", Name: "Legacy Job"}
	contents, err := json.Marshal(node)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(contents, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded["namespace"]; ok {
		t.Fatalf("legacy analysis node unexpectedly has namespace: %v", decoded)
	}
	if _, ok := decoded["localId"]; ok {
		t.Fatalf("legacy analysis node unexpectedly has localId: %v", decoded)
	}
	if decoded["id"] != "LEGACY-JOB" {
		t.Fatalf("legacy analysis node = %v", decoded)
	}
}
