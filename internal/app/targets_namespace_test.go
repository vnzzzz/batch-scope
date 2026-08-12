package app

import (
	"net/http"
	"strings"
	"testing"

	"batchscope/internal/identity"
)

func TestTargetsJobIDReturnsAllNamespaces(t *testing.T) {
	a := newTestApp(t)
	mainID := identity.Canonical("main", "JOB-A")
	drID := identity.Canonical("dr", "JOB-A")
	completeAppGeneration(t, a, "snapshot-namespaces", []appTestNode{
		{id: mainID, typeName: "job", name: "Main A"},
		{id: drID, typeName: "job", name: "DR A"},
	})

	recorder := serveRequest(a, "/v1/targets?jobId=JOB-A")
	assertStatus(t, recorder, http.StatusOK)
	body := decodeObject(t, recorder)
	items := body["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %v", items)
	}
	first := items[0].(map[string]any)
	second := items[1].(map[string]any)
	if first["namespace"] != "dr" || second["namespace"] != "main" {
		t.Fatalf("items = %v", items)
	}
	for _, raw := range items {
		item := raw.(map[string]any)
		if item["localId"] != "JOB-A" || strings.Join(anyStrings(item["matchedBy"]), ",") != "localId" {
			t.Fatalf("item = %v", item)
		}
	}
}

func TestTargetsJobIDCanSelectNamespace(t *testing.T) {
	a := newTestApp(t)
	mainID := identity.Canonical("main", "JOB-A")
	completeAppGeneration(t, a, "snapshot-namespace-filter", []appTestNode{
		{id: mainID, typeName: "job", name: "Main A"},
		{id: identity.Canonical("dr", "JOB-A"), typeName: "job", name: "DR A"},
	})

	recorder := serveRequest(a, "/v1/targets?jobId=JOB-A&namespace=main")
	assertStatus(t, recorder, http.StatusOK)
	items := decodeObject(t, recorder)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != mainID {
		t.Fatalf("items = %v", items)
	}
}

func TestTargetsRejectsAmbiguousSearchParameters(t *testing.T) {
	a := newTestApp(t)
	for _, path := range []string{
		"/v1/targets?jobId=JOB-A&query=JOB-A",
		"/v1/targets?namespace=main&query=JOB-A",
		"/v1/targets?jobId=&namespace=main",
		"/v1/targets?jobId=JOB-A&namespace=",
	} {
		recorder := serveRequest(a, path)
		assertStatus(t, recorder, http.StatusBadRequest)
		if body := decodeObject(t, recorder); body["type"] != "/problems/invalid-request" {
			t.Fatalf("%s problem = %v", path, body)
		}
	}
}

func anyStrings(value any) []string {
	items := value.([]any)
	result := make([]string, len(items))
	for index, item := range items {
		result[index] = item.(string)
	}
	return result
}
