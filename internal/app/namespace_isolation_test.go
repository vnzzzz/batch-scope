package app

import (
	"net/http"
	"testing"

	"batchscope/internal/identity"
)

func TestSameLocalIDAcrossNamespacesKeepsHierarchyRelationsAndAnalysisSeparate(t *testing.T) {
	a := newTestApp(t)
	mainNet := identity.Canonical("main", "NET-A")
	drNet := identity.Canonical("dr", "NET-A")
	mainJob := identity.Canonical("main", "JOB-A")
	drJob := identity.Canonical("dr", "JOB-A")
	mainEnd := identity.Canonical("main", "MAIN-END")
	drEnd := identity.Canonical("dr", "DR-END")

	completeNamespacedAnalysisGeneration(t, a, "namespace-isolation", analysisTestData{
		nodes: []appTestNode{
			{id: mainNet, typeName: "job_network", name: "Main network"},
			{id: drNet, typeName: "job_network", name: "DR network"},
			{id: mainJob, typeName: "job", name: "Main JOB-A", parentID: appString(mainNet)},
			{id: drJob, typeName: "job", name: "DR JOB-A", parentID: appString(drNet)},
			{id: mainEnd, typeName: "job", name: "Main downstream"},
			{id: drEnd, typeName: "job", name: "DR downstream"},
		},
		relations: []analysisTestRelation{
			{id: "R-MAIN", fromID: mainJob, toID: mainEnd, kind: "precedes", origin: "scheduler", certainty: "declared"},
			{id: "R-DR", fromID: drJob, toID: drEnd, kind: "precedes", origin: "scheduler", certainty: "declared"},
		},
		facts: []analysisTestFact{
			{id: "LIMIT-MAIN", ownerID: mainEnd, kind: "raw", sourceText: appString("main only"), origin: "manual", certainty: "confirmed"},
			{id: "LIMIT-DR", ownerID: drEnd, kind: "raw", sourceText: appString("dr only"), origin: "manual", certainty: "confirmed"},
		},
	})

	search := serveRequest(a, "/v1/targets?jobId=JOB-A&type=job")
	assertStatus(t, search, http.StatusOK)
	items := decodeObject(t, search)["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("search items = %v, want two namespace candidates", items)
	}
	seen := map[string]map[string]any{}
	for _, raw := range items {
		item := raw.(map[string]any)
		seen[item["namespace"].(string)] = item
	}
	for namespace, wantParent := range map[string]string{"main": mainNet, "dr": drNet} {
		item := seen[namespace]
		if item == nil || item["localId"] != "JOB-A" {
			t.Fatalf("%s search item = %v", namespace, item)
		}
		ancestors := item["ancestorPath"].([]any)
		if len(ancestors) != 1 || ancestors[0].(map[string]any)["id"] != wantParent || ancestors[0].(map[string]any)["namespace"] != namespace {
			t.Fatalf("%s ancestors = %v", namespace, ancestors)
		}
	}

	assertNamespaceAnalysis := func(targetID, namespace, wantLimit, unwantedLimit string) {
		t.Helper()
		recorder := serveRequest(a, "/v1/downstream-limit-analysis?targetId="+targetID)
		assertStatus(t, recorder, http.StatusOK)
		body := decodeObject(t, recorder)
		target := body["target"].(map[string]any)
		if target["namespace"] != namespace || target["localId"] != "JOB-A" {
			t.Fatalf("%s target = %v", namespace, target)
		}
		downstream := body["limits"].(map[string]any)["downstream"].(map[string]any)
		items := responseLimitItems(downstream)
		if len(items) != 1 || items[0]["fact"].(map[string]any)["id"] != wantLimit {
			t.Fatalf("%s downstream limits = %v, want only %s", namespace, items, wantLimit)
		}
		for _, item := range items {
			if item["fact"].(map[string]any)["id"] == unwantedLimit {
				t.Fatalf("%s analysis leaked %s: %v", namespace, unwantedLimit, item)
			}
			owner := item["limitOwner"].(map[string]any)
			if owner["namespace"] != namespace {
				t.Fatalf("%s limit owner crossed namespace: %v", namespace, owner)
			}
		}
	}

	assertNamespaceAnalysis(mainJob, "main", "LIMIT-MAIN", "LIMIT-DR")
	assertNamespaceAnalysis(drJob, "dr", "LIMIT-DR", "LIMIT-MAIN")
}
