package traversal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"

	_ "modernc.org/sqlite"
)

type testNode struct {
	id       string
	typeName string
	name     string
	path     *string
	parentID *string
}

type testRelation struct {
	id        string
	fromID    string
	toID      string
	kind      string
	origin    string
	certainty string
	evidence  *string
}

func TestTraverseDependencyPatterns(t *testing.T) {
	nodes := []testNode{
		{id: "START", typeName: "job"},
		{id: "SERIAL", typeName: "job"},
		{id: "FILE", typeName: "file"}, {id: "FILE-JOB", typeName: "job"},
		{id: "PATTERN", typeName: "file_pattern"}, {id: "PATTERN-JOB", typeName: "job"},
		{id: "STATUS", typeName: "job_status"}, {id: "STATUS-JOB", typeName: "job"},
		{id: "VENDOR-EVENT", typeName: "external_event"}, {id: "VENDOR-JOB", typeName: "job"},
		{id: "SERVERLESS-EVENT", typeName: "external_event"}, {id: "SERVERLESS-JOB", typeName: "job"},
		{id: "LEFT", typeName: "job"}, {id: "RIGHT", typeName: "job"}, {id: "MERGED", typeName: "job"},
		{id: "C1-A", typeName: "job"}, {id: "C1-FILE", typeName: "file"}, {id: "C1-B", typeName: "job"},
		{id: "C1-BRANCH", typeName: "job"},
		{id: "C2-A", typeName: "job"}, {id: "C2-EVENT", typeName: "external_event"}, {id: "C2-B", typeName: "job"},
	}
	relations := []testRelation{
		relation("01", "START", "SERIAL", "precedes"),
		relation("02", "START", "SERIAL", "triggers"),
		relation("03", "START", "FILE", "produces"), relation("04", "FILE", "FILE-JOB", "triggers"),
		relation("05", "START", "PATTERN", "produces"), relation("06", "PATTERN", "PATTERN-JOB", "consumed_by"),
		relation("07", "START", "STATUS", "produces"), relation("08", "STATUS", "STATUS-JOB", "observed_by"),
		relation("09", "START", "VENDOR-EVENT", "triggers"), relation("10", "VENDOR-EVENT", "VENDOR-JOB", "triggers"),
		relation("11", "START", "SERVERLESS-EVENT", "triggers"), relation("12", "SERVERLESS-EVENT", "SERVERLESS-JOB", "triggers"),
		relation("13", "START", "LEFT", "precedes"), relation("14", "START", "RIGHT", "precedes"),
		relation("15", "LEFT", "MERGED", "precedes"), relation("16", "RIGHT", "MERGED", "precedes"),
		relation("17", "START", "C1-A", "precedes"), relation("18", "START", "C1-B", "precedes"),
		relation("19", "C1-A", "C1-FILE", "produces"), relation("20", "C1-FILE", "C1-B", "triggers"),
		relation("21", "C1-B", "C1-A", "precedes"), relation("22", "C1-B", "C1-BRANCH", "precedes"),
		relation("23", "START", "C2-A", "precedes"), relation("24", "C2-A", "C2-EVENT", "triggers"),
		relation("25", "C2-EVENT", "C2-B", "triggers"), relation("26", "C2-B", "C2-A", "precedes"),
	}
	evidence := `[{"source":"job-definition"}]`
	relations[0].evidence = &evidence
	db := openTestDB(t, nodes, relations)

	result, err := Traverse(context.Background(), db, "START", Limits{})
	if err != nil {
		t.Fatalf("Traverse() error = %v", err)
	}

	for name, path := range map[string][]string{
		"serial job definition": {"START", "SERIAL"},
		"file":                  {"START", "FILE", "FILE-JOB"},
		"file pattern":          {"START", "PATTERN", "PATTERN-JOB"},
		"job status":            {"START", "STATUS", "STATUS-JOB"},
		"vendor event":          {"START", "VENDOR-EVENT", "VENDOR-JOB"},
		"serverless event":      {"START", "SERVERLESS-EVENT", "SERVERLESS-JOB"},
		"branch outside cycle":  {"START", "C1-A", "C1-FILE", "C1-B", "C1-BRANCH"},
	} {
		t.Run(name, func(t *testing.T) {
			if !hasPath(result.Connections, path) {
				t.Errorf("connections do not contain path %v", path)
			}
		})
	}

	serial := findConnection(t, result.Connections, "START", "SERIAL")
	if got := relationIDs(serial.Relations); !slices.Equal(got, []string{"01", "02"}) {
		t.Errorf("relations between START and SERIAL = %v", got)
	}
	if got := string(serial.Relations[0].Evidence); got != evidence {
		t.Errorf("relation evidence = %s, want %s", got, evidence)
	}
	if !slices.ContainsFunc(result.Shared, func(shared Shared) bool {
		return shared.FromID == "RIGHT" && shared.ToID == "MERGED"
	}) {
		t.Errorf("Shared = %#v, want RIGHT -> MERGED", result.Shared)
	}

	wantCycles := [][]string{
		{"C1-A", "C1-FILE", "C1-B", "C1-A"},
		{"C2-A", "C2-EVENT", "C2-B", "C2-A"},
	}
	if got := cyclePaths(result.Cycles); !slices.EqualFunc(got, wantCycles, slices.Equal[[]string]) {
		t.Errorf("Cycles = %v, want %v", got, wantCycles)
	}
	if result.Truncated {
		t.Errorf("Truncated = true, frontier = %#v", result.Frontier)
	}
	if !slices.ContainsFunc(result.Downstream, func(node Downstream) bool { return node.Node.ID == "C1-BRANCH" }) {
		t.Error("cycle-independent branch was not returned")
	}
}

func TestTraverseCycleTerminatesAndDeduplicatesRotatedCycle(t *testing.T) {
	nodes := []testNode{{id: "START", typeName: "job"}, {id: "A", typeName: "job"}, {id: "B", typeName: "file"}, {id: "C", typeName: "job"}}
	relations := []testRelation{
		relation("1", "START", "A", "precedes"), relation("2", "START", "B", "precedes"),
		relation("3", "A", "B", "produces"), relation("4", "B", "C", "triggers"), relation("5", "C", "A", "precedes"),
	}
	db := openTestDB(t, nodes, relations)

	result, err := Traverse(context.Background(), db, "START", Limits{})
	if err != nil {
		t.Fatalf("Traverse() error = %v", err)
	}
	if got, want := cyclePaths(result.Cycles), [][]string{{"A", "B", "C", "A"}}; !slices.EqualFunc(got, want, slices.Equal[[]string]) {
		t.Fatalf("Cycles = %v, want one normalized cycle %v", got, want)
	}
}

func TestNormalizeCycleUsesSmallestFullRotation(t *testing.T) {
	path := []string{"A", "C", "A", "B"}
	if got, want := normalizeCycle(path), []string{"A", "B", "A", "C"}; !slices.Equal(got, want) {
		t.Errorf("normalizeCycle(%v) = %v, want %v", path, got, want)
	}
}

func TestTraverseJobNetworkStartsAndExcludesContainedNodes(t *testing.T) {
	root := "ROOT"
	sub := "SUB"
	nodes := []testNode{
		{id: root, typeName: "job_network"},
		{id: sub, typeName: "job_network", parentID: &root},
		{id: "ENTRY-A", typeName: "job", parentID: &sub},
		{id: "ENTRY-B", typeName: "job", parentID: &root},
		{id: "INTERNAL", typeName: "job", parentID: &sub},
		{id: "EXTERNAL", typeName: "job"},
	}
	relations := []testRelation{
		relation("1", "ENTRY-A", "INTERNAL", "precedes"),
		relation("2", "ENTRY-B", "SUB", "precedes"),
		relation("3", "INTERNAL", "EXTERNAL", "precedes"),
	}
	db := openTestDB(t, nodes, relations)

	result, err := Traverse(context.Background(), db, root, Limits{})
	if err != nil {
		t.Fatalf("Traverse() error = %v", err)
	}
	if got, want := nodeIDs(result.StartNodes), []string{root}; !slices.Equal(got, want) {
		t.Errorf("StartNodes = %v, want %v", got, want)
	}
	if got, want := scopeEntryNodeIDs(result.ScopeEntries, root), []string{"ENTRY-A", "ENTRY-B"}; !slices.Equal(got, want) {
		t.Errorf("ScopeEntries = %v, want %v", got, want)
	}
	if result.UsedScopeFallback {
		t.Error("UsedScopeFallback = true")
	}
	if got, want := downstreamIDs(result.Downstream), []string{"EXTERNAL"}; !slices.Equal(got, want) {
		t.Errorf("Downstream = %v, want %v", got, want)
	}
	if !hasPath(result.Connections, []string{"ENTRY-A", "INTERNAL", "EXTERNAL"}) {
		t.Error("contained outgoing path was not connected to external downstream")
	}
}

func TestTraverseJobNetworkDoesNotReturnTargetAsDownstream(t *testing.T) {
	root := "ROOT"
	nodes := []testNode{
		{id: root, typeName: "job_network"},
		{id: "ENTRY", typeName: "job", parentID: &root},
	}
	relations := []testRelation{
		relation("1", "ENTRY", root, "precedes"),
	}
	db := openTestDB(t, nodes, relations)

	result, err := Traverse(context.Background(), db, root, Limits{})
	if err != nil {
		t.Fatalf("Traverse() error = %v", err)
	}
	if !hasPath(result.Connections, []string{"ENTRY", root}) {
		t.Error("connection from contained node back to target job network was not retained")
	}
	if slices.Contains(downstreamIDs(result.Downstream), root) {
		t.Errorf("Downstream = %v, contains target job network %s", downstreamIDs(result.Downstream), root)
	}
}

func TestTraverseJobNetworkFallsBackToUnvisitedScopeComponent(t *testing.T) {
	root := "ROOT"
	nodes := []testNode{
		{id: root, typeName: "job_network"},
		{id: "A", typeName: "job", parentID: &root},
		{id: "B", typeName: "job", parentID: &root},
		{id: "SUB", typeName: "job_network", parentID: &root},
		{id: "OUT", typeName: "job"},
	}
	relations := []testRelation{
		relation("1", "A", "B", "precedes"), relation("2", "B", "A", "precedes"),
		relation("3", "A", "SUB", "precedes"), relation("4", "SUB", "A", "precedes"),
		relation("5", "B", "OUT", "precedes"),
	}
	db := openTestDB(t, nodes, relations)

	result, err := Traverse(context.Background(), db, root, Limits{})
	if err != nil {
		t.Fatalf("Traverse() error = %v", err)
	}
	if !result.UsedScopeFallback {
		t.Error("UsedScopeFallback = false")
	}
	if got, want := nodeIDs(result.StartNodes), []string{root}; !slices.Equal(got, want) {
		t.Errorf("StartNodes = %v, want logical root %v", got, want)
	}
	if got, want := scopeEntryNodeIDs(result.ScopeEntries, root), []string{"A"}; !slices.Equal(got, want) {
		t.Errorf("fallback ScopeEntries = %v, want %v", got, want)
	}
	if got := downstreamIDs(result.Downstream); !slices.Equal(got, []string{"OUT"}) {
		t.Errorf("Downstream = %v, want [OUT]", got)
	}
}

func TestTraverseJobNetworkRootAndScopeDoNotCreateContainmentConnections(t *testing.T) {
	root := "ROOT"
	nodes := []testNode{
		{id: root, typeName: "job_network"},
		{id: "ENTRY", typeName: "job", parentID: &root},
		{id: "FROM-ROOT", typeName: "job"},
		{id: "FROM-ENTRY", typeName: "job"},
	}
	relations := []testRelation{
		relation("1", root, "FROM-ROOT", "precedes"),
		relation("2", "ENTRY", "FROM-ENTRY", "precedes"),
	}
	result, err := Traverse(context.Background(), openTestDB(t, nodes, relations), root, Limits{})
	if err != nil {
		t.Fatal(err)
	}

	if got, want := nodeIDs(result.StartNodes), []string{root}; !slices.Equal(got, want) {
		t.Errorf("StartNodes = %v, want %v", got, want)
	}
	if got, want := scopeEntryNodeIDs(result.ScopeEntries, root), []string{"ENTRY"}; !slices.Equal(got, want) {
		t.Errorf("ScopeEntries = %v, want %v", got, want)
	}
	if !hasPath(result.Connections, []string{root, "FROM-ROOT"}) ||
		!hasPath(result.Connections, []string{"ENTRY", "FROM-ENTRY"}) {
		t.Errorf("Connections = %#v, want outgoing relations from root and scope", result.Connections)
	}
	if hasPath(result.Connections, []string{root, "ENTRY"}) {
		t.Error("containment was mixed into dependency connections")
	}
}

func TestTraverseEmptyJobNetworkUsesLogicalRootOutgoingRelation(t *testing.T) {
	root := "ROOT"
	nodes := []testNode{
		{id: root, typeName: "job_network"},
		{id: "EXTERNAL", typeName: "job"},
	}
	result, err := Traverse(context.Background(), openTestDB(t, nodes, []testRelation{
		relation("1", root, "EXTERNAL", "precedes"),
	}), root, Limits{})
	if err != nil {
		t.Fatal(err)
	}

	if got, want := nodeIDs(result.StartNodes), []string{root}; !slices.Equal(got, want) {
		t.Errorf("StartNodes = %v, want %v", got, want)
	}
	if len(result.ScopeEntries) != 0 {
		t.Errorf("ScopeEntries = %#v, want no entries for an empty scope", result.ScopeEntries)
	}
	if got, want := downstreamIDs(result.Downstream), []string{"EXTERNAL"}; !slices.Equal(got, want) {
		t.Errorf("Downstream = %v, want %v", got, want)
	}
	if !hasPath(result.Connections, []string{root, "EXTERNAL"}) {
		t.Error("job network logical root outgoing relation was not explored")
	}
}

func TestTraverseJobNetworkFallbackCoversDisconnectedScopeComponents(t *testing.T) {
	root := "ROOT"
	nodes := []testNode{
		{id: root, typeName: "job_network"},
		{id: "A", typeName: "job", parentID: &root},
		{id: "B", typeName: "job", parentID: &root},
		{id: "C", typeName: "job_network", parentID: &root},
		{id: "D", typeName: "job", parentID: &root},
		{id: "E", typeName: "job", parentID: &root},
	}
	relations := []testRelation{
		relation("1", "A", "B", "precedes"),
		relation("2", "C", "D", "precedes"),
		relation("3", "D", "C", "precedes"),
	}
	result, err := Traverse(context.Background(), openTestDB(t, nodes, relations), root, Limits{})
	if err != nil {
		t.Fatal(err)
	}

	if !result.UsedScopeFallback {
		t.Error("UsedScopeFallback = false, want rescue of the C-D cycle")
	}
	if got, want := scopeEntryNodeIDs(result.ScopeEntries, root), []string{"A", "C", "E"}; !slices.Equal(got, want) {
		t.Errorf("ScopeEntries = %v, want incoming-free entries followed by rescued C", got)
	}
	for _, id := range []string{"A", "B", "C", "D", "E", root} {
		if !slices.Contains(visitIDs(result.Nodes), id) {
			t.Errorf("Nodes = %v, missing scope component node %s", visitIDs(result.Nodes), id)
		}
	}
	if got, want := cyclePaths(result.Cycles), [][]string{{"C", "D", "C"}}; !slices.EqualFunc(got, want, slices.Equal[[]string]) {
		t.Errorf("Cycles = %v, want %v", got, want)
	}
}

func TestTraverseFallbackCoversCycleOfNestedJobNetworksAndOutgoingRelation(t *testing.T) {
	root := "ROOT"
	nodes := []testNode{
		{id: root, typeName: "job_network"},
		{id: "NETWORK-A", typeName: "job_network", parentID: &root},
		{id: "NETWORK-B", typeName: "job_network", parentID: &root},
		{id: "EXTERNAL", typeName: "job"},
	}
	relations := []testRelation{
		relation("1", "NETWORK-A", "NETWORK-B", "precedes"),
		relation("2", "NETWORK-B", "NETWORK-A", "precedes"),
		relation("3", "NETWORK-B", "EXTERNAL", "precedes"),
	}
	result, err := Traverse(context.Background(), openTestDB(t, nodes, relations), root, Limits{})
	if err != nil {
		t.Fatal(err)
	}

	if !result.UsedScopeFallback {
		t.Error("UsedScopeFallback = false, want rescue of nested job network cycle")
	}
	if got, want := scopeEntryNodeIDs(result.ScopeEntries, root), []string{"NETWORK-A"}; !slices.Equal(got, want) {
		t.Errorf("ScopeEntries = %v, want %v", got, want)
	}
	if entry := findScopeEntry(t, result.ScopeEntries, root, "NETWORK-A"); !entry.Fallback {
		t.Errorf("scope entry = %#v, want fallback entry", entry)
	}
	if got, want := cyclePaths(result.Cycles), [][]string{{"NETWORK-A", "NETWORK-B", "NETWORK-A"}}; !slices.EqualFunc(got, want, slices.Equal[[]string]) {
		t.Errorf("Cycles = %v, want %v", got, want)
	}
	if got, want := downstreamIDs(result.Downstream), []string{"EXTERNAL"}; !slices.Equal(got, want) {
		t.Errorf("Downstream = %v, want %v", got, want)
	}
}

func TestTraverseExpandsReachedJobNetworkAndKeepsLogicalNetwork(t *testing.T) {
	network := "NETWORK"
	subnetwork := "SUBNETWORK"
	nodes := []testNode{
		{id: "START", typeName: "job"},
		{id: network, typeName: "job_network"},
		{id: "ENTRY", typeName: "job", parentID: &network},
		{id: "INTERNAL", typeName: "job", parentID: &network},
		{id: subnetwork, typeName: "job_network", parentID: &network},
		{id: "DEEP", typeName: "job", parentID: &subnetwork},
		{id: "FROM-NETWORK", typeName: "job"},
		{id: "FROM-INTERNAL", typeName: "job"},
	}
	relations := []testRelation{
		relation("1", "START", network, "precedes"),
		relation("2", network, "FROM-NETWORK", "precedes"),
		relation("3", "ENTRY", "INTERNAL", "precedes"),
		relation("4", "INTERNAL", "FROM-INTERNAL", "precedes"),
	}
	result, err := Traverse(context.Background(), openTestDB(t, nodes, relations), "START", Limits{})
	if err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{network, "ENTRY", "INTERNAL", subnetwork, "DEEP"} {
		if !slices.Contains(downstreamIDs(result.Downstream), id) {
			t.Errorf("Downstream = %v, missing reached network scope node %s", downstreamIDs(result.Downstream), id)
		}
	}
	if !hasPath(result.Connections, []string{network, "FROM-NETWORK"}) ||
		!hasPath(result.Connections, []string{"INTERNAL", "FROM-INTERNAL"}) {
		t.Errorf("Connections = %#v, reached network scope was not explored", result.Connections)
	}
	if hasPath(result.Connections, []string{network, "ENTRY"}) ||
		hasPath(result.Connections, []string{subnetwork, "DEEP"}) {
		t.Error("reached network containment was mixed into dependency connections")
	}
	if got, want := scopeEntryNodeIDs(result.ScopeEntries, network), []string{"DEEP", "ENTRY", subnetwork}; !slices.Equal(got, want) {
		t.Errorf("reached network ScopeEntries = %v, want %v", got, want)
	}
	if got := scopeEntryNodeIDs(result.ScopeEntries, subnetwork); len(got) != 0 {
		t.Errorf("nested network was redundantly expanded with ScopeEntries %v", got)
	}
}

func TestTraverseRecursivelyExcludesTargetNetworkScopeFromDownstream(t *testing.T) {
	root, subnetwork := "ROOT", "SUBNETWORK"
	nodes := []testNode{
		{id: root, typeName: "job_network"},
		{id: subnetwork, typeName: "job_network", parentID: &root},
		{id: "DEEP", typeName: "job", parentID: &subnetwork},
		{id: "EXTERNAL", typeName: "job"},
	}
	relations := []testRelation{
		relation("1", "DEEP", "EXTERNAL", "precedes"),
	}
	result, err := Traverse(context.Background(), openTestDB(t, nodes, relations), root, Limits{})
	if err != nil {
		t.Fatal(err)
	}

	if got, want := downstreamIDs(result.Downstream), []string{"EXTERNAL"}; !slices.Equal(got, want) {
		t.Errorf("Downstream = %v, want only target-scope external node %v", got, want)
	}
	for _, id := range []string{subnetwork, "DEEP"} {
		if !slices.Contains(visitIDs(result.Nodes), id) {
			t.Errorf("Nodes = %v, target scope descendant %s was not explored", visitIDs(result.Nodes), id)
		}
	}
}

func TestTraverseSeparatesGraphDepthAndDependencyDistance(t *testing.T) {
	nodes := []testNode{
		{id: "START", typeName: "job"},
		{id: "DIRECT", typeName: "job"},
		{id: "FILE", typeName: "file"},
		{id: "VIA-FILE", typeName: "job"},
	}
	relations := []testRelation{
		relation("1", "START", "DIRECT", "precedes"),
		relation("2", "START", "FILE", "produces"),
		relation("3", "FILE", "VIA-FILE", "triggers"),
	}
	result, err := Traverse(context.Background(), openTestDB(t, nodes, relations), "START", Limits{})
	if err != nil {
		t.Fatal(err)
	}

	assertVisitDistance(t, result.Nodes, "FILE", 1, 0)
	assertVisitDistance(t, result.Nodes, "DIRECT", 1, 1)
	assertVisitDistance(t, result.Nodes, "VIA-FILE", 2, 1)
	assertDownstreamDistance(t, result.Downstream, "DIRECT", 1, 1)
	assertDownstreamDistance(t, result.Downstream, "VIA-FILE", 2, 1)
}

func TestTraverseDownstreamUsesShortestDependencyDistance(t *testing.T) {
	nodes := []testNode{
		{id: "START", typeName: "job"}, {id: "JOB", typeName: "job"},
		{id: "FILE-1", typeName: "file"}, {id: "FILE-2", typeName: "file"},
		{id: "DESTINATION", typeName: "job"},
	}
	relations := []testRelation{
		relation("1", "START", "JOB", "precedes"),
		relation("2", "JOB", "DESTINATION", "precedes"),
		relation("3", "START", "FILE-1", "produces"),
		relation("4", "FILE-1", "FILE-2", "produces"),
		relation("5", "FILE-2", "DESTINATION", "triggers"),
	}
	result, err := Traverse(context.Background(), openTestDB(t, nodes, relations), "START", Limits{})
	if err != nil {
		t.Fatal(err)
	}

	assertDownstreamDistance(t, result.Downstream, "DESTINATION", 3, 1)
	connection := findConnection(t, result.Connections, "JOB", "DESTINATION")
	if connection.GraphDepth != 2 || connection.DependencyDistance != 2 {
		t.Errorf("JOB -> DESTINATION distances = (%d, %d), want path-specific (2, 2)",
			connection.GraphDepth, connection.DependencyDistance)
	}
}

func TestTraverseReexpandsProcessedNodeAfterScopeFallbackFindsShorterDistance(t *testing.T) {
	root := "NET"
	nodes := []testNode{
		{id: root, typeName: "job_network"},
		{id: "A", typeName: "job", parentID: &root},
		{id: "C", typeName: "job", parentID: &root},
		{id: "D", typeName: "job", parentID: &root},
		{id: "M", typeName: "job"},
		{id: "N", typeName: "job"},
		{id: "X", typeName: "job"},
		{id: "Y", typeName: "job"},
	}
	relations := []testRelation{
		relation("01", "A", "M", "precedes"),
		relation("02", "M", "N", "precedes"),
		relation("03", "N", "X", "precedes"),
		relation("04", "C", "D", "precedes"),
		relation("05", "D", "C", "precedes"),
		relation("06", "C", "X", "precedes"),
		relation("07", "X", "Y", "precedes"),
		relation("08", "X", "Y", "triggers"),
	}

	var baseline []byte
	for iteration := 0; iteration < 2; iteration++ {
		orderedNodes := append([]testNode(nil), nodes...)
		orderedRelations := append([]testRelation(nil), relations...)
		if iteration == 1 {
			slices.Reverse(orderedNodes)
			slices.Reverse(orderedRelations)
		}
		result, err := Traverse(context.Background(), openTestDB(t, orderedNodes, orderedRelations), root, Limits{})
		if err != nil {
			t.Fatal(err)
		}

		if !result.UsedScopeFallback {
			t.Error("UsedScopeFallback = false, want disconnected C-D component to be rescued")
		}
		assertVisitDistance(t, result.Nodes, "X", 1, 1)
		assertVisitDistance(t, result.Nodes, "Y", 2, 2)
		assertDownstreamDistance(t, result.Downstream, "X", 1, 1)
		assertDownstreamDistance(t, result.Downstream, "Y", 2, 2)
		connection := findConnection(t, result.Connections, "X", "Y")
		if connection.GraphDepth != 2 || connection.DependencyDistance != 2 {
			t.Errorf("X -> Y distances = (%d, %d), want re-expanded path distance (2, 2)",
				connection.GraphDepth, connection.DependencyDistance)
		}
		if got, want := relationIDs(connection.Relations), []string{"07", "08"}; !slices.Equal(got, want) {
			t.Errorf("X -> Y relations = %v, want one merged connection with relations %v", got, want)
		}
		count := 0
		for _, candidate := range result.Connections {
			if candidate.FromID == "X" && candidate.To.ID == "Y" {
				count++
			}
		}
		if count != 1 {
			t.Errorf("X -> Y connection count = %d, want 1", count)
		}

		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if baseline == nil {
			baseline = encoded
			continue
		}
		if string(encoded) != string(baseline) {
			t.Fatalf("re-expansion result changed with insertion order\nfirst: %s\n got: %s", baseline, encoded)
		}
	}
}

func TestTraverseKeepsShortestConfirmedDependencyDistanceSeparately(t *testing.T) {
	candidate := relation("1", "START", "DESTINATION", "precedes")
	candidate.certainty = "candidate"
	nodes := []testNode{
		{id: "START", typeName: "job"},
		{id: "INTERMEDIATE", typeName: "job"},
		{id: "DESTINATION", typeName: "job"},
	}
	result, err := Traverse(context.Background(), openTestDB(t, nodes, []testRelation{
		candidate,
		relation("2", "START", "INTERMEDIATE", "precedes"),
		relation("3", "INTERMEDIATE", "DESTINATION", "precedes"),
	}), "START", Limits{})
	if err != nil {
		t.Fatal(err)
	}

	destination := findDownstream(t, result.Downstream, "DESTINATION")
	if destination.DependencyDistance != 1 {
		t.Errorf("DependencyDistance = %d, want candidate path distance 1", destination.DependencyDistance)
	}
	if destination.ConfirmedDependencyDistance == nil || *destination.ConfirmedDependencyDistance != 2 {
		t.Errorf("ConfirmedDependencyDistance = %v, want declared path distance 2", destination.ConfirmedDependencyDistance)
	}
}

func TestTraverseLeavesConfirmedDependencyDistanceNilForCandidateOnlyPath(t *testing.T) {
	candidate := relation("1", "START", "DESTINATION", "precedes")
	candidate.certainty = "candidate"
	result, err := Traverse(context.Background(), openTestDB(t, []testNode{
		{id: "START", typeName: "job"},
		{id: "DESTINATION", typeName: "job"},
	}, []testRelation{candidate}), "START", Limits{})
	if err != nil {
		t.Fatal(err)
	}

	destination := findDownstream(t, result.Downstream, "DESTINATION")
	if destination.ConfirmedDependencyDistance != nil {
		t.Errorf("ConfirmedDependencyDistance = %d, want nil for candidate-only path",
			*destination.ConfirmedDependencyDistance)
	}
}

func TestTraverseTreatsScopeExpansionAsConfirmedWithoutIncreasingDistance(t *testing.T) {
	network := "NETWORK"
	result, err := Traverse(context.Background(), openTestDB(t, []testNode{
		{id: "START", typeName: "job"},
		{id: network, typeName: "job_network"},
		{id: "CHILD", typeName: "job", parentID: &network},
	}, []testRelation{
		relation("1", "START", network, "precedes"),
	}), "START", Limits{})
	if err != nil {
		t.Fatal(err)
	}

	child := findDownstream(t, result.Downstream, "CHILD")
	if child.ConfirmedDependencyDistance == nil || *child.ConfirmedDependencyDistance != 1 {
		t.Errorf("CHILD ConfirmedDependencyDistance = %v, want scope-preserving distance 1",
			child.ConfirmedDependencyDistance)
	}
}

func TestTraverseKeepsNodePathAndParentID(t *testing.T) {
	root, rootPath, childPath, externalPath := "ROOT", "/ROOT", "/ROOT/CHILD", "/EXTERNAL"
	nodes := []testNode{
		{id: root, typeName: "job_network", path: &rootPath},
		{id: "CHILD", typeName: "job", path: &childPath, parentID: &root},
		{id: "EXTERNAL", typeName: "job", path: &externalPath},
	}
	result, err := Traverse(context.Background(), openTestDB(t, nodes, []testRelation{
		relation("1", "CHILD", "EXTERNAL", "precedes"),
	}), root, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Target.Path == nil || *result.Target.Path != rootPath {
		t.Errorf("Target.Path = %v, want %q", result.Target.Path, rootPath)
	}
	child := findVisit(t, result.Nodes, "CHILD").Node
	if child.Path == nil || *child.Path != childPath || child.ParentID == nil || *child.ParentID != root {
		t.Errorf("child node = %#v, want path %q and parent %q", child, childPath, root)
	}
	external := findConnection(t, result.Connections, "CHILD", "EXTERNAL").To
	if external.Path == nil || *external.Path != externalPath || external.ParentID != nil {
		t.Errorf("external connection node = %#v, want path %q and no parent", external, externalPath)
	}
}

func TestTraverseDistinguishesSameNamedJobsByPathAndParentID(t *testing.T) {
	networkA, networkB := "NETWORK-A", "NETWORK-B"
	pathA, pathB := "/NETWORK-A/SHARED", "/NETWORK-B/SHARED"
	nodes := []testNode{
		{id: "START", typeName: "job"},
		{id: networkA, typeName: "job_network"},
		{id: networkB, typeName: "job_network"},
		{id: "JOB-A", typeName: "job", name: "SHARED", path: &pathA, parentID: &networkA},
		{id: "JOB-B", typeName: "job", name: "SHARED", path: &pathB, parentID: &networkB},
	}
	relations := []testRelation{
		relation("1", "START", "JOB-A", "precedes"),
		relation("2", "START", "JOB-B", "precedes"),
	}
	result, err := Traverse(context.Background(), openTestDB(t, nodes, relations), "START", Limits{})
	if err != nil {
		t.Fatal(err)
	}

	jobA := findVisit(t, result.Nodes, "JOB-A").Node
	jobB := findVisit(t, result.Nodes, "JOB-B").Node
	if jobA.Name != jobB.Name {
		t.Fatalf("job names = (%q, %q), want the same name", jobA.Name, jobB.Name)
	}
	if jobA.Path == nil || *jobA.Path != pathA || jobA.ParentID == nil || *jobA.ParentID != networkA {
		t.Errorf("JOB-A = %#v, want path %q and parent %q", jobA, pathA, networkA)
	}
	if jobB.Path == nil || *jobB.Path != pathB || jobB.ParentID == nil || *jobB.ParentID != networkB {
		t.Errorf("JOB-B = %#v, want path %q and parent %q", jobB, pathB, networkB)
	}
}

func TestTraverseReportsGraphDepthAndNodeFrontiers(t *testing.T) {
	nodes := []testNode{{id: "A", typeName: "job"}, {id: "B", typeName: "job"}, {id: "C", typeName: "job"}, {id: "D", typeName: "job"}}
	relations := []testRelation{
		relation("1", "A", "B", "precedes"), relation("2", "B", "C", "precedes"), relation("3", "C", "D", "precedes"),
	}
	db := openTestDB(t, nodes, relations)

	graphDepthResult, err := Traverse(context.Background(), db, "A", Limits{MaxGraphDepth: 1})
	if err != nil {
		t.Fatal(err)
	}
	assertFrontier(t, graphDepthResult, "B", 1, 1, TruncationGraphDepthLimit)
	if hasPath(graphDepthResult.Connections, []string{"B", "C"}) {
		t.Error("graph-depth-limited result retained an untraversed connection")
	}

	nodeResult, err := Traverse(context.Background(), db, "A", Limits{MaxVisitedNodes: 2})
	if err != nil {
		t.Fatal(err)
	}
	assertFrontier(t, nodeResult, "C", 2, 2, TruncationNodeLimit)
	if got := visitIDs(nodeResult.Nodes); !slices.Equal(got, []string{"A", "B"}) {
		t.Errorf("visited nodes = %v, want [A B]", got)
	}
	if !hasPath(nodeResult.Connections, []string{"B", "C"}) {
		t.Error("node-limited result did not retain the connection to frontier")
	}
	if hasPath(nodeResult.Connections, []string{"C", "D"}) {
		t.Error("node-limited result investigated beyond frontier")
	}
}

func TestTraverseReturnsPartialResultAtConnectionLimit(t *testing.T) {
	nodes := []testNode{{id: "START", typeName: "job"}}
	relations := make([]testRelation, 0, 6)
	for index := 0; index < 6; index++ {
		id := fmt.Sprintf("JOB-%02d", index)
		nodes = append(nodes, testNode{id: id, typeName: "job"})
		relations = append(relations, relation(fmt.Sprintf("relation-%02d", index), "START", id, "precedes"))
	}

	var result Result
	var baseline []byte
	for iteration := 0; iteration < 2; iteration++ {
		orderedNodes := append([]testNode(nil), nodes...)
		orderedRelations := append([]testRelation(nil), relations...)
		if iteration == 1 {
			slices.Reverse(orderedNodes)
			slices.Reverse(orderedRelations)
		}
		current, err := Traverse(
			context.Background(), openTestDB(t, orderedNodes, orderedRelations), "START", Limits{MaxConnections: 3},
		)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(current)
		if err != nil {
			t.Fatal(err)
		}
		if iteration == 0 {
			result = current
			baseline = encoded
			continue
		}
		if string(encoded) != string(baseline) {
			t.Fatalf("connection-limit cutoff changed with insertion order\nfirst: %s\n got: %s", baseline, encoded)
		}
	}

	if got, want := downstreamIDs(result.Downstream), []string{"JOB-00", "JOB-01", "JOB-02"}; !slices.Equal(got, want) {
		t.Errorf("Downstream = %v, want deterministic partial result %v", got, want)
	}
	if got := countRelations(result.Connections); got != 3 {
		t.Errorf("relation count = %d, want connection limit 3", got)
	}
	assertFrontier(t, result, "START", 0, 0, TruncationConnectionLimit)
}

func TestTraverseAppliesConnectionLimitToScopeExpansion(t *testing.T) {
	root := "ROOT"
	nodes := []testNode{{id: root, typeName: "job_network"}}
	for index := 0; index < 4; index++ {
		nodes = append(nodes, testNode{
			id: fmt.Sprintf("JOB-%02d", index), typeName: "job", parentID: &root,
		})
	}

	result, err := Traverse(
		context.Background(), openTestDB(t, nodes, nil), root, Limits{MaxConnections: 2},
	)
	if err != nil {
		t.Fatal(err)
	}

	assertFrontier(t, result, root, 0, 0, TruncationConnectionLimit)
	if len(result.ScopeEntries) != 0 {
		t.Errorf("ScopeEntries = %#v, want none before complete scope membership and incoming checks", result.ScopeEntries)
	}
	if got, want := visitIDs(result.Nodes), []string{root}; !slices.Equal(got, want) {
		t.Errorf("Nodes = %v, want only logical root after partial scope read %v", got, want)
	}
}

func TestTraverseAppliesConnectionLimitToScopeIncomingRelations(t *testing.T) {
	root := "ROOT"
	nodes := []testNode{
		{id: root, typeName: "job_network"},
		{id: "A", typeName: "job", parentID: &root},
		{id: "B", typeName: "job", parentID: &root},
		{id: "C", typeName: "job", parentID: &root},
	}
	result, err := Traverse(context.Background(), openTestDB(t, nodes, []testRelation{
		relation("1", "A", "B", "precedes"),
		relation("2", "B", "C", "precedes"),
	}), root, Limits{MaxConnections: 4})
	if err != nil {
		t.Fatal(err)
	}

	assertFrontier(t, result, root, 0, 0, TruncationConnectionLimit)
	if len(result.ScopeEntries) != 0 {
		t.Errorf("ScopeEntries = %#v, want none after partial incoming-relation check", result.ScopeEntries)
	}
}

func TestTraverseBatchesLargeFrontier(t *testing.T) {
	nodes := []testNode{{id: "START", typeName: "job"}, {id: "END", typeName: "job"}}
	relations := make([]testRelation, 0, queryBatchSize*2+2)
	for index := 0; index <= queryBatchSize; index++ {
		id := fmt.Sprintf("N-%03d", index)
		nodes = append(nodes, testNode{id: id, typeName: "file"})
		relations = append(relations,
			relation(fmt.Sprintf("a-%03d", index), "START", id, "produces"),
			relation(fmt.Sprintf("b-%03d", index), id, "END", "triggers"),
		)
	}
	db := openTestDB(t, nodes, relations)

	result, err := Traverse(context.Background(), db, "START", Limits{})
	if err != nil {
		t.Fatalf("Traverse() with %d-node frontier error = %v", queryBatchSize+1, err)
	}
	if got := downstreamIDs(result.Downstream); !slices.Equal(got, []string{"END"}) {
		t.Errorf("Downstream = %v, want [END]", got)
	}
}

func TestTraverseOrderDoesNotDependOnInsertionOrder(t *testing.T) {
	nodes := []testNode{
		{id: "START", typeName: "job"}, {id: "A", typeName: "job"},
		{id: "B", typeName: "file"}, {id: "C", typeName: "job"},
	}
	relations := []testRelation{
		relation("4", "B", "C", "triggers"), relation("2", "START", "B", "produces"),
		relation("3", "A", "C", "precedes"), relation("1", "START", "A", "precedes"),
	}
	var baseline []byte
	for iteration := 0; iteration < 4; iteration++ {
		rotatedNodes := append(append([]testNode(nil), nodes[iteration:]...), nodes[:iteration]...)
		rotatedRelations := append(append([]testRelation(nil), relations[iteration:]...), relations[:iteration]...)
		db := openTestDB(t, rotatedNodes, rotatedRelations)
		result, err := Traverse(context.Background(), db, "START", Limits{})
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if baseline == nil {
			baseline = encoded
			continue
		}
		if string(encoded) != string(baseline) {
			t.Fatalf("iteration %d result order changed\nfirst: %s\n got: %s", iteration, baseline, encoded)
		}
	}
}

func TestTraverseJobNetworkScopeOrderDoesNotDependOnInsertionOrder(t *testing.T) {
	root := "ROOT"
	nodes := []testNode{
		{id: root, typeName: "job_network"},
		{id: "ENTRY", typeName: "job", parentID: &root},
		{id: "CYCLE-A", typeName: "job_network", parentID: &root},
		{id: "CYCLE-B", typeName: "job_network", parentID: &root},
		{id: "EXTERNAL", typeName: "job"},
	}
	relations := []testRelation{
		relation("1", "CYCLE-A", "CYCLE-B", "precedes"),
		relation("2", "CYCLE-B", "CYCLE-A", "precedes"),
		relation("3", "CYCLE-B", "EXTERNAL", "precedes"),
	}

	var baseline []byte
	for iteration := 0; iteration < 2; iteration++ {
		orderedNodes := append([]testNode(nil), nodes...)
		orderedRelations := append([]testRelation(nil), relations...)
		if iteration == 1 {
			slices.Reverse(orderedNodes)
			slices.Reverse(orderedRelations)
		}
		result, err := Traverse(context.Background(), openTestDB(t, orderedNodes, orderedRelations), root, Limits{})
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if baseline == nil {
			baseline = encoded
			continue
		}
		if string(encoded) != string(baseline) {
			t.Fatalf("scope result order changed\nfirst: %s\n got: %s", baseline, encoded)
		}
	}
}

func TestTraverseRejectsMissingAndUnsupportedTargets(t *testing.T) {
	db := openTestDB(t, []testNode{{id: "FILE", typeName: "file"}}, nil)
	if _, err := Traverse(context.Background(), db, "MISSING", Limits{}); !errors.Is(err, ErrTargetNotFound) {
		t.Errorf("missing target error = %v", err)
	}
	if _, err := Traverse(context.Background(), db, "FILE", Limits{}); !errors.Is(err, ErrInvalidTargetType) {
		t.Errorf("file target error = %v", err)
	}
}

func TestResolveLimitsUsesServiceDefaults(t *testing.T) {
	resolved, err := resolveLimits(Limits{})
	if err != nil {
		t.Fatal(err)
	}
	want := Limits{
		MaxVisitedNodes: DefaultMaxVisitedNodes,
		MaxGraphDepth:   DefaultMaxGraphDepth,
		MaxConnections:  DefaultMaxConnections,
	}
	if resolved != want {
		t.Errorf("resolveLimits(Limits{}) = %#v, want %#v", resolved, want)
	}
}

func TestTraverseDoesNotTreatManagementHierarchyAsIntermediate(t *testing.T) {
	nodes := []testNode{
		{id: "START", typeName: "job"},
		{id: "UNIT", typeName: "management_unit"},
		{id: "JOB", typeName: "job"},
	}
	relations := []testRelation{
		relation("1", "START", "UNIT", "precedes"),
		relation("2", "UNIT", "JOB", "precedes"),
	}
	db := openTestDB(t, nodes, relations)

	result, err := Traverse(context.Background(), db, "START", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(downstreamIDs(result.Downstream), "JOB") {
		t.Error("management_unit was used as an execution dependency intermediate")
	}
	if hasPath(result.Connections, []string{"UNIT", "JOB"}) {
		t.Error("management_unit successors were queried")
	}
	if len(result.Unexplored) != 1 {
		t.Fatalf("Unexplored = %#v, want one non-traversable arrival", result.Unexplored)
	}
	unexplored := result.Unexplored[0]
	if unexplored.Node.ID != "UNIT" || unexplored.GraphDepth != 1 || unexplored.DependencyDistance != 0 ||
		unexplored.Reason != UnexploredNodeType {
		t.Errorf("Unexplored[0] = %#v, want UNIT at (1, 0) for %q", unexplored, UnexploredNodeType)
	}
}

func openTestDB(t *testing.T, nodes []testNode, relations []testRelation) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
CREATE TABLE node (
    node_id TEXT PRIMARY KEY,
    node_type TEXT NOT NULL,
    name TEXT NOT NULL,
	path TEXT,
    parent_id TEXT
);
CREATE TABLE relation (
    relation_id TEXT PRIMARY KEY,
    from_id TEXT NOT NULL,
    to_id TEXT NOT NULL,
    relation_kind TEXT NOT NULL,
    origin TEXT NOT NULL,
    certainty TEXT NOT NULL,
    evidence_json TEXT
);
CREATE INDEX idx_relation_from ON relation(from_id);
CREATE INDEX idx_relation_to ON relation(to_id);`); err != nil {
		t.Fatal(err)
	}
	for _, node := range nodes {
		name := node.name
		if name == "" {
			name = node.id
		}
		if _, err := db.Exec(`INSERT INTO node (node_id, node_type, name, path, parent_id) VALUES (?, ?, ?, ?, ?)`, node.id, node.typeName, name, node.path, node.parentID); err != nil {
			t.Fatal(err)
		}
	}
	for _, current := range relations {
		if _, err := db.Exec(`INSERT INTO relation (
            relation_id, from_id, to_id, relation_kind, origin, certainty, evidence_json
        ) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			current.id, current.fromID, current.toID, current.kind,
			current.origin, current.certainty, current.evidence,
		); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func relation(id, fromID, toID, kind string) testRelation {
	return testRelation{id: id, fromID: fromID, toID: toID, kind: kind, origin: "scheduler", certainty: "declared"}
}

func findConnection(t *testing.T, connections []Connection, fromID, toID string) Connection {
	t.Helper()
	for _, connection := range connections {
		if connection.FromID == fromID && connection.To.ID == toID {
			return connection
		}
	}
	t.Fatalf("connection %s -> %s was not found", fromID, toID)
	return Connection{}
}

func hasPath(connections []Connection, path []string) bool {
	for index := 0; index+1 < len(path); index++ {
		found := false
		for _, connection := range connections {
			if connection.FromID == path[index] && connection.To.ID == path[index+1] {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func relationIDs(relations []Relation) []string {
	ids := make([]string, len(relations))
	for index, relation := range relations {
		ids[index] = relation.ID
	}
	return ids
}

func cyclePaths(cycles []Cycle) [][]string {
	paths := make([][]string, len(cycles))
	for index, cycle := range cycles {
		paths[index] = cycle.Path
	}
	return paths
}

func nodeIDs(nodes []Node) []string {
	ids := make([]string, len(nodes))
	for index, node := range nodes {
		ids[index] = node.ID
	}
	return ids
}

func scopeEntryNodeIDs(entries []ScopeEntry, rootID string) []string {
	ids := make([]string, 0)
	for _, entry := range entries {
		if entry.ScopeRootID == rootID {
			ids = append(ids, entry.Node.ID)
		}
	}
	return ids
}

func findScopeEntry(t *testing.T, entries []ScopeEntry, rootID, nodeID string) ScopeEntry {
	t.Helper()
	for _, entry := range entries {
		if entry.ScopeRootID == rootID && entry.Node.ID == nodeID {
			return entry
		}
	}
	t.Fatalf("scope entry %s -> %s was not found", rootID, nodeID)
	return ScopeEntry{}
}

func visitIDs(visits []Visit) []string {
	ids := make([]string, len(visits))
	for index, visit := range visits {
		ids[index] = visit.Node.ID
	}
	return ids
}

func downstreamIDs(nodes []Downstream) []string {
	ids := make([]string, len(nodes))
	for index, node := range nodes {
		ids[index] = node.Node.ID
	}
	return ids
}

func findDownstream(t *testing.T, nodes []Downstream, id string) Downstream {
	t.Helper()
	for _, node := range nodes {
		if node.Node.ID == id {
			return node
		}
	}
	t.Fatalf("downstream %s was not found", id)
	return Downstream{}
}

func findVisit(t *testing.T, visits []Visit, id string) Visit {
	t.Helper()
	for _, visit := range visits {
		if visit.Node.ID == id {
			return visit
		}
	}
	t.Fatalf("visit %s was not found", id)
	return Visit{}
}

func assertVisitDistance(t *testing.T, visits []Visit, id string, graphDepth, dependencyDistance int) {
	t.Helper()
	visit := findVisit(t, visits, id)
	if visit.GraphDepth != graphDepth || visit.DependencyDistance != dependencyDistance {
		t.Errorf("visit %s distances = (%d, %d), want (%d, %d)", id,
			visit.GraphDepth, visit.DependencyDistance, graphDepth, dependencyDistance)
	}
}

func assertDownstreamDistance(t *testing.T, nodes []Downstream, id string, graphDepth, dependencyDistance int) {
	t.Helper()
	for _, node := range nodes {
		if node.Node.ID != id {
			continue
		}
		if node.GraphDepth != graphDepth || node.DependencyDistance != dependencyDistance {
			t.Errorf("downstream %s distances = (%d, %d), want (%d, %d)", id,
				node.GraphDepth, node.DependencyDistance, graphDepth, dependencyDistance)
		}
		return
	}
	t.Errorf("downstream %s was not found", id)
}

func assertFrontier(t *testing.T, result Result, id string, graphDepth, dependencyDistance int, reason TruncationReason) {
	t.Helper()
	if !result.Truncated || result.TruncationReason != reason {
		t.Errorf("truncation = (%t, %q), want (true, %q)", result.Truncated, result.TruncationReason, reason)
	}
	if !slices.ContainsFunc(result.Frontier, func(frontier Frontier) bool {
		return frontier.Node.ID == id && frontier.GraphDepth == graphDepth &&
			frontier.DependencyDistance == dependencyDistance && frontier.Reason == reason
	}) {
		t.Errorf("Frontier = %#v, want %s at graph depth %d and dependency distance %d for %s",
			result.Frontier, id, graphDepth, dependencyDistance, reason)
	}
}
