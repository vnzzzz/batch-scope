package app

import (
	"encoding/json"
	"reflect"

	"batchscope/internal/identity"

	"github.com/danielgtaylor/huma/v2"
)

type analysisNodePublic struct {
	ID        string `json:"id"`
	Namespace string `json:"namespace"`
	LocalID   string `json:"localId"`
	Type      string `json:"type" enum:"management_unit,job_network,job,file,file_pattern,job_status,external_event"`
	Name      string `json:"name"`
}

// MarshalJSON は解析レスポンス組立時に同じ世代のSQLiteから解決した公開identityを出力する。
// 手作りtest DTOなどidentity未設定の場合だけlegacy defaultへfallbackし、ID文字列を暗黙復号しない。
func (node analysisNode) MarshalJSON() ([]byte, error) {
	namespace, localID := node.Namespace, node.LocalID
	if namespace == "" || localID == "" {
		legacy := identity.LegacyPublic(node.ID)
		namespace, localID = legacy.Namespace, legacy.LocalID
	}
	return json.Marshal(analysisNodePublic{
		ID: node.ID, Namespace: namespace, LocalID: localID, Type: node.Type, Name: node.Name,
	})
}

// Schema はcustom MarshalJSONと同じ公開形をOpenAPIへ反映する。
func (analysisNode) Schema(registry huma.Registry) *huma.Schema {
	return registry.Schema(reflect.TypeFor[analysisNodePublic](), true, "")
}
