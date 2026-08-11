package target

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"batchscope/internal/normalize"
)

// MaxSearchResults は応答サイズを制限する。超過時は全件確認済みとせずTruncatedで通知する。
const MaxSearchResults = 1_000

// SearchItem は検索に一致した対象と一致項目を表す。
type SearchItem struct {
	Node
	MatchedBy    []string   `json:"matchedBy" enum:"id,name,path" nullable:"false"`
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

// Search はID、名前、完全パスとの完全一致を検索し、定めた順序で最大1,000件を返す。
func Search(ctx context.Context, db *sql.DB, query string, types []string) (SearchResult, error) {
	candidates, truncated, err := searchCandidates(ctx, db, query, types)
	if err != nil {
		return SearchResult{}, err
	}
	if truncated {
		candidates = candidates[:MaxSearchResults]
	}

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
