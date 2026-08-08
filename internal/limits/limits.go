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
)
