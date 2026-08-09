// Package traversal は、SQLiteに保存された依存関係を後続方向へ探索する。
package traversal

import (
	"container/heap"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	// DefaultMaxVisitedNodes は、一回の探索が保持する訪問状態の量を制限する。
	// 上限へ達した結果は完了扱いにせず、Result.Frontierに未調査の開始地点を残す。
	DefaultMaxVisitedNodes = 2_000_000
	// DefaultMaxGraphDepth は、異常に長い依存経路による処理時間と経路状態の肥大化を防ぐ。
	DefaultMaxGraphDepth = 1_000
	// DefaultMaxConnections は、依存関係とscope展開でSQLiteから受け取る行数を制限する。
	// 上限到達時は未確認の取得元をFrontierへ残し、全件を確認した結果として扱わない。
	DefaultMaxConnections = 4_000_000

	queryBatchSize = 500
)

var (
	ErrTargetNotFound    = errors.New("traversal target not found")
	ErrInvalidTargetType = errors.New("traversal target must be a job or job_network")
)

// Limits は探索内部の処理量を制限する。ゼロ値には既定値を適用する。
// MaxConnectionsは、外向きrelation、scope配下ノード、scopeのincoming relationで読み込む行の合計へ適用する。
type Limits struct {
	MaxVisitedNodes int
	MaxGraphDepth   int
	MaxConnections  int
}

// Node は探索と経路整形に必要なノード情報を表す。
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

// Visit は実際に後続を調べたノードと、採用した経路上の二種類の距離を表す。
type Visit struct {
	Node               Node
	GraphDepth         int
	DependencyDistance int
}

// Connection は一組のノード間にある依存関係をまとめて保持する。
// 距離はこの接続を通ってToへ到達した時点の値であり、Toの別経路上の最短値とは限らない。
type Connection struct {
	FromID             string
	To                 Node
	GraphDepth         int
	DependencyDistance int
	Relations          []Relation
}

// Downstream は対象範囲の外で到達したjobまたはjob_networkを表す。
// ConfirmedDependencyDistanceはdeclaredまたはconfirmedの経路で到達できない場合にnilとなる。
type Downstream struct {
	Node                        Node
	GraphDepth                  int
	DependencyDistance          int
	ConfirmedDependencyDistance *int
}

// UnexploredReason は、依存関係の到達先から先を探索しなかった理由を表す。
type UnexploredReason string

// UnexploredNodeType は、到達先の種別を実行順の中間ノードとして扱わないことを示す。
const UnexploredNodeType UnexploredReason = "non_traversable_node_type"

// Unexplored は、処理上限とは別の理由で後続を調べなかった到達地点を表す。
type Unexplored struct {
	Node               Node
	GraphDepth         int
	DependencyDistance int
	Reason             UnexploredReason
}

// Shared は、決定的な深さ優先走査で別経路の処理済みノードへ合流した接続を表す。
type Shared struct {
	FromID string
	ToID   string
}

// Cycle は有向循環を一周するノード列を表す。
// Pathは辞書順で正規化し、閉路であることが分かるよう先頭IDを末尾にも保持する。
type Cycle struct {
	Path []string
}

// TruncationReason は探索を完了できなかった処理上限を表す。
type TruncationReason string

const (
	TruncationNodeLimit       TruncationReason = "node_limit"
	TruncationGraphDepthLimit TruncationReason = "graph_depth_limit"
	TruncationConnectionLimit TruncationReason = "connection_limit"
)

// Frontier は処理上限により後続を調べなかった地点を表す。
type Frontier struct {
	Node               Node
	GraphDepth         int
	DependencyDistance int
	Reason             TruncationReason
}

// ScopeEntry は、ジョブネットの親子関係を依存接続へ変換せずに探索した配下の入口を表す。
// ScopeRootIDにより、対象または探索途中で到達したどのジョブネットから展開したかを区別する。
type ScopeEntry struct {
	ScopeRootID string
	Node        Node
	Fallback    bool
}

// Result は後続のリミット選定と経路ツリー作成に必要な探索結果を保持する。
// StartNodesは論理ルートだけを、ScopeEntriesは各ジョブネット配下の探索開始点を保持する。
// 各スライスは、同じ入力と処理上限に対して同じ順序で返す。
type Result struct {
	Target            Node
	StartNodes        []Node
	ScopeEntries      []ScopeEntry
	UsedScopeFallback bool
	Nodes             []Visit
	Connections       []Connection
	Downstream        []Downstream
	Unexplored        []Unexplored
	Cycles            []Cycle
	Shared            []Shared
	Frontier          []Frontier
	Truncated         bool
	TruncationReason  TruncationReason
}

type distance struct {
	graph      int
	dependency int
}

type queuedNode struct {
	node Node
	distance
}

type distanceHeap []distance

func (values distanceHeap) Len() int { return len(values) }

func (values distanceHeap) Less(i, j int) bool { return lessDistance(values[i], values[j]) }

func (values distanceHeap) Swap(i, j int) { values[i], values[j] = values[j], values[i] }

func (values *distanceHeap) Push(value any) { *values = append(*values, value.(distance)) }

func (values *distanceHeap) Pop() any {
	last := len(*values) - 1
	value := (*values)[last]
	*values = (*values)[:last]
	return value
}

type scopeState struct {
	rootID  string
	base    distance
	members []Node
}

type traversalState struct {
	ctx              context.Context
	db               *sql.DB
	limits           Limits
	result           Result
	targetScope      map[string]struct{}
	best             map[string]queuedNode
	processed        map[string]struct{}
	queueBuckets     map[distance][]queuedNode
	queueDistances   distanceHeap
	scopes           []*scopeState
	registeredScopes map[string]struct{}
	downstreamByID   map[string]int
	unexploredByKey  map[unexploredKey]int
	frontierByKey    map[frontierKey]int
	connectionsByKey map[connectionKey]int
	scopeEntries     map[string]struct{}
	connectionsRead  int
}

// Traverse はtargetIDの後続をSQLiteから段階ごとに取得する。
// dbはstore.Store.Acquireで取得した検索用SQLite、または同じテーブル構成を持つDBでなければならない。
func Traverse(ctx context.Context, db *sql.DB, targetID string, limits Limits) (Result, error) {
	if db == nil {
		return Result{}, errors.New("traversal database is nil")
	}
	resolved, err := resolveLimits(limits)
	if err != nil {
		return Result{}, err
	}
	target, err := selectNode(ctx, db, targetID)
	if err != nil {
		return Result{}, err
	}
	if !isLogicalNode(target) {
		return Result{}, fmt.Errorf("%w: %s", ErrInvalidTargetType, target.Type)
	}

	state := traversalState{
		ctx:              ctx,
		db:               db,
		limits:           resolved,
		result:           Result{Target: target, StartNodes: []Node{target}},
		targetScope:      make(map[string]struct{}),
		best:             make(map[string]queuedNode),
		processed:        make(map[string]struct{}),
		queueBuckets:     make(map[distance][]queuedNode),
		registeredScopes: make(map[string]struct{}),
		downstreamByID:   make(map[string]int),
		unexploredByKey:  make(map[unexploredKey]int),
		frontierByKey:    make(map[frontierKey]int),
		connectionsByKey: make(map[connectionKey]int),
		scopeEntries:     make(map[string]struct{}),
	}
	state.schedule(target, distance{})

	for {
		current := state.takeNextBatch()
		if len(current) == 0 {
			if state.seedScopeFallback() {
				continue
			}
			break
		}

		for _, currentNode := range current {
			if currentNode.node.Type == "job_network" {
				if err := state.registerScope(currentNode); err != nil {
					return Result{}, err
				}
			}
		}

		connections, unreadFromID, err := selectOutgoing(
			ctx, db, current, resolved.MaxConnections-state.connectionsRead,
		)
		if err != nil {
			return Result{}, err
		}
		state.connectionsRead += countRelations(connections)
		if unreadFromID != "" {
			state.addConnectionFrontiers(current, unreadFromID)
		}
		byFrom := groupByFrom(connections)
		for _, from := range current {
			outgoing := byFrom[from.node.ID]
			if from.graph >= resolved.MaxGraphDepth {
				if len(outgoing) > 0 {
					state.addFrontier(from.node, from.distance, TruncationGraphDepthLimit)
				}
				continue
			}
			for _, connection := range outgoing {
				arrival := distance{
					graph:      from.graph + 1,
					dependency: from.dependency + logicalNodeIncrement(connection.To),
				}
				connection.GraphDepth = arrival.graph
				connection.DependencyDistance = arrival.dependency
				state.addConnection(connection)
				if state.isDownstream(connection.To) {
					state.addDownstream(connection.To, arrival)
				}
				if isTraversable(connection.To) {
					state.schedule(connection.To, arrival)
				} else {
					state.addUnexplored(connection.To, arrival, UnexploredNodeType)
				}
			}
		}
	}

	state.finish()
	return state.result, nil
}

func resolveLimits(limits Limits) (Limits, error) {
	if limits.MaxVisitedNodes < 0 || limits.MaxGraphDepth < 0 || limits.MaxConnections < 0 {
		return Limits{}, errors.New("traversal limits must not be negative")
	}
	if limits.MaxVisitedNodes == 0 {
		limits.MaxVisitedNodes = DefaultMaxVisitedNodes
	}
	if limits.MaxGraphDepth == 0 {
		limits.MaxGraphDepth = DefaultMaxGraphDepth
	}
	if limits.MaxConnections == 0 {
		limits.MaxConnections = DefaultMaxConnections
	}
	return limits, nil
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

func selectScopeNodes(ctx context.Context, db *sql.DB, networkID string, limit int) ([]Node, bool, error) {
	if limit == 0 {
		return nil, true, nil
	}
	rows, err := db.QueryContext(ctx, `
WITH RECURSIVE contained(node_id, node_type, name, path, parent_id) AS (
    SELECT node_id, node_type, name, path, parent_id
    FROM node
    WHERE parent_id = ?
    UNION ALL
    SELECT child.node_id, child.node_type, child.name, child.path, child.parent_id
    FROM node AS child
    JOIN contained AS parent ON child.parent_id = parent.node_id
)
SELECT node_id, node_type, name, path, parent_id
FROM contained
WHERE node_type IN ('job', 'job_network')
ORDER BY node_id
LIMIT ?`, networkID, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("select scope nodes: %w", err)
	}
	defer rows.Close()

	nodes := make([]Node, 0)
	for rows.Next() {
		var node Node
		var path, parentID sql.NullString
		if err := rows.Scan(&node.ID, &node.Type, &node.Name, &path, &parentID); err != nil {
			return nil, false, fmt.Errorf("scan scope node: %w", err)
		}
		if len(nodes) == limit {
			return nodes, true, nil
		}
		node.Path = nullableString(path)
		node.ParentID = nullableString(parentID)
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("select scope nodes: %w", err)
	}
	return nodes, false, nil
}

func selectScopeIncoming(
	ctx context.Context, db *sql.DB, nodes []Node, limit int,
) (map[string]struct{}, int, bool, error) {
	incoming := make(map[string]struct{})
	members := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		members[node.ID] = struct{}{}
	}
	read := 0
	for offset := 0; offset < len(nodes); offset += queryBatchSize {
		if read == limit {
			return incoming, read, true, nil
		}
		end := min(offset+queryBatchSize, len(nodes))
		args := make([]any, 0, end-offset)
		for _, node := range nodes[offset:end] {
			args = append(args, node.ID)
		}
		query := `
SELECT from_id, to_id
FROM relation
WHERE to_id IN (` + placeholders(len(args)) + `)
ORDER BY to_id, from_id, relation_id
LIMIT ?`
		args = append(args, limit-read+1)
		rows, err := db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, 0, false, fmt.Errorf("select scope incoming relations: %w", err)
		}
		for rows.Next() {
			var fromID, toID string
			if err := rows.Scan(&fromID, &toID); err != nil {
				_ = rows.Close()
				return nil, 0, false, fmt.Errorf("scan scope incoming relation: %w", err)
			}
			if read == limit {
				_ = rows.Close()
				return incoming, read, true, nil
			}
			read++
			if _, internal := members[fromID]; internal && fromID != toID {
				incoming[toID] = struct{}{}
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, 0, false, fmt.Errorf("select scope incoming relations: %w", err)
		}
		if err := rows.Close(); err != nil {
			return nil, 0, false, fmt.Errorf("close scope incoming relations: %w", err)
		}
	}
	return incoming, read, false, nil
}

func selectOutgoing(
	ctx context.Context, db *sql.DB, frontier []queuedNode, limit int,
) ([]Connection, string, error) {
	if limit == 0 {
		return nil, frontier[0].node.ID, nil
	}
	all := make([]Connection, 0)
	read := 0
	// SQLiteの変数上限に依存せず、同じ探索段階を固定サイズのIN句へ分割する。
	// 分割後に接続を並べ直すため、バッチ境界が結果順へ影響しない。
	for offset := 0; offset < len(frontier); offset += queryBatchSize {
		if read == limit {
			return mergeConnections(all), frontier[offset].node.ID, nil
		}
		end := min(offset+queryBatchSize, len(frontier))
		args := make([]any, 0, end-offset)
		for _, current := range frontier[offset:end] {
			args = append(args, current.node.ID)
		}
		query := `
SELECT r.relation_id, r.from_id, r.to_id, r.relation_kind, r.origin, r.certainty,
       r.evidence_json, destination.node_type, destination.name,
       destination.path, destination.parent_id
FROM relation AS r
JOIN node AS destination ON destination.node_id = r.to_id
WHERE r.from_id IN (` + placeholders(len(args)) + `)
ORDER BY r.from_id, r.to_id, r.relation_id
LIMIT ?`
		args = append(args, limit-read+1)
		rows, err := db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, "", fmt.Errorf("select outgoing relations: %w", err)
		}
		batch, err := scanConnections(rows)
		if closeErr := rows.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
		if err != nil {
			return nil, "", fmt.Errorf("scan outgoing relations: %w", err)
		}
		remaining := limit - read
		if len(batch) > remaining {
			unreadFromID := batch[remaining].FromID
			all = append(all, batch[:remaining]...)
			sortRawConnections(all)
			return mergeConnections(all), unreadFromID, nil
		}
		all = append(all, batch...)
		read += len(batch)
	}
	sortRawConnections(all)
	return mergeConnections(all), "", nil
}

func sortRawConnections(connections []Connection) {
	sort.Slice(connections, func(i, j int) bool {
		if connections[i].FromID != connections[j].FromID {
			return connections[i].FromID < connections[j].FromID
		}
		if connections[i].To.ID != connections[j].To.ID {
			return connections[i].To.ID < connections[j].To.ID
		}
		return connections[i].Relations[0].ID < connections[j].Relations[0].ID
	})
}

func countRelations(connections []Connection) int {
	total := 0
	for _, connection := range connections {
		total += len(connection.Relations)
	}
	return total
}

func scanConnections(rows *sql.Rows) ([]Connection, error) {
	connections := make([]Connection, 0)
	for rows.Next() {
		var relation Relation
		var node Node
		var fromID string
		var evidence, path, parentID sql.NullString
		if err := rows.Scan(
			&relation.ID, &fromID, &node.ID, &relation.Kind, &relation.Origin,
			&relation.Certainty, &evidence, &node.Type, &node.Name, &path, &parentID,
		); err != nil {
			return nil, err
		}
		if evidence.Valid {
			relation.Evidence = json.RawMessage(evidence.String)
		}
		node.Path = nullableString(path)
		node.ParentID = nullableString(parentID)
		connections = append(connections, Connection{
			FromID:    fromID,
			To:        node,
			Relations: []Relation{relation},
		})
	}
	return connections, rows.Err()
}

func nullableString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}

func mergeConnections(ordered []Connection) []Connection {
	merged := make([]Connection, 0, len(ordered))
	for _, current := range ordered {
		last := len(merged) - 1
		if last >= 0 && merged[last].FromID == current.FromID && merged[last].To.ID == current.To.ID {
			merged[last].Relations = append(merged[last].Relations, current.Relations...)
			continue
		}
		merged = append(merged, current)
	}
	return merged
}

func groupByFrom(connections []Connection) map[string][]Connection {
	grouped := make(map[string][]Connection)
	for _, connection := range connections {
		grouped[connection.FromID] = append(grouped[connection.FromID], connection)
	}
	return grouped
}

func placeholders(count int) string {
	if count == 0 {
		return "NULL"
	}
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}

func (state *traversalState) registerScope(network queuedNode) error {
	if _, registered := state.registeredScopes[network.node.ID]; registered {
		return nil
	}
	state.registeredScopes[network.node.ID] = struct{}{}
	members, truncated, err := selectScopeNodes(
		state.ctx, state.db, network.node.ID, state.remainingConnections(),
	)
	if err != nil {
		return err
	}
	state.connectionsRead += len(members)
	if truncated {
		state.addFrontier(network.node, network.distance, TruncationConnectionLimit)
		return nil
	}
	incoming, read, truncated, err := selectScopeIncoming(
		state.ctx, state.db, members, state.remainingConnections(),
	)
	if err != nil {
		return err
	}
	state.connectionsRead += read
	if truncated {
		state.addFrontier(network.node, network.distance, TruncationConnectionLimit)
		return nil
	}
	scope := &scopeState{
		rootID:  network.node.ID,
		base:    network.distance,
		members: members,
	}
	// scopeは子孫全体を含むため、配下のジョブネットを訪問したときに同じ子孫を再展開しない。
	for _, member := range members {
		if member.Type == "job_network" {
			state.registeredScopes[member.ID] = struct{}{}
		}
	}
	state.scopes = append(state.scopes, scope)
	if network.node.ID == state.result.Target.ID {
		for _, member := range members {
			state.targetScope[member.ID] = struct{}{}
		}
	}

	for _, member := range members {
		if _, hasIncoming := incoming[member.ID]; hasIncoming {
			continue
		}
		state.addScopeEntry(scope, member, false)
		if scope.rootID != state.result.Target.ID && state.isDownstream(member) {
			state.addDownstream(member, scope.base)
		}
		state.schedule(member, scope.base)
	}
	return nil
}

func (state *traversalState) remainingConnections() int {
	return state.limits.MaxConnections - state.connectionsRead
}

func (state *traversalState) addScopeEntry(scope *scopeState, node Node, fallback bool) {
	key := scope.rootID + "\x00" + node.ID
	if _, exists := state.scopeEntries[key]; exists {
		return
	}
	state.scopeEntries[key] = struct{}{}
	state.result.ScopeEntries = append(state.result.ScopeEntries, ScopeEntry{
		ScopeRootID: scope.rootID,
		Node:        node,
		Fallback:    fallback,
	})
}

func (state *traversalState) seedScopeFallback() bool {
	// 通常の入口から到達できる経路を先に調べ、残った成分だけを救済対象とする。
	// scopesとmembersは登録順、ID順なので、複数の成分があっても開始順は変わらない。
	for _, scope := range state.scopes {
		for _, member := range scope.members {
			if _, visited := state.best[member.ID]; visited {
				continue
			}
			state.result.UsedScopeFallback = true
			state.addScopeEntry(scope, member, true)
			if scope.rootID != state.result.Target.ID && state.isDownstream(member) {
				state.addDownstream(member, scope.base)
			}
			if len(state.best) >= state.limits.MaxVisitedNodes {
				state.addFrontier(member, scope.base, TruncationNodeLimit)
				continue
			}
			state.schedule(member, scope.base)
			return true
		}
	}
	return false
}

func (state *traversalState) schedule(node Node, candidate distance) {
	if current, exists := state.best[node.ID]; exists {
		if lessDistance(candidate, current.distance) {
			current.distance = candidate
			state.best[node.ID] = current
			// 再展開は非負の距離が厳密に減少した場合だけなので、一つのノードに対する再展開は有限回で終了する。
			delete(state.processed, node.ID)
			state.enqueue(current)
		}
		return
	}
	if len(state.best) >= state.limits.MaxVisitedNodes {
		state.addFrontier(node, candidate, TruncationNodeLimit)
		return
	}
	queued := queuedNode{node: node, distance: candidate}
	state.best[node.ID] = queued
	state.enqueue(queued)
}

func (state *traversalState) enqueue(node queuedNode) {
	// 処理済みの距離が後から短縮される場合も、現在キューにない距離はヒープへ再登録する。
	// 古い候補はbestとの比較で除外し、同じ距離の有効な候補だけをノードID順へまとめる。
	if _, exists := state.queueBuckets[node.distance]; !exists {
		heap.Push(&state.queueDistances, node.distance)
	}
	state.queueBuckets[node.distance] = append(state.queueBuckets[node.distance], node)
}

func (state *traversalState) takeNextBatch() []queuedNode {
	for state.queueDistances.Len() > 0 {
		next := heap.Pop(&state.queueDistances).(distance)
		candidates := state.queueBuckets[next]
		delete(state.queueBuckets, next)
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].node.ID < candidates[j].node.ID
		})

		current := make([]queuedNode, 0, len(candidates))
		for _, candidate := range candidates {
			best := state.best[candidate.node.ID]
			if best.distance != candidate.distance {
				continue
			}
			if _, done := state.processed[candidate.node.ID]; done {
				continue
			}
			state.processed[candidate.node.ID] = struct{}{}
			current = append(current, candidate)
		}
		if len(current) > 0 {
			return current
		}
	}
	return nil
}

func lessDistance(left, right distance) bool {
	// 後続のdependencyDistanceを最短に保ち、同値の場合だけ依存関係の本数が少ない経路を採用する。
	if left.dependency != right.dependency {
		return left.dependency < right.dependency
	}
	return left.graph < right.graph
}

func isLogicalNode(node Node) bool {
	return node.Type == "job" || node.Type == "job_network"
}

func logicalNodeIncrement(node Node) int {
	if isLogicalNode(node) {
		return 1
	}
	return 0
}

func isTraversable(node Node) bool {
	switch node.Type {
	case "job", "job_network", "file", "file_pattern", "job_status", "external_event":
		return true
	default:
		return false
	}
}

func (state *traversalState) isDownstream(node Node) bool {
	if !isLogicalNode(node) || node.ID == state.result.Target.ID {
		return false
	}
	if state.result.Target.Type == "job" {
		return true
	}
	_, contained := state.targetScope[node.ID]
	return !contained
}

func (state *traversalState) addDownstream(node Node, candidate distance) {
	if index, exists := state.downstreamByID[node.ID]; exists {
		current := &state.result.Downstream[index]
		if lessDistance(candidate, distance{graph: current.GraphDepth, dependency: current.DependencyDistance}) {
			current.GraphDepth = candidate.graph
			current.DependencyDistance = candidate.dependency
			current.Node = node
		}
		return
	}
	state.downstreamByID[node.ID] = len(state.result.Downstream)
	state.result.Downstream = append(state.result.Downstream, Downstream{
		Node:               node,
		GraphDepth:         candidate.graph,
		DependencyDistance: candidate.dependency,
	})
}

type unexploredKey struct {
	nodeID string
	reason UnexploredReason
}

func (state *traversalState) addUnexplored(node Node, candidate distance, reason UnexploredReason) {
	key := unexploredKey{nodeID: node.ID, reason: reason}
	if index, exists := state.unexploredByKey[key]; exists {
		current := &state.result.Unexplored[index]
		if lessDistance(candidate, distance{graph: current.GraphDepth, dependency: current.DependencyDistance}) {
			current.Node = node
			current.GraphDepth = candidate.graph
			current.DependencyDistance = candidate.dependency
		}
		return
	}
	state.unexploredByKey[key] = len(state.result.Unexplored)
	state.result.Unexplored = append(state.result.Unexplored, Unexplored{
		Node:               node,
		GraphDepth:         candidate.graph,
		DependencyDistance: candidate.dependency,
		Reason:             reason,
	})
}

type frontierKey struct {
	nodeID string
	reason TruncationReason
}

type connectionKey struct {
	fromID string
	toID   string
}

func (state *traversalState) addFrontier(node Node, candidate distance, reason TruncationReason) {
	key := frontierKey{nodeID: node.ID, reason: reason}
	if index, exists := state.frontierByKey[key]; exists {
		current := &state.result.Frontier[index]
		if lessDistance(candidate, distance{graph: current.GraphDepth, dependency: current.DependencyDistance}) {
			current.Node = node
			current.GraphDepth = candidate.graph
			current.DependencyDistance = candidate.dependency
		}
		return
	}
	state.frontierByKey[key] = len(state.result.Frontier)
	state.result.Frontier = append(state.result.Frontier, Frontier{
		Node:               node,
		GraphDepth:         candidate.graph,
		DependencyDistance: candidate.dependency,
		Reason:             reason,
	})
	state.result.Truncated = true
	if state.result.TruncationReason == "" {
		state.result.TruncationReason = reason
	}
}

func (state *traversalState) addConnection(candidate Connection) {
	key := connectionKey{fromID: candidate.FromID, toID: candidate.To.ID}
	if index, exists := state.connectionsByKey[key]; exists {
		current := &state.result.Connections[index]
		candidateDistance := distance{graph: candidate.GraphDepth, dependency: candidate.DependencyDistance}
		currentDistance := distance{graph: current.GraphDepth, dependency: current.DependencyDistance}
		if lessDistance(candidateDistance, currentDistance) {
			current.To = candidate.To
			current.GraphDepth = candidate.GraphDepth
			current.DependencyDistance = candidate.DependencyDistance
		}
		// 再展開は同じSQLiteスナップショットを読むため、最初の取得で統合したrelationの内容と順序を維持する。
		return
	}
	state.connectionsByKey[key] = len(state.result.Connections)
	state.result.Connections = append(state.result.Connections, candidate)
}

func (state *traversalState) addConnectionFrontiers(current []queuedNode, unreadFromID string) {
	for _, node := range current {
		if node.node.ID >= unreadFromID {
			state.addFrontier(node.node, node.distance, TruncationConnectionLimit)
		}
	}
}

type confirmedQueueItem struct {
	nodeID   string
	distance int
}

type confirmedQueue []confirmedQueueItem

func (values confirmedQueue) Len() int { return len(values) }

func (values confirmedQueue) Less(i, j int) bool {
	if values[i].distance != values[j].distance {
		return values[i].distance < values[j].distance
	}
	return values[i].nodeID < values[j].nodeID
}

func (values confirmedQueue) Swap(i, j int) { values[i], values[j] = values[j], values[i] }

func (values *confirmedQueue) Push(value any) {
	*values = append(*values, value.(confirmedQueueItem))
}

func (values *confirmedQueue) Pop() any {
	last := len(*values) - 1
	value := (*values)[last]
	*values = (*values)[:last]
	return value
}

func (state *traversalState) setConfirmedDependencyDistances() {
	adjacency := make(map[string][]int)
	for index, connection := range state.result.Connections {
		if !hasConfirmedRelation(connection.Relations) {
			continue
		}
		adjacency[connection.FromID] = append(adjacency[connection.FromID], index)
	}
	// 親子関係は取込時に検査済みなので確定経路へ含めるが、依存関係ではないため距離を増やさない。
	// 負数はScopeEntriesの添字を表し、Connectionsを複製せず再走査用の索引だけを保持する。
	for index, entry := range state.result.ScopeEntries {
		adjacency[entry.ScopeRootID] = append(adjacency[entry.ScopeRootID], -index-1)
	}

	distances := map[string]int{state.result.Target.ID: 0}
	queue := confirmedQueue{{nodeID: state.result.Target.ID}}
	heap.Init(&queue)
	for queue.Len() > 0 {
		current := heap.Pop(&queue).(confirmedQueueItem)
		if distances[current.nodeID] != current.distance {
			continue
		}
		for _, edgeIndex := range adjacency[current.nodeID] {
			var toID string
			increment := 0
			if edgeIndex >= 0 {
				connection := state.result.Connections[edgeIndex]
				toID = connection.To.ID
				increment = logicalNodeIncrement(connection.To)
			} else {
				toID = state.result.ScopeEntries[-edgeIndex-1].Node.ID
			}
			candidate := current.distance + increment
			known, exists := distances[toID]
			if exists && known <= candidate {
				continue
			}
			distances[toID] = candidate
			heap.Push(&queue, confirmedQueueItem{nodeID: toID, distance: candidate})
		}
	}

	for index := range state.result.Downstream {
		value, reachable := distances[state.result.Downstream[index].Node.ID]
		if !reachable {
			continue
		}
		state.result.Downstream[index].ConfirmedDependencyDistance = &value
	}
}

func hasConfirmedRelation(relations []Relation) bool {
	for _, relation := range relations {
		if relation.Certainty == "declared" || relation.Certainty == "confirmed" {
			return true
		}
	}
	return false
}

func (state *traversalState) finish() {
	state.setConfirmedDependencyDistances()
	state.result.Nodes = make([]Visit, 0, len(state.best))
	for _, current := range state.best {
		state.result.Nodes = append(state.result.Nodes, Visit{
			Node:               current.node,
			GraphDepth:         current.graph,
			DependencyDistance: current.dependency,
		})
	}
	sort.Slice(state.result.Nodes, func(i, j int) bool {
		left, right := state.result.Nodes[i], state.result.Nodes[j]
		if left.DependencyDistance != right.DependencyDistance {
			return left.DependencyDistance < right.DependencyDistance
		}
		if left.GraphDepth != right.GraphDepth {
			return left.GraphDepth < right.GraphDepth
		}
		return left.Node.ID < right.Node.ID
	})
	sort.Slice(state.result.ScopeEntries, func(i, j int) bool {
		left, right := state.result.ScopeEntries[i], state.result.ScopeEntries[j]
		if left.ScopeRootID != right.ScopeRootID {
			return left.ScopeRootID < right.ScopeRootID
		}
		return left.Node.ID < right.Node.ID
	})
	sort.Slice(state.result.Connections, func(i, j int) bool {
		left, right := state.result.Connections[i], state.result.Connections[j]
		if left.DependencyDistance != right.DependencyDistance {
			return left.DependencyDistance < right.DependencyDistance
		}
		if left.GraphDepth != right.GraphDepth {
			return left.GraphDepth < right.GraphDepth
		}
		if left.FromID != right.FromID {
			return left.FromID < right.FromID
		}
		return left.To.ID < right.To.ID
	})
	sort.Slice(state.result.Downstream, func(i, j int) bool {
		left, right := state.result.Downstream[i], state.result.Downstream[j]
		if left.DependencyDistance != right.DependencyDistance {
			return left.DependencyDistance < right.DependencyDistance
		}
		if left.GraphDepth != right.GraphDepth {
			return left.GraphDepth < right.GraphDepth
		}
		return left.Node.ID < right.Node.ID
	})
	sort.Slice(state.result.Unexplored, func(i, j int) bool {
		left, right := state.result.Unexplored[i], state.result.Unexplored[j]
		if left.DependencyDistance != right.DependencyDistance {
			return left.DependencyDistance < right.DependencyDistance
		}
		if left.GraphDepth != right.GraphDepth {
			return left.GraphDepth < right.GraphDepth
		}
		if left.Node.ID != right.Node.ID {
			return left.Node.ID < right.Node.ID
		}
		return left.Reason < right.Reason
	})
	sort.Slice(state.result.Frontier, func(i, j int) bool {
		left, right := state.result.Frontier[i], state.result.Frontier[j]
		if left.DependencyDistance != right.DependencyDistance {
			return left.DependencyDistance < right.DependencyDistance
		}
		if left.GraphDepth != right.GraphDepth {
			return left.GraphDepth < right.GraphDepth
		}
		if left.Node.ID != right.Node.ID {
			return left.Node.ID < right.Node.ID
		}
		return left.Reason < right.Reason
	})
	state.result.Cycles, state.result.Shared = classifyConnections(
		state.result.StartNodes, state.result.Nodes, state.result.Connections,
	)
}

func classifyConnections(starts []Node, nodes []Visit, connections []Connection) ([]Cycle, []Shared) {
	visitedNodes := make(map[string]struct{}, len(nodes))
	for _, visit := range nodes {
		visitedNodes[visit.Node.ID] = struct{}{}
	}
	adjacency := make(map[string][]string)
	for _, connection := range connections {
		if _, visited := visitedNodes[connection.To.ID]; visited {
			adjacency[connection.FromID] = append(adjacency[connection.FromID], connection.To.ID)
		}
	}
	for fromID := range adjacency {
		sort.Strings(adjacency[fromID])
	}

	state := make(map[string]uint8)
	position := make(map[string]int)
	stack := make([]string, 0)
	cyclesByKey := make(map[string]Cycle)
	sharedByKey := make(map[string]Shared)
	var visit func(string)
	visit = func(nodeID string) {
		state[nodeID] = 1
		position[nodeID] = len(stack)
		stack = append(stack, nodeID)
		for _, toID := range adjacency[nodeID] {
			switch state[toID] {
			case 0:
				visit(toID)
			case 1:
				path := normalizeCycle(stack[position[toID]:])
				keyBytes, _ := json.Marshal(path)
				closed := append(append([]string(nil), path...), path[0])
				cyclesByKey[string(keyBytes)] = Cycle{Path: closed}
			case 2:
				keyBytes, _ := json.Marshal([]string{nodeID, toID})
				sharedByKey[string(keyBytes)] = Shared{FromID: nodeID, ToID: toID}
			}
		}
		stack = stack[:len(stack)-1]
		delete(position, nodeID)
		state[nodeID] = 2
	}

	orderedStarts := append([]Node(nil), starts...)
	sort.Slice(orderedStarts, func(i, j int) bool { return orderedStarts[i].ID < orderedStarts[j].ID })
	for _, start := range orderedStarts {
		if _, exists := visitedNodes[start.ID]; exists && state[start.ID] == 0 {
			visit(start.ID)
		}
	}
	// 親子関係を依存接続へ加えないため、対象ネット配下の成分は論理ルートと非連結になり得る。
	// その成分も循環と合流の分類対象から落とさない。
	for _, current := range nodes {
		if state[current.Node.ID] == 0 {
			visit(current.Node.ID)
		}
	}

	cycles := make([]Cycle, 0, len(cyclesByKey))
	for _, cycle := range cyclesByKey {
		cycles = append(cycles, cycle)
	}
	sort.Slice(cycles, func(i, j int) bool { return compareStrings(cycles[i].Path, cycles[j].Path) < 0 })
	shared := make([]Shared, 0, len(sharedByKey))
	for _, join := range sharedByKey {
		shared = append(shared, join)
	}
	sort.Slice(shared, func(i, j int) bool {
		if shared[i].FromID != shared[j].FromID {
			return shared[i].FromID < shared[j].FromID
		}
		return shared[i].ToID < shared[j].ToID
	})
	return cycles, shared
}

func normalizeCycle(path []string) []string {
	if len(path) == 0 {
		return nil
	}
	var best []string
	for start := range path {
		rotated := make([]string, len(path))
		for offset := range path {
			rotated[offset] = path[(start+offset)%len(path)]
		}
		if best == nil || compareStrings(rotated, best) < 0 {
			best = rotated
		}
	}
	return best
}

func compareStrings(left, right []string) int {
	for index := 0; index < min(len(left), len(right)); index++ {
		if left[index] < right[index] {
			return -1
		}
		if left[index] > right[index] {
			return 1
		}
	}
	switch {
	case len(left) < len(right):
		return -1
	case len(left) > len(right):
		return 1
	default:
		return 0
	}
}
