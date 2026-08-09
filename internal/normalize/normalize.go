// Package normalize は、完全一致検索で同一視する名前とパスの表記を揃える。
package normalize

import (
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

var unicodeCaseFolder = cases.Fold()

// Name は、表記幅、前後空白、大文字小文字を同一視する検索用の名前を返す。
func Name(value string) string {
	return unicodeCaseFolder.String(strings.TrimSpace(norm.NFKC.String(value)))
}

// Path は、表記幅と前後空白を同一視し、大文字小文字を区別する検索用のパスを返す。
func Path(value string) string {
	return strings.TrimSpace(norm.NFKC.String(value))
}
