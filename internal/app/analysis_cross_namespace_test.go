package app

import (
	"net/http"
	"testing"

	"batchscope/internal/identity"
)

func TestDownstreamLimitAnalysisTraversesCrossNamespaceIndirectDependency(t *testing.T) {
	a := newTestApp(t)
	mainJob := identity.Canonical("main", "JOB-A")
	mainStatus := identity.Canonical("main", "JOB-A.done")
	drJob := identity.Canonical("dr", "JOB-B")
	completeNamespacedAnalysisGeneration(t, a, "cross-namespace", analysisTestData{
		nodes: []appTestNode{
			{id: mainJob, typeName: "job", name: "Main job"},
			{id: mainStatus, typeName: "job_status", name: "Main job done"},
			{id: drJob, typeName: "job", name: "DR job"},
		},
		relations: []analysisTestRelation{
			{id: "R-PRODUCES", fromID: mainJob, toID: mainStatus, kind: "produces", origin: "deterministic_analysis", certainty: "confirmed"},
			{id: "R-TRIGGERS", fromID: mainStatus, toID: drJob, kind: "triggers", origin: "deterministic_analysis", certainty: "confirmed"},
		},
		facts: []analysisTestFact{{
			id: "LIMIT-DR", ownerID: drJob, kind: "raw", sourceText: appString("cross namespace finish check"),
			origin: "manual", certainty: "confirmed",
		}},
	})

	recorder := serveRequest(a, "/v1/downstream-limit-analysis?targetId="+mainJob)
	assertStatus(t, recorder, http.StatusOK)
	body := decodeObject(t, recorder)
	target := body["target"].(map[string]any)
	if target["namespace"] != "main" || target["localId"] != "JOB-A" {
		t.Fatalf("target = %v", target)
	}

	limits := body["limits"].(map[string]any)["downstream"].(map[string]any)
	item := findResponseLimit(t, limits, "LIMIT-DR")
	owner := item["limitOwner"].(map[string]any)
	if owner["namespace"] != "dr" || owner["localId"] != "JOB-B" || owner["id"] != drJob {
		t.Fatalf("cross-namespace limit owner = %v", owner)
	}

	foundDR := false
	foundCompressedStatus := false
	for _, treeNode := range flattenResponseTree(body["tree"].(map[string]any)) {
		node := treeNode["node"].(map[string]any)
		if node["id"] == drJob {
			foundDR = node["namespace"] == "dr" && node["localId"] == "JOB-B"
		}
		for _, rawID := range anySlice(treeNode["hiddenNodeIds"]) {
			if rawID == mainStatus {
				foundCompressedStatus = true
			}
		}
		for _, rawConnection := range anySlice(treeNode["hiddenConnections"]) {
			connection := rawConnection.(map[string]any)
			if connection["fromId"] == mainJob && connection["toId"] == mainStatus {
				foundCompressedStatus = true
			}
		}
	}
	if !foundCompressedStatus || !foundDR {
		t.Fatalf("cross-namespace path is incomplete: compressedStatus=%v dr=%v", foundCompressedStatus, foundDR)
	}
}
