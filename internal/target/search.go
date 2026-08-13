package target

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"

	"batchscope/internal/normalize"
)

// MaxSearchResults は応答サイズを制限する。超過時は全件確認済みとせずTruncatedで通知する。
const MaxSearchResults = 1_000

// SearchItem は検索に一致した対象と一致項目を表す。
type SearchItem struct {
	Node
	MatchedBy    []string   `json:"matchedBy" enum:"id,localId,name,path" nullable:"false"`
	AncestorPath []Ancestor `json:"ancestorPath" nullable:"false"`
}

// SearchResult は完全一致検索の結果を保持する。
type SearchResult struct {
	Items     []SearchItem
	Truncated bool
}

type candidate struct {
	id        string
	matchedBy []string
}

// Search は従来互換のID、名前、完全パスとの完全一致を検索する。
func Search(ctx context.Context, db *sql.DB, query string, types []string) (SearchResult, error) {
	candidates, truncated, err := searchCandidates(ctx, db, query, types)
	if err != nil {
		return SearchResult{}, err
	}
	if truncated {
		candidates = candidates[:MaxSearchResults]
	}
	return loadSearchItems(ctx, db, candidates, truncated)
}

// SearchByLocalID は利用者が認識するジョブIDをnamespaceで任意に絞って完全一致検索する。
// namespaceを省略した場合は、同じlocal IDを持つ全namespaceの候補を返す。
func SearchByLocalID(ctx context.Context, db *sql.DB, localID, namespace string, types []string) (SearchResult, error) {
	indexed, err := hasNodeIdentityTable(ctx, db)
	if err != nil {
		return SearchResult{}, err
	}

	var candidates []candidate
	var truncated bool
	if indexed {
		candidates, truncated, err = searchIndexedLocalIDCandidates(ctx, db, localID, namespace, types)
	} else {
		// namespace導入前のSQLiteにはnamespaced identityが存在しない。
		// 旧node_idは文字列の見た目にかかわらず常にdefault namespaceのlocal IDそのものとして扱う。
		candidates, truncated, err = searchLegacyLocalIDCandidates(ctx, db, localID, namespace, types)
	}
	if err != nil {
		return SearchResult{}, err
	}
	if truncated {
		candidates = candidates[:MaxSearchResults]
	}
	result, err := loadSearchItems(ctx, db, candidates, truncated)
	if err != nil {
		return SearchResult{}, err
	}
	slices.SortStableFunc(result.Items, func(left, right SearchItem) int {
		if value := strings.Compare(left.Namespace, right.Namespace); value != 0 {
			return value
		}
		if value := strings.Compare(left.LocalID, right.LocalID); value != 0 {
			return value
		}
		if value := strings.Compare(left.Type, right.Type); value != 0 {
			return value
		}
		return strings.Compare(left.ID, right.ID)
	})
	return result, nil
}

func hasNodeIdentityTable(ctx context.Context, db *sql.DB) (bool, error) {
	var exists int
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM sqlite_schema WHERE type = 'table' AND name = 'node_identity'
	)`).Scan(&exists); err != nil {
		return false, fmt.Errorf("check node identity table: %w", err)
	}
	return exists != 0, nil
}

func loadSearchItems(ctx context.Context, db *sql.DB, candidates []candidate, truncated bool) (SearchResult, error) {
	ids := make([]string, len(candidates))
	for index, candidate := range candidates {
		ids[index] = candidate.id
	}
	details, err := LoadMany(ctx, db, ids)
	if err != nil {
		return SearchResult{}, err
	}

	items := make([]SearchItem, len(candidates))
	for index, candidate := range candidates {
		detail := details[candidate.id]
		items[index] = SearchItem{
			Node:         detail.Node,
			MatchedBy:    candidate.matchedBy,
			AncestorPath: detail.AncestorPath,
		}
	}
	return SearchResult{Items: items, Truncated: truncated}, nil
}

func searchIndexedLocalIDCandidates(ctx context.Context, db *sql.DB, localID, namespace string, types []string) ([]candidate, bool, error) {
	typePlaceholders := make([]string, len(types))
	args := make([]any, 0, len(types)+3)
	args = append(args, localID)
	condition := "identity.local_id = ?"
	if namespace != "" {
		condition += " AND identity.namespace = ?"
		args = append(args, namespace)
	}
	for index, nodeType := range types {
		typePlaceholders[index] = "?"
		args = append(args, nodeType)
	}
	args = append(args, MaxSearchResults+1)

	statement := fmt.Sprintf(`
SELECT node.node_id
FROM node_identity AS identity
JOIN node ON node.node_id = identity.node_id
WHERE %s
	AND node.node_type IN (%s)
ORDER BY identity.namespace COLLATE BINARY, identity.local_id COLLATE BINARY,
	node.node_type COLLATE BINARY, node.node_id COLLATE BINARY
LIMIT ?`, condition, strings.Join(typePlaceholders, ","))
	return readLocalIDCandidates(ctx, db, statement, args...)
}

func searchLegacyLocalIDCandidates(ctx context.Context, db *sql.DB, localID, namespace string, types []string) ([]candidate, bool, error) {
	if namespace != "" && namespace != "default" {
		return []candidate{}, false, nil
	}
	typePlaceholders := make([]string, len(types))
	args := make([]any, 0, len(types)+2)
	args = append(args, localID)
	for index, nodeType := range types {
		typePlaceholders[index] = "?"
		args = append(args, nodeType)
	}
	args = append(args, MaxSearchResults+1)

	statement := fmt.Sprintf(`
SELECT node.node_id
FROM node
WHERE node.node_id = ?
	AND node.node_type IN (%s)
ORDER BY node.node_type COLLATE BINARY, node.node_id COLLATE BINARY
LIMIT ?`, strings.Join(typePlaceholders, ","))
	return readLocalIDCandidates(ctx, db, statement, args...)
}

func readLocalIDCandidates(ctx context.Context, db *sql.DB, statement string, args ...any) ([]candidate, bool, error) {
	rows, err := db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, false, fmt.Errorf("search target local ID candidates: %w", err)
	}
	defer rows.Close()

	candidates := make([]candidate, 0, MaxSearchResults+1)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, false, fmt.Errorf("scan target local ID candidate: %w", err)
		}
		candidates = append(candidates, candidate{id: id, matchedBy: []string{"localId"}})
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate target local ID candidates: %w", err)
	}
	return candidates, len(candidates) > MaxSearchResults, nil
}

func searchCandidates(ctx context.Context, db *sql.DB, query string, types []string) ([]candidate, bool, error) {
	typePlaceholders := make([]string, len(types))
	args := []any{query, normalize.Name(query), normalize.Path(query)}
	for index, nodeType := range types {
		typePlaceholders[index] = "?"
		args = append(args, nodeType)
	}
	args = append(args, MaxSearchResults+1)

	statement := fmt.Sprintf(`
WITH input(id_query, name_query, path_query) AS (VALUES (?, ?, ?))
SELECT node.node_id,
	node.node_id = input.id_query,
	node.name_normalized = input.name_query,
	COALESCE(node.path_normalized = input.path_query, 0)
FROM node
CROSS JOIN input
WHERE node.node_type IN (%s)
	AND (node.node_id = input.id_query
		OR node.name_normalized = input.name_query
		OR node.path_normalized = input.path_query)
ORDER BY
	CASE
		WHEN node.node_id = input.id_query THEN 0
		WHEN node.name_normalized = input.name_query THEN 1
		ELSE 2
	END,
	CASE WHEN node.path IS NULL THEN 1 ELSE 0 END,
	node.path COLLATE BINARY,
	node.node_id COLLATE BINARY
LIMIT ?`, strings.Join(typePlaceholders, ","))

	rows, err := db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, false, fmt.Errorf("search target candidates: %w", err)
	}
	defer rows.Close()

	candidates := make([]candidate, 0, MaxSearchResults+1)
	for rows.Next() {
		var current candidate
		var matchedID, matchedName, matchedPath bool
		if err := rows.Scan(&current.id, &matchedID, &matchedName, &matchedPath); err != nil {
			return nil, false, fmt.Errorf("scan target candidate: %w", err)
		}
		current.matchedBy = make([]string, 0, 3)
		if matchedID {
			current.matchedBy = append(current.matchedBy, "id")
		}
		if matchedName {
			current.matchedBy = append(current.matchedBy, "name")
		}
		if matchedPath {
			current.matchedBy = append(current.matchedBy, "path")
		}
		candidates = append(candidates, current)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate target candidates: %w", err)
	}
	return candidates, len(candidates) > MaxSearchResults, nil
}
