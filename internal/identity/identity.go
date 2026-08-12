// Package identity は、namespace付きcanonical node identityの生成と復号を提供する。
package identity

import (
	"encoding/base64"
	"errors"
	"strings"
)

const Prefix = "bs1."

var ErrInvalidCanonicalID = errors.New("invalid canonical node ID")

// Canonical はnamespaceとsource-local IDから決定的なcanonical node IDを生成する。
// Raw URL-safe Base64を使うため、入力値に区切り文字相当の文字が含まれても曖昧にならない。
func Canonical(namespace, localID string) string {
	return Prefix + base64.RawURLEncoding.EncodeToString([]byte(namespace)) + "." + base64.RawURLEncoding.EncodeToString([]byte(localID))
}

// LocalIDSuffix はcanonical ID末尾のlocal ID部分を返す。
// namespace未指定のlocal ID検索で、既存のnode_id索引を壊さず候補を絞るために使う。
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

// PublicFromID はAPI表示用のnamespace/localIdをcanonical IDから復元する。
// legacy IDは互換性のため暗黙のdefault namespaceへ写像し、localIdは従来のIDをそのまま使う。
func PublicFromID(id string) (namespace, localID string) {
	namespace, localID, err := Decode(id)
	if err == nil {
		return namespace, localID
	}
	return "default", id
}

// PublicIdentity は明示値がある場合はそれを返し、ない場合だけlegacy fallbackを使う。
func PublicIdentity(id, namespace, localID string) (string, string) {
	if namespace != "" || localID != "" {
		return namespace, localID
	}
	return PublicFromID(id)
}
