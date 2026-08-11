package importer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"testing"

	"batchscope/internal/limits"
	"batchscope/internal/limitscan"
	"batchscope/internal/pathtree"
	"batchscope/internal/snapshot"
	"batchscope/internal/testsupport/graphgen"
	"batchscope/internal/traversal"
)

type pipelineResult struct {
	Traversal traversal.Result
	Limits    limitscan.Result
	PathTree  pathtree.Result
}

func TestScalePipelineSmall(t *testing.T) {
	assertGeneratedDataset(t, graphgen.Small(), false)
}

func TestCapacityBoundaryPipelineCompletes(t *testing.T) {
	dataset := graphgen.CapacityBoundary(
		limits.MaxSnapshotNodes,
		limits.MaxSnapshotRelations,
		limits.MaxSnapshotLimits,
	)
	visitGeneratedPipelines(t, dataset, false, func(_ graphgen.Expectation, result pipelineResult) {
		assertAllInputLimitsReturned(t, dataset.Nodes, result.Limits)
	})
}

func TestSCCCapacityBoundaryPipeline(t *testing.T) {
	t.Run("accepts exact boundary", func(t *testing.T) {
		dataset := graphgen.DenseSCC(limits.MaxSCCNodes, limits.MaxSCCNodes)
		visitGeneratedPipelines(t, dataset, false, func(_ graphgen.Expectation, result pipelineResult) {
			if len(result.Traversal.Cycles) != 1 || len(result.Traversal.Cycles[0].NodeIDs) != limits.MaxSCCNodes {
				t.Fatalf("cycles = %#v, want one SCC with %d nodes", traversalSCCs(result.Traversal), limits.MaxSCCNodes)
			}
			assertAllInputLimitsReturned(t, dataset.Nodes, result.Limits)
		})
	})

	t.Run("rejects overflow and keeps current generation", func(t *testing.T) {
		workspace := t.TempDir()
		storage := newImporterTestStore(t, filepath.Join(workspace, "data"))
		if _, err := Run(context.Background(), workspace, bytes.NewReader(singleNodeArchive(t, "CURRENT")), storage); err != nil {
			t.Fatalf("initial Run() error = %v", err)
		}

		dataset := graphgen.DenseSCC(limits.MaxSCCNodes+1, limits.MaxSCCNodes+1)
		archive, err := dataset.Archive(false)
		if err != nil {
			t.Fatal(err)
		}
		_, err = Run(context.Background(), workspace, bytes.NewReader(archive), storage)
		var validationErr *snapshot.Error
		if !errors.As(err, &validationErr) || validationErr.Kind != snapshot.ErrorCapacityExceeded ||
			validationErr.File != "relations.ndjson" || validationErr.Pointer != "" {
			t.Fatalf("Run() error = %v, want relations.ndjson capacity_exceeded", err)
		}

		db, generation, release, err := storage.Acquire()
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		if got := queryValueForImporterTest(t, db); got != "CURRENT" || generation.SnapshotID != "CURRENT" {
			t.Fatalf("active generation after SCC capacity failure: value=%q metadata=%#v", got, generation)
		}
	})
}

func TestScalePipelinePathological(t *testing.T) {
	for _, kind := range graphgen.PathologicalCases() {
		t.Run(string(kind), func(t *testing.T) {
			dataset := graphgen.Pathological(kind)
			assertGeneratedDataset(t, dataset, false)
		})
	}
}

func TestScalePipelineInputOrderDoesNotChangeResults(t *testing.T) {
	for _, kind := range []graphgen.PathologicalCase{
		graphgen.HighFanIn,
		graphgen.LargeAndMultipleSCC,
		graphgen.ReachedNetworkWithOutbound,
		graphgen.ManyLimits,
	} {
		t.Run(string(kind), func(t *testing.T) {
			dataset := graphgen.Pathological(kind)
			forward := runGeneratedPipelines(t, dataset, false)
			reversed := runGeneratedPipelines(t, dataset, true)
			if !reflect.DeepEqual(forward, reversed) {
				t.Fatal("pipeline result changed after reversing node and relation input order")
			}
		})
	}
}

func runGeneratedPipelines(t *testing.T, dataset graphgen.Dataset, reverse bool) []pipelineResult {
	t.Helper()
	results := make([]pipelineResult, 0, len(dataset.Expectations))
	visitGeneratedPipelines(t, dataset, reverse, func(_ graphgen.Expectation, result pipelineResult) {
		results = append(results, result)
	})
	return results
}

func assertGeneratedDataset(t *testing.T, dataset graphgen.Dataset, reverse bool) {
	t.Helper()
	visitGeneratedPipelines(t, dataset, reverse, func(expectation graphgen.Expectation, result pipelineResult) {
		t.Run(expectation.TargetID, func(t *testing.T) {
			assertGeneratedExpectation(t, expectation, result)
		})
	})
}

func visitGeneratedPipelines(t *testing.T, dataset graphgen.Dataset, reverse bool, visit func(graphgen.Expectation, pipelineResult)) {
	t.Helper()
	archive, err := dataset.Archive(reverse)
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	storage := newImporterTestStore(t, filepath.Join(workspace, "data"))
	ctx := context.Background()
	if _, err := Run(ctx, workspace, bytes.NewReader(archive), storage); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	db, _, release, err := storage.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	assertCount(t, db, "node", len(dataset.Nodes))
	assertCount(t, db, "relation", len(dataset.Relations))
	assertCount(t, db, "limit_fact", inputLimitCount(dataset.Nodes))

	for _, expectation := range dataset.Expectations {
		reached, err := traversal.Traverse(ctx, db, expectation.TargetID)
		if err != nil {
			t.Fatalf("Traverse(%q) error = %v", expectation.TargetID, err)
		}
		limits, err := limitscan.Scan(ctx, db, reached)
		if err != nil {
			t.Fatalf("Scan(%q) error = %v", expectation.TargetID, err)
		}
		tree, err := pathtree.Build(ctx, reached, limits)
		if err != nil {
			t.Fatalf("Build(%q) error = %v", expectation.TargetID, err)
		}
		visit(expectation, pipelineResult{Traversal: reached, Limits: limits, PathTree: tree})
	}
}

func inputLimitCount(nodes []graphgen.Node) int {
	count := 0
	for _, node := range nodes {
		count += len(node.LimitFacts)
	}
	return count
}

func assertAllInputLimitsReturned(t *testing.T, nodes []graphgen.Node, result limitscan.Result) {
	t.Helper()
	want := make([]string, 0, inputLimitCount(nodes))
	for _, node := range nodes {
		for _, limit := range node.LimitFacts {
			want = append(want, limit.ID)
		}
	}
	sort.Strings(want)

	gotFacts := limitFacts(result)
	got := make([]string, 0, len(gotFacts))
	for id := range gotFacts {
		got = append(got, id)
	}
	sort.Strings(got)
	if !slices.Equal(got, want) {
		t.Fatalf("returned limit IDs differ: got %d, want %d", len(got), len(want))
	}
}

func assertGeneratedExpectation(t *testing.T, want graphgen.Expectation, result pipelineResult) {
	t.Helper()
	if got := reachedNodes(result.Traversal); !reflect.DeepEqual(got, want.ReachedNodes) {
		t.Fatalf("reached nodes differ: got %d, want %d", len(got), len(want.ReachedNodes))
	}
	if got := reachedNodeIDs(result.Traversal); !slices.Equal(got, want.ReachedNodeIDs) {
		t.Fatalf("reached node IDs differ: got %d, want %d", len(got), len(want.ReachedNodeIDs))
	}
	if got := reachedRelations(result.Traversal); !slices.Equal(got, want.ReachedRelations) {
		t.Fatalf("reached relations differ: got %d, want %d", len(got), len(want.ReachedRelations))
	}
	if got := scopeEdges(result.Traversal); !slices.Equal(got, want.ScopeEdges) {
		t.Fatalf("scope edges differ: got %d, want %d", len(got), len(want.ScopeEdges))
	}
	if got := endpoints(result.Traversal); !slices.Equal(got, want.Endpoints) {
		t.Fatalf("endpoints = %#v, want %#v", got, want.Endpoints)
	}
	if got := traversalSCCs(result.Traversal); !reflect.DeepEqual(got, sccNodeIDs(want.SCCs)) {
		t.Fatalf("traversal SCCs = %#v, want %#v", got, sccNodeIDs(want.SCCs))
	}
	if got := limitIDs(result.Limits); !reflect.DeepEqual(got, want.Limits) {
		t.Fatalf("limits = %#v, want %#v", got, want.Limits)
	}
	if got := limitFacts(result.Limits); !reflect.DeepEqual(got, want.LimitFacts) {
		t.Fatalf("limit facts differ: got %d, want %d", len(got), len(want.LimitFacts))
	}
	assertPathTree(t, result, want)
}

func reachedNodes(result traversal.Result) []graphgen.ReachedNode {
	nodes := make([]graphgen.ReachedNode, len(result.Nodes))
	for index, reached := range result.Nodes {
		node := graphgen.ReachedNode{
			ID: reached.Node.ID, Type: reached.Node.Type, Name: reached.Node.Name, Membership: string(reached.Membership),
		}
		if reached.Node.ParentID != nil {
			node.ParentID = *reached.Node.ParentID
			node.HasParent = true
		}
		nodes[index] = node
	}
	return nodes
}

func reachedNodeIDs(result traversal.Result) []string {
	ids := make([]string, len(result.Nodes))
	for index, reached := range result.Nodes {
		ids[index] = reached.Node.ID
	}
	return ids
}

func reachedRelations(result traversal.Result) []graphgen.RelationValue {
	values := make([]graphgen.RelationValue, 0, result.Stats.RelationRows)
	for _, connection := range result.Connections {
		for _, relation := range connection.Relations {
			values = append(values, graphgen.RelationValue{
				FromID: connection.FromID, ToID: connection.ToID, Kind: relation.Kind,
				Origin: relation.Origin, Certainty: relation.Certainty,
			})
		}
	}
	sort.Slice(values, func(i, j int) bool { return relationValueKey(values[i]) < relationValueKey(values[j]) })
	return values
}

func relationValueKey(value graphgen.RelationValue) string {
	return value.FromID + "\x00" + value.ToID + "\x00" + value.Kind + "\x00" + value.Origin + "\x00" + value.Certainty
}

func scopeEdges(result traversal.Result) []graphgen.Edge {
	edges := make([]graphgen.Edge, len(result.ScopeEdges))
	for index, edge := range result.ScopeEdges {
		edges[index] = graphgen.Edge{FromID: edge.ParentID, ToID: edge.ChildID}
	}
	return edges
}

func endpoints(result traversal.Result) []graphgen.Endpoint {
	values := make([]graphgen.Endpoint, len(result.Endpoints))
	for index, endpoint := range result.Endpoints {
		values[index] = graphgen.Endpoint{
			NodeID: endpoint.Node.ID, NodeType: endpoint.Node.Type, NodeName: endpoint.Node.Name, Reason: string(endpoint.Reason),
		}
	}
	return values
}

func limitFacts(result limitscan.Result) map[string]graphgen.LimitExpectation {
	values := make(map[string]graphgen.LimitExpectation)
	for _, limits := range []limitscan.Limits{result.Target, result.Contained, result.Downstream} {
		items := make([]limitscan.Item, 0)
		for _, group := range limits.FinishByGroups {
			items = append(items, group.Items...)
		}
		items = append(items, limits.MaxElapsed.Items...)
		items = append(items, limits.Raw.Items...)
		for _, item := range items {
			expectation := graphgen.LimitExpectation{
				ID: item.Fact.ID, OwnerID: item.LimitOwner.ID, Kind: string(item.Fact.Kind),
				BusinessDayOffset: item.Fact.BusinessDayOffset, DurationSeconds: item.Fact.DurationSeconds,
				Origin: item.Fact.Origin, Certainty: item.Fact.Certainty,
			}
			if item.ScopeRoot != nil {
				expectation.ScopeRootID = item.ScopeRoot.ID
			}
			if item.Fact.LocalTime != nil {
				expectation.LocalTime = *item.Fact.LocalTime
			}
			if item.Fact.TimeZone != nil {
				expectation.TimeZone = *item.Fact.TimeZone
			}
			if item.Fact.SourceText != nil {
				expectation.SourceText = *item.Fact.SourceText
				expectation.HasSourceText = true
			}
			values[expectation.ID] = expectation
		}
	}
	return values
}

func traversalSCCs(result traversal.Result) [][]string {
	values := make([][]string, len(result.Cycles))
	for index, cycle := range result.Cycles {
		values[index] = append([]string(nil), cycle.NodeIDs...)
	}
	return values
}

func sccNodeIDs(expectations []graphgen.SCCExpectation) [][]string {
	values := make([][]string, len(expectations))
	for index, expectation := range expectations {
		values[index] = append([]string(nil), expectation.NodeIDs...)
	}
	return values
}

func limitIDs(result limitscan.Result) graphgen.LimitsExpectation {
	return graphgen.LimitsExpectation{
		Target: limitGroupIDs(result.Target), Contained: limitGroupIDs(result.Contained), Downstream: limitGroupIDs(result.Downstream),
	}
}

func limitGroupIDs(limits limitscan.Limits) graphgen.LimitIDs {
	result := graphgen.LimitIDs{}
	for _, group := range limits.FinishByGroups {
		for _, item := range group.Items {
			result.FinishBy = append(result.FinishBy, item.Fact.ID)
		}
	}
	for _, item := range limits.MaxElapsed.Items {
		result.MaxElapsed = append(result.MaxElapsed, item.Fact.ID)
	}
	for _, item := range limits.Raw.Items {
		result.Raw = append(result.Raw, item.Fact.ID)
	}
	return result
}

func assertPathTree(t *testing.T, result pipelineResult, want graphgen.Expectation) {
	t.Helper()
	ordered := flattenTree(&result.PathTree.Tree)
	if len(ordered) != want.TreeNodeCount {
		t.Errorf("tree node count = %d, want %d", len(ordered), want.TreeNodeCount)
	}
	treeByID := make(map[string]*pathtree.TreeNode, len(ordered))
	for _, node := range ordered {
		if _, duplicate := treeByID[node.TreeNodeID]; duplicate {
			t.Errorf("duplicate tree node ID %q", node.TreeNodeID)
		}
		treeByID[node.TreeNodeID] = node
		if node.ReferenceTo != "" && len(node.Children) != 0 {
			t.Errorf("reference %q has children", node.TreeNodeID)
		}
	}
	for _, node := range ordered {
		if node.ReferenceTo != "" {
			if target := treeByID[node.ReferenceTo]; target == nil || target.Node.ID != node.Node.ID {
				t.Errorf("reference %q does not point to the same node", node.TreeNodeID)
			}
		}
	}

	gotHidden := hiddenConnections(ordered)
	if !slices.Equal(gotHidden, want.HiddenConnections) {
		t.Errorf("hidden connections differ: got %d, want %d", len(gotHidden), len(want.HiddenConnections))
		for index := 0; index < min(len(gotHidden), len(want.HiddenConnections)); index++ {
			if gotHidden[index] != want.HiddenConnections[index] {
				t.Errorf("first hidden connection difference at %d: got %#v, want %#v", index, gotHidden[index], want.HiddenConnections[index])
				break
			}
		}
	}
	if largestHiddenConnectionCount(ordered) > 1_000 {
		assertHiddenNodeIDLimitIndependent(t, ordered)
	}

	gotUncovered := make([]graphgen.UncoveredExpectation, len(result.PathTree.UncoveredRoutes))
	gotReasonCount := make(map[string]int)
	for index, route := range result.PathTree.UncoveredRoutes {
		gotUncovered[index] = graphgen.UncoveredExpectation{NodeID: route.Boundary.ID, Reason: string(route.Reason)}
		gotReasonCount[string(route.Reason)]++
		node := treeByID[route.TreeNodeID]
		if node == nil || node.Node.ID != route.Boundary.ID {
			t.Errorf("uncovered route %q has invalid tree reference %q", route.Boundary.ID, route.TreeNodeID)
		}
	}
	if !slices.Equal(gotUncovered, want.UncoveredRoutes) || !reflect.DeepEqual(gotReasonCount, want.UncoveredReasonCount) {
		t.Errorf("uncovered routes = %#v (%v), want %#v (%v)", gotUncovered, gotReasonCount, want.UncoveredRoutes, want.UncoveredReasonCount)
	}

	gotCycles := make([]graphgen.SCCExpectation, len(result.PathTree.Cycles))
	gotStepCounts := make([]int, len(result.PathTree.Cycles))
	for index, cycle := range result.PathTree.Cycles {
		for _, node := range cycle.Nodes {
			gotCycles[index].NodeIDs = append(gotCycles[index].NodeIDs, node.ID)
		}
		for _, step := range cycle.Route {
			gotCycles[index].Route = append(gotCycles[index].Route, graphgen.Edge{FromID: step.FromID, ToID: step.ToID})
		}
		gotStepCounts[index] = len(cycle.Route)
	}
	cyclesEqual := slices.EqualFunc(gotCycles, want.SCCs, func(left, right graphgen.SCCExpectation) bool {
		return reflect.DeepEqual(left, right)
	})
	if !cyclesEqual || !slices.Equal(gotStepCounts, want.CycleRouteStepCount) {
		t.Errorf("path tree cycles differ: got %#v, want %#v", gotCycles, want.SCCs)
	}

	owners := limitOwnerIDs(result.Limits)
	if len(result.PathTree.LimitReferences) != len(owners) {
		t.Errorf("limit references = %d, want %d owners", len(result.PathTree.LimitReferences), len(owners))
	}
	for _, ownerID := range owners {
		reference, ok := result.PathTree.LimitReferences[ownerID]
		if !ok || treeByID[reference.TreeNodeID] == nil || treeByID[reference.TreeNodeID].Node.ID != ownerID {
			t.Errorf("limit owner %q has invalid tree reference %#v", ownerID, reference)
		}
	}

	gotEdges := treeEdges(&result.PathTree.Tree)
	wantEdges := graphEdges(result.Traversal)
	if !reflect.DeepEqual(gotEdges, wantEdges) {
		t.Errorf("tree connections differ from reachable graph: got %d, want %d", len(gotEdges), len(wantEdges))
	}
	gotConnections := treeConnectionValues(&result.PathTree.Tree)
	wantConnections := graphConnectionValues(result.Traversal)
	if !reflect.DeepEqual(gotConnections, wantConnections) {
		t.Errorf("tree relation and scope details differ: got %d, want %d", len(gotConnections), len(wantConnections))
	}
}

func flattenTree(root *pathtree.TreeNode) []*pathtree.TreeNode {
	result := make([]*pathtree.TreeNode, 0)
	stack := []*pathtree.TreeNode{root}
	for len(stack) > 0 {
		last := len(stack) - 1
		current := stack[last]
		stack = stack[:last]
		result = append(result, current)
		for index := len(current.Children) - 1; index >= 0; index-- {
			stack = append(stack, current.Children[index])
		}
	}
	return result
}

func hiddenConnections(nodes []*pathtree.TreeNode) []graphgen.Edge {
	result := make([]graphgen.Edge, 0)
	for _, node := range nodes {
		for _, connection := range node.HiddenConnections {
			result = append(result, graphgen.Edge{FromID: connection.FromID, ToID: connection.ToID})
		}
	}
	return result
}

func largestHiddenConnectionCount(nodes []*pathtree.TreeNode) int {
	largest := 0
	for _, node := range nodes {
		largest = max(largest, len(node.HiddenConnections))
	}
	return largest
}

func assertHiddenNodeIDLimitIndependent(t *testing.T, nodes []*pathtree.TreeNode) {
	t.Helper()
	found := false
	for _, node := range nodes {
		if len(node.HiddenConnections) <= 1_000 {
			continue
		}
		found = true
		if len(node.HiddenNodeIDs) != 1_000 || !node.HiddenNodeIDsTruncated {
			t.Errorf("hidden IDs = %d, truncated = %t", len(node.HiddenNodeIDs), node.HiddenNodeIDsTruncated)
		}
	}
	if !found {
		t.Error("no compressed node retained more than 1,000 hidden connections")
	}
}

func limitOwnerIDs(result limitscan.Result) []string {
	set := make(map[string]struct{})
	for _, limits := range []limitscan.Limits{result.Target, result.Contained, result.Downstream} {
		for _, group := range limits.FinishByGroups {
			for _, item := range group.Items {
				set[item.LimitOwner.ID] = struct{}{}
			}
		}
		for _, item := range limits.MaxElapsed.Items {
			set[item.LimitOwner.ID] = struct{}{}
		}
		for _, item := range limits.Raw.Items {
			set[item.LimitOwner.ID] = struct{}{}
		}
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func treeEdges(root *pathtree.TreeNode) []string {
	values := make([]string, 0)
	var visit func(*pathtree.TreeNode)
	visit = func(parent *pathtree.TreeNode) {
		for _, child := range parent.Children {
			if len(child.HiddenConnections) != 0 {
				for _, connection := range child.HiddenConnections {
					values = append(values, fmt.Sprintf("%s\x00%s", connection.FromID, connection.ToID))
				}
			} else {
				values = append(values, fmt.Sprintf("%s\x00%s", parent.Node.ID, child.Node.ID))
			}
			visit(child)
		}
	}
	visit(root)
	sort.Strings(values)
	return values
}

func graphEdges(result traversal.Result) []string {
	values := make([]string, 0, len(result.Connections)+len(result.ScopeEdges))
	for _, connection := range result.Connections {
		values = append(values, fmt.Sprintf("%s\x00%s", connection.FromID, connection.ToID))
	}
	for _, edge := range result.ScopeEdges {
		values = append(values, fmt.Sprintf("%s\x00%s", edge.ParentID, edge.ChildID))
	}
	sort.Strings(values)
	return values
}

func treeConnectionValues(root *pathtree.TreeNode) []string {
	values := make([]string, 0)
	var visit func(*pathtree.TreeNode)
	visit = func(parent *pathtree.TreeNode) {
		for _, child := range parent.Children {
			if len(child.HiddenConnections) != 0 {
				for _, connection := range child.HiddenConnections {
					values = append(values, connectionValue(connection.FromID, connection.ToID, connection.ViaScope, connection.ViaRelations))
				}
			} else {
				values = append(values, connectionValue(parent.Node.ID, child.Node.ID, child.ViaScope, child.ViaRelations))
			}
			visit(child)
		}
	}
	visit(root)
	sort.Strings(values)
	return values
}

func graphConnectionValues(result traversal.Result) []string {
	values := make([]string, 0, len(result.Connections)+len(result.ScopeEdges))
	for _, connection := range result.Connections {
		values = append(values, connectionValue(connection.FromID, connection.ToID, false, connection.Relations))
	}
	for _, edge := range result.ScopeEdges {
		values = append(values, connectionValue(edge.ParentID, edge.ChildID, true, nil))
	}
	sort.Strings(values)
	return values
}

func connectionValue(fromID, toID string, scope bool, relations []traversal.Relation) string {
	if scope {
		return "scope\x00" + fromID + "\x00" + toID
	}
	value := "relation\x00" + fromID + "\x00" + toID
	for _, relation := range relations {
		value += "\x00" + relation.ID
	}
	return value
}
