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

const (
	// DefaultMaxVisitedNodes は、一回の探索が保持するノードとSQLite問い合わせの量を制限する。
	// 上限へ達した結果は完了扱いにせず、Result.Frontierに未調査の開始地点を残す。
	DefaultMaxVisitedNodes = 10_000
	// DefaultMaxDepth は、異常に長い依存関係による処理時間と経路状態の肥大化を防ぐ。
	DefaultMaxDepth = 1_000

	queryBatchSize = 500
)

var (
	ErrTargetNotFound    = errors.New("traversal target not found")
	ErrInvalidTargetType = errors.New("traversal target must be a job or job_network")
)

// Limits は探索内部の処理量を制限する。ゼロ値には既定値を適用する。
type Limits struct {
	MaxVisitedNodes int
	MaxDepth        int
}

// Node は探索と経路整形に必要なノード情報を表す。
type Node struct {
	ID   string
	Type string
	Name string
}

// Relation は同じ二ノード間でも種類や根拠が異なる依存関係を区別する。
type Relation struct {
	ID        string
	Kind      string
	Origin    string
	Certainty string
	Evidence  json.RawMessage
}

// Visit は実際に後続を調べたノードと、いずれかの開始点からの最短距離を表す。
type Visit struct {
	Node  Node
	Depth int
}

// Connection は一組のノード間にある依存関係をまとめて保持する。
// Depthはこの接続を通ってToへ到達したときの深さであり、To自身の最短距離とは限らない。
type Connection struct {
	FromID    string
	To        Node
	Depth     int
	Relations []Relation
}

// Downstream は対象範囲の外で到達したjobまたはjob_networkを表す。
type Downstream struct {
	Node  Node
	Depth int
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
	TruncationNodeLimit  TruncationReason = "node_limit"
	TruncationDepthLimit TruncationReason = "depth_limit"
)

// Frontier は処理上限により後続を調べなかった地点を表す。
type Frontier struct {
	Node   Node
	Depth  int
	Reason TruncationReason
}

// Result は後続のリミット選定と経路ツリー作成に必要な探索結果を保持する。
// NodesとConnectionsは経路を再構成できる情報を持ち、どちらも同じ入力には同じ順序で返す。
type Result struct {
	Target            Node
	StartNodes        []Node
	UsedStartFallback bool
	Nodes             []Visit
	Connections       []Connection
	Downstream        []Downstream
	Cycles            []Cycle
	Shared            []Shared
	Frontier          []Frontier
	Truncated         bool
	TruncationReason  TruncationReason
}

type queuedNode struct {
	node  Node
	depth int
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
	if target.Type != "job" && target.Type != "job_network" {
		return Result{}, fmt.Errorf("%w: %s", ErrInvalidTargetType, target.Type)
	}

	result := Result{Target: target}
	contained := make(map[string]struct{})
	starts := []Node{target}
	if target.Type == "job_network" {
		var fallback bool
		starts, contained, fallback, err = networkStarts(ctx, db, target.ID)
		if err != nil {
			return Result{}, err
		}
		result.UsedStartFallback = fallback
	}
	result.StartNodes = append([]Node(nil), starts...)

	visited := make(map[string]Visit)
	downstreamSeen := make(map[string]struct{})
	frontierSeen := make(map[frontierKey]struct{})
	current := make([]queuedNode, 0, min(len(starts), resolved.MaxVisitedNodes))
	for _, start := range starts {
		if len(visited) == resolved.MaxVisitedNodes {
			addFrontier(&result, frontierSeen, start, 0, TruncationNodeLimit)
			continue
		}
		visit := Visit{Node: start, Depth: 0}
		visited[start.ID] = visit
		current = append(current, queuedNode{node: start})
	}

	for len(current) > 0 {
		connections, err := selectOutgoing(ctx, db, current)
		if err != nil {
			return Result{}, err
		}
		next := make([]queuedNode, 0)
		byFrom := groupByFrom(connections)
		for _, from := range current {
			outgoing := byFrom[from.node.ID]
			if from.depth >= resolved.MaxDepth {
				if len(outgoing) > 0 {
					addFrontier(&result, frontierSeen, from.node, from.depth, TruncationDepthLimit)
				}
				continue
			}
			for _, connection := range outgoing {
				connection.Depth = from.depth + 1
				result.Connections = append(result.Connections, connection)
				if isExternalDownstream(connection.To, target, contained) {
					addDownstream(&result, downstreamSeen, connection.To, connection.Depth)
				}
				if !isTraversable(connection.To) {
					continue
				}
				if _, exists := visited[connection.To.ID]; exists {
					continue
				}
				if len(visited) == resolved.MaxVisitedNodes {
					addFrontier(&result, frontierSeen, connection.To, connection.Depth, TruncationNodeLimit)
					continue
				}
				visit := Visit{Node: connection.To, Depth: connection.Depth}
				visited[connection.To.ID] = visit
				next = append(next, queuedNode{node: connection.To, depth: connection.Depth})
			}
		}
		current = next
	}

	result.Nodes = make([]Visit, 0, len(visited))
	for _, visit := range visited {
		result.Nodes = append(result.Nodes, visit)
	}
	sort.Slice(result.Nodes, func(i, j int) bool {
		if result.Nodes[i].Depth != result.Nodes[j].Depth {
			return result.Nodes[i].Depth < result.Nodes[j].Depth
		}
		return result.Nodes[i].Node.ID < result.Nodes[j].Node.ID
	})
	sortResult(&result)
	result.Cycles, result.Shared = classifyConnections(result.StartNodes, result.Nodes, result.Connections)
	return result, nil
}

func resolveLimits(limits Limits) (Limits, error) {
	if limits.MaxVisitedNodes < 0 || limits.MaxDepth < 0 {
		return Limits{}, errors.New("traversal limits must not be negative")
	}
	if limits.MaxVisitedNodes == 0 {
		limits.MaxVisitedNodes = DefaultMaxVisitedNodes
	}
	if limits.MaxDepth == 0 {
		limits.MaxDepth = DefaultMaxDepth
	}
	return limits, nil
}

func selectNode(ctx context.Context, db *sql.DB, id string) (Node, error) {
	var node Node
	err := db.QueryRowContext(ctx, `
SELECT node_id, node_type, name
FROM node
WHERE node_id = ?`, id).Scan(&node.ID, &node.Type, &node.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return Node{}, fmt.Errorf("%w: %s", ErrTargetNotFound, id)
	}
	if err != nil {
		return Node{}, fmt.Errorf("select traversal target: %w", err)
	}
	return node, nil
}

func networkStarts(ctx context.Context, db *sql.DB, targetID string) ([]Node, map[string]struct{}, bool, error) {
	rows, err := db.QueryContext(ctx, `
WITH RECURSIVE contained(node_id, node_type, name) AS (
    SELECT node_id, node_type, name
    FROM node
    WHERE parent_id = ?
    UNION ALL
    SELECT child.node_id, child.node_type, child.name
    FROM node AS child
    JOIN contained AS parent ON child.parent_id = parent.node_id
)
SELECT node_id, node_type, name
FROM contained
ORDER BY node_id`, targetID)
	if err != nil {
		return nil, nil, false, fmt.Errorf("select contained nodes: %w", err)
	}
	defer rows.Close()

	nodes := make([]Node, 0)
	contained := make(map[string]struct{})
	for rows.Next() {
		var node Node
		if err := rows.Scan(&node.ID, &node.Type, &node.Name); err != nil {
			return nil, nil, false, fmt.Errorf("scan contained node: %w", err)
		}
		nodes = append(nodes, node)
		contained[node.ID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, false, fmt.Errorf("select contained nodes: %w", err)
	}

	incoming, err := selectContainedIncoming(ctx, db, nodes, contained)
	if err != nil {
		return nil, nil, false, err
	}
	starts := make([]Node, 0)
	for _, node := range nodes {
		if _, hasIncoming := incoming[node.ID]; !hasIncoming {
			starts = append(starts, node)
		}
	}
	if len(starts) > 0 {
		return starts, contained, false, nil
	}
	for _, node := range nodes {
		if node.Type == "job" {
			starts = append(starts, node)
		}
	}
	return starts, contained, true, nil
}

func selectContainedIncoming(ctx context.Context, db *sql.DB, nodes []Node, contained map[string]struct{}) (map[string]struct{}, error) {
	incoming := make(map[string]struct{})
	for offset := 0; offset < len(nodes); offset += queryBatchSize {
		end := min(offset+queryBatchSize, len(nodes))
		args := make([]any, 0, end-offset)
		for _, node := range nodes[offset:end] {
			args = append(args, node.ID)
		}
		query := `
SELECT from_id, to_id
FROM relation
WHERE to_id IN (` + placeholders(len(args)) + `)
ORDER BY to_id, from_id, relation_id`
		rows, err := db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("select contained incoming relations: %w", err)
		}
		for rows.Next() {
			var fromID, toID string
			if err := rows.Scan(&fromID, &toID); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan contained incoming relation: %w", err)
			}
			if _, internal := contained[fromID]; internal {
				incoming[toID] = struct{}{}
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("select contained incoming relations: %w", err)
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("close contained incoming relations: %w", err)
		}
	}
	return incoming, nil
}

func selectOutgoing(ctx context.Context, db *sql.DB, frontier []queuedNode) ([]Connection, error) {
	all := make([]Connection, 0)
	// SQLiteの変数上限に依存せず、同じ探索段階を固定サイズのIN句へ分割する。
	// 分割後に接続を並べ直すため、バッチ境界が結果順へ影響しない。
	for offset := 0; offset < len(frontier); offset += queryBatchSize {
		end := min(offset+queryBatchSize, len(frontier))
		args := make([]any, 0, end-offset)
		for _, current := range frontier[offset:end] {
			args = append(args, current.node.ID)
		}
		query := `
SELECT r.relation_id, r.from_id, r.to_id, r.relation_kind, r.origin, r.certainty,
       r.evidence_json, destination.node_type, destination.name
FROM relation AS r
JOIN node AS destination ON destination.node_id = r.to_id
WHERE r.from_id IN (` + placeholders(len(args)) + `)
ORDER BY r.from_id, r.to_id, r.relation_id`
		rows, err := db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("select outgoing relations: %w", err)
		}
		batch, err := scanConnections(rows)
		if closeErr := rows.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
		if err != nil {
			return nil, fmt.Errorf("scan outgoing relations: %w", err)
		}
		all = append(all, batch...)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].FromID != all[j].FromID {
			return all[i].FromID < all[j].FromID
		}
		if all[i].To.ID != all[j].To.ID {
			return all[i].To.ID < all[j].To.ID
		}
		return all[i].Relations[0].ID < all[j].Relations[0].ID
	})
	return mergeConnections(all), nil
}

func scanConnections(rows *sql.Rows) ([]Connection, error) {
	connections := make([]Connection, 0)
	for rows.Next() {
		var relation Relation
		var fromID, toID, nodeType, name string
		var evidence sql.NullString
		if err := rows.Scan(
			&relation.ID, &fromID, &toID, &relation.Kind, &relation.Origin,
			&relation.Certainty, &evidence, &nodeType, &name,
		); err != nil {
			return nil, err
		}
		if evidence.Valid {
			relation.Evidence = json.RawMessage(evidence.String)
		}
		connections = append(connections, Connection{
			FromID:    fromID,
			To:        Node{ID: toID, Type: nodeType, Name: name},
			Relations: []Relation{relation},
		})
	}
	return connections, rows.Err()
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

func isExternalDownstream(node, target Node, contained map[string]struct{}) bool {
	if node.Type != "job" && node.Type != "job_network" {
		return false
	}
	if node.ID == target.ID {
		return false
	}
	if target.Type == "job" {
		return true
	}
	_, internal := contained[node.ID]
	return !internal
}

func isTraversable(node Node) bool {
	switch node.Type {
	case "job", "job_network", "file", "file_pattern", "job_status", "external_event":
		return true
	default:
		return false
	}
}

func addDownstream(result *Result, seen map[string]struct{}, node Node, depth int) {
	if _, exists := seen[node.ID]; exists {
		return
	}
	seen[node.ID] = struct{}{}
	result.Downstream = append(result.Downstream, Downstream{Node: node, Depth: depth})
}

type frontierKey struct {
	nodeID string
	reason TruncationReason
}

func addFrontier(result *Result, seen map[frontierKey]struct{}, node Node, depth int, reason TruncationReason) {
	key := frontierKey{nodeID: node.ID, reason: reason}
	if _, exists := seen[key]; exists {
		return
	}
	seen[key] = struct{}{}
	result.Frontier = append(result.Frontier, Frontier{Node: node, Depth: depth, Reason: reason})
	result.Truncated = true
	if result.TruncationReason == "" {
		result.TruncationReason = reason
	}
}

func sortResult(result *Result) {
	sort.Slice(result.Downstream, func(i, j int) bool {
		if result.Downstream[i].Depth != result.Downstream[j].Depth {
			return result.Downstream[i].Depth < result.Downstream[j].Depth
		}
		return result.Downstream[i].Node.ID < result.Downstream[j].Node.ID
	})
	sort.Slice(result.Frontier, func(i, j int) bool {
		if result.Frontier[i].Depth != result.Frontier[j].Depth {
			return result.Frontier[i].Depth < result.Frontier[j].Depth
		}
		if result.Frontier[i].Node.ID != result.Frontier[j].Node.ID {
			return result.Frontier[i].Node.ID < result.Frontier[j].Node.ID
		}
		return result.Frontier[i].Reason < result.Frontier[j].Reason
	})
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
	// 上限で開始点が省かれた場合を除き、外部から再到達できない訪問済みノードも分類する。
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
