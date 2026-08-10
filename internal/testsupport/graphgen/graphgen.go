// Package graphgen は、取込と静的解析の規模検査に使う決定的なスナップショットを生成する。
package graphgen

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

const (
	SmallNodeCount      = 10_000
	SmallRelationCount  = 25_000
	MediumNodeCount     = 100_000
	MediumRelationCount = 300_000
	ScaleNodeCount      = 1_000_000
	ScaleRelationCount  = 3_000_000
	generatedAt         = "2026-08-10T00:00:00Z"
	defaultOrigin       = "scheduler"
	defaultCertainty    = "declared"
)

// PathologicalCase は、個別に生成できる病理的なグラフ形状を識別する。
type PathologicalCase string

const (
	LongChain                  PathologicalCase = "long-chain"
	HighFanOut                 PathologicalCase = "high-fan-out"
	HighFanIn                  PathologicalCase = "high-fan-in"
	LargeAndMultipleSCC        PathologicalCase = "large-and-multiple-scc"
	CycleWithExit              PathologicalCase = "cycle-with-exit"
	DeepNestedNetworks         PathologicalCase = "deep-nested-networks"
	ReachedNetworkWithOutbound PathologicalCase = "reached-network-with-outbound"
	ManyLimits                 PathologicalCase = "many-limits"
	ParallelRelations          PathologicalCase = "parallel-relations"
	LongCompression            PathologicalCase = "long-compression"
	CoveredAndUncoveredMerge   PathologicalCase = "covered-and-uncovered-merge"
	UncoveredCycleAndEndpoint  PathologicalCase = "uncovered-cycle-and-endpoint"
	LargePathTreeSCC           PathologicalCase = "large-pathtree-scc"
)

// PathologicalCases は、規模検証で要求する独立ケースを固定順で返す。
func PathologicalCases() []PathologicalCase {
	return []PathologicalCase{
		LongChain, HighFanOut, HighFanIn, LargeAndMultipleSCC, CycleWithExit,
		DeepNestedNetworks, ReachedNetworkWithOutbound, ManyLimits, ParallelRelations,
		LongCompression, CoveredAndUncoveredMerge, UncoveredCycleAndEndpoint, LargePathTreeSCC,
	}
}

// Limit は、node.schema.jsonへ出力するリミットを表す。
type Limit struct {
	ID                string `json:"id"`
	Kind              string `json:"kind"`
	BusinessDayOffset *int   `json:"businessDayOffset,omitempty"`
	LocalTime         string `json:"localTime,omitempty"`
	TimeZone          string `json:"timeZone,omitempty"`
	Duration          string `json:"duration,omitempty"`
	SourceText        string `json:"sourceText,omitempty"`
	Origin            string `json:"origin"`
	Certainty         string `json:"certainty"`
}

// Node は、nodes.ndjsonへ出力する一行を表す。
type Node struct {
	Type       string  `json:"type"`
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	ParentID   *string `json:"parentId,omitempty"`
	LimitFacts []Limit `json:"limitFacts"`
}

// Relation は、relations.ndjsonへ出力する一行を表す。
type Relation struct {
	FromID    string `json:"fromId"`
	ToID      string `json:"toId"`
	Kind      string `json:"kind"`
	Origin    string `json:"origin"`
	Certainty string `json:"certainty"`
}

// RelationValue は、SQLiteが生成するrelation IDに依存せずrelation全項目を照合する期待値である。
type RelationValue struct {
	FromID    string
	ToID      string
	Kind      string
	Origin    string
	Certainty string
}

// Edge は、scopeまたは表示経路の一接続を表す。
type Edge struct {
	FromID string
	ToID   string
}

// Endpoint は、意味上の探索対象外端点を表す。
type Endpoint struct {
	NodeID   string
	NodeType string
	NodeName string
	Reason   string
}

// ReachedNode は、到達ノードの保存内容と対象との関係を表す。
type ReachedNode struct {
	ID         string
	Type       string
	Name       string
	ParentID   string
	HasParent  bool
	Membership string
}

// LimitExpectation は、リミット定義をSQLiteから復元した後の値を表す。
type LimitExpectation struct {
	ID                string
	OwnerID           string
	ScopeRootID       string
	Kind              string
	BusinessDayOffset *int64
	LocalTime         string
	TimeZone          string
	DurationSeconds   *int64
	SourceText        string
	HasSourceText     bool
	Origin            string
	Certainty         string
}

// LimitIDs は、種類別の正確なリミット順を保持する。
type LimitIDs struct {
	FinishBy   []string
	MaxElapsed []string
	Raw        []string
}

// LimitsExpectation は、対象との関係別にリミットを保持する。
type LimitsExpectation struct {
	Target     LimitIDs
	Contained  LimitIDs
	Downstream LimitIDs
}

// SCCExpectation は、SCCのノードと一周分の表示経路を構成時に確定した値である。
type SCCExpectation struct {
	NodeIDs []string
	Route   []Edge
}

// UncoveredExpectation は、リミット未通過で到達できる境界と理由を表す。
type UncoveredExpectation struct {
	NodeID string
	Reason string
}

// Expectation は、生成規則から入力と同時に確定する解析結果である。
// TreeNodeCountは圧縮後の表示ノード数、HiddenConnectionsは省略しても保持すべき全ホップを表す。
type Expectation struct {
	InputNodeCount       int
	InputRelationCount   int
	InputLimitCount      int
	TargetID             string
	ReachedNodes         []ReachedNode
	ReachedNodeIDs       []string
	ReachedRelations     []RelationValue
	ScopeEdges           []Edge
	Endpoints            []Endpoint
	SCCs                 []SCCExpectation
	Limits               LimitsExpectation
	LimitFacts           map[string]LimitExpectation
	TreeNodeCount        int
	HiddenConnections    []Edge
	UncoveredRoutes      []UncoveredExpectation
	UncoveredReasonCount map[string]int
	CycleRouteStepCount  []int
}

// Dataset は、同じ入力順と逆順を出力できるスナップショットと解析対象ごとの構成的期待値を保持する。
type Dataset struct {
	Name         string
	Nodes        []Node
	Relations    []Relation
	Expectations []Expectation
}

// Files は、展開済みスナップショット三ファイルのパスを保持する。
type Files struct {
	Directory string
	Manifest  string
	Nodes     string
	Relations string
}

// Small は、10,000ノードと25,000 relationを持つCI用データを返す。
func Small() Dataset { return scaleProfile("small", SmallNodeCount, SmallRelationCount) }

// Medium は、通常CIでは呼び出さない100,000ノード規模のデータを返す。
func Medium() Dataset { return scaleProfile("medium", MediumNodeCount, MediumRelationCount) }

// Scale は、通常CIでは呼び出さない1,000,000ノード規模のデータを返す。
func Scale() Dataset { return scaleProfile("scale", ScaleNodeCount, ScaleRelationCount) }

// Custom は、規模に対する取込と解析の増加特性を測定するデータを返す。
// 呼出側はSmall以上のノード数と、ノード数の2.5倍から3倍までのrelation数を指定する。
func Custom(nodeCount, relationCount int) Dataset {
	return scaleProfile(fmt.Sprintf("custom-%d-%d", nodeCount, relationCount), nodeCount, relationCount)
}

// Pathological は、指定した病理形状だけを含む軽量データを返す。
// kindはPathologicalCasesが返す値のいずれかでなければならない。
func Pathological(kind PathologicalCase) Dataset {
	switch kind {
	case LongChain:
		return chainDataset(string(kind), 2_001)
	case HighFanOut:
		return fanOutDataset()
	case HighFanIn:
		return fanInDataset()
	case LargeAndMultipleSCC:
		return multipleSCCDataset()
	case CycleWithExit:
		return cycleWithExitDataset()
	case DeepNestedNetworks:
		return nestedNetworksDataset()
	case ReachedNetworkWithOutbound:
		return reachedNetworkDataset()
	case ManyLimits:
		return manyLimitsDataset()
	case ParallelRelations:
		return parallelRelationsDataset()
	case LongCompression:
		return chainDataset(string(kind), 1_102)
	case CoveredAndUncoveredMerge:
		return coveredMergeDataset()
	case UncoveredCycleAndEndpoint:
		return uncoveredCycleDataset()
	case LargePathTreeSCC:
		return largePathTreeSCCDataset()
	default:
		panic(fmt.Sprintf("unknown pathological case %q", kind))
	}
}

// Archive は、importer.Runへ渡せるtar.gzを決定的なヘッダーと入力順で生成する。
func (dataset Dataset) Archive(reverse bool) ([]byte, error) {
	files, err := dataset.files(reverse)
	if err != nil {
		return nil, err
	}
	var result bytes.Buffer
	gzipWriter, err := gzip.NewWriterLevel(&result, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}
	gzipWriter.Header.ModTime = unixEpoch
	gzipWriter.Header.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)
	for _, name := range []string{"manifest.json", "nodes.ndjson", "relations.ndjson"} {
		contents := files[name]
		header := &tar.Header{Name: name, Mode: 0o600, Size: int64(len(contents)), Typeflag: tar.TypeReg, ModTime: unixEpoch}
		if err := tarWriter.WriteHeader(header); err != nil {
			return nil, err
		}
		if _, err := tarWriter.Write(contents); err != nil {
			return nil, err
		}
	}
	if err := tarWriter.Close(); err != nil {
		return nil, err
	}
	if err := gzipWriter.Close(); err != nil {
		return nil, err
	}
	return result.Bytes(), nil
}

// WriteFiles は、snapshot.Validateのベンチマークと検査が共有できる展開済み三ファイルを書く。
func (dataset Dataset) WriteFiles(directory string, reverse bool) (Files, error) {
	files, err := dataset.files(reverse)
	if err != nil {
		return Files{}, err
	}
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return Files{}, err
	}
	extracted := Files{
		Directory: directory,
		Manifest:  filepath.Join(directory, "manifest.json"),
		Nodes:     filepath.Join(directory, "nodes.ndjson"),
		Relations: filepath.Join(directory, "relations.ndjson"),
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(directory, name), contents, 0o600); err != nil {
			return Files{}, err
		}
	}
	return extracted, nil
}

func (dataset Dataset) files(reverse bool) (map[string][]byte, error) {
	nodes := slices.Clone(dataset.Nodes)
	relations := slices.Clone(dataset.Relations)
	if reverse {
		slices.Reverse(nodes)
		slices.Reverse(relations)
	}
	manifest, err := json.Marshal(map[string]any{
		"schemaVersion": "0.5", "snapshotId": "graphgen-" + dataset.Name,
		"generatedAt": generatedAt, "nodeCount": len(nodes), "relationCount": len(relations),
		"producer": map[string]string{"name": "batchscope-graphgen", "version": "1"},
	})
	if err != nil {
		return nil, err
	}
	nodeLines, err := ndjson(nodes)
	if err != nil {
		return nil, err
	}
	relationLines, err := ndjson(relations)
	if err != nil {
		return nil, err
	}
	return map[string][]byte{"manifest.json": manifest, "nodes.ndjson": nodeLines, "relations.ndjson": relationLines}, nil
}

func ndjson[T any](values []T) ([]byte, error) {
	var result bytes.Buffer
	writer := bufio.NewWriter(&result)
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	for _, value := range values {
		if err := encoder.Encode(value); err != nil {
			return nil, err
		}
	}
	if err := writer.Flush(); err != nil {
		return nil, err
	}
	return result.Bytes(), nil
}

type builder struct {
	name        string
	targetID    string
	nodes       []Node
	relations   []Relation
	membership  map[string]string
	sccs        []SCCExpectation
	treeNodes   int
	hidden      []Edge
	uncovered   []UncoveredExpectation
	endpointIDs map[string]string
}

func newBuilder(name, targetID string) *builder {
	return &builder{name: name, targetID: targetID, membership: make(map[string]string), endpointIDs: make(map[string]string)}
}

func (b *builder) node(kind, id, parentID, membership string, limits ...Limit) {
	var parent *string
	if parentID != "" {
		parentCopy := parentID
		parent = &parentCopy
	}
	if limits == nil {
		limits = []Limit{}
	}
	b.nodes = append(b.nodes, Node{Type: kind, ID: id, Name: id, ParentID: parent, LimitFacts: limits})
	if membership != "" {
		b.membership[id] = membership
	}
}

func (b *builder) relation(fromID, toID, kind string) {
	b.relationWith(fromID, toID, kind, defaultOrigin, defaultCertainty)
}

func (b *builder) relationWith(fromID, toID, kind, origin, certainty string) {
	b.relations = append(b.relations, Relation{FromID: fromID, ToID: toID, Kind: kind, Origin: origin, Certainty: certainty})
}

func (b *builder) finish() Dataset {
	expectation := buildExpectation(b.nodes, b.relations, expectationSpec{
		targetID: b.targetID, membership: b.membership, sccs: b.sccs,
		treeNodes: b.treeNodes, hidden: b.hidden, uncovered: b.uncovered, endpointIDs: b.endpointIDs,
	})
	return Dataset{Name: b.name, Nodes: b.nodes, Relations: b.relations, Expectations: []Expectation{expectation}}
}

type expectationSpec struct {
	targetID    string
	membership  map[string]string
	sccs        []SCCExpectation
	treeNodes   int
	hidden      []Edge
	uncovered   []UncoveredExpectation
	endpointIDs map[string]string
}

func buildExpectation(nodes []Node, relations []Relation, spec expectationSpec) Expectation {
	sccs := slices.Clone(spec.sccs)
	if sccs == nil {
		sccs = []SCCExpectation{}
	}
	expectation := Expectation{
		InputNodeCount: len(nodes), InputRelationCount: len(relations), TargetID: spec.targetID,
		TreeNodeCount: spec.treeNodes, HiddenConnections: slices.Clone(spec.hidden),
		SCCs: sccs, UncoveredRoutes: slices.Clone(spec.uncovered),
		ReachedNodes: []ReachedNode{}, ReachedNodeIDs: []string{}, ReachedRelations: []RelationValue{}, ScopeEdges: []Edge{}, Endpoints: []Endpoint{},
		LimitFacts: make(map[string]LimitExpectation), UncoveredReasonCount: make(map[string]int),
	}
	nodesByID := make(map[string]Node, len(nodes))
	for _, node := range nodes {
		nodesByID[node.ID] = node
	}
	for _, node := range nodes {
		membership, reached := spec.membership[node.ID]
		if reached {
			expectation.ReachedNodeIDs = append(expectation.ReachedNodeIDs, node.ID)
			reachedNode := ReachedNode{ID: node.ID, Type: node.Type, Name: node.Name, Membership: membership}
			if node.ParentID != nil {
				reachedNode.ParentID = *node.ParentID
				reachedNode.HasParent = true
			}
			expectation.ReachedNodes = append(expectation.ReachedNodes, reachedNode)
		}
		for _, limit := range node.LimitFacts {
			expectation.InputLimitCount++
			if reached {
				appendLimitExpectation(&expectation.Limits, membership, limit, node.ID)
				expectation.LimitFacts[limit.ID] = expectedLimit(limit, node, membership, nodesByID, spec.membership)
			}
		}
		if node.ParentID != nil {
			if _, parentReached := spec.membership[*node.ParentID]; parentReached && (node.Type == "job" || node.Type == "job_network") {
				expectation.ScopeEdges = append(expectation.ScopeEdges, Edge{FromID: *node.ParentID, ToID: node.ID})
			}
		}
	}
	for _, relation := range relations {
		if _, reached := spec.membership[relation.FromID]; reached {
			expectation.ReachedRelations = append(expectation.ReachedRelations, RelationValue{
				FromID: relation.FromID, ToID: relation.ToID, Kind: relation.Kind,
				Origin: relation.Origin, Certainty: relation.Certainty,
			})
		}
	}
	for id, reason := range spec.endpointIDs {
		node := nodesByID[id]
		expectation.Endpoints = append(expectation.Endpoints, Endpoint{NodeID: id, NodeType: node.Type, NodeName: node.Name, Reason: reason})
	}
	for _, route := range expectation.UncoveredRoutes {
		expectation.UncoveredReasonCount[route.Reason]++
	}
	for _, component := range expectation.SCCs {
		expectation.CycleRouteStepCount = append(expectation.CycleRouteStepCount, len(component.Route))
	}
	sortExpectation(&expectation)
	return expectation
}

func expectedLimit(limit Limit, owner Node, membership string, nodes map[string]Node, memberships map[string]string) LimitExpectation {
	result := LimitExpectation{
		ID: limit.ID, OwnerID: owner.ID, Kind: limit.Kind, LocalTime: limit.LocalTime, TimeZone: limit.TimeZone,
		SourceText: limit.SourceText, HasSourceText: limit.SourceText != "", Origin: limit.Origin, Certainty: limit.Certainty,
	}
	if limit.BusinessDayOffset != nil {
		converted := int64(*limit.BusinessDayOffset)
		result.BusinessDayOffset = &converted
	}
	if limit.Duration != "" {
		converted := int64(durationSeconds(limit.Duration))
		result.DurationSeconds = &converted
	}
	if membership != "downstream" || owner.ParentID == nil {
		return result
	}
	currentID := owner.ID
	for {
		current := nodes[currentID]
		if current.ParentID == nil {
			break
		}
		parentID := *current.ParentID
		if memberships[parentID] != "downstream" {
			break
		}
		result.ScopeRootID = parentID
		currentID = parentID
	}
	return result
}

func appendLimitExpectation(expectation *LimitsExpectation, membership string, limit Limit, ownerID string) {
	var destination *LimitIDs
	switch membership {
	case "target":
		destination = &expectation.Target
	case "contained":
		destination = &expectation.Contained
	case "downstream":
		destination = &expectation.Downstream
	default:
		return
	}
	key := limitSortKey(limit, ownerID)
	switch limit.Kind {
	case "finish_by":
		destination.FinishBy = append(destination.FinishBy, key)
	case "max_elapsed":
		destination.MaxElapsed = append(destination.MaxElapsed, key)
	case "raw":
		destination.Raw = append(destination.Raw, key)
	}
}

func limitSortKey(limit Limit, ownerID string) string {
	switch limit.Kind {
	case "finish_by":
		return fmt.Sprintf("%s\x00%+04d\x00%s\x00%s\x00%s", limit.TimeZone, value(limit.BusinessDayOffset), limit.LocalTime, ownerID, limit.ID)
	case "max_elapsed":
		return fmt.Sprintf("%012d\x00%s\x00%s", durationSeconds(limit.Duration), ownerID, limit.ID)
	default:
		return ownerID + "\x00" + limit.ID
	}
}

func sortExpectation(expectation *Expectation) {
	sort.Strings(expectation.ReachedNodeIDs)
	sort.Slice(expectation.ReachedNodes, func(i, j int) bool { return expectation.ReachedNodes[i].ID < expectation.ReachedNodes[j].ID })
	sort.Slice(expectation.ReachedRelations, func(i, j int) bool {
		left, right := expectation.ReachedRelations[i], expectation.ReachedRelations[j]
		return relationSortKey(left) < relationSortKey(right)
	})
	sort.Slice(expectation.ScopeEdges, func(i, j int) bool { return edgeKey(expectation.ScopeEdges[i]) < edgeKey(expectation.ScopeEdges[j]) })
	sort.Slice(expectation.Endpoints, func(i, j int) bool { return expectation.Endpoints[i].NodeID < expectation.Endpoints[j].NodeID })
	sort.Slice(expectation.UncoveredRoutes, func(i, j int) bool {
		left, right := expectation.UncoveredRoutes[i], expectation.UncoveredRoutes[j]
		return left.NodeID+"\x00"+left.Reason < right.NodeID+"\x00"+right.Reason
	})
	sortLimitIDs := func(ids *LimitIDs) {
		sort.Strings(ids.FinishBy)
		sort.Strings(ids.MaxElapsed)
		sort.Strings(ids.Raw)
		stripLimitSortKeys(ids)
	}
	sortLimitIDs(&expectation.Limits.Target)
	sortLimitIDs(&expectation.Limits.Contained)
	sortLimitIDs(&expectation.Limits.Downstream)
}

func stripLimitSortKeys(ids *LimitIDs) {
	for _, values := range []*[]string{&ids.FinishBy, &ids.MaxElapsed, &ids.Raw} {
		for index, value := range *values {
			parts := strings.Split(value, "\x00")
			(*values)[index] = parts[len(parts)-1]
		}
	}
}

func relationSortKey(value RelationValue) string {
	return value.FromID + "\x00" + value.ToID + "\x00" + value.Kind + "\x00" + value.Origin + "\x00" + value.Certainty
}

func edgeKey(edge Edge) string { return edge.FromID + "\x00" + edge.ToID }

func value(pointer *int) int {
	if pointer == nil {
		return 0
	}
	return *pointer
}

func durationSeconds(duration string) int {
	var seconds int
	_, _ = fmt.Sscanf(duration, "PT%dS", &seconds)
	return seconds
}

func rawLimit(id string) Limit {
	return Limit{ID: id, Kind: "raw", SourceText: id, Origin: defaultOrigin, Certainty: defaultCertainty}
}

func elapsedLimit(id string, seconds int) Limit {
	return Limit{ID: id, Kind: "max_elapsed", Duration: fmt.Sprintf("PT%dS", seconds), Origin: defaultOrigin, Certainty: defaultCertainty}
}

func finishLimit(id string, offset int, hour int) Limit {
	return Limit{ID: id, Kind: "finish_by", BusinessDayOffset: &offset, LocalTime: fmt.Sprintf("%02d:00:00", hour), TimeZone: "Asia/Tokyo", Origin: defaultOrigin, Certainty: defaultCertainty}
}

func scaleProfile(name string, nodeCount, relationCount int) Dataset {
	b := newBuilder(name, "")
	b.node("management_unit", "UNIT", "", "")
	b.node("job_network", "NET-TARGET", "UNIT", "")
	b.node("job_network", "NET-TARGET-NESTED", "NET-TARGET", "")
	b.node("job_network", "NET-BULK", "UNIT", "")
	b.node("job_network", "NET-DOWNSTREAM", "UNIT", "")
	b.node("job_network", "NET-DOWNSTREAM-NESTED", "NET-DOWNSTREAM", "")

	resourceCount := nodeCount / 10
	compressionCount := max(64, nodeCount/100)
	const fixedNodeCount = 1 + 5 + 3
	regularCount := nodeCount - fixedNodeCount - resourceCount - compressionCount
	if regularCount < 32 {
		panic(fmt.Sprintf("scale profile %q is too small for layered generation", name))
	}

	targetLimits := []Limit{
		finishLimit("LIMIT-TARGET-FINISH", 0, 6),
		elapsedLimit("LIMIT-TARGET-ELAPSED", 3_600),
		rawLimit("LIMIT-TARGET-RAW"),
	}
	b.node("job", "JOB-TARGET", "NET-TARGET-NESTED", "", targetLimits...)
	b.node("job", "JOB-UNCOVERED", "NET-TARGET", "")
	b.node("job", "JOB-DOWNSTREAM-LIMIT", "NET-DOWNSTREAM-NESTED", "",
		finishLimit("LIMIT-DOWNSTREAM-FINISH", 1, 5),
		elapsedLimit("LIMIT-DOWNSTREAM-ELAPSED", 7_200),
		rawLimit("LIMIT-DOWNSTREAM-RAW"),
	)

	compressionIDs := make([]string, compressionCount)
	for index := range compressionIDs {
		compressionIDs[index] = fmt.Sprintf("COMPRESS-%07d", index)
		b.node("job", compressionIDs[index], "NET-BULK", "")
	}
	regularIDs := make([]string, regularCount)
	for index := range regularIDs {
		id := fmt.Sprintf("JOB-%07d", index)
		regularIDs[index] = id
		limits := []Limit(nil)
		if index%211 == 0 {
			switch (index / 211) % 3 {
			case 0:
				limits = []Limit{finishLimit("LIMIT-"+id, index%3, index%24)}
			case 1:
				limits = []Limit{elapsedLimit("LIMIT-"+id, index%7200+1)}
			case 2:
				limits = []Limit{rawLimit("LIMIT-" + id)}
			}
		}
		b.node("job", id, "NET-BULK", "", limits...)
	}

	resourceIDs := make([]string, resourceCount)
	resourceKinds := []string{"file", "file_pattern", "job_status", "external_event"}
	for index := 0; index < resourceCount; index++ {
		id := fmt.Sprintf("RESOURCE-%07d", index)
		resourceIDs[index] = id
		b.node(resourceKinds[index%len(resourceKinds)], id, "", "")
	}

	// 圧縮区間には追加辺を接続せず、規模に比例して伸びる直列区間の全ホップが
	// HiddenConnectionsへ保存されるという不変条件を生成規則だけで確定できるようにする。
	b.relation("JOB-TARGET", compressionIDs[0], "precedes")
	for index := 0; index+1 < len(compressionIDs); index++ {
		b.relation(compressionIDs[index], compressionIDs[index+1], "precedes")
	}
	b.relation(compressionIDs[len(compressionIDs)-1], regularIDs[0], "precedes")
	for index := 0; index+1 < len(regularIDs); index++ {
		b.relation(regularIDs[index], regularIDs[index+1], "precedes")
	}

	cycleStarts := []int{regularCount / 4, regularCount / 2, regularCount * 3 / 4}
	cycleForIndex := make(map[int]int, len(cycleStarts)*3)
	sccs := make([]SCCExpectation, 0, len(cycleStarts))
	for cycleIndex, start := range cycleStarts {
		ids := []string{regularIDs[start], regularIDs[start+1], regularIDs[start+2]}
		for index := start; index < start+3; index++ {
			cycleForIndex[index] = cycleIndex
		}
		b.relation(ids[2], ids[0], "precedes")
		sccs = append(sccs, SCCExpectation{
			NodeIDs: ids,
			Route:   []Edge{{FromID: ids[0], ToID: ids[1]}, {FromID: ids[1], ToID: ids[2]}, {FromID: ids[2], ToID: ids[0]}},
		})
	}

	b.relation(regularIDs[regularCount/3], "NET-DOWNSTREAM", "precedes")
	for index, id := range resourceIDs {
		b.relation(regularIDs[len(regularIDs)-1], id, resourceRelationKind(resourceKinds[index%len(resourceKinds)]))
	}
	b.relation("JOB-UNCOVERED", resourceIDs[0], "precedes")

	// ノード数の平方根を層幅とする前向き辺は、直列の最長経路を残したまま、各層へ多数の分岐と合流を加える。
	// 代表経路の深さも平方根に比例して伸びるため、Smallの-race検査で経路コピー量を抑えつつ深さを定数にしない。
	// SCC内の近道だけは除外し、三辺の循環表示経路を規則から一意に保つ。
	layerWidth := integerSquareRoot(len(regularIDs))
	for band := 1; len(b.relations) < relationCount; band++ {
		for _, distance := range []int{band * layerWidth, band*layerWidth + 1} {
			if distance >= len(regularIDs) {
				panic(fmt.Sprintf("scale profile %q cannot reach %d relations", name, relationCount))
			}
			for from := 0; from+distance < len(regularIDs) && len(b.relations) < relationCount; from++ {
				to := from + distance
				if left, inCycle := cycleForIndex[from]; inCycle && cycleForIndex[to] == left {
					continue
				}
				b.relation(regularIDs[from], regularIDs[to], "precedes")
			}
		}
	}

	networkMembership := map[string]string{
		"NET-TARGET": "target", "NET-TARGET-NESTED": "contained",
		"JOB-TARGET": "contained", "JOB-UNCOVERED": "contained",
		"NET-DOWNSTREAM": "downstream", "NET-DOWNSTREAM-NESTED": "downstream",
		"JOB-DOWNSTREAM-LIMIT": "downstream",
	}
	jobMembership := map[string]string{
		"JOB-TARGET": "target", "NET-DOWNSTREAM": "downstream",
		"NET-DOWNSTREAM-NESTED": "downstream", "JOB-DOWNSTREAM-LIMIT": "downstream",
	}
	for _, id := range append(slices.Clone(compressionIDs), regularIDs...) {
		networkMembership[id] = "downstream"
		jobMembership[id] = "downstream"
	}
	for _, id := range resourceIDs {
		networkMembership[id] = "downstream"
		jobMembership[id] = "downstream"
	}
	hidden := make([]Edge, 0, len(compressionIDs)+1)
	hidden = append(hidden, Edge{FromID: "JOB-TARGET", ToID: compressionIDs[0]})
	for index := 0; index+1 < len(compressionIDs); index++ {
		hidden = append(hidden, Edge{FromID: compressionIDs[index], ToID: compressionIDs[index+1]})
	}
	hidden = append(hidden, Edge{FromID: compressionIDs[len(compressionIDs)-1], ToID: regularIDs[0]})

	dataset := Dataset{Name: name, Nodes: b.nodes, Relations: b.relations}
	dataset.Expectations = []Expectation{
		buildExpectation(dataset.Nodes, dataset.Relations, expectationSpec{
			targetID: "NET-TARGET", membership: networkMembership, sccs: sccs,
			treeNodes: scaleTreeNodeCount(relationCount, 5, compressionCount), hidden: hidden,
			uncovered: []UncoveredExpectation{{NodeID: resourceIDs[0], Reason: "terminal_without_limit"}},
		}),
		buildExpectation(dataset.Nodes, dataset.Relations, expectationSpec{
			targetID: "JOB-TARGET", membership: jobMembership, sccs: sccs,
			treeNodes: scaleTreeNodeCount(relationCount-1, 2, compressionCount), hidden: hidden,
			uncovered: []UncoveredExpectation{},
		}),
	}
	return dataset
}

func scaleTreeNodeCount(relationEdges, scopeEdges, compressionCount int) int {
	// 代表経路の木はrootと到達可能な各接続の出現を一つずつ持つ。
	// 追加辺を隔離した直列区間だけが圧縮されるため、その中間ジョブ数だけを構成時に差し引ける。
	return 1 + relationEdges + scopeEdges - compressionCount
}

func integerSquareRoot(value int) int {
	root := 1
	for (root+1)*(root+1) <= value {
		root++
	}
	return root
}

func resourceRelationKind(nodeType string) string {
	switch nodeType {
	case "file", "file_pattern":
		return "produces"
	case "job_status":
		return "observed_by"
	default:
		return "triggers"
	}
}

func chainDataset(name string, edges int) Dataset {
	b := newBuilder(name, "CHAIN-0000")
	for index := 0; index <= edges; index++ {
		id := fmt.Sprintf("CHAIN-%04d", index)
		membership := "downstream"
		if index == 0 {
			membership = "target"
		}
		limits := []Limit(nil)
		if index == edges {
			limits = []Limit{rawLimit("LIMIT-CHAIN-END")}
		}
		b.node("job", id, "", membership, limits...)
		if index > 0 {
			b.relation(fmt.Sprintf("CHAIN-%04d", index-1), id, "precedes")
		}
	}
	b.treeNodes = 2
	for index := 0; index < edges; index++ {
		b.hidden = append(b.hidden, Edge{FromID: fmt.Sprintf("CHAIN-%04d", index), ToID: fmt.Sprintf("CHAIN-%04d", index+1)})
	}
	return b.finish()
}

func fanOutDataset() Dataset {
	b := newBuilder(string(HighFanOut), "FANOUT-ROOT")
	b.node("job", b.targetID, "", "target")
	for index := 0; index < 256; index++ {
		id := fmt.Sprintf("FANOUT-%03d", index)
		b.node("job", id, "", "downstream", rawLimit("LIMIT-"+id))
		b.relation(b.targetID, id, "precedes")
	}
	b.treeNodes = 257
	return b.finish()
}

func fanInDataset() Dataset {
	b := newBuilder(string(HighFanIn), "FANIN-ROOT")
	b.node("job", b.targetID, "", "target")
	b.node("job", "FANIN-JOIN", "", "downstream", rawLimit("LIMIT-FANIN-JOIN"))
	for index := 0; index < 256; index++ {
		id := fmt.Sprintf("FANIN-%03d", index)
		b.node("job", id, "", "downstream")
		b.relation(b.targetID, id, "precedes")
		b.relation(id, "FANIN-JOIN", "precedes")
	}
	b.treeNodes = 257
	for index := 0; index < 256; index++ {
		id := fmt.Sprintf("FANIN-%03d", index)
		b.hidden = append(b.hidden, Edge{FromID: b.targetID, ToID: id}, Edge{FromID: id, ToID: "FANIN-JOIN"})
	}
	return b.finish()
}

func addRing(b *builder, prefix string, size int) SCCExpectation {
	nodes := make([]string, size)
	for index := range size {
		nodes[index] = fmt.Sprintf("%s-%03d", prefix, index)
		b.node("job", nodes[index], "", "downstream")
	}
	route := make([]Edge, size)
	for index := range size {
		next := (index + 1) % size
		b.relation(nodes[index], nodes[next], "precedes")
		route[index] = Edge{FromID: nodes[index], ToID: nodes[next]}
	}
	return SCCExpectation{NodeIDs: nodes, Route: route}
}

func multipleSCCDataset() Dataset {
	b := newBuilder(string(LargeAndMultipleSCC), "SCC-ROOT")
	b.node("job", b.targetID, "", "target")
	for _, spec := range []struct {
		prefix string
		size   int
	}{{"SCC-A", 128}, {"SCC-B", 4}, {"SCC-C", 4}} {
		component := addRing(b, spec.prefix, spec.size)
		b.sccs = append(b.sccs, component)
		b.relation(b.targetID, component.NodeIDs[0], "precedes")
		b.uncovered = append(b.uncovered, UncoveredExpectation{NodeID: component.NodeIDs[0], Reason: "cycle_without_limit"})
	}
	b.treeNodes = 1 + len(b.relations)
	return b.finish()
}

func cycleWithExitDataset() Dataset {
	b := newBuilder(string(CycleWithExit), "EXIT-00")
	b.node("job", b.targetID, "", "target")
	component := make([]string, 8)
	component[0] = b.targetID
	for index := 1; index < len(component); index++ {
		component[index] = fmt.Sprintf("EXIT-%02d", index)
		b.node("job", component[index], "", "downstream")
	}
	route := make([]Edge, len(component))
	for index := range component {
		next := (index + 1) % len(component)
		b.relation(component[index], component[next], "precedes")
		route[index] = Edge{FromID: component[index], ToID: component[next]}
	}
	b.node("job", "EXIT-LIMIT", "", "downstream", rawLimit("LIMIT-EXIT"))
	b.relation(component[3], "EXIT-LIMIT", "precedes")
	b.sccs = []SCCExpectation{{NodeIDs: component, Route: route}}
	b.uncovered = []UncoveredExpectation{{NodeID: b.targetID, Reason: "cycle_without_limit"}}
	b.treeNodes = 1 + len(b.relations)
	return b.finish()
}

func nestedNetworksDataset() Dataset {
	b := newBuilder(string(DeepNestedNetworks), "NEST-NET-000")
	for index := 0; index < 128; index++ {
		id := fmt.Sprintf("NEST-NET-%03d", index)
		parent := ""
		membership := "contained"
		if index == 0 {
			membership = "target"
		} else {
			parent = fmt.Sprintf("NEST-NET-%03d", index-1)
		}
		b.node("job_network", id, parent, membership)
	}
	b.node("job", "NEST-JOB", "NEST-NET-127", "contained", rawLimit("LIMIT-NEST"))
	b.treeNodes = 129
	return b.finish()
}

func reachedNetworkDataset() Dataset {
	b := newBuilder(string(ReachedNetworkWithOutbound), "REACHED-ROOT")
	b.node("job", b.targetID, "", "target")
	b.node("job_network", "REACHED-NET", "", "downstream")
	b.node("job", "REACHED-CHILD", "REACHED-NET", "downstream")
	b.node("job", "REACHED-LIMIT", "", "downstream", rawLimit("LIMIT-REACHED"))
	b.relation(b.targetID, "REACHED-NET", "precedes")
	b.relation("REACHED-CHILD", "REACHED-LIMIT", "precedes")
	b.treeNodes = 4
	return b.finish()
}

func manyLimitsDataset() Dataset {
	b := newBuilder(string(ManyLimits), "LIMITS-ROOT")
	b.node("job", b.targetID, "", "target", rawLimit("LIMIT-TARGET"))
	for index := 0; index < 300; index++ {
		id := fmt.Sprintf("LIMITS-%03d", index)
		b.node("job", id, "", "downstream",
			finishLimit("FINISH-"+id, index%3, index%24), elapsedLimit("ELAPSED-"+id, index+1), rawLimit("RAW-"+id))
		b.relation(b.targetID, id, "precedes")
	}
	b.treeNodes = 301
	return b.finish()
}

func parallelRelationsDataset() Dataset {
	b := newBuilder(string(ParallelRelations), "PARALLEL-ROOT")
	b.node("job", b.targetID, "", "target")
	b.node("job", "PARALLEL-END", "", "downstream", rawLimit("LIMIT-PARALLEL"))
	b.relationWith(b.targetID, "PARALLEL-END", "precedes", "scheduler", "declared")
	b.relationWith(b.targetID, "PARALLEL-END", "triggers", "deterministic_analysis", "confirmed")
	b.relationWith(b.targetID, "PARALLEL-END", "observed_by", "ai_analysis", "inferred")
	b.treeNodes = 2
	return b.finish()
}

func coveredMergeDataset() Dataset {
	b := newBuilder(string(CoveredAndUncoveredMerge), "MERGE-ROOT")
	b.node("job", b.targetID, "", "target")
	b.node("job", "A-LIMITED", "", "downstream", rawLimit("LIMIT-MERGE"))
	b.node("job", "B-CLEAR", "", "downstream")
	b.node("job", "MERGE-END", "", "downstream")
	b.relation(b.targetID, "A-LIMITED", "precedes")
	b.relation("A-LIMITED", "MERGE-END", "precedes")
	b.relation(b.targetID, "B-CLEAR", "precedes")
	b.relation("B-CLEAR", "MERGE-END", "precedes")
	b.treeNodes = 4
	b.hidden = []Edge{{FromID: b.targetID, ToID: "B-CLEAR"}, {FromID: "B-CLEAR", ToID: "MERGE-END"}}
	b.uncovered = []UncoveredExpectation{{NodeID: "MERGE-END", Reason: "terminal_without_limit"}}
	return b.finish()
}

func uncoveredCycleDataset() Dataset {
	b := newBuilder(string(UncoveredCycleAndEndpoint), "UNCOVERED-ROOT")
	b.node("job", b.targetID, "", "target")
	b.node("job", "UNCOVERED-A", "", "downstream")
	b.node("job", "UNCOVERED-B", "", "downstream")
	b.node("management_unit", "UNCOVERED-ENDPOINT", "", "")
	b.relation(b.targetID, "UNCOVERED-A", "precedes")
	b.relation("UNCOVERED-A", "UNCOVERED-B", "precedes")
	b.relation("UNCOVERED-B", "UNCOVERED-A", "precedes")
	b.relation(b.targetID, "UNCOVERED-ENDPOINT", "precedes")
	b.endpointIDs["UNCOVERED-ENDPOINT"] = "non_traversable_node_type"
	b.sccs = []SCCExpectation{{NodeIDs: []string{"UNCOVERED-A", "UNCOVERED-B"}, Route: []Edge{{"UNCOVERED-A", "UNCOVERED-B"}, {"UNCOVERED-B", "UNCOVERED-A"}}}}
	b.uncovered = []UncoveredExpectation{{NodeID: "UNCOVERED-A", Reason: "cycle_without_limit"}, {NodeID: "UNCOVERED-ENDPOINT", Reason: "non_traversable_node_type"}}
	b.treeNodes = 1 + len(b.relations)
	return b.finish()
}

func largePathTreeSCCDataset() Dataset {
	b := newBuilder(string(LargePathTreeSCC), "PATH-SCC-ROOT")
	b.node("job", b.targetID, "", "target")
	component := addRing(b, "PATH-SCC", 400)
	b.relation(b.targetID, component.NodeIDs[0], "precedes")
	b.sccs = []SCCExpectation{component}
	b.uncovered = []UncoveredExpectation{{NodeID: component.NodeIDs[0], Reason: "cycle_without_limit"}}
	b.treeNodes = 1 + len(b.relations)
	return b.finish()
}

// unixEpochはアーカイブをバイト単位でも決定的にする。
var unixEpoch = time.Unix(0, 0).UTC()
