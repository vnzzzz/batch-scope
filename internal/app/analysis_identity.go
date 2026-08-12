package app

import (
	"encoding/json"

	"batchscope/internal/identity"

	"github.com/danielgtaylor/huma/v2"
)

// MarshalJSONはschema 0.6のcanonical IDからnamespace/localIdを公開する。
// 旧形式IDは従来のid/type/nameだけを返し、0.5レスポンス互換を維持する。
func (node analysisNode) MarshalJSON() ([]byte, error) {
	namespace, localID, namespaced := identity.Decode(node.ID)
	if !namespaced {
		return json.Marshal(struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			Name string `json:"name"`
		}{ID: node.ID, Type: node.Type, Name: node.Name})
	}
	return json.Marshal(struct {
		ID        string `json:"id"`
		Namespace string `json:"namespace"`
		LocalID   string `json:"localId"`
		Type      string `json:"type"`
		Name      string `json:"name"`
	}{
		ID: node.ID, Namespace: namespace, LocalID: localID, Type: node.Type, Name: node.Name,
	})
}

// Schemaはnamespaced responseで追加される公開identityフィールドをOpenAPIへ反映する。
// namespace/localIdはlegacy schema 0.5 responseでは省略されるためrequiredにはしない。
func (analysisNode) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{
		Type: huma.TypeObject,
		Properties: map[string]*huma.Schema{
			"id":        {Type: huma.TypeString},
			"namespace": {Type: huma.TypeString},
			"localId":   {Type: huma.TypeString},
			"type": {
				Type: huma.TypeString,
				Enum: []any{"management_unit", "job_network", "job", "file", "file_pattern", "job_status", "external_event"},
			},
			"name": {Type: huma.TypeString},
		},
		Required: []string{"id", "type", "name"},
	}
}
