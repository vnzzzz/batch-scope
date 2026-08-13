// Package identity は、namespace付きcanonical node identityの生成と公開identityの解決を提供する。
package identity

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

const Prefix = "bs1."

var ErrInvalidCanonicalID = errors.New("invalid canonical node ID")

// Public はAPIで表示するnamespaceとsource-local IDを保持する。
type Public struct {
	Namespace string
	LocalID   string
}

// Canonical はnamespaceとsource-local IDから決定的なcanonical node IDを生成する。
// Raw URL-safe Base64を使うため、入力値に区切り文字相当の文字が含まれても曖昧にならない。
func Canonical(namespace, localID string) string {
	return Prefix + base64.RawURLEncoding.EncodeToString([]byte(namespace)) + "." + base64.RawURLEncoding.EncodeToString([]byte(localID))
}

// LocalIDSuffix はcanonical ID末尾のlocal ID部分を返す。
// namespace未指定のlocal ID検索で、旧SQLiteのfallback候補を絞るために使う。
func LocalIDSuffix(localID string) string {
	return "." + base64.RawURLEncoding.EncodeToString([]byte(localID))
}

// Decode はCanonicalで生成したIDをnamespaceとsource-local IDへ戻す。
func Decode(id string) (namespace, localID string, err error) {
	if !strings.HasPrefix(id, Prefix) {
		return "", "", ErrInvalidCanonicalID
	}
	parts := strings.Split(strings.TrimPrefix(id, Prefix), ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", ErrInvalidCanonicalID
	}
	namespaceBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(namespaceBytes) == 0 {
		return "", "", ErrInvalidCanonicalID
	}
	localIDBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(localIDBytes) == 0 {
		return "", "", ErrInvalidCanonicalID
	}
	return string(namespaceBytes), string(localIDBytes), nil
}

// LegacyPublic はnamespace情報を持たない従来nodeを暗黙のdefault namespaceへ写像する。
// IDが偶然bs1形式に見えても復号せず、従来IDをlocalIdとしてそのまま保持する。
func LegacyPublic(id string) Public {
	return Public{Namespace: "default", LocalID: id}
}

// PublicIdentity はsnapshot入力からSQLiteへ保存する公開identityを決める。
// namespace/localIdが明示されない従来nodeではcanonical IDの見た目を解釈しない。
func PublicIdentity(id, namespace, localID string) (string, string) {
	if namespace != "" || localID != "" {
		return namespace, localID
	}
	legacy := LegacyPublic(id)
	return legacy.Namespace, legacy.LocalID
}

// LoadPublic はSQLiteに保存した公開identityをnode IDごとに返す。
// namespace導入前の旧SQLiteにはnode_identityが存在しないため、その場合は全IDをlegacyとして扱う。
// 新SQLiteで個別行が欠けている場合も、手作りtest DBとの互換性のためlegacyへfallbackする。
func LoadPublic(ctx context.Context, db *sql.DB, ids []string) (map[string]Public, error) {
	result := make(map[string]Public, len(ids))
	requested := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if _, exists := requested[id]; exists {
			continue
		}
		requested[id] = struct{}{}
		result[id] = LegacyPublic(id)
	}
	if len(requested) == 0 {
		return result, nil
	}

	var exists int
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM sqlite_schema WHERE type = 'table' AND name = 'node_identity'
	)`).Scan(&exists); err != nil {
		return nil, fmt.Errorf("check node identity table: %w", err)
	}
	if exists == 0 {
		return result, nil
	}

	// 大規模解析では全件を一度走査した方が、数百回のIN queryより安定する。
	if len(requested) > 2_000 {
		rows, err := db.QueryContext(ctx, `SELECT node_id, namespace, local_id FROM node_identity`)
		if err != nil {
			return nil, fmt.Errorf("load node identities: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id, namespace, localID string
			if err := rows.Scan(&id, &namespace, &localID); err != nil {
				return nil, fmt.Errorf("scan node identity: %w", err)
			}
			if _, wanted := requested[id]; wanted {
				result[id] = Public{Namespace: namespace, LocalID: localID}
			}
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterate node identities: %w", err)
		}
		return result, nil
	}

	const batchSize = 500
	uniqueIDs := make([]string, 0, len(requested))
	for id := range requested {
		uniqueIDs = append(uniqueIDs, id)
	}
	for start := 0; start < len(uniqueIDs); start += batchSize {
		end := min(start+batchSize, len(uniqueIDs))
		batch := uniqueIDs[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, len(batch))
		for index, id := range batch {
			args[index] = id
		}
		rows, err := db.QueryContext(ctx, `SELECT node_id, namespace, local_id
			FROM node_identity WHERE node_id IN (`+placeholders+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("load node identities: %w", err)
		}
		for rows.Next() {
			var id, namespace, localID string
			if err := rows.Scan(&id, &namespace, &localID); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan node identity: %w", err)
			}
			result[id] = Public{Namespace: namespace, LocalID: localID}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("iterate node identities: %w", err)
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("close node identities: %w", err)
		}
	}
	return result, nil
}
