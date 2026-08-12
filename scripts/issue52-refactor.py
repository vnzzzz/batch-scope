#!/usr/bin/env python3
from pathlib import Path
import re


def replace_once(path: str, old: str, new: str) -> None:
    file = Path(path)
    text = file.read_text()
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"{path}: expected one exact match, got {count}")
    file.write_text(text.replace(old, new, 1))


def regex_once(path: str, pattern: str, replacement: str) -> None:
    file = Path(path)
    text = file.read_text()
    updated, count = re.subn(pattern, replacement, text, count=1, flags=re.S)
    if count != 1:
        raise SystemExit(f"{path}: expected one regex match, got {count}")
    file.write_text(updated)


# pathtree: do not retain root-to-node lexical key slices on every candidate path.
replace_once(
    "internal/pathtree/pathtree.go",
    '"sort"\n',
    '"sort"\n\t"sync"\n',
)
replace_once(
    "internal/pathtree/pathtree.go",
    '''type path struct {
\tnodeID             string
\tdependencyDistance int
\tgraphDepth         int
\thopCount           int
\trelationIDCount    int
\tprevious           *path
\tvia                graphEdge
\tnodeIDs            []string
\trelationIDs        []string
}
''',
    '''type path struct {
\tnodeID             string
\tdependencyDistance int
\tgraphDepth         int
\thopCount           int
\trelationIDCount    int
\tprevious           *path
\tvia                graphEdge
}
''',
)
replace_once(
    "internal/pathtree/pathtree.go",
    '''\tstate.confirmedPaths, err = shortestPaths(state.ctx, currentGraph, traversalResult.Target.ID, true, nil)
\tif err != nil {
\t\treturn err
\t}
\tstate.limitFreePaths, err = shortestPaths(state.ctx, currentGraph, traversalResult.Target.ID, false, state.limitOwners)
\tif err != nil {
\t\treturn err
\t}
\t// 確定経路の存在は距離より先に比較するため、全relationの最短経路とは別に選択結果を保持する。
\tstate.selectedPaths = make(map[string]*path, len(state.allPaths))
\tfor id, allPath := range state.allPaths {
\t\tif err := state.ctx.Err(); err != nil {
\t\t\treturn err
\t\t}
\t\tif confirmedPath, ok := state.confirmedPaths[id]; ok {
\t\t\tstate.selectedPaths[id] = confirmedPath
\t\t} else {
\t\t\tstate.selectedPaths[id] = allPath
\t\t}
\t}
''',
    '''\tif allEdgesConfirmed(currentGraph) {
\t\t// 全接続が確定済みならconfirmed-only探索はallPathsと完全に同じになる。
\t\t// 大規模snapshotで同じpath DAGを二重保持しない。
\t\tstate.confirmedPaths = state.allPaths
\t\tstate.selectedPaths = state.allPaths
\t} else {
\t\tstate.confirmedPaths, err = shortestPaths(state.ctx, currentGraph, traversalResult.Target.ID, true, nil)
\t\tif err != nil {
\t\t\treturn err
\t\t}
\t\t// 確定経路の存在は距離より先に比較するため、全relationの最短経路とは別に選択結果を保持する。
\t\tstate.selectedPaths = make(map[string]*path, len(state.allPaths))
\t\tfor id, allPath := range state.allPaths {
\t\t\tif err := state.ctx.Err(); err != nil {
\t\t\t\treturn err
\t\t\t}
\t\t\tif confirmedPath, ok := state.confirmedPaths[id]; ok {
\t\t\t\tstate.selectedPaths[id] = confirmedPath
\t\t\t} else {
\t\t\t\tstate.selectedPaths[id] = allPath
\t\t\t}
\t\t}
\t}
\tstate.limitFreePaths, err = shortestPaths(state.ctx, currentGraph, traversalResult.Target.ID, false, state.limitOwners)
\tif err != nil {
\t\treturn err
\t}
''',
)
replace_once(
    "internal/pathtree/pathtree.go",
    '''func extendPath(parent *path, edge graphEdge, destination traversal.Node) *path {
''',
    '''func allEdgesConfirmed(current graph) bool {
\tfor _, edge := range current.edges {
\t\tif !edge.confirmed() {
\t\t\treturn false
\t\t}
\t}
\treturn true
}

func extendPath(parent *path, edge graphEdge, destination traversal.Node) *path {
''',
)
regex_once(
    "internal/pathtree/pathtree.go",
    r'''func lessPath\(left, right \*path\) bool \{.*?\n\}\n\nfunc buildCycles''',
    '''const maxPooledPathSteps = 4_096

var pathStepsPool = sync.Pool{New: func() any {
\treturn make([]*path, 0, 64)
}}

func lessPath(left, right *path) bool {
\tif left.dependencyDistance != right.dependencyDistance {
\t\treturn left.dependencyDistance < right.dependencyDistance
\t}
\tif left.graphDepth != right.graphDepth {
\t\treturn left.graphDepth < right.graphDepth
\t}

\tleftSteps := acquirePathSteps(left)
\trightSteps := acquirePathSteps(right)
\tcomparison := comparePathNodeIDs(leftSteps, rightSteps)
\tif comparison == 0 {
\t\tcomparison = comparePathRelationIDs(leftSteps, rightSteps)
\t}
\treleasePathSteps(leftSteps)
\treleasePathSteps(rightSteps)
\treturn comparison < 0
}

func acquirePathSteps(current *path) []*path {
\tsteps := pathStepsPool.Get().([]*path)[:0]
\tfor step := current; step != nil; step = step.previous {
\t\tsteps = append(steps, step)
\t}
\tslices.Reverse(steps)
\treturn steps
}

func releasePathSteps(steps []*path) {
\tif cap(steps) > maxPooledPathSteps {
\t\treturn
\t}
\tclear(steps)
\tpathStepsPool.Put(steps[:0])
}

func comparePathNodeIDs(left, right []*path) int {
\tfor index := 0; index < min(len(left), len(right)); index++ {
\t\tif left[index].nodeID < right[index].nodeID {
\t\t\treturn -1
\t\t}
\t\tif left[index].nodeID > right[index].nodeID {
\t\t\treturn 1
\t\t}
\t}
\tif len(left) < len(right) {
\t\treturn -1
\t}
\tif len(left) > len(right) {
\t\treturn 1
\t}
\treturn 0
}

func comparePathRelationIDs(left, right []*path) int {
\tleftStep, leftRelation := 1, 0
\trightStep, rightRelation := 1, 0
\tfor {
\t\tleftID, leftOK := nextPathRelationID(left, &leftStep, &leftRelation)
\t\trightID, rightOK := nextPathRelationID(right, &rightStep, &rightRelation)
\t\tif !leftOK || !rightOK {
\t\t\tswitch {
\t\t\tcase leftOK:
\t\t\t\treturn 1
\t\t\tcase rightOK:
\t\t\t\treturn -1
\t\t\tdefault:
\t\t\t\treturn 0
\t\t\t}
\t\t}
\t\tif leftID < rightID {
\t\t\treturn -1
\t\t}
\t\tif leftID > rightID {
\t\t\treturn 1
\t\t}
\t}
}

func nextPathRelationID(steps []*path, stepIndex, relationIndex *int) (string, bool) {
\tfor *stepIndex < len(steps) {
\t\tstep := steps[*stepIndex]
\t\tif step.via.kind != edgeRelation || *relationIndex >= len(step.via.relations) {
\t\t\t*stepIndex++
\t\t\t*relationIndex = 0
\t\t\tcontinue
\t\t}
\t\tid := step.via.relations[*relationIndex].ID
\t\t*relationIndex++
\t\treturn id, true
\t}
\treturn "", false
}

func buildCycles''',
)
# Fix Python-generated invalid Go increments (`*ptr++`) to explicit pointer assignments.
replace_once(
    "internal/pathtree/pathtree.go",
    '''\t\t\t*stepIndex++
''',
    '''\t\t\t*stepIndex = *stepIndex + 1
''',
)
replace_once(
    "internal/pathtree/pathtree.go",
    '''\t\t*relationIndex++
''',
    '''\t\t*relationIndex = *relationIndex + 1
''',
)
replace_once(
    "internal/pathtree/pathtree.go",
    '''\tcomponentFor := make(map[string]string, len(current.nodes))
\tcomponentNodes := make(map[string][]string)
\tfor id := range current.nodes {
\t\tcomponentID := "node:" + id
\t\tif cycleID, ok := cycleByNode[id]; ok {
\t\t\tcomponentID = cycleID
\t\t}
\t\tcomponentFor[id] = componentID
\t\tcomponentNodes[componentID] = append(componentNodes[componentID], id)
\t}
\tfor componentID := range componentNodes {
\t\tsort.Strings(componentNodes[componentID])
\t}

\ttype componentEdge struct{ from, to, key string }
\tedgeSet := make(map[string]struct{})
\tindegree := make(map[string]int, len(componentNodes))
''',
    '''\tcomponentFor := make(map[string]string, len(current.nodes))
\tcomponents := make(map[string]struct{}, len(current.nodes))
\tfor id := range current.nodes {
\t\tcomponentID := "node:" + id
\t\tif cycleID, ok := cycleByNode[id]; ok {
\t\t\tcomponentID = cycleID
\t\t}
\t\tcomponentFor[id] = componentID
\t\tcomponents[componentID] = struct{}{}
\t}

\ttype componentEdge struct{ from, to, key string }
\tedgeSet := make(map[string]struct{})
\tindegree := make(map[string]int, len(components))
''',
)
replace_once(
    "internal/pathtree/pathtree.go",
    '''\tfor componentID := range componentNodes {
\t\tsort.Slice(outgoing[componentID], func(i, j int) bool {
''',
    '''\tfor componentID := range components {
\t\tsort.Slice(outgoing[componentID], func(i, j int) bool {
''',
)
replace_once(
    "internal/pathtree/pathtree.go",
    '''\tfor componentID := range componentNodes {
\t\tif indegree[componentID] == 0 {
''',
    '''\tfor componentID := range components {
\t\tif indegree[componentID] == 0 {
''',
)
replace_once(
    "internal/pathtree/pathtree.go",
    '''\tpathCounts := make(map[string]int, len(componentNodes))
''',
    '''\tpathCounts := make(map[string]int, len(components))
''',
)
replace_once(
    "internal/pathtree/pathtree.go",
    '''\tif processed != len(componentNodes) {
''',
    '''\tif processed != len(components) {
''',
)

# Validation: production calls keep production limits, focused tests can inject small streaming boundaries.
replace_once(
    "internal/snapshot/validate.go",
    '''\torderedNodeIDs, nodeByID, nodeCount, limitCount, duplicateErr, err := readNodes(ctx, extracted.Nodes, schemas.node)
''',
    '''\torderedNodeIDs, nodeByID, nodeCount, limitCount, duplicateErr, err := readNodes(
\t\tctx, extracted.Nodes, schemas.node, limits.MaxSnapshotNodes, limits.MaxSnapshotLimits,
\t)
''',
)
replace_once(
    "internal/snapshot/validate.go",
    '''\trelationCount, err := readRelations(ctx, extracted.Relations, schemas.relation, nodeByID, graph)
''',
    '''\trelationCount, err := readRelations(
\t\tctx, extracted.Relations, schemas.relation, nodeByID, graph, limits.MaxSnapshotRelations,
\t)
''',
)
replace_once(
    "internal/snapshot/validate.go",
    '''func readNodes(ctx context.Context, path string, schema *jsonschema.Schema) ([]string, map[string]nodeRecord, int, int, *Error, error) {
''',
    '''func readNodes(
\tctx context.Context,
\tpath string,
\tschema *jsonschema.Schema,
\tmaxNodes int,
\tmaxLimits int,
) ([]string, map[string]nodeRecord, int, int, *Error, error) {
''',
)
replace_once(
    "internal/snapshot/validate.go",
    '''\t\tif count > limits.MaxSnapshotNodes {
''',
    '''\t\tif count > maxNodes {
''',
)
replace_once(
    "internal/snapshot/validate.go",
    '''\t\tremaining := limits.MaxSnapshotLimits - limitCount
''',
    '''\t\tremaining := maxLimits - limitCount
''',
)
replace_once(
    "internal/snapshot/validate.go",
    '''func readRelations(ctx context.Context, path string, schema *jsonschema.Schema, nodes map[string]nodeRecord, graph *sccGraph) (int, error) {
''',
    '''func readRelations(
\tctx context.Context,
\tpath string,
\tschema *jsonschema.Schema,
\tnodes map[string]nodeRecord,
\tgraph *sccGraph,
\tmaxRelations int,
) (int, error) {
''',
)
replace_once(
    "internal/snapshot/validate.go",
    '''\t\tif count > limits.MaxSnapshotRelations {
''',
    '''\t\tif count > maxRelations {
''',
)
regex_once(
    "internal/snapshot/validate_test.go",
    r'''func TestValidateRejectsActualNDJSONCapacityWhileStreaming\(t \*testing\.T\) \{.*?\n\}\n\nfunc TestValidateRejectsLimitAndJobNetworkDepthCapacity''',
    '''func TestValidateRejectsActualNDJSONCapacityWhileStreaming(t *testing.T) {
\tschemas, err := getSchemas()
\tif err != nil {
\t\tt.Fatal(err)
\t}

\tt.Run("nodes", func(t *testing.T) {
\t\tconst maxNodes = 3
\t\tnodes := make([]string, maxNodes+2)
\t\tfor index := 0; index <= maxNodes; index++ {
\t\t\tnodes[index] = fmt.Sprintf(`{"type":"job","id":"JOB-%d","name":"Job"}`, index)
\t\t}
\t\t// 末尾の不正な行は、境界を越えて走査が続いたことを検出する番兵である。
\t\tnodes[len(nodes)-1] = "{"
\t\textracted := writeExtractedSnapshot(t, nodes, nil)
\t\t_, _, _, _, _, err := readNodes(context.Background(), extracted.Nodes, schemas.node, maxNodes, limits.MaxSnapshotLimits)
\t\tassertValidationError(t, err, ErrorCapacityExceeded, nodesName, maxNodes+1, "")
\t})

\tt.Run("relations", func(t *testing.T) {
\t\tconst maxRelations = 3
\t\trelations := make([]string, maxRelations+2)
\t\tfor index := 0; index <= maxRelations; index++ {
\t\t\trelations[index] = fmt.Sprintf(
\t\t\t\t`{"fromId":"JOB","toId":"JOB","kind":"precedes","origin":"scheduler","certainty":"declared","evidence":[{"source":"%d"}]}`,
\t\t\t\tindex,
\t\t\t)
\t\t}
\t\t// 末尾の不正な行は、境界を越えて走査が続いたことを検出する番兵である。
\t\trelations[len(relations)-1] = "{"
\t\textracted := writeExtractedSnapshot(t, []string{testNode("job", "JOB", nil, nil)}, relations)
\t\tnodes := map[string]nodeRecord{"JOB": {Type: nodeKindJob, Line: 1}}
\t\tgraph := newSCCGraph([]string{"JOB"}, nodes)
\t\t_, err := readRelations(context.Background(), extracted.Relations, schemas.relation, nodes, graph, maxRelations)
\t\tassertValidationError(t, err, ErrorCapacityExceeded, relationsName, maxRelations+1, "")
\t})
}

func TestValidateRejectsLimitAndJobNetworkDepthCapacity''',
)

# HTTP capacity test follows the production node limit instead of the old 10,000 boundary.
replace_once(
    "internal/app/snapshot_imports_test.go",
    '"errors"\n',
    '"errors"\n\t"fmt"\n',
)
replace_once(
    "internal/app/snapshot_imports_test.go",
    '''\t\tarchive := makeAppSnapshotArchive(t, map[string]string{
\t\t\t"manifest.json":    `{"schemaVersion":"0.5","snapshotId":"too-large","generatedAt":"2026-08-11T00:00:00Z","nodeCount":10001,"relationCount":0,"producer":{"name":"secret-input","version":"1"}}`,
''',
    '''\t\tarchive := makeAppSnapshotArchive(t, map[string]string{
\t\t\t"manifest.json": fmt.Sprintf(
\t\t\t\t`{"schemaVersion":"0.5","snapshotId":"too-large","generatedAt":"2026-08-11T00:00:00Z","nodeCount":%d,"relationCount":0,"producer":{"name":"secret-input","version":"1"}}`,
\t\t\t\tlimits.MaxSnapshotNodes+1,
\t\t\t),
''',
)

# The accepted scale now includes measured >10s complete analyses. The deadline remains an error bound, not a result cutoff.
replace_once(
    "internal/app/analysis.go",
    '''const analysisDeadline = 10 * time.Second
''',
    '''const analysisDeadline = 60 * time.Second
''',
)

print("issue52 refactor applied")
