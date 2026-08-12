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

// PublicIdentity はnamespace/localIdが明示されないlegacy nodeをdefault namespaceへ写像する。
// 既存snapshotのcanonical ID自体は変更しない。
func PublicIdentity(id, namespace, localID string) (string, string) {
	if namespace != "" || localID != "" {
		return namespace, localID
	}
	return "default", id
}
