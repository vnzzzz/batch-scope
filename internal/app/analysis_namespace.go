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

// MarshalJSON はlegacy snapshotを含め、解析レスポンス中の全nodeへ利用者向けidentityを付与する。
func (node analysisNode) MarshalJSON() ([]byte, error) {
	namespace, localID := identity.PublicFromID(node.ID)
	return json.Marshal(analysisNodePublic{
		ID: node.ID, Namespace: namespace, LocalID: localID, Type: node.Type, Name: node.Name,
	})
}

// Schema はcustom MarshalJSONと同じ公開形をOpenAPIへ反映する。
func (analysisNode) Schema(registry huma.Registry) *huma.Schema {
	return registry.Schema(reflect.TypeFor[analysisNodePublic](), true, "")
}
