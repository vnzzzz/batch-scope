// Package limits は、サービスが入力と処理量へ適用する上限を定義する。
package limits

const (
	// MaxCompressedArchiveBytes は、受信中のディスク使用量を制限する。
	MaxCompressedArchiveBytes int64 = 500 << 20
	// MaxExtractedArchiveBytes は、圧縮率の高い入力による一時ディスクの枯渇を防ぐ。
	MaxExtractedArchiveBytes int64 = 4 << 30
	// MaxManifestBytes は、JSON decode前に保持するmanifest.jsonのメモリ量を制限する。
	MaxManifestBytes int64 = 1 << 20
	// MaxNDJSONLineBytes は、一件の検査で保持するメモリ量を制限する。
	MaxNDJSONLineBytes int64 = 16 << 20

	// MaxSnapshotNodesは、後続解析全体がp95 1秒の目標内に収まった測定上の最大規模を採用する。
	// 10,000ノードと25,000 relationでは中央値396.929 ms、倍の規模では1.125秒だった。
	MaxSnapshotNodes = 10_000
	// MaxSnapshotRelationsは、MaxSnapshotNodesと組み合わせて396.929 msで完遂した測定条件に合わせる。
	// relationを50,000件へ増やした条件はHTTP層の処理前にp95 1秒の予算を使い切った。
	MaxSnapshotRelations = 25_000
	// MaxSnapshotLimitsは、p95 1秒を確認した入力からリミット数だけが無制限に増える未測定条件を受け入れない。
	// ScanとBuildの完遂保証を測定済みの容量枠へ留めるため、受入時の総数を5,000件に制限する。
	MaxSnapshotLimits = 5_000
	// MaxJobNetworkDepthは、p95 1秒を確認した入力からscope展開の深さだけが増える未測定条件を受け入れない。
	// 入れ子を64階層まで取込時に許可し、検索時には深さを理由に結果を打ち切らない。
	MaxJobNetworkDepth = 64
	// MaxSearchConnectionsは、並行度4でp95 839.499 msとなり、後続解析の1秒目標を満たした接続数である。
	// 並行度8では接続待ち以外が制約となったため、測定根拠のない接続追加は行わない。
	MaxSearchConnections = 4
)
