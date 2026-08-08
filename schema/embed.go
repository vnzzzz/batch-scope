// Package schema は、取込検査で使うJSON Schemaを提供する。
package schema

import "embed"

// Files は、実行時の作業ディレクトリに依存せず取込形式を検査するためのSchemaを保持する。
//
//go:embed *.schema.json
var Files embed.FS
