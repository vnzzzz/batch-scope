// Package traversal は、SQLiteに保存された依存関係を後続方向へ探索する。
package traversal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const queryBatchSize = 500

var (
	ErrTargetNotFound    = errors.New("traversal target not found")
	ErrInvalidTargetType = errors.New("traversal target must be a job or job_network")
)

// Node は探索結果に必要なノード情報を表す。
// PathとParentIDはSQLiteのNULLをnilのまま保持し、未設定と空文字列を混同しない。
type Node struct {
	ID       string
	Type     string
	Name     string
	Path     *string
	ParentID *string
}

// Relation は同じ二ノード間でも種類や根拠が異なる依存関係を区別する。
type Relation struct {
	ID        string
	Kind      string
	Origin    string
	Certainty string
	Evidence  json.RawMessage
}

// Membership は到達したノードと対象との関係を表す。
type Membership string

const (
	MembershipTarget     Membership = "target"
	MembershipContained  Membership = "contained"
	MembershipDownstream Membership = "downstream"
)

// Reached は探索したノードと、その対象との関係を表す。
type Reached struct {
	Node       Node
	Membership Membership
}

// Connection は一組のノード間にある依存関係をまとめて保持する。
type Connection struct {
	FromID    string
	ToID      string
	Relations []Relation
}

// ScopeEdge はジョブネットから直接の論理子ノードへのscope遷移を表す。
type ScopeEdge struct {
	ParentID string
	ChildID  string
}

// EndpointReason は、到達先から先を探索しなかった意味上の理由を表す。
type EndpointReason string

// EndpointNonTraversableNodeType は、到達先の種別を依存関係の中間ノードとして扱わないことを示す。
const EndpointNonTraversableNodeType EndpointReason = "non_traversable_node_type"

// Endpoint は、処理上限ではなく意味上の理由で探索を続けなかった到達地点を表す。
type Endpoint struct {
	Node   Node
	Reason EndpointReason
}

// Cycle は到達した依存部分グラフの循環成分を表す。
type Cycle struct {
	NodeIDs []string
}

// Stats は探索がSQLiteから読み込んだ量を表す。
// 各値は探索結果を省略する条件には使わない。
type Stats struct {
	ExpandedNodes   int
	RelationQueries int
	RelationRows    int
	ScopeQueries    int
	ScopeRows       int
}

// Result は到達可能範囲を全件探索した結果を保持する。
type Result struct {
	Target      Node
	Nodes       []Reached
	Connections []Connection
	ScopeEdges  []ScopeEdge
	Endpoints   []Endpoint
	Cycles      []Cycle
	Stats       Stats
}

type connectionKey struct {
	fromID string
	toID   string
}

type scopeEdgeKey struct {
	parentID string
	childID  string
}

type traversalState struct {
	ctx    context.Context
	db     *sql.DB
	target Node
	nodes  map[string]Node
	// pendingにはnodesへ初めて登録したIDだけが入り、同じIDは二度入らない。
	// このため、各job_networkのscopeも高々一度だけ展開される。
	// pendingHeadより前を取り出し済みとして残し、バッチごとのスライス再確保を避ける。
	pending     []string
	pendingHead int
	connections map[connectionKey]Connection
	scopeEdges  map[scopeEdgeKey]ScopeEdge
	endpoints   map[string]Endpoint
	stats       Stats
}

// Traverse はtargetIDから依存関係とジョブネットのscopeを通じて到達できる全ノードを返す。
// dbはstore.Store.Acquireで取得した検索用SQLite、または同じテーブル構成を持つDBでなければならない。
func Traverse(ctx context.Context, db *sql.DB, targetID string) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if db == nil {
		return Result{}, errors.New("traversal database is nil")
	}

	target, err := selectNode(ctx, db, targetID)
	if err != nil {
		return Result{}, err
	}
	if !isTargetType(target.Type) {
		return Result{}, fmt.Errorf("%w: %s", ErrInvalidTargetType, target.Type)
	}

	state := traversalState{
		ctx:         ctx,
		db:          db,
		target:      target,
		nodes:       map[string]Node{target.ID: target},
		pending:     []string{target.ID},
		connections: make(map[connectionKey]Connection),
		scopeEdges:  make(map[scopeEdgeKey]ScopeEdge),
		endpoints:   make(map[string]Endpoint),
	}
	if err := state.run(); err != nil {
		// 呼出側が不完全な結果を成功結果と取り違えないよう、失敗時は蓄積済みの内容を返さない。
		return Result{}, err
	}
	return state.finish()
}

func (state *traversalState) run() error {
	for state.pendingHead < len(state.pending) {
		if err := state.ctx.Err(); err != nil {
			return err
		}
		batch := state.takeBatch()
		networkIDs := make([]string, 0, len(batch))
		for _, id := range batch {
			node := state.nodes[id]
			if node.Type == "job_network" {
				networkIDs = append(networkIDs, id)
			}
		}
		if err := state.expandScopes(networkIDs); err != nil {
			return err
		}
		if err := state.expandRelations(batch); err != nil {
			return err
		}
	}
	return state.ctx.Err()
}

func (state *traversalState) takeBatch() []string {
	end := min(state.pendingHead+queryBatchSize, len(state.pending))
	batch := state.pending[state.pendingHead:end]
	state.pendingHead = end
	return batch
}

func (state *traversalState) expandScopes(networkIDs []string) error {
	if len(networkIDs) == 0 {
		return nil
	}

	args := make([]any, len(networkIDs))
	for index, id := range networkIDs {
		args[index] = id
	}
	query := `
SELECT node_id, node_type, name, path, parent_id
FROM node
WHERE parent_id IN (` + placeholders(len(networkIDs)) + `)
  AND node_type IN ('job', 'job_network')
ORDER BY parent_id, node_id`
	rows, err := state.db.QueryContext(state.ctx, query, args...)
	if err != nil {
		return fmt.Errorf("select scope children: %w", err)
	}
	state.stats.ScopeQueries++
	defer rows.Close()

	for rows.Next() {
		child, err := scanNode(rows)
		if err != nil {
			return fmt.Errorf("scan scope child: %w", err)
		}
		state.stats.ScopeRows++
		if child.ParentID == nil {
			return fmt.Errorf("scope child %q has no parent", child.ID)
		}
		if err := state.discover(child); err != nil {
			return err
		}
		key := scopeEdgeKey{parentID: *child.ParentID, childID: child.ID}
		state.scopeEdges[key] = ScopeEdge{ParentID: *child.ParentID, ChildID: child.ID}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("select scope children: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close scope children: %w", err)
	}
	return nil
}

func (state *traversalState) expandRelations(fromIDs []string) error {
	args := make([]any, len(fromIDs))
	for index, id := range fromIDs {
		args[index] = id
	}
	query := `
SELECT r.relation_id, r.from_id, r.to_id, r.relation_kind, r.origin, r.certainty,
       r.evidence_json, destination.node_id, destination.node_type, destination.name,
       destination.path, destination.parent_id
FROM relation AS r
LEFT JOIN node AS destination ON destination.node_id = r.to_id
WHERE r.from_id IN (` + placeholders(len(fromIDs)) + `)
ORDER BY r.from_id, r.to_id, r.relation_id`
	rows, err := state.db.QueryContext(state.ctx, query, args...)
	if err != nil {
		return fmt.Errorf("select outgoing relations: %w", err)
	}
	state.stats.RelationQueries++
	state.stats.ExpandedNodes += len(fromIDs)
	defer rows.Close()

	for rows.Next() {
		var relation Relation
		var fromID, toID string
		var evidence sql.NullString
		var destinationID, destinationType, destinationName sql.NullString
		var path, parentID sql.NullString
		if err := rows.Scan(
			&relation.ID, &fromID, &toID, &relation.Kind, &relation.Origin, &relation.Certainty,
			&evidence, &destinationID, &destinationType, &destinationName, &path, &parentID,
		); err != nil {
			return fmt.Errorf("scan outgoing relation: %w", err)
		}
		state.stats.RelationRows++
		if !destinationID.Valid || !destinationType.Valid || !destinationName.Valid {
			return fmt.Errorf("relation %q references missing destination node %q", relation.ID, toID)
		}
		if destinationID.String != toID {
			return fmt.Errorf("relation %q destination mismatch: relation=%q node=%q", relation.ID, toID, destinationID.String)
		}
		if evidence.Valid {
			relation.Evidence = json.RawMessage(evidence.String)
		}
		destination := Node{
			ID:       destinationID.String,
			Type:     destinationType.String,
			Name:     destinationName.String,
			Path:     nullableString(path),
			ParentID: nullableString(parentID),
		}
		key := connectionKey{fromID: fromID, toID: toID}
		connection := state.connections[key]
		connection.FromID = fromID
		connection.ToID = toID
		connection.Relations = append(connection.Relations, relation)
		state.connections[key] = connection

		if isTraversableType(destination.Type) {
			if err := state.discover(destination); err != nil {
				return err
			}
		} else {
			if existing, ok := state.endpoints[destination.ID]; ok && !sameNode(existing.Node, destination) {
				return fmt.Errorf("node %q has inconsistent data", destination.ID)
			}
			state.endpoints[destination.ID] = Endpoint{
				Node: destination, Reason: EndpointNonTraversableNodeType,
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("select outgoing relations: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close outgoing relations: %w", err)
	}
	return nil
}

func (state *traversalState) discover(node Node) error {
	if existing, ok := state.nodes[node.ID]; ok {
		if !sameNode(existing, node) {
			return fmt.Errorf("node %q has inconsistent data", node.ID)
		}
		return nil
	}
	state.nodes[node.ID] = node
	state.pending = append(state.pending, node.ID)
	return nil
}

func (state *traversalState) finish() (Result, error) {
	if err := state.ctx.Err(); err != nil {
		return Result{}, err
	}

	connections := make([]Connection, 0, len(state.connections))
	for _, connection := range state.connections {
		sort.Slice(connection.Relations, func(i, j int) bool {
			return connection.Relations[i].ID < connection.Relations[j].ID
		})
		connections = append(connections, connection)
	}
	sort.Slice(connections, func(i, j int) bool {
		if connections[i].FromID != connections[j].FromID {
			return connections[i].FromID < connections[j].FromID
		}
		return connections[i].ToID < connections[j].ToID
	})

	scopeEdges := make([]ScopeEdge, 0, len(state.scopeEdges))
	for _, edge := range state.scopeEdges {
		scopeEdges = append(scopeEdges, edge)
	}
	sort.Slice(scopeEdges, func(i, j int) bool {
		if scopeEdges[i].ParentID != scopeEdges[j].ParentID {
			return scopeEdges[i].ParentID < scopeEdges[j].ParentID
		}
		return scopeEdges[i].ChildID < scopeEdges[j].ChildID
	})

	contained, err := containedNodeIDs(state.ctx, state.target, scopeEdges)
	if err != nil {
		return Result{}, err
	}
	nodeIDs := make([]string, 0, len(state.nodes))
	for id := range state.nodes {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Strings(nodeIDs)
	nodes := make([]Reached, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		if err := state.ctx.Err(); err != nil {
			return Result{}, err
		}
		membership := MembershipDownstream
		if id == state.target.ID {
			membership = MembershipTarget
		} else if _, ok := contained[id]; ok {
			membership = MembershipContained
		}
		nodes = append(nodes, Reached{Node: state.nodes[id], Membership: membership})
	}

	endpointIDs := make([]string, 0, len(state.endpoints))
	for id := range state.endpoints {
		endpointIDs = append(endpointIDs, id)
	}
	sort.Strings(endpointIDs)
	endpoints := make([]Endpoint, 0, len(endpointIDs))
	for _, id := range endpointIDs {
		endpoints = append(endpoints, state.endpoints[id])
	}

	cycles, err := findCycles(state.ctx, connections, scopeEdges)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Target:      state.target,
		Nodes:       nodes,
		Connections: connections,
		ScopeEdges:  scopeEdges,
		Endpoints:   endpoints,
		Cycles:      cycles,
		Stats:       state.stats,
	}, nil
}

func selectNode(ctx context.Context, db *sql.DB, id string) (Node, error) {
	var node Node
	var path, parentID sql.NullString
	err := db.QueryRowContext(ctx, `
SELECT node_id, node_type, name, path, parent_id
FROM node
WHERE node_id = ?`, id).Scan(&node.ID, &node.Type, &node.Name, &path, &parentID)
	if errors.Is(err, sql.ErrNoRows) {
		return Node{}, fmt.Errorf("%w: %s", ErrTargetNotFound, id)
	}
	if err != nil {
		return Node{}, fmt.Errorf("select traversal target: %w", err)
	}
	node.Path = nullableString(path)
	node.ParentID = nullableString(parentID)
	return node, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanNode(row rowScanner) (Node, error) {
	var node Node
	var path, parentID sql.NullString
	if err := row.Scan(&node.ID, &node.Type, &node.Name, &path, &parentID); err != nil {
		return Node{}, err
	}
	node.Path = nullableString(path)
	node.ParentID = nullableString(parentID)
	return node, nil
}

func nullableString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}

func sameNode(left, right Node) bool {
	return left.ID == right.ID && left.Type == right.Type && left.Name == right.Name &&
		sameOptionalString(left.Path, right.Path) && sameOptionalString(left.ParentID, right.ParentID)
}

func sameOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func isTargetType(nodeType string) bool {
	return nodeType == "job" || nodeType == "job_network"
}

func isTraversableType(nodeType string) bool {
	switch nodeType {
	case "job", "job_network", "file", "file_pattern", "job_status", "external_event":
		return true
	default:
		return false
	}
}

func placeholders(count int) string {
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}

func containedNodeIDs(ctx context.Context, target Node, edges []ScopeEdge) (map[string]struct{}, error) {
	contained := make(map[string]struct{})
	if target.Type != "job_network" {
		return contained, nil
	}
	children := make(map[string][]string)
	for _, edge := range edges {
		children[edge.ParentID] = append(children[edge.ParentID], edge.ChildID)
	}
	queue := append([]string(nil), children[target.ID]...)
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id := queue[0]
		queue = queue[1:]
		if _, seen := contained[id]; seen {
			continue
		}
		contained[id] = struct{}{}
		queue = append(queue, children[id]...)
	}
	return contained, nil
}

// findCyclesはscopeと依存関係を同じ有向グラフとして強連結成分へ分解する。
// scopeを含めることで、子ジョブから親ジョブネットへ戻るrelationが作る循環を検出できる。
// 全単純閉路ではなく成分を返すため、大きな循環でも結果件数が閉路の組合せ数に膨らまない。
func findCycles(ctx context.Context, connections []Connection, scopeEdges []ScopeEdge) ([]Cycle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	adjacency := make(map[string][]string)
	reverse := make(map[string][]string)
	vertices := make(map[string]struct{})
	selfLoops := make(map[string]struct{})
	for _, connection := range connections {
		vertices[connection.FromID] = struct{}{}
		vertices[connection.ToID] = struct{}{}
		adjacency[connection.FromID] = append(adjacency[connection.FromID], connection.ToID)
		reverse[connection.ToID] = append(reverse[connection.ToID], connection.FromID)
		if connection.FromID == connection.ToID {
			selfLoops[connection.FromID] = struct{}{}
		}
	}
	for _, edge := range scopeEdges {
		vertices[edge.ParentID] = struct{}{}
		vertices[edge.ChildID] = struct{}{}
		adjacency[edge.ParentID] = append(adjacency[edge.ParentID], edge.ChildID)
		reverse[edge.ChildID] = append(reverse[edge.ChildID], edge.ParentID)
	}
	ids := make([]string, 0, len(vertices))
	for id := range vertices {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for id := range vertices {
		sort.Strings(adjacency[id])
		sort.Strings(reverse[id])
	}

	visited := make(map[string]struct{}, len(vertices))
	order := make([]string, 0, len(vertices))
	for _, id := range ids {
		if _, seen := visited[id]; seen {
			continue
		}
		if err := appendFinishOrder(ctx, id, adjacency, visited, &order); err != nil {
			return nil, err
		}
	}

	assigned := make(map[string]struct{}, len(vertices))
	cycles := make([]Cycle, 0)
	for index := len(order) - 1; index >= 0; index-- {
		start := order[index]
		if _, seen := assigned[start]; seen {
			continue
		}
		component, err := collectComponent(ctx, start, reverse, assigned)
		if err != nil {
			return nil, err
		}
		if len(component) == 1 {
			if _, selfLoop := selfLoops[component[0]]; !selfLoop {
				continue
			}
		}
		sort.Strings(component)
		cycles = append(cycles, Cycle{NodeIDs: component})
	}
	sort.Slice(cycles, func(i, j int) bool {
		return cycles[i].NodeIDs[0] < cycles[j].NodeIDs[0]
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return cycles, nil
}

type depthFirstFrame struct {
	id   string
	next int
}

func appendFinishOrder(
	ctx context.Context,
	start string,
	adjacency map[string][]string,
	visited map[string]struct{},
	order *[]string,
) error {
	visited[start] = struct{}{}
	stack := []depthFirstFrame{{id: start}}
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		last := len(stack) - 1
		frame := &stack[last]
		neighbors := adjacency[frame.id]
		if frame.next < len(neighbors) {
			next := neighbors[frame.next]
			frame.next++
			if _, seen := visited[next]; !seen {
				visited[next] = struct{}{}
				stack = append(stack, depthFirstFrame{id: next})
			}
			continue
		}
		*order = append(*order, frame.id)
		stack = stack[:last]
	}
	return nil
}

func collectComponent(
	ctx context.Context,
	start string,
	reverse map[string][]string,
	assigned map[string]struct{},
) ([]string, error) {
	assigned[start] = struct{}{}
	stack := []string{start}
	component := make([]string, 0)
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		last := len(stack) - 1
		id := stack[last]
		stack = stack[:last]
		component = append(component, id)
		for _, next := range reverse[id] {
			if _, seen := assigned[next]; seen {
				continue
			}
			assigned[next] = struct{}{}
			stack = append(stack, next)
		}
	}
	return component, nil
}
