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
	if got, want := nodeIDs(result.StartNodes), []string{"ENTRY-A", "ENTRY-B"}; !slices.Equal(got, want) {
		t.Errorf("StartNodes = %v, want %v", got, want)
	}
	if result.UsedStartFallback {
		t.Error("UsedStartFallback = true")
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

func TestTraverseJobNetworkFallsBackToContainedJobs(t *testing.T) {
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
	if !result.UsedStartFallback {
		t.Error("UsedStartFallback = false")
	}
	if got, want := nodeIDs(result.StartNodes), []string{"A", "B"}; !slices.Equal(got, want) {
		t.Errorf("fallback StartNodes = %v, want jobs only %v", got, want)
	}
	if got := downstreamIDs(result.Downstream); !slices.Equal(got, []string{"OUT"}) {
		t.Errorf("Downstream = %v, want [OUT]", got)
	}
}

func TestTraverseReportsDepthAndNodeFrontiers(t *testing.T) {
	nodes := []testNode{{id: "A", typeName: "job"}, {id: "B", typeName: "job"}, {id: "C", typeName: "job"}, {id: "D", typeName: "job"}}
	relations := []testRelation{
		relation("1", "A", "B", "precedes"), relation("2", "B", "C", "precedes"), relation("3", "C", "D", "precedes"),
	}
	db := openTestDB(t, nodes, relations)

	depthResult, err := Traverse(context.Background(), db, "A", Limits{MaxDepth: 1})
	if err != nil {
		t.Fatal(err)
	}
	assertFrontier(t, depthResult, "B", 1, TruncationDepthLimit)
	if hasPath(depthResult.Connections, []string{"B", "C"}) {
		t.Error("depth-limited result retained an untraversed connection")
	}

	nodeResult, err := Traverse(context.Background(), db, "A", Limits{MaxVisitedNodes: 2})
	if err != nil {
		t.Fatal(err)
	}
	assertFrontier(t, nodeResult, "C", 2, TruncationNodeLimit)
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

func TestTraverseRejectsMissingAndUnsupportedTargets(t *testing.T) {
	db := openTestDB(t, []testNode{{id: "FILE", typeName: "file"}}, nil)
	if _, err := Traverse(context.Background(), db, "MISSING", Limits{}); !errors.Is(err, ErrTargetNotFound) {
		t.Errorf("missing target error = %v", err)
	}
	if _, err := Traverse(context.Background(), db, "FILE", Limits{}); !errors.Is(err, ErrInvalidTargetType) {
		t.Errorf("file target error = %v", err)
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
		if _, err := db.Exec(`INSERT INTO node (node_id, node_type, name, parent_id) VALUES (?, ?, ?, ?)`, node.id, node.typeName, node.id, node.parentID); err != nil {
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

func assertFrontier(t *testing.T, result Result, id string, depth int, reason TruncationReason) {
	t.Helper()
	if !result.Truncated || result.TruncationReason != reason {
		t.Errorf("truncation = (%t, %q), want (true, %q)", result.Truncated, result.TruncationReason, reason)
	}
	if !slices.ContainsFunc(result.Frontier, func(frontier Frontier) bool {
		return frontier.Node.ID == id && frontier.Depth == depth && frontier.Reason == reason
	}) {
		t.Errorf("Frontier = %#v, want %s at depth %d for %s", result.Frontier, id, depth, reason)
	}
}
