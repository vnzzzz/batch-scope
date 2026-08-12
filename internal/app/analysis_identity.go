package app

import (
	"encoding/json"

	"batchscope/internal/identity"

	"github.com/danielgtaylor/huma/v2"
)

// MarshalJSONは内部canonical IDを維持したまま、チャットやAPI利用者が直接使えるnamespace/localIdを各解析nodeへ付与する。
func (node analysisNode) MarshalJSON() ([]byte, error) {
	namespace, localID := identity.PublicFields(node.ID)
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

// SchemaはMarshalJSONで追加する公開identityフィールドをOpenAPIにも反映する。
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
		Required: []string{"id", "namespace", "localId", "type", "name"},
	}
}
