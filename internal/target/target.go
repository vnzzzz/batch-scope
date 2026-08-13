// Package target は、対象ノードの公開表現と親子階層の組立処理を提供する。
package target

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"batchscope/internal/identity"
)

// Node はAPIで公開する対象ノードの共通表現である。
type Node struct {
	ID        string  `json:"id"`
	Namespace string  `json:"namespace"`
	LocalID   string  `json:"localId"`
	Type      string  `json:"type" enum:"job,job_network"`
	Name      string  `json:"name"`
	Path      *string `json:"path,omitempty"`
}

// Ancestor は対象ノードの親子階層を構成する祖先の公開表現である。
type Ancestor struct {
	ID        string `json:"id"`
	Namespace string `json:"namespace"`
	LocalID   string `json:"localId"`
	Type      string `json:"type" enum:"management_unit,job_network"`
	Name      string `json:"name"`
}

// Details は対象ノードと、最上位から直近の親までの祖先を保持する。
type Details struct {
	Node
	AncestorPath []Ancestor `json:"ancestorPath" nullable:"false"`
}

// LoadOne は一件のノードIDから公開表現とancestorPathを組み立てる。
func LoadOne(ctx context.Context, db *sql.DB, nodeID string) (Details, error) {
	details, err := LoadMany(ctx, db, []string{nodeID})
	if err != nil {
		return Details{}, err
	}
	detail, ok := details[nodeID]
	if !ok {
		return Details{}, sql.ErrNoRows
	}
	return detail, nil
}

// LoadMany は複数のノード詳細とancestorPathを一回のSQLiteクエリで取得する。
// idsは重複のない既存ノードIDでなければならない。
func LoadMany(ctx context.Context, db *sql.DB, ids []string) (map[string]Details, error) {
	if len(ids) == 0 {
		return map[string]Details{}, nil
	}

	values := make([]string, len(ids))
	args := make([]any, len(ids))
	for index, id := range ids {
		values[index] = "(?)"
		args[index] = id
	}
	query := fmt.Sprintf(`
WITH RECURSIVE requested(target_id) AS (
	VALUES %s
), lineage(target_id, node_id, node_type, name, path, parent_id, depth) AS (
	SELECT requested.target_id, node.node_id, node.node_type, node.name, node.path, node.parent_id, 0
	FROM requested
	JOIN node ON node.node_id = requested.target_id
	UNION ALL
	SELECT lineage.target_id, parent.node_id, parent.node_type, parent.name, parent.path, parent.parent_id, lineage.depth + 1
	FROM lineage
	JOIN node AS parent ON parent.node_id = lineage.parent_id
)
SELECT target_id, node_id, node_type, name, path, depth
FROM lineage
ORDER BY target_id COLLATE BINARY, depth DESC`, strings.Join(values, ","))

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("load target details: %w", err)
	}
	defer rows.Close()

	result := make(map[string]Details, len(ids))
	identityIDs := make(map[string]struct{})
	for rows.Next() {
		var targetID, nodeID, nodeType, name string
		var path sql.NullString
		var depth int
		if err := rows.Scan(&targetID, &nodeID, &nodeType, &name, &path, &depth); err != nil {
			return nil, fmt.Errorf("scan target details: %w", err)
		}
		identityIDs[nodeID] = struct{}{}
		if depth == 0 {
			detail := result[targetID]
			detail.Node = Node{ID: nodeID, Type: nodeType, Name: name, Path: optionalString(path)}
			if detail.AncestorPath == nil {
				detail.AncestorPath = []Ancestor{}
			}
			result[targetID] = detail
			continue
		}
		detail := result[targetID]
		if detail.AncestorPath == nil {
			detail.AncestorPath = []Ancestor{}
		}
		detail.AncestorPath = append(detail.AncestorPath, Ancestor{ID: nodeID, Type: nodeType, Name: name})
		result[targetID] = detail
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate target details: %w", err)
	}

	publicIDs := make([]string, 0, len(identityIDs))
	for id := range identityIDs {
		publicIDs = append(publicIDs, id)
	}
	public, err := identity.LoadPublic(ctx, db, publicIDs)
	if err != nil {
		return nil, fmt.Errorf("load target public identities: %w", err)
	}
	for targetID, detail := range result {
		if value, ok := public[detail.ID]; ok {
			detail.Namespace = value.Namespace
			detail.LocalID = value.LocalID
		}
		for index := range detail.AncestorPath {
			ancestor := &detail.AncestorPath[index]
			if value, ok := public[ancestor.ID]; ok {
				ancestor.Namespace = value.Namespace
				ancestor.LocalID = value.LocalID
			}
		}
		result[targetID] = detail
	}

	for _, id := range ids {
		detail, ok := result[id]
		if !ok || detail.ID == "" {
			return nil, fmt.Errorf("load target %q: %w", id, sql.ErrNoRows)
		}
	}
	return result, nil
}

func optionalString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}
