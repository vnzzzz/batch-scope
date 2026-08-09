// Package limitscan は、後続探索の到達結果に設定されたリミットを全件抽出する。
package limitscan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"batchscope/internal/traversal"
)

// queryBatchSizeは、一回のSQLに含めるプレースホルダー数を抑えるための件数である。
// 抽出件数の上限ではなく、すべての到達ノードを複数回の問い合わせで処理する。
const queryBatchSize = 500

// ErrInvalidLimitOwner は、job以外のノードがリミットを所有する不正な検索DBを示す。
var ErrInvalidLimitOwner = errors.New("limit owner must be a job")

// Kind は保存されたリミットの種類を表す。
type Kind string

const (
	KindFinishBy   Kind = "finish_by"
	KindMaxElapsed Kind = "max_elapsed"
	KindRaw        Kind = "raw"
)

// Node はリミットの設定先または後続scopeの起点を表す。
type Node struct {
	ID   string
	Type string
	Name string
}

// Fact はSQLiteから復元したリミット定義を表す。
// 種類に該当しない値はnilとし、未設定とゼロ値を区別する。
type Fact struct {
	ID                string
	Kind              Kind
	BusinessDayOffset *int64
	LocalTime         *string
	TimeZone          *string
	DurationSeconds   *int64
	SourceText        *string
	Origin            string
	Certainty         string
}

// Item は一件のリミットと、その設定先および影響先scopeを保持する。
// ScopeRootはdownstreamの設定先がscope遷移で到達した場合だけ設定する。
type Item struct {
	LimitOwner Node
	ScopeRoot  *Node
	Fact       Fact
}

// FinishByGroup は同じタイムゾーンに属するfinish_byを保持する。
type FinishByGroup struct {
	TimeZone string
	Total    int
	Items    []Item
}

// Group は種類をまたいで順位付けしない単一種類のリミット集合を保持する。
type Group struct {
	Total int
	Items []Item
}

// Limits は対象との関係が同じリミットを種類ごとに分けて保持する。
type Limits struct {
	FinishByGroups []FinishByGroup
	MaxElapsed     Group
	Raw            Group
}

// Result は対象、対象ジョブネット配下、後続の区分ごとに全リミットを保持する。
// 各Totalは対応するItemsの件数と一致する。
type Result struct {
	Target     Limits
	Contained  Limits
	Downstream Limits
}

type candidate struct {
	membership       traversal.Membership
	item             Item
	localTimeSeconds int64
	durationSeconds  int64
}

type scanState struct {
	ctx         context.Context
	db          *sql.DB
	reached     map[string]traversal.Reached
	reachedIDs  []string
	scopeParent map[string]string
	candidates  map[string]candidate
}

// Scan は到達済みノードに設定されたリミットを全件読み、区分と種類ごとの閲覧順で返す。
// dbはTraverseに使ったstore.Store.Acquire由来のSQLite、または同じテーブル構成を持つDBでなければならない。
func Scan(ctx context.Context, db *sql.DB, traversalResult traversal.Result) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if db == nil {
		return Result{}, errors.New("limit scan database is nil")
	}

	state := scanState{
		ctx:         ctx,
		db:          db,
		reached:     make(map[string]traversal.Reached, len(traversalResult.Nodes)),
		reachedIDs:  make([]string, 0, len(traversalResult.Nodes)),
		scopeParent: make(map[string]string, len(traversalResult.ScopeEdges)),
		candidates:  make(map[string]candidate),
	}
	if err := state.prepare(traversalResult); err != nil {
		return Result{}, err
	}
	if err := state.readAll(); err != nil {
		// 複数バッチの途中で失敗しても、呼出側が蓄積済みの内容を全件と取り違えないよう破棄する。
		return Result{}, err
	}
	result, err := state.finish()
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

func (state *scanState) prepare(result traversal.Result) error {
	for _, reached := range result.Nodes {
		if err := state.ctx.Err(); err != nil {
			return err
		}
		if !isMembership(reached.Membership) {
			return fmt.Errorf("node %q has unsupported traversal membership %q", reached.Node.ID, reached.Membership)
		}
		if existing, ok := state.reached[reached.Node.ID]; ok {
			if existing.Membership != reached.Membership || !sameTraversalNode(existing.Node, reached.Node) {
				return fmt.Errorf("node %q has inconsistent traversal results", reached.Node.ID)
			}
		} else {
			state.reached[reached.Node.ID] = reached
		}
		// IDは入力どおりバッチへ渡し、別バッチから同じ主キー行を再取得してもlimit_idで一件へまとめる。
		state.reachedIDs = append(state.reachedIDs, reached.Node.ID)
	}
	for _, edge := range result.ScopeEdges {
		if err := state.ctx.Err(); err != nil {
			return err
		}
		if parent, ok := state.scopeParent[edge.ChildID]; ok && parent != edge.ParentID {
			return fmt.Errorf("scope child %q has multiple parents", edge.ChildID)
		}
		state.scopeParent[edge.ChildID] = edge.ParentID
	}
	return nil
}

func (state *scanState) readAll() error {
	for start := 0; start < len(state.reachedIDs); start += queryBatchSize {
		if err := state.ctx.Err(); err != nil {
			return err
		}
		end := min(start+queryBatchSize, len(state.reachedIDs))
		if err := state.readBatch(state.reachedIDs[start:end]); err != nil {
			return err
		}
	}
	return state.ctx.Err()
}

func (state *scanState) readBatch(nodeIDs []string) error {
	args := make([]any, len(nodeIDs))
	for index, id := range nodeIDs {
		args[index] = id
	}
	query := `
SELECT fact.limit_id, fact.node_id, fact.kind, fact.business_day_offset,
       fact.local_time_seconds, fact.time_zone, fact.duration_seconds,
       fact.source_text, fact.origin, fact.certainty,
       owner.node_id, owner.node_type, owner.name
FROM limit_fact AS fact
LEFT JOIN node AS owner ON owner.node_id = fact.node_id
WHERE fact.node_id IN (` + placeholders(len(nodeIDs)) + `)
ORDER BY fact.node_id, fact.limit_id`
	rows, err := state.db.QueryContext(state.ctx, query, args...)
	if err != nil {
		return fmt.Errorf("select reached limits: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		current, err := state.scanCandidate(rows)
		if err != nil {
			return err
		}
		if existing, ok := state.candidates[current.item.Fact.ID]; ok {
			if !sameCandidate(existing, current) {
				return fmt.Errorf("limit %q has inconsistent rows", current.item.Fact.ID)
			}
			continue
		}
		state.candidates[current.item.Fact.ID] = current
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("select reached limits: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close reached limits: %w", err)
	}
	return nil
}

func (state *scanState) scanCandidate(row rowScanner) (candidate, error) {
	var fact Fact
	var ownerNodeID, storedOwnerID, ownerType, ownerName sql.NullString
	var kind string
	var businessDayOffset, localTimeSeconds, durationSeconds sql.NullInt64
	var timeZone, sourceText sql.NullString
	if err := row.Scan(
		&fact.ID, &ownerNodeID, &kind, &businessDayOffset,
		&localTimeSeconds, &timeZone, &durationSeconds,
		&sourceText, &fact.Origin, &fact.Certainty,
		&storedOwnerID, &ownerType, &ownerName,
	); err != nil {
		return candidate{}, fmt.Errorf("scan reached limit: %w", err)
	}
	if !ownerNodeID.Valid || !storedOwnerID.Valid || !ownerType.Valid || !ownerName.Valid || storedOwnerID.String != ownerNodeID.String {
		return candidate{}, fmt.Errorf("limit %q references missing owner", fact.ID)
	}
	if ownerType.String != "job" {
		return candidate{}, fmt.Errorf("%w: limit %q owner %q has type %q", ErrInvalidLimitOwner, fact.ID, ownerNodeID.String, ownerType.String)
	}
	reached, ok := state.reached[ownerNodeID.String]
	if !ok {
		return candidate{}, fmt.Errorf("limit %q owner %q is not in traversal result", fact.ID, ownerNodeID.String)
	}
	if reached.Node.Type != ownerType.String || reached.Node.Name != ownerName.String {
		return candidate{}, fmt.Errorf("limit %q owner %q differs from traversal result", fact.ID, ownerNodeID.String)
	}

	fact.Kind = Kind(kind)
	fact.SourceText = nullableString(sourceText)
	current := candidate{
		membership: reached.Membership,
		item: Item{
			LimitOwner: Node{ID: storedOwnerID.String, Type: ownerType.String, Name: ownerName.String},
			Fact:       fact,
		},
	}
	switch fact.Kind {
	case KindFinishBy:
		if !businessDayOffset.Valid || !localTimeSeconds.Valid || !timeZone.Valid {
			return candidate{}, fmt.Errorf("finish_by limit %q is missing a sortable value", fact.ID)
		}
		localTime, err := formatLocalTime(localTimeSeconds.Int64)
		if err != nil {
			return candidate{}, fmt.Errorf("finish_by limit %q: %w", fact.ID, err)
		}
		current.item.Fact.BusinessDayOffset = int64Pointer(businessDayOffset.Int64)
		current.item.Fact.LocalTime = &localTime
		current.item.Fact.TimeZone = stringPointer(timeZone.String)
		current.localTimeSeconds = localTimeSeconds.Int64
	case KindMaxElapsed:
		if !durationSeconds.Valid {
			return candidate{}, fmt.Errorf("max_elapsed limit %q is missing duration_seconds", fact.ID)
		}
		current.item.Fact.DurationSeconds = int64Pointer(durationSeconds.Int64)
		current.durationSeconds = durationSeconds.Int64
	case KindRaw:
		if current.item.Fact.SourceText == nil {
			return candidate{}, fmt.Errorf("raw limit %q is missing source_text", fact.ID)
		}
	default:
		return candidate{}, fmt.Errorf("limit %q has unsupported kind %q", fact.ID, fact.Kind)
	}
	if current.membership == traversal.MembershipDownstream {
		root, err := state.scopeRoot(ownerNodeID.String)
		if err != nil {
			return candidate{}, fmt.Errorf("resolve scope root for limit %q: %w", fact.ID, err)
		}
		current.item.ScopeRoot = root
	}
	return current, nil
}

func (state *scanState) scopeRoot(ownerID string) (*Node, error) {
	parentID, ok := state.scopeParent[ownerID]
	if !ok {
		return nil, nil
	}
	seen := map[string]struct{}{ownerID: {}}
	rootID := parentID
	for {
		if err := state.ctx.Err(); err != nil {
			return nil, err
		}
		if _, duplicate := seen[rootID]; duplicate {
			return nil, fmt.Errorf("scope edges contain a cycle at %q", rootID)
		}
		seen[rootID] = struct{}{}
		parentID, ok = state.scopeParent[rootID]
		if !ok {
			break
		}
		rootID = parentID
	}
	reached, ok := state.reached[rootID]
	if !ok {
		return nil, fmt.Errorf("scope root %q is not in traversal result", rootID)
	}
	if reached.Node.Type != "job_network" {
		return nil, fmt.Errorf("scope root %q has type %q, want job_network", rootID, reached.Node.Type)
	}
	root := Node{ID: reached.Node.ID, Type: reached.Node.Type, Name: reached.Node.Name}
	return &root, nil
}

func (state *scanState) finish() (Result, error) {
	if err := state.ctx.Err(); err != nil {
		return Result{}, err
	}
	buckets := map[traversal.Membership]*bucket{
		traversal.MembershipTarget:     {},
		traversal.MembershipContained:  {},
		traversal.MembershipDownstream: {},
	}
	for _, current := range state.candidates {
		if err := state.ctx.Err(); err != nil {
			return Result{}, err
		}
		currentBucket := buckets[current.membership]
		switch current.item.Fact.Kind {
		case KindFinishBy:
			currentBucket.finishBy = append(currentBucket.finishBy, current)
		case KindMaxElapsed:
			currentBucket.maxElapsed = append(currentBucket.maxElapsed, current)
		case KindRaw:
			currentBucket.raw = append(currentBucket.raw, current)
		}
	}

	target := finishBucket(buckets[traversal.MembershipTarget])
	contained := finishBucket(buckets[traversal.MembershipContained])
	downstream := finishBucket(buckets[traversal.MembershipDownstream])
	if err := state.ctx.Err(); err != nil {
		return Result{}, err
	}
	return Result{Target: target, Contained: contained, Downstream: downstream}, nil
}

type bucket struct {
	finishBy   []candidate
	maxElapsed []candidate
	raw        []candidate
}

func finishBucket(source *bucket) Limits {
	finishByZones := make(map[string][]candidate)
	for _, current := range source.finishBy {
		finishByZones[*current.item.Fact.TimeZone] = append(finishByZones[*current.item.Fact.TimeZone], current)
	}
	zones := make([]string, 0, len(finishByZones))
	for zone := range finishByZones {
		zones = append(zones, zone)
	}
	sort.Strings(zones)
	finishByGroups := make([]FinishByGroup, 0, len(zones))
	for _, zone := range zones {
		candidates := finishByZones[zone]
		sort.Slice(candidates, func(i, j int) bool {
			left, right := candidates[i], candidates[j]
			if *left.item.Fact.BusinessDayOffset != *right.item.Fact.BusinessDayOffset {
				return *left.item.Fact.BusinessDayOffset < *right.item.Fact.BusinessDayOffset
			}
			if left.localTimeSeconds != right.localTimeSeconds {
				return left.localTimeSeconds < right.localTimeSeconds
			}
			return lessByOwnerAndLimit(left.item, right.item)
		})
		items := candidateItems(candidates)
		finishByGroups = append(finishByGroups, FinishByGroup{TimeZone: zone, Total: len(items), Items: items})
	}

	sort.Slice(source.maxElapsed, func(i, j int) bool {
		if source.maxElapsed[i].durationSeconds != source.maxElapsed[j].durationSeconds {
			return source.maxElapsed[i].durationSeconds < source.maxElapsed[j].durationSeconds
		}
		return lessByOwnerAndLimit(source.maxElapsed[i].item, source.maxElapsed[j].item)
	})
	sort.Slice(source.raw, func(i, j int) bool {
		return lessByOwnerAndLimit(source.raw[i].item, source.raw[j].item)
	})
	maxElapsedItems := candidateItems(source.maxElapsed)
	rawItems := candidateItems(source.raw)
	return Limits{
		FinishByGroups: finishByGroups,
		MaxElapsed:     Group{Total: len(maxElapsedItems), Items: maxElapsedItems},
		Raw:            Group{Total: len(rawItems), Items: rawItems},
	}
}

func candidateItems(candidates []candidate) []Item {
	items := make([]Item, len(candidates))
	for index, current := range candidates {
		items[index] = current.item
	}
	return items
}

func lessByOwnerAndLimit(left, right Item) bool {
	if left.LimitOwner.ID != right.LimitOwner.ID {
		return left.LimitOwner.ID < right.LimitOwner.ID
	}
	return left.Fact.ID < right.Fact.ID
}

func formatLocalTime(seconds int64) (string, error) {
	if seconds < 0 || seconds >= 24*60*60 {
		return "", fmt.Errorf("local_time_seconds %d is outside one day", seconds)
	}
	hours := seconds / (60 * 60)
	minutes := seconds / 60 % 60
	remainingSeconds := seconds % 60
	return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, remainingSeconds), nil
}

func nullableString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return stringPointer(value.String)
}

func stringPointer(value string) *string {
	return &value
}

func int64Pointer(value int64) *int64 {
	return &value
}

func isMembership(value traversal.Membership) bool {
	switch value {
	case traversal.MembershipTarget, traversal.MembershipContained, traversal.MembershipDownstream:
		return true
	default:
		return false
	}
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

func sameCandidate(left, right candidate) bool {
	return left.membership == right.membership &&
		left.localTimeSeconds == right.localTimeSeconds &&
		left.durationSeconds == right.durationSeconds &&
		sameItem(left.item, right.item)
}

func sameItem(left, right Item) bool {
	return left.LimitOwner == right.LimitOwner && sameNodePointer(left.ScopeRoot, right.ScopeRoot) &&
		left.Fact.ID == right.Fact.ID && left.Fact.Kind == right.Fact.Kind &&
		sameInt64Pointer(left.Fact.BusinessDayOffset, right.Fact.BusinessDayOffset) &&
		sameOptionalString(left.Fact.LocalTime, right.Fact.LocalTime) &&
		sameOptionalString(left.Fact.TimeZone, right.Fact.TimeZone) &&
		sameInt64Pointer(left.Fact.DurationSeconds, right.Fact.DurationSeconds) &&
		sameOptionalString(left.Fact.SourceText, right.Fact.SourceText) &&
		left.Fact.Origin == right.Fact.Origin && left.Fact.Certainty == right.Fact.Certainty
}

func sameNodePointer(left, right *Node) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func sameInt64Pointer(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

type rowScanner interface {
	Scan(dest ...any) error
}

func placeholders(count int) string {
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}
