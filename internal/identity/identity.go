// Package identity は、namespace内のlocal IDとsnapshot内canonical IDの相互変換を提供する。
package identity

import (
	"strconv"
	"strings"
)

const (
	// DefaultNamespace はschema 0.5以前の単一定義セットをAPI上で表す暗黙namespaceである。
	DefaultNamespace = "default"
	// MaxNamespaceLength はcanonical snapshotとHTTP selectorで共有するnamespaceのUnicode文字数上限である。
	MaxNamespaceLength = 256
	canonicalPrefix    = "bsid1:"
)

// Encode はnamespaceとlocal IDから決定的なcanonical node IDを生成する。
// namespaceのUTF-8 byte長を先に記録するため、どちらの値に':'等が含まれても曖昧にならない。
func Encode(namespace, localID string) string {
	return canonicalPrefix + strconv.Itoa(len([]byte(namespace))) + ":" + namespace + ":" + localID
}

// Decode はEncodeで生成したcanonical node IDをnamespaceとlocal IDへ戻す。
// canonicalでない表記、空namespace、空local IDは受理しない。
func Decode(canonicalID string) (namespace string, localID string, ok bool) {
	if !strings.HasPrefix(canonicalID, canonicalPrefix) {
		return "", "", false
	}
	rest := canonicalID[len(canonicalPrefix):]
	separator := strings.IndexByte(rest, ':')
	if separator <= 0 {
		return "", "", false
	}
	lengthText := rest[:separator]
	if len(lengthText) > 1 && lengthText[0] == '0' {
		return "", "", false
	}
	namespaceBytes, err := strconv.Atoi(lengthText)
	if err != nil || namespaceBytes <= 0 {
		return "", "", false
	}
	rest = rest[separator+1:]
	if namespaceBytes >= len(rest) || rest[namespaceBytes] != ':' {
		return "", "", false
	}
	namespace = rest[:namespaceBytes]
	localID = rest[namespaceBytes+1:]
	if namespace == "" || localID == "" || Encode(namespace, localID) != canonicalID {
		return "", "", false
	}
	return namespace, localID, true
}

// PublicFields はcanonical IDを利用者向けnamespace/local IDへ変換する。
// legacy snapshotのopaque IDは暗黙default namespaceのlocal IDとして扱う。
func PublicFields(canonicalID string) (namespace string, localID string) {
	if namespace, localID, ok := Decode(canonicalID); ok {
		return namespace, localID
	}
	return DefaultNamespace, canonicalID
}
