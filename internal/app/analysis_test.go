package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"batchscope/internal/store"
)

type analysisTestRelation struct {
	id, fromID, toID, kind, origin, certainty string
	evidence                                  *string
}

type analysisTestFact struct {
	id, ownerID, kind, timeZone, origin, certainty string
	businessDayOffset, localTimeSeconds            *int64
	durationSeconds                                *int64
	sourceText                                     *string
}

type analysisTestData struct {
	nodes     []appTestNode
	relations []analysisTestRelation
	facts     []analysisTestFact
}

func TestDownstreamLimitAnalysisPublicContractAndResults(t *testing.T) {
	a := newTestApp(t)
	completeAnalysisGeneration(t, a, "analysis-snapshot", analysisFixture("FIX"))

	base := serveRequest(a, "/v1/downstream-limit-analysis?targetId=NET-TARGET")
	assertStatus(t, base, http.StatusOK)
	assertContentType(t, base, "application/json")
	body := decodeObject(t, base)
	assertKeys(t, body, "bootId", "snapshotId", "target", "limits", "tree", "uncoveredRoutes", "cycles")
	for _, forbidden := range []string{"summary", "analysisComplete", "truncated", "frontier", "policyVersion", "rank", "selectionReason", "ranking"} {
		if _, ok := body[forbidden]; ok {
			t.Errorf("top-level field %q is present", forbidden)
		}
	}
	if body["snapshotId"] != "analysis-snapshot" || body["bootId"] != a.bootID {
		t.Fatalf("identity fields = %v", body)
	}

	targetResponse := serveRequest(a, "/v1/targets?query=NET-TARGET&type=job_network")
	assertStatus(t, targetResponse, http.StatusOK)
	targetItem := decodeObject(t, targetResponse)["items"].([]any)[0].(map[string]any)
	delete(targetItem, "matchedBy")
	if !reflect.DeepEqual(body["target"], targetItem) {
		t.Fatalf("analysis target = %v, exact target = %v", body["target"], targetItem)
	}

	limits := body["limits"].(map[string]any)
	assertResponseLimitTotals(t, limits)
	containedIDs := responseLimitIDs(limits["contained"].(map[string]any))
	downstreamIDs := responseLimitIDs(limits["downstream"].(map[string]any))
	if strings.Join(containedIDs, ",") != "LIMIT-CONTAINED" {
		t.Fatalf("contained limit IDs = %v", containedIDs)
	}
	if strings.Join(downstreamIDs, ",") != "LIMIT-SCOPED,LIMIT-SHARED,LIMIT-DIRECT" {
		t.Fatalf("downstream limit IDs = %v", downstreamIDs)
	}

	treeNodes := flattenResponseTree(body["tree"].(map[string]any))
	for _, sectionName := range []string{"target", "contained", "downstream"} {
		for _, item := range responseLimitItems(limits[sectionName].(map[string]any)) {
			treeNodeID := item["treeNodeId"].(string)
			treeNode := treeNodes[treeNodeID]
			if treeNode == nil {
				t.Fatalf("limit %v references missing tree node %q", item, treeNodeID)
			}
			owner := item["limitOwner"].(map[string]any)
			if treeNode["node"].(map[string]any)["id"] != owner["id"] {
				t.Fatalf("limit owner %v resolves to tree node %v", owner, treeNode)
			}
		}
	}

	scoped := findResponseLimit(t, limits["downstream"].(map[string]any), "LIMIT-SCOPED")
	if scoped["limitOwner"].(map[string]any)["id"] != "JOB-SCOPED" || scoped["scopeRoot"].(map[string]any)["id"] != "NET-DOWN" {
		t.Fatalf("scoped downstream limit = %v", scoped)
	}
	direct := findResponseLimit(t, limits["downstream"].(map[string]any), "LIMIT-DIRECT")
	if _, ok := direct["scopeRoot"]; ok {
		t.Fatalf("direct downstream limit has scopeRoot: %v", direct)
	}
	if duration := direct["fact"].(map[string]any)["duration"]; duration != "P1DT2H3M4S" {
		t.Fatalf("duration = %v, want P1DT2H3M4S", duration)
	}
	shared := findResponseLimit(t, limits["downstream"].(map[string]any), "LIMIT-SHARED")
	if shared["alternatePathCount"] != float64(1) {
		t.Fatalf("shared alternatePathCount = %v, want 1", shared["alternatePathCount"])
	}

	assertTreeConnectionRepresentations(t, body["tree"].(map[string]any))
	assertReferenceNodes(t, body["tree"].(map[string]any))
	assertUncoveredRoutes(t, body, treeNodes)
	assertCycles(t, body)

	withUnknown := serveRequest(a, "/v1/downstream-limit-analysis?targetId=NET-TARGET&maxDepth=1&limit=1&treeNodes=1")
	assertStatus(t, withUnknown, http.StatusOK)
	if !bytes.Equal(base.Body.Bytes(), withUnknown.Body.Bytes()) {
		t.Fatal("unknown depth and count parameters changed the response")
	}
}

func TestDownstreamLimitAnalysisReturnsTargetLimits(t *testing.T) {
	a := newTestApp(t)
	data := analysisFixture("TARGET")
	data.nodes = append(data.nodes, appTestNode{id: "JOB-TARGET-LIMIT", typeName: "job", name: "Target limit"})
	data.facts = append(data.facts, analysisTestFact{
		id: "LIMIT-TARGET", ownerID: "JOB-TARGET-LIMIT", kind: "max_elapsed",
		durationSeconds: analysisInt64(1_800), sourceText: appString("sensitive target source"),
		origin: "scheduler", certainty: "declared",
	})
	completeAnalysisGeneration(t, a, "target-limit-snapshot", data)

	recorder := serveRequest(a, "/v1/downstream-limit-analysis?targetId=JOB-TARGET-LIMIT")
	assertStatus(t, recorder, http.StatusOK)
	body := decodeObject(t, recorder)
	limits := body["limits"].(map[string]any)
	targetLimit := findResponseLimit(t, limits["target"].(map[string]any), "LIMIT-TARGET")
	if targetLimit["fact"].(map[string]any)["duration"] != "PT30M" {
		t.Fatalf("target limit = %v", targetLimit)
	}
	if len(body["uncoveredRoutes"].([]any)) != 0 {
		t.Fatalf("target-owned limit left uncovered routes: %v", body["uncoveredRoutes"])
	}
}

func TestDownstreamLimitAnalysisEvidenceToggleIsConsistent(t *testing.T) {
	a := newTestApp(t)
	completeAnalysisGeneration(t, a, "evidence-snapshot", analysisFixture("EVIDENCE"))

	without := decodeObject(t, serveRequest(a, "/v1/downstream-limit-analysis?targetId=NET-TARGET"))
	with := decodeObject(t, serveRequest(a, "/v1/downstream-limit-analysis?targetId=NET-TARGET&includeEvidence=true"))
	assertRelationEvidencePresence(t, without, false)
	assertRelationEvidencePresence(t, with, true)
	for _, body := range []map[string]any{without, with} {
		limits := body["limits"].(map[string]any)
		for _, section := range []string{"target", "contained", "downstream"} {
			for _, item := range responseLimitItems(limits[section].(map[string]any)) {
				if _, ok := item["fact"].(map[string]any)["evidence"]; ok {
					t.Fatalf("limit fact invented evidence: %v", item)
				}
			}
		}
	}
}

func TestDownstreamLimitAnalysisHiddenNodeIDLimitDoesNotTruncateConnections(t *testing.T) {
	a := newTestApp(t)
	const hiddenCount = 1_002
	data := analysisTestData{
		nodes: []appTestNode{{id: "LONG-START", typeName: "job", name: "Start"}},
	}
	previous := "LONG-START"
	for index := range hiddenCount {
		id := fmt.Sprintf("HIDDEN-%04d", index)
		data.nodes = append(data.nodes, appTestNode{id: id, typeName: "job", name: id})
		data.relations = append(data.relations, analysisTestRelation{
			id: fmt.Sprintf("LONG-R-%04d", index), fromID: previous, toID: id,
			kind: "precedes", origin: "scheduler", certainty: "declared",
		})
		previous = id
	}
	data.nodes = append(data.nodes, appTestNode{id: "LONG-LIMIT", typeName: "job", name: "Limit"})
	data.relations = append(data.relations, analysisTestRelation{
		id: "LONG-R-LAST", fromID: previous, toID: "LONG-LIMIT",
		kind: "precedes", origin: "scheduler", certainty: "declared",
	})
	data.facts = append(data.facts, analysisTestFact{
		id: "LONG-FACT", ownerID: "LONG-LIMIT", kind: "raw", sourceText: appString("raw"),
		origin: "scheduler", certainty: "declared",
	})
	completeAnalysisGeneration(t, a, "long-snapshot", data)

	recorder := serveRequest(a, "/v1/downstream-limit-analysis?targetId=LONG-START")
	assertStatus(t, recorder, http.StatusOK)
	root := decodeObject(t, recorder)["tree"].(map[string]any)
	child := root["children"].([]any)[0].(map[string]any)
	if got := len(child["hiddenNodeIds"].([]any)); got != 1_000 {
		t.Fatalf("hiddenNodeIds length = %d, want 1000", got)
	}
	if child["hiddenNodeIdsTruncated"] != true {
		t.Fatalf("hiddenNodeIdsTruncated = %v", child["hiddenNodeIdsTruncated"])
	}
	if got, want := len(child["hiddenConnections"].([]any)), hiddenCount+1; got != want {
		t.Fatalf("hiddenConnections length = %d, want %d", got, want)
	}
}

func TestNormalizeISODuration(t *testing.T) {
	tests := []struct {
		name    string
		seconds int64
		want    string
	}{
		{name: "zero", seconds: 0, want: "PT0S"},
		{name: "seconds only", seconds: 45, want: "PT45S"},
		{name: "minutes only", seconds: 1_800, want: "PT30M"},
		{name: "hours and minutes", seconds: 5_400, want: "PT1H30M"},
		{name: "whole day", seconds: 86_400, want: "P1D"},
		{name: "compound", seconds: 93_784, want: "P1DT2H3M4S"},
		{name: "large", seconds: 3650*86_400 + 86_399, want: "P3650DT23H59M59S"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeISODuration(test.seconds)
			if err != nil || got != test.want {
				t.Fatalf("normalizeISODuration(%d) = %q, %v; want %q", test.seconds, got, err, test.want)
			}
		})
	}
	if _, err := normalizeISODuration(-1); err == nil {
		t.Fatal("normalizeISODuration(-1) succeeded")
	}
}

func TestDownstreamLimitAnalysisErrors(t *testing.T) {
	t.Run("snapshot not loaded", func(t *testing.T) {
		a := newTestApp(t)
		assertAnalysisProblem(t, a, "/v1/downstream-limit-analysis?targetId=JOB", http.StatusServiceUnavailable, "/problems/snapshot-not-loaded")
	})
	t.Run("invalid request", func(t *testing.T) {
		a := newTestApp(t)
		for _, path := range []string{
			"/v1/downstream-limit-analysis",
			"/v1/downstream-limit-analysis?targetId=",
			"/v1/downstream-limit-analysis?targetId=A&targetId=B",
			"/v1/downstream-limit-analysis?targetId=A&includeEvidence=yes",
			"/v1/downstream-limit-analysis?targetId=A&includeEvidence=true&includeEvidence=false",
		} {
			assertAnalysisProblem(t, a, path, http.StatusBadRequest, "/problems/invalid-request")
		}
	})
	t.Run("target not found", func(t *testing.T) {
		a := newTestApp(t)
		completeAnalysisGeneration(t, a, "not-found-snapshot", analysisFixture("NOT-FOUND"))
		assertAnalysisProblem(t, a, "/v1/downstream-limit-analysis?targetId=MISSING", http.StatusNotFound, "/problems/target-not-found")
		assertAnalysisProblem(t, a, "/v1/downstream-limit-analysis?targetId=UNIT", http.StatusNotFound, "/problems/target-not-found")
	})
	t.Run("internal error", func(t *testing.T) {
		a := newTestApp(t)
		if err := a.store.Close(); err != nil {
			t.Fatal(err)
		}
		assertAnalysisProblem(t, a, "/v1/downstream-limit-analysis?targetId=JOB", http.StatusInternalServerError, "/problems/internal-error")
	})
	t.Run("internal inconsistency", func(t *testing.T) {
		a := newTestApp(t)
		data := analysisTestData{
			nodes: []appTestNode{{id: "BROKEN-NET", typeName: "job_network", name: "Broken"}},
			facts: []analysisTestFact{{
				id: "BROKEN-LIMIT", ownerID: "BROKEN-NET", kind: "raw", sourceText: appString("raw"),
				origin: "manual", certainty: "declared",
			}},
		}
		completeAnalysisGeneration(t, a, "broken-snapshot", data)
		assertAnalysisProblem(t, a, "/v1/downstream-limit-analysis?targetId=BROKEN-NET", http.StatusInternalServerError, "/problems/internal-error")
	})
}

func TestDownstreamLimitAnalysisOpenAPIParameters(t *testing.T) {
	operation := OpenAPISpec().Paths["/v1/downstream-limit-analysis"].Get
	queryParameters := make(map[string]bool)
	for _, parameter := range operation.Parameters {
		if parameter.In != "query" {
			continue
		}
		queryParameters[parameter.Name] = true
		if parameter.Name == "targetId" && !parameter.Required {
			t.Error("targetId is not required")
		}
		if parameter.Name == "includeEvidence" {
			if parameter.Schema.Type != "boolean" || parameter.Schema.Default != false {
				t.Errorf("includeEvidence schema = %#v", parameter.Schema)
			}
		}
	}
	if !reflect.DeepEqual(queryParameters, map[string]bool{"targetId": true, "includeEvidence": true}) {
		t.Fatalf("query parameters = %v", queryParameters)
	}
	if operation.Responses["422"] != nil {
		t.Error("analysis endpoint exposes 422")
	}
}

func TestDownstreamLimitAnalysisIsDeterministic(t *testing.T) {
	a := newTestApp(t)
	completeAnalysisGeneration(t, a, "deterministic-snapshot", analysisFixture("DETERMINISTIC"))
	first := serveRequest(a, "/v1/downstream-limit-analysis?targetId=NET-TARGET&includeEvidence=true")
	second := serveRequest(a, "/v1/downstream-limit-analysis?targetId=NET-TARGET&includeEvidence=true")
	assertStatus(t, first, http.StatusOK)
	assertStatus(t, second, http.StatusOK)
	if !bytes.Equal(first.Body.Bytes(), second.Body.Bytes()) {
		t.Fatalf("responses differ:\n%s\n%s", first.Body.Bytes(), second.Body.Bytes())
	}
}

func TestDownstreamLimitAnalysisKeepsSnapshotAndResultsInSameGeneration(t *testing.T) {
	a := newTestApp(t)
	completeAnalysisGeneration(t, a, "old-analysis", generationAnalysisFixture("OLD"))

	responses := []*httptest.ResponseRecorder{serveRequest(a, "/v1/downstream-limit-analysis?targetId=COMMON")}
	operation := beginAnalysisGeneration(t, a, generationAnalysisFixture("NEW"))
	const concurrentRequests = 8
	concurrent := make([]*httptest.ResponseRecorder, concurrentRequests)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index := range concurrent {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			concurrent[index] = serveRequest(a, "/v1/downstream-limit-analysis?targetId=COMMON")
		}(index)
	}
	close(start)
	if err := operation.Complete(context.Background(), analysisGeneration("new-analysis", 2, 1, 1)); err != nil {
		t.Fatal(err)
	}
	wait.Wait()
	responses = append(responses, concurrent...)
	responses = append(responses, serveRequest(a, "/v1/downstream-limit-analysis?targetId=COMMON"))

	for _, recorder := range responses {
		assertStatus(t, recorder, http.StatusOK)
		body := decodeObject(t, recorder)
		snapshotID := body["snapshotId"].(string)
		prefix := map[string]string{"old-analysis": "OLD", "new-analysis": "NEW"}[snapshotID]
		if prefix == "" {
			t.Fatalf("snapshotId = %q", snapshotID)
		}
		limits := body["limits"].(map[string]any)["downstream"].(map[string]any)
		item := responseLimitItems(limits)[0]
		if !strings.HasPrefix(item["fact"].(map[string]any)["id"].(string), prefix) {
			t.Fatalf("snapshot %q contains limit %v", snapshotID, item)
		}
	}
}

func TestDownstreamLimitAnalysisStructuredLog(t *testing.T) {
	a := newTestApp(t)
	completeAnalysisGeneration(t, a, "log-snapshot", analysisFixture("SECRET-EVIDENCE"))
	var buffer bytes.Buffer
	a.logger = slog.New(slog.NewJSONHandler(&buffer, nil))

	request := httptest.NewRequest(http.MethodGet, "/v1/downstream-limit-analysis?targetId=NET-TARGET", nil)
	request.Header.Set("X-Request-Id", "analysis-request")
	recorder := httptest.NewRecorder()
	a.Handler().ServeHTTP(recorder, request)
	assertStatus(t, recorder, http.StatusOK)

	var entry map[string]any
	if err := json.Unmarshal(buffer.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"request_id": "analysis-request", "operation": analysisOperation, "boot_id": a.bootID,
		"snapshot_id": "log-snapshot", "target_id": "NET-TARGET",
	} {
		if entry[key] != want {
			t.Errorf("%s = %v, want %v", key, entry[key], want)
		}
	}
	for _, key := range []string{"duration_ms", "reached_nodes", "returned_tree_nodes", "returned_limits", "cycles_detected", "uncovered_routes"} {
		if _, ok := entry[key]; !ok {
			t.Errorf("log field %q is missing: %v", key, entry)
		}
	}
	logged := buffer.String()
	for _, secret := range []string{"Secret target network", "/UNIT/NET-TARGET", "SECRET-EVIDENCE", "sensitive limit source"} {
		if strings.Contains(logged, secret) {
			t.Errorf("log contains sensitive value %q: %s", secret, logged)
		}
	}
}

func analysisFixture(evidenceSource string) analysisTestData {
	evidence := fmt.Sprintf(`[{"source":%q,"location":{"startLine":1,"endLine":2,"jsonPointer":"/relations/0"},"note":"private note"}]`, evidenceSource)
	unit := "UNIT"
	targetNetwork := "NET-TARGET"
	downstreamNetwork := "NET-DOWN"
	path := "/UNIT/NET-TARGET"
	nodes := []appTestNode{
		{id: unit, typeName: "management_unit", name: "Unit"},
		{id: targetNetwork, typeName: "job_network", name: "Secret target network", path: &path, parentID: &unit},
		{id: "JOB-START", typeName: "job", name: "Start", parentID: &targetNetwork},
		{id: "JOB-CONTAINED", typeName: "job", name: "Contained", parentID: &targetNetwork},
		{id: downstreamNetwork, typeName: "job_network", name: "Downstream network", parentID: &unit},
		{id: "JOB-SCOPED", typeName: "job", name: "Scoped", parentID: &downstreamNetwork},
		{id: "MID-1", typeName: "job", name: "Middle 1"},
		{id: "MID-2", typeName: "job", name: "Middle 2"},
		{id: "JOB-DIRECT", typeName: "job", name: "Direct"},
		{id: "JOIN-A", typeName: "job", name: "Join A"},
		{id: "JOIN-B", typeName: "job", name: "Join B"},
		{id: "JOB-SHARED", typeName: "job", name: "Shared"},
		{id: "CYCLE-A", typeName: "job", name: "Cycle A"},
		{id: "CYCLE-B", typeName: "job", name: "Cycle B"},
		{id: "UNCOVERED-END", typeName: "job", name: "End"},
		{id: "UNIT-END", typeName: "management_unit", name: "Non traversable"},
	}
	relationValues := [][6]string{
		{"R-01", "JOB-START", "NET-DOWN", "precedes", "scheduler", "declared"},
		{"R-02", "JOB-START", "MID-1", "precedes", "scheduler", "declared"},
		{"R-03", "MID-1", "MID-2", "precedes", "scheduler", "declared"},
		{"R-04", "MID-2", "JOB-DIRECT", "precedes", "scheduler", "declared"},
		{"R-05", "JOB-START", "JOIN-A", "precedes", "scheduler", "declared"},
		{"R-06", "JOIN-A", "JOB-SHARED", "precedes", "scheduler", "declared"},
		{"R-07", "JOB-START", "JOIN-B", "precedes", "scheduler", "declared"},
		{"R-08", "JOIN-B", "JOB-SHARED", "precedes", "scheduler", "declared"},
		{"R-09", "JOB-START", "CYCLE-A", "triggers", "ai_analysis", "candidate"},
		{"R-10", "CYCLE-A", "CYCLE-B", "triggers", "ai_analysis", "inferred"},
		{"R-11", "CYCLE-B", "CYCLE-A", "triggers", "scheduler", "declared"},
		{"R-12", "JOB-START", "UNCOVERED-END", "precedes", "scheduler", "declared"},
		{"R-13", "JOB-START", "UNIT-END", "observed_by", "deterministic_analysis", "confirmed"},
		{"R-14", "JOB-SCOPED", "NET-DOWN", "precedes", "scheduler", "declared"},
	}
	relations := make([]analysisTestRelation, len(relationValues))
	for index, value := range relationValues {
		relations[index] = analysisTestRelation{
			id: value[0], fromID: value[1], toID: value[2], kind: value[3], origin: value[4], certainty: value[5], evidence: &evidence,
		}
	}
	return analysisTestData{
		nodes: nodes, relations: relations,
		facts: []analysisTestFact{
			{id: "LIMIT-CONTAINED", ownerID: "JOB-CONTAINED", kind: "raw", sourceText: appString("sensitive limit source"), origin: "manual", certainty: "declared"},
			{id: "LIMIT-SCOPED", ownerID: "JOB-SCOPED", kind: "finish_by", businessDayOffset: analysisInt64(1), localTimeSeconds: analysisInt64(19_800), timeZone: "Asia/Tokyo", sourceText: appString("next day"), origin: "scheduler", certainty: "declared"},
			{id: "LIMIT-SHARED", ownerID: "JOB-SHARED", kind: "finish_by", businessDayOffset: analysisInt64(0), localTimeSeconds: analysisInt64(21_600), timeZone: "UTC", sourceText: appString("six"), origin: "scheduler", certainty: "declared"},
			{id: "LIMIT-DIRECT", ownerID: "JOB-DIRECT", kind: "max_elapsed", durationSeconds: analysisInt64(93_784), sourceText: appString("compound duration"), origin: "scheduler", certainty: "declared"},
		},
	}
}

func generationAnalysisFixture(prefix string) analysisTestData {
	return analysisTestData{
		nodes: []appTestNode{
			{id: "COMMON", typeName: "job", name: "Common"},
			{id: prefix + "-OWNER", typeName: "job", name: prefix + " owner"},
		},
		relations: []analysisTestRelation{{
			id: prefix + "-REL", fromID: "COMMON", toID: prefix + "-OWNER",
			kind: "precedes", origin: "scheduler", certainty: "declared",
		}},
		facts: []analysisTestFact{{
			id: prefix + "-LIMIT", ownerID: prefix + "-OWNER", kind: "raw", sourceText: appString(prefix),
			origin: "scheduler", certainty: "declared",
		}},
	}
}

func completeAnalysisGeneration(t *testing.T, a *App, snapshotID string, data analysisTestData) {
	t.Helper()
	operation := beginAnalysisGeneration(t, a, data)
	if err := operation.Complete(context.Background(), analysisGeneration(snapshotID, len(data.nodes), len(data.relations), len(data.facts))); err != nil {
		t.Fatal(err)
	}
}

func beginAnalysisGeneration(t *testing.T, a *App, data analysisTestData) *store.Import {
	t.Helper()
	operation := beginAppGeneration(t, a, data.nodes)
	for _, relation := range data.relations {
		if _, err := operation.DB().Exec(`INSERT INTO relation (
			relation_id, from_id, to_id, relation_kind, origin, certainty, evidence_json
		) VALUES (?, ?, ?, ?, ?, ?, ?)`, relation.id, relation.fromID, relation.toID, relation.kind, relation.origin, relation.certainty, relation.evidence); err != nil {
			t.Fatal(err)
		}
	}
	for _, fact := range data.facts {
		finishSortSeconds := any(nil)
		if fact.businessDayOffset != nil && fact.localTimeSeconds != nil {
			finishSortSeconds = *fact.businessDayOffset*86_400 + *fact.localTimeSeconds
		}
		if _, err := operation.DB().Exec(`INSERT INTO limit_fact (
			limit_id, node_id, kind, business_day_offset, local_time_seconds, time_zone,
			finish_sort_seconds, duration_seconds, source_text, origin, certainty
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			fact.id, fact.ownerID, fact.kind, fact.businessDayOffset, fact.localTimeSeconds, nullableAnalysisString(fact.timeZone),
			finishSortSeconds, fact.durationSeconds, fact.sourceText, fact.origin, fact.certainty,
		); err != nil {
			t.Fatal(err)
		}
	}
	return operation
}

func analysisGeneration(snapshotID string, nodeCount, relationCount, limitCount int) store.Generation {
	result := generation(snapshotID, nodeCount)
	result.RelationCount = relationCount
	result.LimitCount = limitCount
	return result
}

func nullableAnalysisString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func analysisInt64(value int64) *int64 { return &value }

func responseLimitItems(section map[string]any) []map[string]any {
	items := make([]map[string]any, 0)
	for _, rawGroup := range section["finishByGroups"].([]any) {
		for _, rawItem := range rawGroup.(map[string]any)["items"].([]any) {
			items = append(items, rawItem.(map[string]any))
		}
	}
	for _, name := range []string{"maxElapsed", "raw"} {
		for _, rawItem := range section[name].(map[string]any)["items"].([]any) {
			items = append(items, rawItem.(map[string]any))
		}
	}
	return items
}

func responseLimitIDs(section map[string]any) []string {
	items := responseLimitItems(section)
	ids := make([]string, len(items))
	for index, item := range items {
		ids[index] = item["fact"].(map[string]any)["id"].(string)
	}
	return ids
}

func findResponseLimit(t *testing.T, section map[string]any, id string) map[string]any {
	t.Helper()
	for _, item := range responseLimitItems(section) {
		if item["fact"].(map[string]any)["id"] == id {
			return item
		}
	}
	t.Fatalf("limit %q not found in %v", id, section)
	return nil
}

func flattenResponseTree(root map[string]any) map[string]map[string]any {
	result := make(map[string]map[string]any)
	stack := []map[string]any{root}
	for len(stack) > 0 {
		last := len(stack) - 1
		current := stack[last]
		stack = stack[:last]
		result[current["treeNodeId"].(string)] = current
		for _, child := range current["children"].([]any) {
			stack = append(stack, child.(map[string]any))
		}
	}
	return result
}

func assertTreeConnectionRepresentations(t *testing.T, root map[string]any) {
	t.Helper()
	foundRelation, foundScope, foundHidden := false, false, false
	for _, node := range flattenResponseTree(root) {
		if relations, ok := node["viaRelations"].([]any); ok && len(relations) > 0 {
			foundRelation = true
			if node["viaScope"] == true {
				t.Fatalf("node mixes relation and scope connection: %v", node)
			}
		}
		if node["viaScope"] == true {
			foundScope = true
			if relations, ok := node["viaRelations"].([]any); ok && len(relations) != 0 {
				t.Fatalf("scope node has relations: %v", node)
			}
		}
		if raw, ok := node["hiddenConnections"].([]any); ok && len(raw) > 0 {
			foundHidden = true
			if _, ok := node["viaRelations"]; ok || node["viaScope"] == true {
				t.Fatalf("compressed node duplicates its connection: %v", node)
			}
			previous := ""
			for index, rawConnection := range raw {
				connection := rawConnection.(map[string]any)
				if index > 0 && connection["fromId"] != previous {
					t.Fatalf("hidden connections are discontinuous: %v", raw)
				}
				previous = connection["toId"].(string)
			}
			if previous != node["node"].(map[string]any)["id"] {
				t.Fatalf("hidden connections do not end at node: %v", node)
			}
		}
	}
	if !foundRelation || !foundScope || !foundHidden {
		t.Fatalf("connection forms: relation=%v scope=%v hidden=%v", foundRelation, foundScope, foundHidden)
	}
}

func assertReferenceNodes(t *testing.T, root map[string]any) {
	t.Helper()
	referenceTypes := make(map[string]bool)
	for _, node := range flattenResponseTree(root) {
		referenceType, ok := node["referenceType"].(string)
		if !ok {
			continue
		}
		referenceTypes[referenceType] = true
		if len(node["children"].([]any)) != 0 || node["referenceTo"] == "" {
			t.Fatalf("reference node is expanded or unresolved: %v", node)
		}
		if referenceType == "cycle" && node["cycleId"] == "" {
			t.Fatalf("cycle reference has no cycleId: %v", node)
		}
	}
	if !referenceTypes["shared"] || !referenceTypes["cycle"] {
		t.Fatalf("reference types = %v", referenceTypes)
	}
}

func assertUncoveredRoutes(t *testing.T, body map[string]any, treeNodes map[string]map[string]any) {
	t.Helper()
	limitOwnerIDs := make(map[string]bool)
	sections := body["limits"].(map[string]any)
	for _, name := range []string{"target", "contained", "downstream"} {
		for _, item := range responseLimitItems(sections[name].(map[string]any)) {
			limitOwnerIDs[item["limitOwner"].(map[string]any)["id"].(string)] = true
		}
	}
	reasons := make(map[string]bool)
	for _, raw := range body["uncoveredRoutes"].([]any) {
		route := raw.(map[string]any)
		reasons[route["reason"].(string)] = true
		treeNode := treeNodes[route["treeNodeId"].(string)]
		if treeNode == nil || treeNode["node"].(map[string]any)["id"] != route["boundary"].(map[string]any)["id"] {
			t.Fatalf("uncovered route does not resolve to its boundary: %v", route)
		}
		path := responseTreePath(body["tree"].(map[string]any), route["treeNodeId"].(string))
		if path == nil {
			t.Fatalf("uncovered route path was not found: %v", route)
		}
		for _, node := range path {
			if limitOwnerIDs[node["node"].(map[string]any)["id"].(string)] {
				t.Fatalf("uncovered route passes a limit owner: route=%v path=%v", route, path)
			}
		}
		if route["reason"] == "cycle_without_limit" && route["cycleId"] == "" {
			t.Fatalf("cycle route has no cycleId: %v", route)
		}
	}
	want := map[string]bool{"terminal_without_limit": true, "cycle_without_limit": true, "non_traversable_node_type": true}
	if !reflect.DeepEqual(reasons, want) {
		t.Fatalf("uncovered reasons = %v, want %v", reasons, want)
	}
}

func assertResponseLimitTotals(t *testing.T, sections map[string]any) {
	t.Helper()
	for _, sectionName := range []string{"target", "contained", "downstream"} {
		section := sections[sectionName].(map[string]any)
		for _, rawGroup := range section["finishByGroups"].([]any) {
			group := rawGroup.(map[string]any)
			if group["total"] != float64(len(group["items"].([]any))) {
				t.Fatalf("%s finishBy total differs from items: %v", sectionName, group)
			}
		}
		for _, groupName := range []string{"maxElapsed", "raw"} {
			group := section[groupName].(map[string]any)
			if group["total"] != float64(len(group["items"].([]any))) {
				t.Fatalf("%s %s total differs from items: %v", sectionName, groupName, group)
			}
		}
	}
}

func responseTreePath(root map[string]any, treeNodeID string) []map[string]any {
	if root["treeNodeId"] == treeNodeID {
		return []map[string]any{root}
	}
	for _, rawChild := range root["children"].([]any) {
		if childPath := responseTreePath(rawChild.(map[string]any), treeNodeID); childPath != nil {
			return append([]map[string]any{root}, childPath...)
		}
	}
	return nil
}

func assertCycles(t *testing.T, body map[string]any) {
	t.Helper()
	cycles := body["cycles"].([]any)
	if len(cycles) != 2 {
		t.Fatalf("cycles = %v", cycles)
	}
	cycle := cycles[0].(map[string]any)
	ids := make([]string, len(cycle["nodes"].([]any)))
	for index, rawNode := range cycle["nodes"].([]any) {
		ids[index] = rawNode.(map[string]any)["id"].(string)
	}
	if !sort.StringsAreSorted(ids) || strings.Join(ids, ",") != "CYCLE-A,CYCLE-B" {
		t.Fatalf("cycle nodes = %v", ids)
	}
	route := cycle["route"].([]any)
	if len(route) != 2 || route[0].(map[string]any)["fromId"] != "CYCLE-A" || route[1].(map[string]any)["toId"] != "CYCLE-A" {
		t.Fatalf("cycle route = %v", route)
	}
	if cycle["containsImplicitRelation"] != true || cycle["containsUncertainRelation"] != true {
		t.Fatalf("cycle flags = %v", cycle)
	}
	foundScope := false
	for _, rawStep := range cycles[1].(map[string]any)["route"].([]any) {
		step := rawStep.(map[string]any)
		if step["viaScope"] == true {
			foundScope = true
			if len(step["viaRelations"].([]any)) != 0 {
				t.Fatalf("cycle scope step contains relations: %v", step)
			}
		}
	}
	if !foundScope {
		t.Fatalf("cycle route has no scope transition: %v", cycles[1])
	}
}

func assertRelationEvidencePresence(t *testing.T, body map[string]any, want bool) {
	t.Helper()
	relations := make([]map[string]any, 0)
	for _, node := range flattenResponseTree(body["tree"].(map[string]any)) {
		for _, raw := range anySlice(node["viaRelations"]) {
			relations = append(relations, raw.(map[string]any))
		}
		for _, rawConnection := range anySlice(node["hiddenConnections"]) {
			for _, raw := range anySlice(rawConnection.(map[string]any)["viaRelations"]) {
				relations = append(relations, raw.(map[string]any))
			}
		}
	}
	for _, rawCycle := range body["cycles"].([]any) {
		for _, rawStep := range rawCycle.(map[string]any)["route"].([]any) {
			for _, raw := range anySlice(rawStep.(map[string]any)["viaRelations"]) {
				relations = append(relations, raw.(map[string]any))
			}
		}
	}
	if len(relations) == 0 {
		t.Fatal("no public relations found")
	}
	for _, relation := range relations {
		_, hasEvidence := relation["evidence"]
		if hasEvidence != want {
			t.Fatalf("relation evidence presence = %v, want %v: %v", hasEvidence, want, relation)
		}
		for _, required := range []string{"kind", "origin", "certainty"} {
			if relation[required] == nil {
				t.Fatalf("relation field %q missing: %v", required, relation)
			}
		}
	}
}

func anySlice(value any) []any {
	if value == nil {
		return nil
	}
	return value.([]any)
}

func assertAnalysisProblem(t *testing.T, a *App, path string, status int, problemType string) {
	t.Helper()
	recorder := serveRequest(a, path)
	assertStatus(t, recorder, status)
	assertContentType(t, recorder, "application/problem+json")
	body := decodeObject(t, recorder)
	if body["type"] != problemType || body["status"] != float64(status) {
		t.Fatalf("%s problem = %v", path, body)
	}
}
