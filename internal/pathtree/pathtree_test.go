package pathtree

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"batchscope/internal/limitscan"
	"batchscope/internal/traversal"
)

func TestBuildAddsAPathReferenceForEveryLimitOwner(t *testing.T) {
	result := traversal.Result{
		Target: node("ROOT", "job_network"),
		Nodes: []traversal.Reached{
			reached("ROOT", "job_network"),
			reached("CONTAINED", "job"),
			reached("DOWNSTREAM", "job"),
		},
		ScopeEdges: []traversal.ScopeEdge{{ParentID: "ROOT", ChildID: "CONTAINED"}},
		Connections: []traversal.Connection{{
			FromID: "CONTAINED", ToID: "DOWNSTREAM",
			Relations: []traversal.Relation{
				relation("R-2", "triggers", "confirmed"),
				{ID: "R-1", Kind: "precedes", Origin: "scheduler", Certainty: "declared", Evidence: json.RawMessage(`[{"source":"definition"}]`)},
			},
		}},
	}
	limits := limitscan.Result{
		Contained: limitscan.Limits{Raw: limitGroup(
			limit("L-CONTAINED", "CONTAINED"),
		)},
		Downstream: limitscan.Limits{
			Raw: limitGroup(limit("L-DOWNSTREAM-RAW", "DOWNSTREAM")),
			MaxElapsed: limitscan.Group{Total: 1, Items: []limitscan.Item{
				limit("L-DOWNSTREAM-ELAPSED", "DOWNSTREAM"),
			}},
		},
	}

	got, err := Build(context.Background(), result, limits)
	if err != nil {
		t.Fatal(err)
	}
	for _, ownerID := range []string{"CONTAINED", "DOWNSTREAM"} {
		reference, ok := got.LimitReferences[ownerID]
		if !ok || reference.TreeNodeID == "" {
			t.Errorf("LimitReferences[%q] = %#v, want a tree node", ownerID, reference)
			continue
		}
		if treeNode := findTreeNodeByTreeID(&got.Tree, reference.TreeNodeID); treeNode == nil || treeNode.Node.ID != ownerID {
			t.Errorf("LimitReferences[%q] points to %#v", ownerID, treeNode)
		}
	}
	contained := findCanonicalTreeNode(t, &got.Tree, got.LimitReferences["CONTAINED"].TreeNodeID)
	if !contained.ViaScope || len(contained.ViaRelations) != 0 {
		t.Errorf("contained connection = scope %v, relations %#v, want a distinct scope transition", contained.ViaScope, contained.ViaRelations)
	}
	downstream := findCanonicalTreeNode(t, &got.Tree, got.LimitReferences["DOWNSTREAM"].TreeNodeID)
	if gotIDs := relationIDs(downstream.ViaRelations); !slices.Equal(gotIDs, []string{"R-1", "R-2"}) {
		t.Errorf("ViaRelations IDs = %v, want aggregated [R-1 R-2]", gotIDs)
	}
	if string(downstream.ViaRelations[0].Evidence) != `[{"source":"definition"}]` {
		t.Errorf("Evidence = %s, want retained data", downstream.ViaRelations[0].Evidence)
	}
}

func TestBuildIsDeterministicForReorderedInput(t *testing.T) {
	result := traversal.Result{
		Target: node("START", "job"),
		Nodes: []traversal.Reached{
			reached("START", "job"), reached("A", "job"), reached("B", "job"),
			reached("MERGE", "job"), reached("END", "job"),
		},
		Connections: []traversal.Connection{
			connection("START", "B", relation("R-2", "precedes", "declared")),
			connection("B", "MERGE", relation("R-4", "precedes", "declared")),
			connection("START", "A", relation("R-1", "precedes", "declared")),
			connection("A", "MERGE", relation("R-3", "precedes", "declared")),
			connection("MERGE", "END", relation("R-5", "precedes", "declared")),
		},
	}
	limits := limitscan.Result{Downstream: limitscan.Limits{Raw: limitGroup(limit("L-END", "END"))}}

	first, err := Build(context.Background(), result, limits)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(result.Nodes)
	slices.Reverse(result.Connections)
	for index := range result.Connections {
		slices.Reverse(result.Connections[index].Relations)
	}
	second, err := Build(context.Background(), result, limits)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("result differs after input reordering:\nfirst  = %#v\nsecond = %#v", first, second)
	}
	if got := first.LimitReferences["END"].AlternatePathCount; got != 1 {
		t.Errorf("END AlternatePathCount = %d, want propagated alternate route count 1", got)
	}
	var merge *TreeNode
	walkTree(&first.Tree, func(current *TreeNode) {
		if current.Node.ID == "MERGE" && current.ReferenceType == "" {
			merge = current
		}
	})
	if merge == nil || !slices.Equal(merge.HiddenNodeIDs, []string{"A"}) {
		t.Errorf("representative MERGE path = %#v, want lexically first START -> A -> MERGE", merge)
	}
}

func TestBuildPrefersEntirelyConfirmedRepresentativePath(t *testing.T) {
	result := traversal.Result{
		Target: node("START", "job"),
		Nodes: []traversal.Reached{
			reached("START", "job"), reached("A", "job"), reached("LIMIT", "job"),
		},
		Connections: []traversal.Connection{
			connection("START", "LIMIT", relation("R-DIRECT", "precedes", "candidate")),
			connection("START", "A", relation("R-A", "precedes", "declared")),
			connection("A", "LIMIT", relation("R-LIMIT", "precedes", "confirmed")),
		},
	}
	limits := limitscan.Result{Downstream: limitscan.Limits{Raw: limitGroup(limit("L", "LIMIT"))}}

	got, err := Build(context.Background(), result, limits)
	if err != nil {
		t.Fatal(err)
	}
	limitNode := findCanonicalTreeNode(t, &got.Tree, got.LimitReferences["LIMIT"].TreeNodeID)
	if limitNode.GraphDepth != 2 || limitNode.DependencyDistance != 2 {
		t.Errorf("representative distance = graph %d, dependency %d, want confirmed two-relation path", limitNode.GraphDepth, limitNode.DependencyDistance)
	}
	if limitNode.ConfirmedDependencyDistance == nil || *limitNode.ConfirmedDependencyDistance != 2 {
		t.Errorf("ConfirmedDependencyDistance = %#v, want 2", limitNode.ConfirmedDependencyDistance)
	}
	if !slices.Equal(limitNode.HiddenNodeIDs, []string{"A"}) {
		t.Errorf("HiddenNodeIDs = %v, want confirmed intermediate A", limitNode.HiddenNodeIDs)
	}
}

func TestBuildKeepsDescendantRepresentativeOccurrenceExpanded(t *testing.T) {
	result := traversal.Result{
		Target: node("START", "job"),
		Nodes: []traversal.Reached{
			reached("START", "job"), reached("P", "job"), reached("X", "job"), reached("Y", "job"),
		},
		Connections: []traversal.Connection{
			connection("START", "X", relation("R-CAND", "precedes", "candidate")),
			connection("START", "P", relation("R-P", "precedes", "declared")),
			connection("P", "X", relation("R-PX", "precedes", "declared")),
			connection("X", "Y", relation("R-XY", "precedes", "candidate")),
		},
	}
	limits := limitscan.Result{Downstream: limitscan.Limits{Raw: limitGroup(limit("L-Y", "Y"))}}

	got, err := Build(context.Background(), result, limits)
	if err != nil {
		t.Fatal(err)
	}
	walkTree(&got.Tree, func(current *TreeNode) {
		if current.ReferenceType != "" && len(current.Children) != 0 {
			t.Errorf("reference node %s (%s) has %d children, want a leaf", current.TreeNodeID, current.Node.ID, len(current.Children))
		}
	})
	xOccurrences := make([]*TreeNode, 0, 2)
	walkTree(&got.Tree, func(current *TreeNode) {
		if current.Node.ID == "X" {
			xOccurrences = append(xOccurrences, current)
		}
	})
	if len(xOccurrences) != 2 {
		t.Fatalf("X occurrence count = %d, want 2", len(xOccurrences))
	}
	var expandedX, referenceX *TreeNode
	for _, occurrence := range xOccurrences {
		if len(occurrence.Children) == 0 {
			referenceX = occurrence
		} else {
			expandedX = occurrence
		}
	}
	if expandedX == nil || expandedX.ReferenceType != "" {
		t.Errorf("expanded X = %#v, want a non-reference node with children", expandedX)
	}
	if referenceX == nil || referenceX.ReferenceType != ReferenceShared {
		t.Errorf("leaf X = %#v, want a shared reference leaf", referenceX)
	} else if expandedX != nil && referenceX.ReferenceTo != expandedX.TreeNodeID {
		t.Errorf("leaf X ReferenceTo = %q, want expanded X %q", referenceX.ReferenceTo, expandedX.TreeNodeID)
	}
	yReference := got.LimitReferences["Y"]
	if yReference.TreeNodeID == "" {
		t.Fatal("LimitReferences[Y] has no tree node")
	}
	if !treeNodeReachableWithoutReference(&got.Tree, yReference.TreeNodeID) {
		t.Errorf("LimitReferences[Y] target %q is not reachable without traversing a reference node", yReference.TreeNodeID)
	}
	if yReference.AlternatePathCount != 1 {
		t.Errorf("Y AlternatePathCount = %d, want 1", yReference.AlternatePathCount)
	}
}

func TestBuildDistinguishesMergeFromCycleAndBuildsOneCycleRoute(t *testing.T) {
	result := traversal.Result{
		Target: node("START", "job"),
		Nodes: []traversal.Reached{
			reached("START", "job"), reached("LEFT", "job"), reached("RIGHT", "job"),
			reached("MERGE", "job"), reached("CYCLE-A", "job"), reached("CYCLE-B", "job"), reached("CYCLE-C", "job"),
		},
		Connections: []traversal.Connection{
			connection("START", "LEFT", relation("R-1", "precedes", "declared")),
			connection("START", "RIGHT", relation("R-2", "precedes", "declared")),
			connection("LEFT", "MERGE", relation("R-3", "precedes", "declared")),
			connection("RIGHT", "MERGE", relation("R-4", "precedes", "declared")),
			connection("MERGE", "CYCLE-A", relation("R-5", "precedes", "declared")),
			connection("CYCLE-A", "CYCLE-B", relation("R-6", "precedes", "declared")),
			connection("CYCLE-B", "CYCLE-A", relation("R-7", "precedes", "candidate")),
			connection("CYCLE-A", "CYCLE-C", relation("R-8", "precedes", "declared")),
			connection("CYCLE-C", "CYCLE-A", relation("R-9", "precedes", "declared")),
		},
		Cycles: []traversal.Cycle{{NodeIDs: []string{"CYCLE-C", "CYCLE-B", "CYCLE-A"}}},
	}
	limits := limitscan.Result{Downstream: limitscan.Limits{Raw: limitGroup(limit("L-MERGE", "MERGE"))}}

	got, err := Build(context.Background(), result, limits)
	if err != nil {
		t.Fatal(err)
	}
	shared, cycle := 0, 0
	walkTree(&got.Tree, func(current *TreeNode) {
		switch current.ReferenceType {
		case ReferenceShared:
			shared++
			if current.CycleID != "" {
				t.Errorf("shared reference %s has cycle ID %q", current.TreeNodeID, current.CycleID)
			}
		case ReferenceCycle:
			cycle++
			if current.CycleID != "cycle-1" {
				t.Errorf("cycle reference %s CycleID = %q, want cycle-1", current.TreeNodeID, current.CycleID)
			}
			if len(current.Children) != 0 {
				t.Errorf("cycle reference %s has %d children, want a leaf", current.TreeNodeID, len(current.Children))
			}
		}
		if current.ReferenceType != "" && findTreeNodeByTreeID(&got.Tree, current.ReferenceTo) == nil {
			t.Errorf("reference %s points to missing tree node %q", current.TreeNodeID, current.ReferenceTo)
		}
	})
	if shared == 0 || cycle == 0 {
		t.Errorf("reference counts = shared %d, cycle %d, want both kinds", shared, cycle)
	}
	if len(got.Cycles) != 1 {
		t.Fatalf("Cycles = %#v, want one SCC", got.Cycles)
	}
	gotCycle := got.Cycles[0]
	if ids := traversalNodeIDs(gotCycle.Nodes); !slices.Equal(ids, []string{"CYCLE-A", "CYCLE-B", "CYCLE-C"}) {
		t.Errorf("cycle nodes = %v, want sorted SCC nodes", ids)
	}
	if len(gotCycle.Route) != 4 || gotCycle.Route[0].FromID != "CYCLE-A" || gotCycle.Route[len(gotCycle.Route)-1].ToID != "CYCLE-A" {
		t.Errorf("cycle route = %#v, want one closed round covering the three-node SCC from CYCLE-A", gotCycle.Route)
	}
	if !gotCycle.ContainsUncertainRelation {
		t.Error("ContainsUncertainRelation = false, want candidate relation in SCC")
	}
}

func TestBuildCompressesLongSerialPathWithoutLosingLimitReference(t *testing.T) {
	const hiddenJobs = 1_001
	nodes := make([]traversal.Reached, 0, hiddenJobs+2)
	connections := make([]traversal.Connection, 0, hiddenJobs+1)
	nodes = append(nodes, reached("START", "job"))
	previous := "START"
	for index := 0; index < hiddenJobs; index++ {
		id := fmt.Sprintf("HIDDEN-%04d", index)
		nodes = append(nodes, reached(id, "job"))
		connections = append(connections, connection(previous, id, relation(fmt.Sprintf("R-%04d", index), "precedes", "declared")))
		previous = id
	}
	nodes = append(nodes, reached("LIMIT", "job"))
	connections = append(connections, connection(previous, "LIMIT", relation("R-LIMIT", "precedes", "declared")))
	result := traversal.Result{Target: node("START", "job"), Nodes: nodes, Connections: connections}
	limits := limitscan.Result{Downstream: limitscan.Limits{Raw: limitGroup(limit("L", "LIMIT"))}}

	got, err := Build(context.Background(), result, limits)
	if err != nil {
		t.Fatal(err)
	}
	reference := got.LimitReferences["LIMIT"]
	if reference.TreeNodeID == "" {
		t.Fatal("LIMIT has no tree reference")
	}
	limitNode := findCanonicalTreeNode(t, &got.Tree, reference.TreeNodeID)
	if limitNode.Node.ID != "LIMIT" {
		t.Errorf("limit reference points to %q, want LIMIT", limitNode.Node.ID)
	}
	if limitNode.HiddenJobCount != hiddenJobs {
		t.Errorf("HiddenJobCount = %d, want %d", limitNode.HiddenJobCount, hiddenJobs)
	}
	if len(limitNode.HiddenNodeIDs) != 1_000 {
		t.Errorf("len(HiddenNodeIDs) = %d, want 1000", len(limitNode.HiddenNodeIDs))
	} else if first, last := limitNode.HiddenNodeIDs[0], limitNode.HiddenNodeIDs[999]; first != "HIDDEN-0000" || last != "HIDDEN-0999" {
		t.Errorf("HiddenNodeIDs range = %q ... %q, want HIDDEN-0000 ... HIDDEN-0999", first, last)
	}
	if !limitNode.HiddenNodeIDsTruncated {
		t.Error("HiddenNodeIDsTruncated = false, want true")
	}
	if got.Tree.TreeNodeID == reference.TreeNodeID {
		t.Error("compression incorrectly removed the limit owner into the root")
	}
}

func TestBuildSeparatesLimitFreeTerminalAndNonTraversableEndpoint(t *testing.T) {
	result := traversal.Result{
		Target: node("START", "job"),
		Nodes:  []traversal.Reached{reached("START", "job"), reached("END", "job")},
		Connections: []traversal.Connection{
			connection("START", "END", relation("R-END", "precedes", "declared")),
			connection("START", "UNIT", relation("R-UNIT", "precedes", "declared")),
		},
		Endpoints: []traversal.Endpoint{{
			Node: node("UNIT", "management_unit"), Reason: traversal.EndpointNonTraversableNodeType,
		}},
	}

	got, err := Build(context.Background(), result, limitscan.Result{})
	if err != nil {
		t.Fatal(err)
	}
	reasons := make(map[string]UncoveredReason)
	for _, route := range got.UncoveredRoutes {
		reasons[route.Boundary.ID] = route.Reason
		if route.TreeNodeID == "" {
			t.Errorf("uncovered route %s has no tree reference", route.Boundary.ID)
		}
	}
	if reasons["END"] != UncoveredTerminal {
		t.Errorf("END reason = %q, want %q", reasons["END"], UncoveredTerminal)
	}
	if reasons["UNIT"] != UncoveredNonTraversableType {
		t.Errorf("UNIT reason = %q, want %q", reasons["UNIT"], UncoveredNonTraversableType)
	}
}

func TestBuildReturnsNoPartialResultWhenContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := Build(ctx, traversal.Result{}, limitscan.Result{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if !reflect.DeepEqual(got, Result{}) {
		t.Errorf("result = %#v, want zero result", got)
	}
}

func node(id, nodeType string) traversal.Node {
	return traversal.Node{ID: id, Type: nodeType, Name: id}
}

func reached(id, nodeType string) traversal.Reached {
	membership := traversal.MembershipDownstream
	if id == "START" || id == "ROOT" {
		membership = traversal.MembershipTarget
	}
	return traversal.Reached{Node: node(id, nodeType), Membership: membership}
}

func relation(id, kind, certainty string) traversal.Relation {
	return traversal.Relation{ID: id, Kind: kind, Origin: "scheduler", Certainty: certainty}
}

func connection(fromID, toID string, relations ...traversal.Relation) traversal.Connection {
	return traversal.Connection{FromID: fromID, ToID: toID, Relations: relations}
}

func limit(id, ownerID string) limitscan.Item {
	return limitscan.Item{
		LimitOwner: limitscan.Node{ID: ownerID, Type: "job", Name: ownerID},
		Fact:       limitscan.Fact{ID: id, Kind: limitscan.KindRaw, Origin: "manual", Certainty: "declared"},
	}
}

func limitGroup(items ...limitscan.Item) limitscan.Group {
	return limitscan.Group{Total: len(items), Items: items}
}

func walkTree(root *TreeNode, visit func(*TreeNode)) {
	stack := []*TreeNode{root}
	for len(stack) > 0 {
		last := len(stack) - 1
		current := stack[last]
		stack = stack[:last]
		visit(current)
		stack = append(stack, current.Children...)
	}
}

func findTreeNodeByTreeID(root *TreeNode, treeNodeID string) *TreeNode {
	var found *TreeNode
	walkTree(root, func(current *TreeNode) {
		if current.TreeNodeID == treeNodeID {
			found = current
		}
	})
	return found
}

func findCanonicalTreeNode(t *testing.T, root *TreeNode, treeNodeID string) *TreeNode {
	t.Helper()
	result := findTreeNodeByTreeID(root, treeNodeID)
	if result == nil {
		t.Fatalf("tree node %q was not found", treeNodeID)
	}
	return result
}

func treeNodeReachableWithoutReference(root *TreeNode, treeNodeID string) bool {
	stack := []*TreeNode{root}
	for len(stack) > 0 {
		last := len(stack) - 1
		current := stack[last]
		stack = stack[:last]
		if current.ReferenceType != "" {
			continue
		}
		if current.TreeNodeID == treeNodeID {
			return true
		}
		stack = append(stack, current.Children...)
	}
	return false
}

func relationIDs(relations []traversal.Relation) []string {
	ids := make([]string, len(relations))
	for index, relation := range relations {
		ids[index] = relation.ID
	}
	return ids
}

func traversalNodeIDs(nodes []traversal.Node) []string {
	ids := make([]string, len(nodes))
	for index, current := range nodes {
		ids[index] = current.ID
	}
	return ids
}
