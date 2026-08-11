package traversal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
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

func TestTraverseReachesEndOfLongSerialPath(t *testing.T) {
	const edgeCount = 2_001
	nodes := make([]testNode, 0, edgeCount+1)
	relations := make([]testRelation, 0, edgeCount)
	for index := 0; index <= edgeCount; index++ {
		id := fmt.Sprintf("N-%04d", index)
		nodes = append(nodes, testNode{id: id, typeName: "job"})
		if index > 0 {
			relations = append(relations, relation(
				fmt.Sprintf("R-%04d", index),
				fmt.Sprintf("N-%04d", index-1),
				id,
				"precedes",
			))
		}
	}

	result, err := Traverse(context.Background(), openTestDB(t, nodes, relations), "N-0000")
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Nodes[len(result.Nodes)-1].Node.ID; got != "N-2001" {
		t.Fatalf("last reached node = %q, want N-2001", got)
	}
	if got, want := len(result.Nodes), edgeCount+1; got != want {
		t.Errorf("len(Nodes) = %d, want %d", got, want)
	}
	if got, want := result.Stats.ExpandedNodes, edgeCount+1; got != want {
		t.Errorf("ExpandedNodes = %d, want %d", got, want)
	}
	if got, want := result.Stats.RelationRows, edgeCount; got != want {
		t.Errorf("RelationRows = %d, want %d", got, want)
	}
}

func TestTraverseReachesEveryHighFanOutBranch(t *testing.T) {
	const branches = 1_200
	nodes := []testNode{{id: "START", typeName: "job"}}
	relations := make([]testRelation, 0, branches)
	for index := 0; index < branches; index++ {
		id := fmt.Sprintf("BRANCH-%04d", index)
		nodes = append(nodes, testNode{id: id, typeName: "job"})
		relations = append(relations, relation(fmt.Sprintf("R-%04d", index), "START", id, "precedes"))
	}

	result, err := Traverse(context.Background(), openTestDB(t, nodes, relations), "START")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(result.Nodes), branches+1; got != want {
		t.Errorf("len(Nodes) = %d, want %d", got, want)
	}
	if got, want := result.Stats.RelationRows, branches; got != want {
		t.Errorf("RelationRows = %d, want %d", got, want)
	}
}

func TestTraverseExpandsMergedNodeOnce(t *testing.T) {
	nodes := jobs("START", "LEFT", "RIGHT", "MERGED", "END")
	relations := []testRelation{
		relation("1", "START", "LEFT", "precedes"),
		relation("2", "START", "RIGHT", "precedes"),
		relation("3", "LEFT", "MERGED", "precedes"),
		relation("4", "RIGHT", "MERGED", "precedes"),
		relation("5", "MERGED", "END", "precedes"),
	}

	result, err := Traverse(context.Background(), openTestDB(t, nodes, relations), "START")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := result.Stats.ExpandedNodes, 5; got != want {
		t.Errorf("ExpandedNodes = %d, want %d", got, want)
	}
	if got, want := result.Stats.RelationRows, 5; got != want {
		t.Errorf("RelationRows = %d, want %d", got, want)
	}
	if !hasConnection(result.Connections, "MERGED", "END") {
		t.Error("connection MERGED -> END was not returned")
	}
}

func TestTraverseDetectsStronglyConnectedComponentsAndContinuesPastCycle(t *testing.T) {
	const cycleSize = 40
	nodes := jobs("START", "EXIT", "SELF", "X", "Y")
	relations := []testRelation{
		relation("START-C", "START", "C-00", "precedes"),
		relation("START-SELF", "START", "SELF", "precedes"),
		relation("START-X", "START", "X", "precedes"),
		relation("SELF", "SELF", "SELF", "precedes"),
		relation("X-Y", "X", "Y", "precedes"),
		relation("Y-X", "Y", "X", "precedes"),
	}
	largeCycle := make([]string, 0, cycleSize)
	for index := 0; index < cycleSize; index++ {
		id := fmt.Sprintf("C-%02d", index)
		next := fmt.Sprintf("C-%02d", (index+1)%cycleSize)
		nodes = append(nodes, testNode{id: id, typeName: "job"})
		largeCycle = append(largeCycle, id)
		relations = append(relations, relation(fmt.Sprintf("C-R-%02d", index), id, next, "precedes"))
	}
	relations = append(relations, relation("C-EXIT", "C-17", "EXIT", "precedes"))

	result, err := Traverse(context.Background(), openTestDB(t, nodes, relations), "START")
	if err != nil {
		t.Fatal(err)
	}
	wantCycles := []Cycle{
		{NodeIDs: largeCycle},
		{NodeIDs: []string{"SELF"}},
		{NodeIDs: []string{"X", "Y"}},
	}
	if !reflect.DeepEqual(result.Cycles, wantCycles) {
		t.Errorf("Cycles = %#v, want %#v", result.Cycles, wantCycles)
	}
	if findReached(t, result.Nodes, "EXIT").Membership != MembershipDownstream {
		t.Error("branch outside cycle was not explored")
	}
}

func TestTraverseDetectsCycleAcrossScopeAndRelationAndContinuesPastCycle(t *testing.T) {
	network := "NETWORK"
	nodes := []testNode{
		{id: network, typeName: "job_network"},
		{id: "CHILD", typeName: "job", parentID: &network},
		{id: "AFTER", typeName: "job"},
	}
	relations := []testRelation{
		relation("1", "CHILD", network, "precedes"),
		relation("2", "CHILD", "AFTER", "precedes"),
	}

	result, err := Traverse(context.Background(), openTestDB(t, nodes, relations), network)
	if err != nil {
		t.Fatal(err)
	}
	wantCycles := []Cycle{{NodeIDs: []string{"CHILD", network}}}
	if !reflect.DeepEqual(result.Cycles, wantCycles) {
		t.Errorf("Cycles = %#v, want %#v", result.Cycles, wantCycles)
	}
	wantScopeEdges := []ScopeEdge{{ParentID: network, ChildID: "CHILD"}}
	if !reflect.DeepEqual(result.ScopeEdges, wantScopeEdges) {
		t.Errorf("ScopeEdges = %#v, want %#v", result.ScopeEdges, wantScopeEdges)
	}
	if hasConnection(result.Connections, network, "CHILD") {
		t.Errorf("scope edge %s -> CHILD was mixed into Connections", network)
	}
	for _, pair := range [][2]string{{"CHILD", network}, {"CHILD", "AFTER"}} {
		if !hasConnection(result.Connections, pair[0], pair[1]) {
			t.Errorf("connection %s -> %s was not returned", pair[0], pair[1])
		}
	}
	if got := findReached(t, result.Nodes, "AFTER").Membership; got != MembershipDownstream {
		t.Errorf("AFTER membership = %q, want %q", got, MembershipDownstream)
	}
}

func TestFindCyclesDoesNotTreatScopeSelfLoopAsCycle(t *testing.T) {
	cycles, err := findCycles(
		context.Background(),
		nil,
		[]ScopeEdge{{ParentID: "NETWORK", ChildID: "NETWORK"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(cycles) != 0 {
		t.Errorf("Cycles = %#v, want scope self-loop to be ignored", cycles)
	}
}

func TestTraverseTargetJobNetworkExploresOwnRelationsAndAllScope(t *testing.T) {
	root := "ROOT"
	sub := "SUB"
	nodes := []testNode{
		{id: root, typeName: "job_network"},
		{id: sub, typeName: "job_network", parentID: &root},
		{id: "DIRECT", typeName: "job", parentID: &root},
		{id: "NESTED", typeName: "job", parentID: &sub},
		{id: "ROOT-OUT", typeName: "job"},
		{id: "DIRECT-OUT", typeName: "job"},
		{id: "NESTED-OUT", typeName: "job"},
	}
	relations := []testRelation{
		relation("1", root, "ROOT-OUT", "precedes"),
		relation("2", "DIRECT", "DIRECT-OUT", "precedes"),
		relation("3", "NESTED", "NESTED-OUT", "precedes"),
	}

	result, err := Traverse(context.Background(), openTestDB(t, nodes, relations), root)
	if err != nil {
		t.Fatal(err)
	}
	wantEdges := []ScopeEdge{
		{ParentID: root, ChildID: "DIRECT"},
		{ParentID: root, ChildID: sub},
		{ParentID: sub, ChildID: "NESTED"},
	}
	if !reflect.DeepEqual(result.ScopeEdges, wantEdges) {
		t.Errorf("ScopeEdges = %#v, want %#v", result.ScopeEdges, wantEdges)
	}
	for _, pair := range [][2]string{{root, "ROOT-OUT"}, {"DIRECT", "DIRECT-OUT"}, {"NESTED", "NESTED-OUT"}} {
		if !hasConnection(result.Connections, pair[0], pair[1]) {
			t.Errorf("connection %s -> %s was not returned", pair[0], pair[1])
		}
	}
	for _, id := range []string{root, "DIRECT", sub, "NESTED", "ROOT-OUT", "DIRECT-OUT", "NESTED-OUT"} {
		want := MembershipDownstream
		switch id {
		case root:
			want = MembershipTarget
		case "DIRECT", sub, "NESTED":
			want = MembershipContained
		}
		if got := findReached(t, result.Nodes, id).Membership; got != want {
			t.Errorf("membership of %s = %q, want %q", id, got, want)
		}
	}
	for _, edge := range result.ScopeEdges {
		if hasConnection(result.Connections, edge.ParentID, edge.ChildID) {
			t.Errorf("scope edge %s -> %s was mixed into Connections", edge.ParentID, edge.ChildID)
		}
	}
}

func TestTraverseKeepsContainedMembershipWhenDependencyPathsReachTargetScope(t *testing.T) {
	root := "ROOT"
	nodes := []testNode{
		{id: root, typeName: "job_network"},
		{id: "FROM", typeName: "job", parentID: &root},
		{id: "IN-SCOPE", typeName: "job", parentID: &root},
		{id: "RETURNED", typeName: "job", parentID: &root},
		{id: "OUTSIDE", typeName: "job"},
	}
	relations := []testRelation{
		relation("1", "FROM", "IN-SCOPE", "precedes"),
		relation("2", "FROM", "OUTSIDE", "precedes"),
		relation("3", "OUTSIDE", "RETURNED", "precedes"),
	}

	result, err := Traverse(context.Background(), openTestDB(t, nodes, relations), root)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"IN-SCOPE", "RETURNED"} {
		if got := findReached(t, result.Nodes, id).Membership; got != MembershipContained {
			t.Errorf("membership of %s = %q, want %q", id, got, MembershipContained)
		}
	}
	if got := findReached(t, result.Nodes, "OUTSIDE").Membership; got != MembershipDownstream {
		t.Errorf("membership of OUTSIDE = %q, want %q", got, MembershipDownstream)
	}
	for _, pair := range [][2]string{{"FROM", "IN-SCOPE"}, {"FROM", "OUTSIDE"}, {"OUTSIDE", "RETURNED"}} {
		if !hasConnection(result.Connections, pair[0], pair[1]) {
			t.Errorf("connection %s -> %s was not returned", pair[0], pair[1])
		}
	}
}

func TestTraverseExpandsJobNetworkReachedByScopeAndDependencyOnce(t *testing.T) {
	root := "ROOT"
	network := "SHARED-NETWORK"
	peerNetwork := "PEER-NETWORK"
	nodes := []testNode{
		{id: root, typeName: "job_network"},
		{id: "SOURCE", typeName: "job", parentID: &root},
		{id: network, typeName: "job_network", parentID: &root},
		{id: peerNetwork, typeName: "job_network", parentID: &root},
		{id: "INNER", typeName: "job", parentID: &network},
		{id: "PEER-INNER", typeName: "job", parentID: &peerNetwork},
	}
	relations := []testRelation{
		relation("1", "SOURCE", network, "precedes"),
	}

	result, err := Traverse(context.Background(), openTestDB(t, nodes, relations), root)
	if err != nil {
		t.Fatal(err)
	}
	wantEdges := []ScopeEdge{
		{ParentID: peerNetwork, ChildID: "PEER-INNER"},
		{ParentID: root, ChildID: peerNetwork},
		{ParentID: root, ChildID: network},
		{ParentID: root, ChildID: "SOURCE"},
		{ParentID: network, ChildID: "INNER"},
	}
	if !reflect.DeepEqual(result.ScopeEdges, wantEdges) {
		t.Errorf("ScopeEdges = %#v, want %#v", result.ScopeEdges, wantEdges)
	}
	if !hasConnection(result.Connections, "SOURCE", network) {
		t.Errorf("connection SOURCE -> %s was not returned", network)
	}
	if got, want := result.Stats.ScopeQueries, 2; got != want {
		t.Errorf("ScopeQueries = %d, want %d", got, want)
	}
	if got, want := result.Stats.ScopeRows, 5; got != want {
		t.Errorf("ScopeRows = %d, want %d", got, want)
	}
}

func TestTraverseDeeplyNestedJobNetworks(t *testing.T) {
	const depth = 35
	nodes := []testNode{{id: "NET-00", typeName: "job_network"}}
	parent := "NET-00"
	for index := 1; index <= depth; index++ {
		id := fmt.Sprintf("NET-%02d", index)
		parentID := parent
		nodes = append(nodes, testNode{id: id, typeName: "job_network", parentID: &parentID})
		parent = id
	}
	leafParent := parent
	nodes = append(nodes, testNode{id: "LEAF", typeName: "job", parentID: &leafParent})

	result, err := Traverse(context.Background(), openTestDB(t, nodes, nil), "NET-00")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(result.ScopeEdges), depth+1; got != want {
		t.Errorf("len(ScopeEdges) = %d, want %d", got, want)
	}
	if got := findReached(t, result.Nodes, "LEAF").Membership; got != MembershipContained {
		t.Errorf("LEAF membership = %q, want %q", got, MembershipContained)
	}
	if got, want := result.Stats.ScopeQueries, depth+1; got != want {
		t.Errorf("ScopeQueries = %d, want %d", got, want)
	}
	if len(result.Cycles) != 0 {
		t.Errorf("Cycles = %#v, want no cycle from parent-child scope alone", result.Cycles)
	}
}

func TestTraverseReachedJobNetworkExploresScopeAndOutgoingRelations(t *testing.T) {
	network := "NETWORK"
	subnetwork := "SUBNETWORK"
	nodes := []testNode{
		{id: "START", typeName: "job"},
		{id: network, typeName: "job_network"},
		{id: "CHILD", typeName: "job", parentID: &network},
		{id: subnetwork, typeName: "job_network", parentID: &network},
		{id: "INNER", typeName: "job", parentID: &subnetwork},
		{id: "OUT", typeName: "job"},
		{id: "NETWORK-OUT", typeName: "job"},
	}
	relations := []testRelation{
		relation("1", "START", network, "precedes"),
		relation("2", "INNER", "OUT", "precedes"),
		relation("3", network, "NETWORK-OUT", "precedes"),
	}

	result, err := Traverse(context.Background(), openTestDB(t, nodes, relations), "START")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{network, "CHILD", subnetwork, "INNER", "OUT", "NETWORK-OUT"} {
		if got := findReached(t, result.Nodes, id).Membership; got != MembershipDownstream {
			t.Errorf("membership of %s = %q, want %q", id, got, MembershipDownstream)
		}
	}
	if !hasConnection(result.Connections, "INNER", "OUT") || !hasConnection(result.Connections, network, "NETWORK-OUT") {
		t.Errorf("Connections = %#v, want scope and network outgoing relations", result.Connections)
	}
}

func TestTraverseStopsAtNonTraversableNodeType(t *testing.T) {
	nodes := []testNode{
		{id: "START", typeName: "job"},
		{id: "UNIT", typeName: "management_unit"},
		{id: "HIDDEN", typeName: "job"},
	}
	relations := []testRelation{
		relation("1", "START", "UNIT", "precedes"),
		relation("2", "UNIT", "HIDDEN", "precedes"),
	}

	result, err := Traverse(context.Background(), openTestDB(t, nodes, relations), "START")
	if err != nil {
		t.Fatal(err)
	}
	wantEndpoints := []Endpoint{{
		Node:   Node{ID: "UNIT", Type: "management_unit", Name: "UNIT"},
		Reason: EndpointNonTraversableNodeType,
	}}
	if !reflect.DeepEqual(result.Endpoints, wantEndpoints) {
		t.Errorf("Endpoints = %#v, want %#v", result.Endpoints, wantEndpoints)
	}
	if hasReached(result.Nodes, "UNIT") || hasReached(result.Nodes, "HIDDEN") {
		t.Errorf("Nodes = %#v, contains node past non-traversable endpoint", result.Nodes)
	}
	if !hasConnection(result.Connections, "START", "UNIT") || hasConnection(result.Connections, "UNIT", "HIDDEN") {
		t.Errorf("Connections = %#v, want only arrival at UNIT", result.Connections)
	}
}

func TestTraverseFollowsDependencyIntermediateTypes(t *testing.T) {
	nodes := []testNode{
		{id: "START", typeName: "job"},
		{id: "FILE", typeName: "file"},
		{id: "PATTERN", typeName: "file_pattern"},
		{id: "STATUS", typeName: "job_status"},
		{id: "EVENT", typeName: "external_event"},
		{id: "END", typeName: "job"},
	}
	relations := []testRelation{
		relation("1", "START", "FILE", "produces"),
		relation("2", "FILE", "PATTERN", "consumed_by"),
		relation("3", "PATTERN", "STATUS", "observed_by"),
		relation("4", "STATUS", "EVENT", "triggers"),
		relation("5", "EVENT", "END", "triggers"),
	}
	evidence := `[{"source":"definition"}]`
	relations[0].evidence = &evidence

	result, err := Traverse(context.Background(), openTestDB(t, nodes, relations), "START")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"FILE", "PATTERN", "STATUS", "EVENT", "END"} {
		if !hasReached(result.Nodes, id) {
			t.Errorf("node %s was not reached", id)
		}
	}
	connection := findConnection(t, result.Connections, "START", "FILE")
	if got := string(connection.Relations[0].Evidence); got != evidence {
		t.Errorf("Evidence = %s, want %s", got, evidence)
	}
}

func TestTraverseReturnsNoPartialResultOnCancellationOrDatabaseError(t *testing.T) {
	t.Run("canceled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		result, err := Traverse(ctx, openTestDB(t, jobs("START"), nil), "START")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
		if !reflect.DeepEqual(result, Result{}) {
			t.Errorf("result = %#v, want zero result", result)
		}
	})

	t.Run("relation query error", func(t *testing.T) {
		db := openIncompleteTestDB(t)
		result, err := Traverse(context.Background(), db, "START")
		if err == nil {
			t.Fatal("error = nil, want relation query error")
		}
		if !reflect.DeepEqual(result, Result{}) {
			t.Errorf("result = %#v, want zero result", result)
		}
	})
}

func TestTraverseOrderDoesNotDependOnInsertionOrder(t *testing.T) {
	root := "ROOT"
	sub := "SUB"
	nodes := []testNode{
		{id: root, typeName: "job_network"},
		{id: "B", typeName: "job", parentID: &root},
		{id: sub, typeName: "job_network", parentID: &root},
		{id: "A", typeName: "job", parentID: &sub},
		{id: "C", typeName: "job"},
		{id: "D", typeName: "job"},
	}
	relations := []testRelation{
		relation("6", "D", "C", "triggers"),
		relation("2", "A", "C", "precedes"),
		relation("5", "C", "D", "precedes"),
		relation("4", "B", "C", "triggers"),
		relation("3", "A", "C", "triggers"),
		relation("1", root, "D", "precedes"),
	}

	first, err := Traverse(context.Background(), openTestDB(t, nodes, relations), root)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(nodes)
	slices.Reverse(relations)
	second, err := Traverse(context.Background(), openTestDB(t, nodes, relations), root)
	if err != nil {
		t.Fatal(err)
	}
	for name, values := range map[string][2]any{
		"Nodes":       {first.Nodes, second.Nodes},
		"Connections": {first.Connections, second.Connections},
		"ScopeEdges":  {first.ScopeEdges, second.ScopeEdges},
		"Cycles":      {first.Cycles, second.Cycles},
	} {
		if !reflect.DeepEqual(values[0], values[1]) {
			t.Errorf("%s differs by insertion order:\nfirst  = %#v\nsecond = %#v", name, values[0], values[1])
		}
	}
	if got := relationIDs(findConnection(t, first.Connections, "A", "C").Relations); !slices.Equal(got, []string{"2", "3"}) {
		t.Errorf("relation order = %v, want [2 3]", got)
	}
}

func TestTraverseRejectsMissingAndUnsupportedTargets(t *testing.T) {
	db := openTestDB(t, []testNode{{id: "UNIT", typeName: "management_unit"}}, nil)
	if _, err := Traverse(context.Background(), db, "MISSING"); !errors.Is(err, ErrTargetNotFound) {
		t.Errorf("missing target error = %v, want ErrTargetNotFound", err)
	}
	if _, err := Traverse(context.Background(), db, "UNIT"); !errors.Is(err, ErrInvalidTargetType) {
		t.Errorf("unsupported target error = %v, want ErrInvalidTargetType", err)
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
CREATE INDEX idx_node_parent ON node(parent_id);
CREATE INDEX idx_relation_from ON relation(from_id);`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range nodes {
		name := node.name
		if name == "" {
			name = node.id
		}
		if _, err := tx.Exec(
			`INSERT INTO node (node_id, node_type, name, path, parent_id) VALUES (?, ?, ?, ?, ?)`,
			node.id, node.typeName, name, node.path, node.parentID,
		); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	for _, current := range relations {
		if _, err := tx.Exec(`INSERT INTO relation (
            relation_id, from_id, to_id, relation_kind, origin, certainty, evidence_json
        ) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			current.id, current.fromID, current.toID, current.kind,
			current.origin, current.certainty, current.evidence,
		); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return db
}

func openIncompleteTestDB(t *testing.T) *sql.DB {
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
INSERT INTO node (node_id, node_type, name) VALUES ('START', 'job', 'START');`); err != nil {
		t.Fatal(err)
	}
	return db
}

func jobs(ids ...string) []testNode {
	nodes := make([]testNode, len(ids))
	for index, id := range ids {
		nodes[index] = testNode{id: id, typeName: "job"}
	}
	return nodes
}

func relation(id, fromID, toID, kind string) testRelation {
	return testRelation{
		id: id, fromID: fromID, toID: toID, kind: kind,
		origin: "scheduler", certainty: "declared",
	}
}

func hasReached(nodes []Reached, id string) bool {
	return slices.ContainsFunc(nodes, func(reached Reached) bool { return reached.Node.ID == id })
}

func findReached(t *testing.T, nodes []Reached, id string) Reached {
	t.Helper()
	for _, reached := range nodes {
		if reached.Node.ID == id {
			return reached
		}
	}
	t.Fatalf("reached node %q was not found", id)
	return Reached{}
}

func hasConnection(connections []Connection, fromID, toID string) bool {
	return slices.ContainsFunc(connections, func(connection Connection) bool {
		return connection.FromID == fromID && connection.ToID == toID
	})
}

func findConnection(t *testing.T, connections []Connection, fromID, toID string) Connection {
	t.Helper()
	for _, connection := range connections {
		if connection.FromID == fromID && connection.ToID == toID {
			return connection
		}
	}
	t.Fatalf("connection %s -> %s was not found", fromID, toID)
	return Connection{}
}

func relationIDs(relations []Relation) []string {
	ids := make([]string, len(relations))
	for index, relation := range relations {
		ids[index] = relation.ID
	}
	return ids
}
