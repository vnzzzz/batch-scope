// Package pathtree は、後続リミットと未説明の到達地点を説明する経路ツリーを生成する。
package pathtree

import (
	"container/heap"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"

	"batchscope/internal/limitscan"
	"batchscope/internal/traversal"
)

const maxHiddenNodeIDs = 1_000

// ReferenceType は、既出ノードを再展開しない理由を表す。
type ReferenceType string

const (
	ReferenceShared ReferenceType = "shared"
	ReferenceCycle  ReferenceType = "cycle"
)

// UncoveredReason は、返却リミットだけでは経路を説明できない理由を表す。
type UncoveredReason string

const (
	UncoveredTerminal           UncoveredReason = "terminal_without_limit"
	UncoveredCycle              UncoveredReason = "cycle_without_limit"
	UncoveredNonTraversableType UncoveredReason = "non_traversable_node_type"
)

// TreeNode は、経路ツリー上の一地点を表す。
// ViaScopeがtrueのとき、親からの接続は依存relationではなくscope遷移である。
type TreeNode struct {
	TreeNodeID                  string
	Node                        traversal.Node
	ViaRelations                []traversal.Relation
	ViaScope                    bool
	Children                    []*TreeNode
	ReferenceType               ReferenceType
	ReferenceTo                 string
	CycleID                     string
	AlternatePathCount          int
	DependencyDistance          int
	GraphDepth                  int
	ConfirmedDependencyDistance *int
	HiddenJobCount              int
	HiddenNodeIDs               []string
	HiddenNodeIDsTruncated      bool
}

// LimitReference は、同じ設定先にある全リミットが共有する経路参照を表す。
type LimitReference struct {
	TreeNodeID         string
	AlternatePathCount int
}

// CycleStep は、SCCを一周する表示経路の一辺を表す。
// ViaScopeがtrueの辺はscope遷移であり、ViaRelationsへ混在させない。
type CycleStep struct {
	FromID       string
	ToID         string
	ViaRelations []traversal.Relation
	ViaScope     bool
}

// Cycle は、一つのSCCと、その中で選んだ一周分の表示経路を表す。
type Cycle struct {
	CycleID                   string
	Nodes                     []traversal.Node
	Route                     []CycleStep
	ContainsImplicitRelation  bool
	ContainsUncertainRelation bool
}

// UncoveredRoute は、リミットを持たない終端、循環、探索対象外の端点を表す。
type UncoveredRoute struct {
	Boundary   traversal.Node
	Reason     UncoveredReason
	TreeNodeID string
	CycleID    string
}

// Result は、リミットから参照する経路ツリーと、循環および未説明経路を保持する。
// LimitReferencesはリミット設定先ノードIDをキーにし、同じ経路をリミットごとに複製しない。
type Result struct {
	Tree            TreeNode
	LimitReferences map[string]LimitReference
	Cycles          []Cycle
	UncoveredRoutes []UncoveredRoute
}

type edgeKind uint8

const (
	edgeRelation edgeKind = iota
	edgeScope
)

type graphEdge struct {
	fromID    string
	toID      string
	kind      edgeKind
	relations []traversal.Relation
	key       string
}

func (edge graphEdge) confirmed() bool {
	if edge.kind == edgeScope {
		return true
	}
	for _, relation := range edge.relations {
		if relation.Certainty == "declared" || relation.Certainty == "confirmed" {
			return true
		}
	}
	return false
}

type graph struct {
	nodes     map[string]traversal.Node
	reached   map[string]struct{}
	endpoints map[string]traversal.EndpointReason
	outgoing  map[string][]graphEdge
	incoming  map[string][]graphEdge
	edges     []graphEdge
}

type path struct {
	nodeID             string
	dependencyDistance int
	graphDepth         int
	hopCount           int
	relationIDCount    int
	previous           *path
	via                graphEdge
	nodeIDs            []string
	relationIDs        []string
}

type pathQueue []*path

func (queue pathQueue) Len() int { return len(queue) }

func (queue pathQueue) Less(i, j int) bool { return lessPath(queue[i], queue[j]) }

func (queue pathQueue) Swap(i, j int) { queue[i], queue[j] = queue[j], queue[i] }

func (queue *pathQueue) Push(value any) { *queue = append(*queue, value.(*path)) }

func (queue *pathQueue) Pop() any {
	old := *queue
	last := len(old) - 1
	value := old[last]
	*queue = old[:last]
	return value
}

type treeWorkNode struct {
	value           *TreeNode
	children        []*treeWorkNode
	viaKey          string
	referenceTarget *treeWorkNode
	hiddenNodeCount int
}

type buildState struct {
	ctx                   context.Context
	targetID              string
	graph                 graph
	allPaths              map[string]*path
	confirmedPaths        map[string]*path
	selectedPaths         map[string]*path
	alternatePathCounts   map[string]int
	cycleByNode           map[string]string
	cycles                []Cycle
	limitOwners           map[string]limitscan.Node
	uncovered             []UncoveredRoute
	root                  *treeWorkNode
	referenceTargets      map[string]*treeWorkNode
	selectedPathTargets   map[string]*treeWorkNode
	occurrences           map[string][]*treeWorkNode
	treeByPath            map[*path]*treeWorkNode
	limitReferenceTargets map[string]*treeWorkNode
}

// Build は、到達可能部分グラフと全リミットから、共有可能な説明用経路を生成する。
// キャンセルを含む失敗時は、呼出側が部分結果を全件と取り違えないようゼロ値を返す。
func Build(ctx context.Context, traversalResult traversal.Result, limitResult limitscan.Result) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	state := buildState{
		ctx:                   ctx,
		limitOwners:           make(map[string]limitscan.Node),
		referenceTargets:      make(map[string]*treeWorkNode),
		selectedPathTargets:   make(map[string]*treeWorkNode),
		occurrences:           make(map[string][]*treeWorkNode),
		treeByPath:            make(map[*path]*treeWorkNode),
		limitReferenceTargets: make(map[string]*treeWorkNode),
	}
	if err := state.prepare(traversalResult, limitResult); err != nil {
		return Result{}, err
	}
	if err := state.buildTree(); err != nil {
		return Result{}, err
	}
	result, err := state.finish()
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

func (state *buildState) prepare(traversalResult traversal.Result, limitResult limitscan.Result) error {
	currentGraph, err := prepareGraph(state.ctx, traversalResult)
	if err != nil {
		return err
	}
	state.graph = currentGraph
	state.targetID = traversalResult.Target.ID
	if err := state.collectLimitOwners(limitResult); err != nil {
		return err
	}

	state.allPaths, err = shortestPaths(state.ctx, currentGraph, traversalResult.Target.ID, false)
	if err != nil {
		return err
	}
	state.confirmedPaths, err = shortestPaths(state.ctx, currentGraph, traversalResult.Target.ID, true)
	if err != nil {
		return err
	}
	// 確定経路の存在は距離より先に比較するため、全relationの最短経路とは別に選択結果を保持する。
	state.selectedPaths = make(map[string]*path, len(state.allPaths))
	for id, allPath := range state.allPaths {
		if err := state.ctx.Err(); err != nil {
			return err
		}
		if confirmedPath, ok := state.confirmedPaths[id]; ok {
			state.selectedPaths[id] = confirmedPath
		} else {
			state.selectedPaths[id] = allPath
		}
	}
	if len(state.selectedPaths) != len(currentGraph.nodes) {
		return errors.New("pathtree graph contains a node unreachable from target")
	}

	state.cycles, state.cycleByNode, err = buildCycles(state.ctx, traversalResult.Cycles, currentGraph)
	if err != nil {
		return err
	}
	state.alternatePathCounts, err = countAlternatePaths(state.ctx, currentGraph, state.cycleByNode, traversalResult.Target.ID)
	if err != nil {
		return err
	}
	state.uncovered, err = state.findUncoveredRoutes()
	return err
}

func prepareGraph(ctx context.Context, result traversal.Result) (graph, error) {
	if result.Target.ID == "" {
		return graph{}, errors.New("pathtree target ID is empty")
	}
	current := graph{
		nodes:     make(map[string]traversal.Node, len(result.Nodes)+len(result.Endpoints)),
		reached:   make(map[string]struct{}, len(result.Nodes)),
		endpoints: make(map[string]traversal.EndpointReason, len(result.Endpoints)),
		outgoing:  make(map[string][]graphEdge),
		incoming:  make(map[string][]graphEdge),
	}
	for _, reached := range result.Nodes {
		if err := ctx.Err(); err != nil {
			return graph{}, err
		}
		if reached.Node.ID == "" {
			return graph{}, errors.New("pathtree traversal node ID is empty")
		}
		if existing, ok := current.nodes[reached.Node.ID]; ok && !sameTraversalNode(existing, reached.Node) {
			return graph{}, fmt.Errorf("pathtree node %q has inconsistent data", reached.Node.ID)
		}
		current.nodes[reached.Node.ID] = reached.Node
		current.reached[reached.Node.ID] = struct{}{}
	}
	target, ok := current.nodes[result.Target.ID]
	if !ok || !sameTraversalNode(target, result.Target) {
		return graph{}, fmt.Errorf("pathtree target %q is missing from traversal nodes", result.Target.ID)
	}
	for _, endpoint := range result.Endpoints {
		if err := ctx.Err(); err != nil {
			return graph{}, err
		}
		if endpoint.Node.ID == "" {
			return graph{}, errors.New("pathtree endpoint ID is empty")
		}
		if endpoint.Reason != traversal.EndpointNonTraversableNodeType {
			return graph{}, fmt.Errorf("pathtree endpoint %q has unsupported reason %q", endpoint.Node.ID, endpoint.Reason)
		}
		if _, reached := current.reached[endpoint.Node.ID]; reached {
			return graph{}, fmt.Errorf("pathtree endpoint %q is also a reached node", endpoint.Node.ID)
		}
		if existing, ok := current.nodes[endpoint.Node.ID]; ok && !sameTraversalNode(existing, endpoint.Node) {
			return graph{}, fmt.Errorf("pathtree endpoint %q has inconsistent data", endpoint.Node.ID)
		}
		current.nodes[endpoint.Node.ID] = endpoint.Node
		current.endpoints[endpoint.Node.ID] = endpoint.Reason
	}

	edgeKeys := make(map[string]struct{}, len(result.Connections)+len(result.ScopeEdges))
	connectionPairs := make(map[string]struct{}, len(result.Connections))
	for _, connection := range result.Connections {
		if err := ctx.Err(); err != nil {
			return graph{}, err
		}
		if _, ok := current.reached[connection.FromID]; !ok {
			return graph{}, fmt.Errorf("pathtree connection source %q is not a reached node", connection.FromID)
		}
		if _, ok := current.nodes[connection.ToID]; !ok {
			return graph{}, fmt.Errorf("pathtree connection destination %q is missing", connection.ToID)
		}
		if len(connection.Relations) == 0 {
			return graph{}, fmt.Errorf("pathtree connection %q -> %q has no relations", connection.FromID, connection.ToID)
		}
		pair := connection.FromID + "\x00" + connection.ToID
		if _, duplicate := connectionPairs[pair]; duplicate {
			return graph{}, fmt.Errorf("pathtree connection %q -> %q is not aggregated", connection.FromID, connection.ToID)
		}
		connectionPairs[pair] = struct{}{}
		relations := cloneRelations(connection.Relations)
		sort.Slice(relations, func(i, j int) bool { return relations[i].ID < relations[j].ID })
		for index, relation := range relations {
			if relation.ID == "" {
				return graph{}, fmt.Errorf("pathtree connection %q -> %q has an empty relation ID", connection.FromID, connection.ToID)
			}
			if index > 0 && relation.ID == relations[index-1].ID {
				return graph{}, fmt.Errorf("pathtree connection %q -> %q repeats relation %q", connection.FromID, connection.ToID, relation.ID)
			}
		}
		key := relationEdgeKey(connection.FromID, connection.ToID, relations)
		if _, duplicate := edgeKeys[key]; duplicate {
			return graph{}, fmt.Errorf("pathtree repeats connection %q -> %q", connection.FromID, connection.ToID)
		}
		edgeKeys[key] = struct{}{}
		current.addEdge(graphEdge{fromID: connection.FromID, toID: connection.ToID, kind: edgeRelation, relations: relations, key: key})
	}
	for _, scope := range result.ScopeEdges {
		if err := ctx.Err(); err != nil {
			return graph{}, err
		}
		parent, parentOK := current.nodes[scope.ParentID]
		child, childOK := current.nodes[scope.ChildID]
		if !parentOK || !childOK {
			return graph{}, fmt.Errorf("pathtree scope edge %q -> %q references a missing node", scope.ParentID, scope.ChildID)
		}
		if parent.Type != "job_network" || (child.Type != "job" && child.Type != "job_network") {
			return graph{}, fmt.Errorf("pathtree scope edge %q -> %q has invalid node types", scope.ParentID, scope.ChildID)
		}
		key := scopeEdgeKey(scope.ParentID, scope.ChildID)
		if _, duplicate := edgeKeys[key]; duplicate {
			return graph{}, fmt.Errorf("pathtree repeats scope edge %q -> %q", scope.ParentID, scope.ChildID)
		}
		edgeKeys[key] = struct{}{}
		current.addEdge(graphEdge{fromID: scope.ParentID, toID: scope.ChildID, kind: edgeScope, key: key})
	}
	for id := range current.nodes {
		sort.Slice(current.outgoing[id], func(i, j int) bool { return lessEdge(current.outgoing[id][i], current.outgoing[id][j]) })
		sort.Slice(current.incoming[id], func(i, j int) bool { return lessEdge(current.incoming[id][i], current.incoming[id][j]) })
	}
	return current, nil
}

func (current *graph) addEdge(edge graphEdge) {
	current.edges = append(current.edges, edge)
	current.outgoing[edge.fromID] = append(current.outgoing[edge.fromID], edge)
	current.incoming[edge.toID] = append(current.incoming[edge.toID], edge)
}

func (state *buildState) collectLimitOwners(result limitscan.Result) error {
	groups := []*limitscan.Limits{&result.Target, &result.Contained, &result.Downstream}
	for _, limits := range groups {
		for _, group := range limits.FinishByGroups {
			for _, item := range group.Items {
				if err := state.addLimitOwner(item); err != nil {
					return err
				}
			}
		}
		for _, item := range limits.MaxElapsed.Items {
			if err := state.addLimitOwner(item); err != nil {
				return err
			}
		}
		for _, item := range limits.Raw.Items {
			if err := state.addLimitOwner(item); err != nil {
				return err
			}
		}
	}
	return state.ctx.Err()
}

func (state *buildState) addLimitOwner(item limitscan.Item) error {
	if err := state.ctx.Err(); err != nil {
		return err
	}
	node, ok := state.graph.nodes[item.LimitOwner.ID]
	if !ok {
		return fmt.Errorf("pathtree limit %q owner %q is not in traversal result", item.Fact.ID, item.LimitOwner.ID)
	}
	if node.Type != item.LimitOwner.Type || node.Name != item.LimitOwner.Name || node.Type != "job" {
		return fmt.Errorf("pathtree limit %q owner %q differs from traversal result", item.Fact.ID, item.LimitOwner.ID)
	}
	if existing, ok := state.limitOwners[item.LimitOwner.ID]; ok && existing != item.LimitOwner {
		return fmt.Errorf("pathtree limit owner %q has inconsistent data", item.LimitOwner.ID)
	}
	state.limitOwners[item.LimitOwner.ID] = item.LimitOwner
	return nil
}

func shortestPaths(ctx context.Context, current graph, targetID string, confirmedOnly bool) (map[string]*path, error) {
	start := &path{nodeID: targetID}
	best := map[string]*path{targetID: start}
	queue := pathQueue{start}
	heap.Init(&queue)
	for queue.Len() > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		currentPath := heap.Pop(&queue).(*path)
		if best[currentPath.nodeID] != currentPath {
			continue
		}
		for _, edge := range current.outgoing[currentPath.nodeID] {
			if confirmedOnly && !edge.confirmed() {
				continue
			}
			candidate := extendPath(currentPath, edge, current.nodes[edge.toID])
			existing, exists := best[edge.toID]
			if exists && !lessPath(candidate, existing) {
				continue
			}
			best[edge.toID] = candidate
			heap.Push(&queue, candidate)
		}
	}
	return best, nil
}

func extendPath(parent *path, edge graphEdge, destination traversal.Node) *path {
	candidate := &path{
		nodeID:             edge.toID,
		dependencyDistance: parent.dependencyDistance,
		graphDepth:         parent.graphDepth,
		hopCount:           parent.hopCount + 1,
		relationIDCount:    parent.relationIDCount,
		previous:           parent,
		via:                edge,
	}
	if edge.kind == edgeRelation {
		candidate.graphDepth++
		if destination.Type == "job" || destination.Type == "job_network" {
			candidate.dependencyDistance++
		}
		candidate.relationIDCount += len(edge.relations)
	}
	return candidate
}

func lessPath(left, right *path) bool {
	if left.dependencyDistance != right.dependencyDistance {
		return left.dependencyDistance < right.dependencyDistance
	}
	if left.graphDepth != right.graphDepth {
		return left.graphDepth < right.graphDepth
	}
	if comparison := slices.Compare(pathNodeIDs(left), pathNodeIDs(right)); comparison != 0 {
		return comparison < 0
	}
	return slices.Compare(pathRelationIDs(left), pathRelationIDs(right)) < 0
}

func pathNodeIDs(current *path) []string {
	if current.nodeIDs != nil {
		return current.nodeIDs
	}
	original := current
	result := make([]string, current.hopCount+1)
	for index := current.hopCount; index >= 0; index-- {
		result[index] = current.nodeID
		current = current.previous
	}
	original.nodeIDs = result
	return original.nodeIDs
}

func pathRelationIDs(current *path) []string {
	if current.relationIDs != nil {
		return current.relationIDs
	}
	result := make([]string, current.relationIDCount)
	index := len(result)
	for step := current; step.previous != nil; step = step.previous {
		if step.via.kind != edgeRelation {
			continue
		}
		index -= len(step.via.relations)
		for relationIndex, relation := range step.via.relations {
			result[index+relationIndex] = relation.ID
		}
	}
	current.relationIDs = result
	return current.relationIDs
}

func buildCycles(ctx context.Context, input []traversal.Cycle, current graph) ([]Cycle, map[string]string, error) {
	components := make([][]string, len(input))
	seen := make(map[string]struct{})
	for index, source := range input {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if len(source.NodeIDs) == 0 {
			return nil, nil, errors.New("pathtree cycle has no nodes")
		}
		ids := slices.Clone(source.NodeIDs)
		sort.Strings(ids)
		for nodeIndex := 1; nodeIndex < len(ids); nodeIndex++ {
			if ids[nodeIndex] == ids[nodeIndex-1] {
				return nil, nil, fmt.Errorf("pathtree cycle repeats node %q", ids[nodeIndex])
			}
		}
		for _, id := range ids {
			if _, ok := current.nodes[id]; !ok {
				return nil, nil, fmt.Errorf("pathtree cycle references missing node %q", id)
			}
			if _, duplicate := seen[id]; duplicate {
				return nil, nil, fmt.Errorf("pathtree node %q occurs in multiple cycles", id)
			}
			seen[id] = struct{}{}
		}
		components[index] = ids
	}
	sort.Slice(components, func(i, j int) bool { return slices.Compare(components[i], components[j]) < 0 })
	cycles := make([]Cycle, 0, len(components))
	cycleByNode := make(map[string]string, len(seen))
	cycleIndexes := make(map[string]int, len(components))
	for index, ids := range components {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		cycleID := fmt.Sprintf("cycle-%d", index+1)
		cycleIndexes[cycleID] = index
		allowed := make(map[string]struct{}, len(ids))
		nodes := make([]traversal.Node, len(ids))
		for nodeIndex, id := range ids {
			allowed[id] = struct{}{}
			nodes[nodeIndex] = current.nodes[id]
			cycleByNode[id] = cycleID
		}
		route, err := oneCycle(ctx, current, ids[0], allowed)
		if err != nil {
			return nil, nil, fmt.Errorf("pathtree %s: %w", cycleID, err)
		}
		cycles = append(cycles, Cycle{CycleID: cycleID, Nodes: nodes, Route: route})
	}
	for _, edge := range current.edges {
		if edge.kind != edgeRelation {
			continue
		}
		fromCycle, fromInside := cycleByNode[edge.fromID]
		toCycle, toInside := cycleByNode[edge.toID]
		if !fromInside || !toInside || fromCycle != toCycle {
			continue
		}
		cycle := &cycles[cycleIndexes[fromCycle]]
		for _, relation := range edge.relations {
			if relation.Origin == "deterministic_analysis" || relation.Origin == "ai_analysis" {
				cycle.ContainsImplicitRelation = true
			}
			if relation.Certainty == "inferred" || relation.Certainty == "candidate" {
				cycle.ContainsUncertainRelation = true
			}
		}
	}
	return cycles, cycleByNode, nil
}

type hopPath struct {
	nodeID   string
	steps    []graphEdge
	nodeIDs  []string
	edgeKeys []string
}

type hopQueue []*hopPath

func (queue hopQueue) Len() int      { return len(queue) }
func (queue hopQueue) Swap(i, j int) { queue[i], queue[j] = queue[j], queue[i] }
func (queue hopQueue) Less(i, j int) bool {
	if len(queue[i].steps) != len(queue[j].steps) {
		return len(queue[i].steps) < len(queue[j].steps)
	}
	if comparison := slices.Compare(queue[i].nodeIDs, queue[j].nodeIDs); comparison != 0 {
		return comparison < 0
	}
	return slices.Compare(queue[i].edgeKeys, queue[j].edgeKeys) < 0
}
func (queue *hopQueue) Push(value any) { *queue = append(*queue, value.(*hopPath)) }
func (queue *hopQueue) Pop() any {
	old := *queue
	last := len(old) - 1
	value := old[last]
	*queue = old[:last]
	return value
}

func oneCycle(ctx context.Context, current graph, start string, allowed map[string]struct{}) ([]CycleStep, error) {
	if len(allowed) == 1 {
		for _, edge := range current.outgoing[start] {
			if edge.toID == start {
				return cycleSteps([]graphEdge{edge}), nil
			}
		}
		return nil, fmt.Errorf("cycle component containing %q has no self-loop", start)
	}

	visited := map[string]struct{}{start: {}}
	currentID := start
	route := make([]graphEdge, 0, len(allowed))
	for len(visited) < len(allowed) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		remaining := make([]string, 0, len(allowed)-len(visited))
		for id := range allowed {
			if _, seen := visited[id]; !seen {
				remaining = append(remaining, id)
			}
		}
		sort.Strings(remaining)
		segment, err := shortestHopPath(ctx, current, currentID, remaining[0], allowed)
		if err != nil {
			return nil, err
		}
		route = append(route, segment...)
		for _, edge := range segment {
			visited[edge.toID] = struct{}{}
		}
		currentID = remaining[0]
	}
	segment, err := shortestHopPath(ctx, current, currentID, start, allowed)
	if err != nil {
		return nil, err
	}
	route = append(route, segment...)
	return cycleSteps(route), nil
}

func shortestHopPath(
	ctx context.Context,
	current graph,
	start string,
	destination string,
	allowed map[string]struct{},
) ([]graphEdge, error) {
	initial := &hopPath{nodeID: start, nodeIDs: []string{start}}
	queue := hopQueue{initial}
	heap.Init(&queue)
	best := map[string]*hopPath{start: initial}
	for queue.Len() > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		currentPath := heap.Pop(&queue).(*hopPath)
		if best[currentPath.nodeID] != currentPath {
			continue
		}
		if currentPath.nodeID == destination {
			return currentPath.steps, nil
		}
		for _, edge := range current.outgoing[currentPath.nodeID] {
			if _, ok := allowed[edge.toID]; !ok {
				continue
			}
			candidate := &hopPath{
				nodeID:   edge.toID,
				steps:    append(slices.Clone(currentPath.steps), edge),
				nodeIDs:  append(slices.Clone(currentPath.nodeIDs), edge.toID),
				edgeKeys: append(slices.Clone(currentPath.edgeKeys), edge.key),
			}
			existing, exists := best[edge.toID]
			if exists && !lessHopPath(candidate, existing) {
				continue
			}
			best[edge.toID] = candidate
			heap.Push(&queue, candidate)
		}
	}
	return nil, fmt.Errorf("cycle component has no route from %q to %q", start, destination)
}

func lessHopPath(left, right *hopPath) bool {
	if len(left.steps) != len(right.steps) {
		return len(left.steps) < len(right.steps)
	}
	if comparison := slices.Compare(left.nodeIDs, right.nodeIDs); comparison != 0 {
		return comparison < 0
	}
	return slices.Compare(left.edgeKeys, right.edgeKeys) < 0
}

func cycleSteps(edges []graphEdge) []CycleStep {
	steps := make([]CycleStep, len(edges))
	for index, edge := range edges {
		steps[index] = CycleStep{
			FromID: edge.fromID, ToID: edge.toID,
			ViaRelations: cloneRelations(edge.relations), ViaScope: edge.kind == edgeScope,
		}
	}
	return steps
}

func countAlternatePaths(ctx context.Context, current graph, cycleByNode map[string]string, targetID string) (map[string]int, error) {
	// SCC内の周回を一地点へまとめ、有限なDAG上で経路数を伝播させる。
	// これにより、単純経路を列挙せずに合流後の代替経路数も引き継げる。
	componentFor := make(map[string]string, len(current.nodes))
	componentNodes := make(map[string][]string)
	for id := range current.nodes {
		componentID := "node:" + id
		if cycleID, ok := cycleByNode[id]; ok {
			componentID = cycleID
		}
		componentFor[id] = componentID
		componentNodes[componentID] = append(componentNodes[componentID], id)
	}
	for componentID := range componentNodes {
		sort.Strings(componentNodes[componentID])
	}

	type componentEdge struct{ from, to, key string }
	edgeSet := make(map[string]struct{})
	indegree := make(map[string]int, len(componentNodes))
	outgoing := make(map[string][]componentEdge)
	for _, edge := range current.edges {
		from := componentFor[edge.fromID]
		to := componentFor[edge.toID]
		if from == to {
			continue
		}
		key := from + "\x00" + to + "\x00" + edge.key
		if _, duplicate := edgeSet[key]; duplicate {
			continue
		}
		edgeSet[key] = struct{}{}
		componentEdge := componentEdge{from: from, to: to, key: edge.key}
		outgoing[from] = append(outgoing[from], componentEdge)
		indegree[to]++
	}
	for componentID := range componentNodes {
		sort.Slice(outgoing[componentID], func(i, j int) bool {
			if outgoing[componentID][i].to != outgoing[componentID][j].to {
				return outgoing[componentID][i].to < outgoing[componentID][j].to
			}
			return outgoing[componentID][i].key < outgoing[componentID][j].key
		})
	}

	ready := &stringHeap{}
	heap.Init(ready)
	for componentID := range componentNodes {
		if indegree[componentID] == 0 {
			heap.Push(ready, componentID)
		}
	}
	pathCounts := make(map[string]int, len(componentNodes))
	pathCounts[componentFor[targetID]] = 1
	processed := 0
	for ready.Len() > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		componentID := heap.Pop(ready).(string)
		processed++
		for _, edge := range outgoing[componentID] {
			pathCounts[edge.to] = saturatingAdd(pathCounts[edge.to], pathCounts[componentID])
			indegree[edge.to]--
			if indegree[edge.to] == 0 {
				heap.Push(ready, edge.to)
			}
		}
	}
	if processed != len(componentNodes) {
		return nil, errors.New("pathtree cycles do not cover every graph cycle")
	}
	counts := make(map[string]int, len(current.nodes))
	for id, componentID := range componentFor {
		if pathCounts[componentID] > 0 {
			counts[id] = pathCounts[componentID] - 1
		}
	}
	return counts, nil
}

type stringHeap []string

func (values stringHeap) Len() int           { return len(values) }
func (values stringHeap) Less(i, j int) bool { return values[i] < values[j] }
func (values stringHeap) Swap(i, j int)      { values[i], values[j] = values[j], values[i] }
func (values *stringHeap) Push(value any)    { *values = append(*values, value.(string)) }
func (values *stringHeap) Pop() any {
	old := *values
	last := len(old) - 1
	value := old[last]
	*values = old[:last]
	return value
}

func saturatingAdd(left, right int) int {
	// 経路数はDAGでも指数的に増える。オーバーフローで負数にせず、存在する代替経路を最大値で保持する。
	if right > math.MaxInt-left {
		return math.MaxInt
	}
	return left + right
}

func (state *buildState) findUncoveredRoutes() ([]UncoveredRoute, error) {
	routes := make([]UncoveredRoute, 0)
	for id := range state.graph.reached {
		if err := state.ctx.Err(); err != nil {
			return nil, err
		}
		if len(state.graph.outgoing[id]) != 0 {
			continue
		}
		if _, hasLimit := state.limitOwners[id]; hasLimit {
			continue
		}
		routes = append(routes, UncoveredRoute{Boundary: state.graph.nodes[id], Reason: UncoveredTerminal})
	}
	for id, reason := range state.graph.endpoints {
		if reason != traversal.EndpointNonTraversableNodeType {
			continue
		}
		routes = append(routes, UncoveredRoute{Boundary: state.graph.nodes[id], Reason: UncoveredNonTraversableType})
	}
	for _, cycle := range state.cycles {
		hasLimit := false
		for _, node := range cycle.Nodes {
			if _, ok := state.limitOwners[node.ID]; ok {
				hasLimit = true
				break
			}
		}
		if !hasLimit {
			routes = append(routes, UncoveredRoute{
				Boundary: state.graph.nodes[cycle.Route[0].FromID], Reason: UncoveredCycle, CycleID: cycle.CycleID,
			})
		}
	}
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Boundary.ID != routes[j].Boundary.ID {
			return routes[i].Boundary.ID < routes[j].Boundary.ID
		}
		if routes[i].Reason != routes[j].Reason {
			return routes[i].Reason < routes[j].Reason
		}
		return routes[i].CycleID < routes[j].CycleID
	})
	return routes, nil
}

func (state *buildState) buildTree() error {
	rootPath := state.selectedPaths[state.targetID]
	if rootPath == nil {
		return errors.New("pathtree target has no selected path")
	}
	state.root = state.newTreeNode(state.graph.nodes[state.targetID], nil, nil)
	state.referenceTargets[state.targetID] = state.root
	state.selectedPathTargets[state.targetID] = state.root
	state.occurrences[state.targetID] = []*treeWorkNode{state.root}
	state.treeByPath[rootPath] = state.root
	state.treeByPath[state.allPaths[state.targetID]] = state.root
	state.treeByPath[state.confirmedPaths[state.targetID]] = state.root

	ids := make([]string, 0, len(state.selectedPaths))
	for id := range state.selectedPaths {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := state.ctx.Err(); err != nil {
			return err
		}
		terminal := state.insertPath(state.selectedPaths[id])
		state.referenceTargets[id] = terminal
		state.selectedPathTargets[id] = terminal
		if _, isLimitOwner := state.limitOwners[id]; isLimitOwner {
			state.limitReferenceTargets[id] = terminal
		}
	}

	for id, occurrences := range state.occurrences {
		referenceTarget := state.referenceTargets[id]
		if len(referenceTarget.children) == 0 {
			for _, occurrence := range occurrences {
				if len(occurrence.children) != 0 {
					referenceTarget = occurrence
					state.referenceTargets[id] = occurrence
					break
				}
			}
		}
		for _, occurrence := range occurrences {
			// 子を持つ出現は他ノードの代表経路を保持する。葉だけを参照化し、配下への経路を参照で途切らない。
			if occurrence != referenceTarget && len(occurrence.children) == 0 {
				occurrence.value.ReferenceType = ReferenceShared
				occurrence.referenceTarget = referenceTarget
			}
		}
	}
	// 代表経路の木に採用されなかった辺は参照ノードで閉じ、合流先やSCCを再展開しない。
	for _, edge := range state.graph.edges {
		if err := state.ctx.Err(); err != nil {
			return err
		}
		parent := state.referenceTargets[edge.fromID]
		if childByEdge(parent, edge) != nil {
			continue
		}
		child := state.newTreeNode(state.graph.nodes[edge.toID], &edge, parent)
		child.referenceTarget = state.referenceTargets[edge.toID]
		if cycleID, ok := sameCycle(state.cycleByNode, edge.fromID, edge.toID); ok {
			child.value.ReferenceType = ReferenceCycle
			child.value.CycleID = cycleID
		} else {
			child.value.ReferenceType = ReferenceShared
		}
		parent.children = append(parent.children, child)
	}
	state.sortTree()
	state.compressTree()
	return state.ctx.Err()
}

func (state *buildState) insertPath(selected *path) *treeWorkNode {
	if existing := state.treeByPath[selected]; existing != nil {
		return existing
	}
	pending := make([]*path, 0)
	currentPath := selected
	for state.treeByPath[currentPath] == nil {
		pending = append(pending, currentPath)
		currentPath = currentPath.previous
	}
	current := state.treeByPath[currentPath]
	for index := len(pending) - 1; index >= 0; index-- {
		currentPath = pending[index]
		edge := currentPath.via
		child := childByEdge(current, edge)
		if child == nil {
			child = state.newTreeNode(state.graph.nodes[edge.toID], &edge, current)
			current.children = append(current.children, child)
			state.occurrences[edge.toID] = append(state.occurrences[edge.toID], child)
		}
		current = child
		state.treeByPath[currentPath] = current
	}
	return current
}

func (state *buildState) newTreeNode(node traversal.Node, via *graphEdge, parent *treeWorkNode) *treeWorkNode {
	value := &TreeNode{Node: node, AlternatePathCount: state.alternatePathCounts[node.ID]}
	if confirmed, ok := state.confirmedPaths[node.ID]; ok {
		distance := confirmed.dependencyDistance
		value.ConfirmedDependencyDistance = &distance
	}
	work := &treeWorkNode{value: value}
	if via == nil {
		return work
	}
	work.viaKey = via.key
	value.ViaScope = via.kind == edgeScope
	value.ViaRelations = cloneRelations(via.relations)
	value.DependencyDistance = parent.value.DependencyDistance
	value.GraphDepth = parent.value.GraphDepth
	if via.kind == edgeRelation {
		value.GraphDepth++
		if node.Type == "job" || node.Type == "job_network" {
			value.DependencyDistance++
		}
	}
	return work
}

func childByEdge(parent *treeWorkNode, edge graphEdge) *treeWorkNode {
	for _, child := range parent.children {
		if child.value.Node.ID == edge.toID && child.viaKey == edge.key {
			return child
		}
	}
	return nil
}

func (state *buildState) sortTree() {
	stack := []*treeWorkNode{state.root}
	for len(stack) > 0 {
		last := len(stack) - 1
		current := stack[last]
		stack = stack[:last]
		sort.Slice(current.children, func(i, j int) bool {
			left, right := current.children[i], current.children[j]
			if left.value.Node.ID != right.value.Node.ID {
				return left.value.Node.ID < right.value.Node.ID
			}
			return left.viaKey < right.viaKey
		})
		stack = append(stack, current.children...)
	}
}

func (state *buildState) compressTree() {
	postorder := make([]*treeWorkNode, 0)
	stack := []*treeWorkNode{state.root}
	for len(stack) > 0 {
		last := len(stack) - 1
		current := stack[last]
		stack = stack[:last]
		postorder = append(postorder, current)
		stack = append(stack, current.children...)
	}
	for index := len(postorder) - 1; index >= 0; index-- {
		parent := postorder[index]
		for childIndex, child := range parent.children {
			for state.canHide(child) && len(child.children) == 1 {
				next := child.children[0]
				next.hiddenNodeCount += child.hiddenNodeCount + 1
				hiddenIDs := make([]string, 0, min(maxHiddenNodeIDs, 1+len(child.value.HiddenNodeIDs)+len(next.value.HiddenNodeIDs)))
				hiddenIDs = append(hiddenIDs, child.value.Node.ID)
				hiddenIDs = append(hiddenIDs, child.value.HiddenNodeIDs...)
				hiddenIDs = append(hiddenIDs, next.value.HiddenNodeIDs...)
				if len(hiddenIDs) > maxHiddenNodeIDs {
					hiddenIDs = hiddenIDs[:maxHiddenNodeIDs]
				}
				next.value.HiddenNodeIDs = hiddenIDs
				next.value.HiddenJobCount += child.value.HiddenJobCount
				if child.value.Node.Type == "job" {
					next.value.HiddenJobCount++
				}
				next.value.HiddenNodeIDsTruncated = child.value.HiddenNodeIDsTruncated || next.value.HiddenNodeIDsTruncated || next.hiddenNodeCount > maxHiddenNodeIDs
				child = next
			}
			parent.children[childIndex] = child
		}
	}
	state.sortTree()
}

func (state *buildState) canHide(node *treeWorkNode) bool {
	if node == state.root || node.referenceTarget != nil || node.value.ReferenceType != "" {
		return false
	}
	id := node.value.Node.ID
	if _, hasLimit := state.limitOwners[id]; hasLimit {
		return false
	}
	if _, inCycle := state.cycleByNode[id]; inCycle {
		return false
	}
	if _, endpoint := state.graph.endpoints[id]; endpoint {
		return false
	}
	if len(state.occurrences[id]) > 1 {
		return false
	}
	if len(state.graph.incoming[id]) != 1 || len(state.graph.outgoing[id]) != 1 {
		return false
	}
	for _, edge := range []graphEdge{state.graph.incoming[id][0], state.graph.outgoing[id][0]} {
		if edge.kind == edgeScope || hasUncertainRelation(edge) {
			return false
		}
		if state.graph.nodes[edge.fromID].Type == "external_event" || state.graph.nodes[edge.toID].Type == "external_event" {
			return false
		}
	}
	return true
}

func hasUncertainRelation(edge graphEdge) bool {
	for _, relation := range edge.relations {
		if relation.Certainty == "inferred" || relation.Certainty == "candidate" {
			return true
		}
	}
	return false
}

func (state *buildState) finish() (Result, error) {
	if err := state.ctx.Err(); err != nil {
		return Result{}, err
	}
	ordered := make([]*treeWorkNode, 0)
	stack := []*treeWorkNode{state.root}
	for len(stack) > 0 {
		if err := state.ctx.Err(); err != nil {
			return Result{}, err
		}
		last := len(stack) - 1
		current := stack[last]
		stack = stack[:last]
		ordered = append(ordered, current)
		for index := len(current.children) - 1; index >= 0; index-- {
			stack = append(stack, current.children[index])
		}
	}
	for index, current := range ordered {
		current.value.TreeNodeID = fmt.Sprintf("tree-%d", index+1)
		current.value.Children = make([]*TreeNode, len(current.children))
		for childIndex, child := range current.children {
			current.value.Children[childIndex] = child.value
		}
	}
	for _, current := range ordered {
		if current.referenceTarget != nil {
			current.value.ReferenceTo = current.referenceTarget.value.TreeNodeID
		}
	}

	limitReferences := make(map[string]LimitReference, len(state.limitReferenceTargets))
	for ownerID, target := range state.limitReferenceTargets {
		if target.value.TreeNodeID == "" {
			return Result{}, fmt.Errorf("pathtree limit owner %q was removed from tree", ownerID)
		}
		limitReferences[ownerID] = LimitReference{
			TreeNodeID: target.value.TreeNodeID, AlternatePathCount: state.alternatePathCounts[ownerID],
		}
	}
	for index := range state.uncovered {
		target := state.selectedPathTargets[state.uncovered[index].Boundary.ID]
		if target == nil || target.value.TreeNodeID == "" {
			return Result{}, fmt.Errorf("pathtree uncovered boundary %q was removed from tree", state.uncovered[index].Boundary.ID)
		}
		state.uncovered[index].TreeNodeID = target.value.TreeNodeID
	}
	return Result{
		Tree: *state.root.value, LimitReferences: limitReferences,
		Cycles: slices.Clone(state.cycles), UncoveredRoutes: slices.Clone(state.uncovered),
	}, nil
}

func sameCycle(cycleByNode map[string]string, left, right string) (string, bool) {
	leftCycle, leftOK := cycleByNode[left]
	rightCycle, rightOK := cycleByNode[right]
	return leftCycle, leftOK && rightOK && leftCycle == rightCycle
}

func lessEdge(left, right graphEdge) bool {
	if left.toID != right.toID {
		return left.toID < right.toID
	}
	if left.kind != right.kind {
		return left.kind < right.kind
	}
	return left.key < right.key
}

func relationEdgeKey(fromID, toID string, relations []traversal.Relation) string {
	key := "relation\x00" + fromID + "\x00" + toID
	for _, relation := range relations {
		key += "\x00" + relation.ID
	}
	return key
}

func scopeEdgeKey(parentID, childID string) string {
	return "scope\x00" + parentID + "\x00" + childID
}

func cloneRelations(source []traversal.Relation) []traversal.Relation {
	if len(source) == 0 {
		return nil
	}
	result := make([]traversal.Relation, len(source))
	for index, relation := range source {
		result[index] = relation
		result[index].Evidence = json.RawMessage(slices.Clone(relation.Evidence))
	}
	return result
}

func sameTraversalNode(left, right traversal.Node) bool {
	return left.ID == right.ID && left.Type == right.Type && left.Name == right.Name &&
		sameOptionalString(left.Path, right.Path) && sameOptionalString(left.ParentID, right.ParentID)
}

func sameOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
