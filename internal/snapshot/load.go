package snapshot

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"batchscope/internal/identity"
	"batchscope/internal/normalize"
)

const secondsPerBusinessDay int64 = 24 * 60 * 60

// Load は検査済みの展開結果を再読込し、一つのトランザクションで検索用SQLiteへ登録する。
// validatedは同じextractedに対するValidateの結果でなければならない。
func Load(ctx context.Context, db *sql.DB, extracted Extracted, validated ValidationResult) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin snapshot load: %w", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()

	// 入力順に関係なく親より先に子を登録できるよう、外部キー検査は全行登録後のcommitまで遅延する。
	if _, err := tx.ExecContext(ctx, "PRAGMA defer_foreign_keys = ON"); err != nil {
		return fmt.Errorf("defer snapshot foreign keys: %w", err)
	}

	// graph本体はcanonical node_idを主キーのまま維持し、利用者向けidentityだけを検索用tableへ分離する。
	// local_id先頭のindexにより、namespace未指定のジョブID検索でも全node scanを避ける。
	if _, err := tx.ExecContext(ctx, `
CREATE TABLE node_identity (
    node_id TEXT PRIMARY KEY REFERENCES node(node_id),
    namespace TEXT NOT NULL,
    local_id TEXT NOT NULL,
    UNIQUE(namespace, local_id)
);
CREATE INDEX idx_node_identity_local ON node_identity(local_id, namespace, node_id);`); err != nil {
		return fmt.Errorf("create node identity index: %w", err)
	}

	nodeStatement, err := tx.PrepareContext(ctx, `INSERT INTO node (
        node_id, node_type, name, name_normalized, path, path_normalized,
        parent_id, locator_json, attributes_json
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare node insert: %w", err)
	}
	defer nodeStatement.Close()

	identityStatement, err := tx.PrepareContext(ctx, `INSERT INTO node_identity (
        node_id, namespace, local_id
    ) VALUES (?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare node identity insert: %w", err)
	}
	defer identityStatement.Close()

	limitStatement, err := tx.PrepareContext(ctx, `INSERT INTO limit_fact (
        limit_id, node_id, kind, business_day_offset, local_time_seconds,
        time_zone, finish_sort_seconds, duration_seconds, source_text, origin, certainty
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare limit insert: %w", err)
	}
	defer limitStatement.Close()

	relationStatement, err := tx.PrepareContext(ctx, `INSERT INTO relation (
        relation_id, from_id, to_id, relation_kind, origin, certainty, evidence_json
    ) VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare relation insert: %w", err)
	}
	defer relationStatement.Close()

	nodeCount, err := loadNodes(ctx, extracted.Nodes, nodeStatement, identityStatement, limitStatement)
	if err != nil {
		return err
	}
	if nodeCount != validated.NodeCount {
		return fmt.Errorf("nodes changed after validation: loaded %d, validated %d", nodeCount, validated.NodeCount)
	}

	relationCount, err := loadRelations(ctx, extracted.Relations, relationStatement)
	if err != nil {
		return err
	}
	if relationCount != validated.RelationCount {
		return fmt.Errorf("relations changed after validation: loaded %d, validated %d", relationCount, validated.RelationCount)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit snapshot load: %w", err)
	}
	return nil
}

func loadNodes(ctx context.Context, path string, nodes, identities, limits *sql.Stmt) (int, error) {
	count := 0
	err := readNDJSON(ctx, path, nodesName, func(line int, contents []byte) error {
		count++
		var current nodeInput
		if err := json.Unmarshal(contents, &current); err != nil {
			return &Error{Kind: ErrorInvalidJSON, File: nodesName, Line: line, Err: err}
		}
		var explicit struct {
			Namespace string `json:"namespace"`
			LocalID   string `json:"localId"`
		}
		if err := json.Unmarshal(contents, &explicit); err != nil {
			return &Error{Kind: ErrorInvalidJSON, File: nodesName, Line: line, Err: err}
		}
		namespace, localID := identity.PublicIdentity(current.ID, explicit.Namespace, explicit.LocalID)

		var normalizedPath any
		if current.Path != nil {
			normalizedPath = normalize.Path(*current.Path)
		}
		if _, err := nodes.ExecContext(ctx,
			current.ID,
			current.Type,
			current.Name,
			normalize.Name(current.Name),
			current.Path,
			normalizedPath,
			current.ParentID,
			nullableJSON(current.Locator),
			nullableJSON(current.Attributes),
		); err != nil {
			return fmt.Errorf("insert %s:%d: %w", nodesName, line, err)
		}
		if _, err := identities.ExecContext(ctx, current.ID, namespace, localID); err != nil {
			return fmt.Errorf("insert %s:%d identity: %w", nodesName, line, err)
		}

		for factIndex, fact := range current.LimitFacts {
			if err := insertLimit(ctx, limits, current.ID, fact); err != nil {
				return fmt.Errorf("insert %s:%d limitFacts[%d]: %w", nodesName, line, factIndex, err)
			}
		}
		return nil
	})
	return count, err
}

func insertLimit(ctx context.Context, statement *sql.Stmt, nodeID string, fact limitFact) error {
	var businessDayOffset, localTimeSeconds, timeZone, finishSortSeconds, duration any
	switch fact.Kind {
	case "finish_by":
		seconds, err := parseLocalTimeSeconds(fact.LocalTime)
		if err != nil {
			return err
		}
		offset, ok := jsonIntegerAsInt64(fact.BusinessDayOffset)
		if !ok {
			return fmt.Errorf("business day offset %q is not an int64", fact.BusinessDayOffset)
		}
		businessDayOffset = offset
		localTimeSeconds = seconds
		timeZone = fact.TimeZone
		finishSortSeconds = offset*secondsPerBusinessDay + seconds
	case "max_elapsed":
		seconds, ok := durationSeconds(fact.Duration)
		if !ok {
			return fmt.Errorf("duration %q is not a fixed number of seconds", fact.Duration)
		}
		duration = seconds
	case "raw":
		// rawは比較可能な値へ推測変換せず、入力元の表記だけを保存する。
	default:
		return fmt.Errorf("unsupported limit kind %q", fact.Kind)
	}

	_, err := statement.ExecContext(ctx,
		fact.ID,
		nodeID,
		fact.Kind,
		businessDayOffset,
		localTimeSeconds,
		timeZone,
		finishSortSeconds,
		duration,
		fact.SourceText,
		fact.Origin,
		fact.Certainty,
	)
	return err
}

func loadRelations(ctx context.Context, path string, statement *sql.Stmt) (int, error) {
	count := 0
	err := readNDJSON(ctx, path, relationsName, func(line int, contents []byte) error {
		count++
		var current relation
		if err := json.Unmarshal(contents, &current); err != nil {
			return &Error{Kind: ErrorInvalidJSON, File: relationsName, Line: line, Err: err}
		}
		id, err := relationID(current)
		if err != nil {
			return fmt.Errorf("regenerate relation ID for %s:%d: %w", relationsName, line, err)
		}
		if _, err := statement.ExecContext(ctx,
			hex.EncodeToString(id[:]),
			current.FromID,
			current.ToID,
			current.Kind,
			current.Origin,
			current.Certainty,
			nullableJSON(current.Evidence),
		); err != nil {
			return fmt.Errorf("insert %s:%d: %w", relationsName, line, err)
		}
		return nil
	})
	return count, err
}

func parseLocalTimeSeconds(value string) (int64, error) {
	parsed, err := time.Parse("15:04:05", value)
	if err != nil {
		return 0, fmt.Errorf("invalid local time %q", value)
	}
	return int64(parsed.Hour()*60*60 + parsed.Minute()*60 + parsed.Second()), nil
}

func nullableJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return string(value)
}
