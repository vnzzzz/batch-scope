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
	completeAnalysisGeneration(t, a, "cross-namespace", analysisTestData{
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

	foundStatus := false
	foundDR := false
	for _, treeNode := range flattenResponseTree(body["tree"].(map[string]any)) {
		node := treeNode["node"].(map[string]any)
		switch node["id"] {
		case mainStatus:
			foundStatus = node["namespace"] == "main" && node["localId"] == "JOB-A.done"
		case drJob:
			foundDR = node["namespace"] == "dr" && node["localId"] == "JOB-B"
		}
	}
	if !foundStatus || !foundDR {
		t.Fatalf("tree omitted namespaced nodes: status=%v dr=%v", foundStatus, foundDR)
	}
}
