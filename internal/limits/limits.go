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

	// MaxSnapshotNodesは、単一検索の内部処理（Traverse、Scan、Build）の中央値が396.929 msだった測定規模を採用する。
	// 同じ規模における並行度4の内部処理のp95は839.499 msであり、HTTP層を含む最終p95はIssue #13で確認する。
	MaxSnapshotNodes = 10_000
	// MaxSnapshotRelationsは、MaxSnapshotNodesと組み合わせた単一検索の内部処理中央値を測定した条件に合わせる。
	// relationを50,000件へ増やした条件は、HTTP層の処理前に内部処理の中央値が1秒を超えた。
	MaxSnapshotRelations = 25_000
	// MaxSnapshotLimitsは、ノード数とrelation数が受入上限の条件で、5,000件を欠落なく返した測定規模を採用する。
	// 53件から5,000件へ増やしたときの内部処理時間の増加は約9%であり、リミット数は支配要因ではなかった。
	MaxSnapshotLimits = 5_000
	// MaxSCCNodesは、BuildがSCCサイズに対して超線形に増えるため、内部処理の中央値が717 msだった3,000ノードを受入上限とする。
	// 4,000ノードでは1.305秒となったため取込時に拒否し、受入済みスナップショットの検索はSCCサイズで打ち切らない。
	MaxSCCNodes = 3_000
	// MaxJobNetworkDepthは、測定した入力からscope展開の深さだけが増える未測定条件を受け入れない。
	// 入れ子を64階層まで取込時に許可し、検索時には深さを理由に結果を打ち切らない。
	MaxJobNetworkDepth = 64
	// MaxSearchConnectionsは、並行度4の内部処理がp95 839.499 msとなり、後続解析の1秒目標を満たした接続数である。
	// 並行度8では接続待ち以外が制約となったため、測定根拠のない接続追加は行わない。
	MaxSearchConnections = 4
)
