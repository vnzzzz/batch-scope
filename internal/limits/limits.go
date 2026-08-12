// Package limits は、サービスが入力と処理量へ適用する上限を定義する。
package limits

import "time"

const (
	// MaxCompressedArchiveBytes は、受信中のディスク使用量を制限する。
	MaxCompressedArchiveBytes int64 = 500 << 20
	// SnapshotUploadDeadlineは、500 MiBを約0.85 MiB/秒で送信できる10分を受信上限とする。
	// 検索SLOとは異なり、遅い送信元が一件だけの取込枠を保持し続けることを防ぐ。
	SnapshotUploadDeadline = 10 * time.Minute
	// MaxExtractedArchiveBytes は、圧縮率の高い入力による一時ディスクの枯渇を防ぐ。
	MaxExtractedArchiveBytes int64 = 4 << 30
	// MaxManifestBytes は、JSON decode前に保持するmanifest.jsonのメモリ量を制限する。
	MaxManifestBytes int64 = 1 << 20
	// MaxNDJSONLineBytes は、一件の検査で保持するメモリ量を制限する。
	MaxNDJSONLineBytes int64 = 16 << 20
	// MaxNodeIDLengthはcanonical node IDのJSON SchemaとHTTP検索境界を一致させる。
	// 受入済みスナップショットに存在し得ない長さのIDをSQLiteへ渡さない。
	MaxNodeIDLength = 1_024

	// MaxSnapshotNodesは、実運用で確認された40万ノード級を受け入れる境界とする。
	// 検索時の打切り値にはせず、この規模を超える入力を取込時に拒否して全件解析を保証する。
	MaxSnapshotNodes = 400_000
	// MaxSnapshotRelationsは、同じ実運用snapshotで確認された30万relation級を受け入れる境界とする。
	// ノード数とは独立に取込時に検査し、検索途中でrelationを切り捨てない。
	MaxSnapshotRelations = 300_000
	// MaxSnapshotLimitsは、ノード数とrelation数が受入上限の条件で全件返却を確認した最大の測定値を採用する。
	// リミット数による検索時の打切りは行わず、超過する入力を取込時に拒否する。
	MaxSnapshotLimits = 5_000
	// MaxSCCNodesは、経路ツリー生成がSCCサイズに対して超線形に増えるため、性能基準を満たした最大の測定点を採用する。
	// 検索途中では打ち切らず、超過する探索グラフを取込時に拒否する。
	MaxSCCNodes = 3_000
	// MaxJobNetworkDepthは、測定した入力からscope展開の深さだけが増える未測定条件を受け入れない。
	// 入れ子を64階層まで取込時に許可し、検索時には深さを理由に結果を打ち切らない。
	MaxJobNetworkDepth = 64
	// MaxSearchConnectionsは、想定同時検索数で性能目標を満たし、より多い接続で明確な改善が続かなかった測定条件を採用する。
	// 測定根拠のない接続追加を避け、接続数の変更は性能測定と合わせて行う。
	MaxSearchConnections = 4
)
