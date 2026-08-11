// Package observabilityは、構造化ログで共有する安全なフィールドを定義する。
package observability

import (
	"log/slog"
	"time"
)

const (
	RequestID         = "request_id"
	Operation         = "operation"
	DurationMS        = "duration_ms"
	BootID            = "boot_id"
	SnapshotID        = "snapshot_id"
	ImportID          = "import_id"
	TargetID          = "target_id"
	ReachedNodes      = "reached_nodes"
	ReturnedTreeNodes = "returned_tree_nodes"
	ReturnedLimits    = "returned_limits"
	ReturnedTargets   = "returned_targets"
	CyclesDetected    = "cycles_detected"
	UncoveredRoutes   = "uncovered_routes"
	NodeCount         = "node_count"
	RelationCount     = "relation_count"
	LimitCount        = "limit_count"
	ImportState       = "import_state"
	ErrorType         = "error_type"
)

// Fieldsは既定ログへ載せられる共通情報だけを受け取る。
// 任意のmapを受け取らないことで、ジョブ名、完全パス、query、evidence、入力内容を誤って共通ログへ展開しない。
type Fields struct {
	RequestID         string
	Operation         string
	Duration          time.Duration
	BootID            string
	SnapshotID        string
	ImportID          string
	TargetID          string
	ReachedNodes      int
	ReturnedTreeNodes int
	ReturnedLimits    int
	ReturnedTargets   int
	CyclesDetected    int
	UncoveredRoutes   int
	NodeCount         int
	RelationCount     int
	LimitCount        int
	ImportState       string
	ErrorType         string
}

// Attrsは、設定された共通フィールドをsnake_caseの安定した名前で返す。
func Attrs(fields Fields) []slog.Attr {
	attrs := make([]slog.Attr, 0, 18)
	attrs = appendString(attrs, RequestID, fields.RequestID)
	attrs = appendString(attrs, Operation, fields.Operation)
	if fields.Duration > 0 {
		attrs = append(attrs, slog.Int64(DurationMS, fields.Duration.Milliseconds()))
	}
	attrs = appendString(attrs, BootID, fields.BootID)
	attrs = appendString(attrs, SnapshotID, fields.SnapshotID)
	attrs = appendString(attrs, ImportID, fields.ImportID)
	attrs = appendString(attrs, TargetID, fields.TargetID)
	attrs = appendCount(attrs, ReachedNodes, fields.ReachedNodes)
	attrs = appendCount(attrs, ReturnedTreeNodes, fields.ReturnedTreeNodes)
	attrs = appendCount(attrs, ReturnedLimits, fields.ReturnedLimits)
	attrs = appendCount(attrs, ReturnedTargets, fields.ReturnedTargets)
	attrs = appendCount(attrs, CyclesDetected, fields.CyclesDetected)
	attrs = appendCount(attrs, UncoveredRoutes, fields.UncoveredRoutes)
	attrs = appendCount(attrs, NodeCount, fields.NodeCount)
	attrs = appendCount(attrs, RelationCount, fields.RelationCount)
	attrs = appendCount(attrs, LimitCount, fields.LimitCount)
	attrs = appendString(attrs, ImportState, fields.ImportState)
	attrs = appendString(attrs, ErrorType, fields.ErrorType)
	return attrs
}

func appendString(attrs []slog.Attr, name, value string) []slog.Attr {
	if value == "" {
		return attrs
	}
	return append(attrs, slog.String(name, value))
}

func appendCount(attrs []slog.Attr, name string, value int) []slog.Attr {
	if value == 0 {
		return attrs
	}
	return append(attrs, slog.Int(name, value))
}
