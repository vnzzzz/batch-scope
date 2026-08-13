package snapshot

import (
	"encoding/json"
	"fmt"

	"batchscope/internal/identity"
)

// UnmarshalJSON はlegacy nodeの互換性を維持しつつ、namespace/localIdが明示されたnodeでは
// canonical IDと親namespaceの整合性を行単位で検査する。
func (node *nodeInput) UnmarshalJSON(data []byte) error {
	type plainNodeInput nodeInput
	var wire struct {
		plainNodeInput
		Namespace *string `json:"namespace"`
		LocalID   *string `json:"localId"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*node = nodeInput(wire.plainNodeInput)

	if wire.Namespace == nil && wire.LocalID == nil {
		return nil
	}
	if wire.Namespace == nil || wire.LocalID == nil || *wire.Namespace == "" || *wire.LocalID == "" {
		return fmt.Errorf("namespace and localId must be specified together")
	}
	if *wire.Namespace == "default" {
		return fmt.Errorf("namespace %q is reserved for legacy nodes", *wire.Namespace)
	}
	if expected := identity.Canonical(*wire.Namespace, *wire.LocalID); node.ID != expected {
		return fmt.Errorf("canonical id %q does not match namespace/localId; want %q", node.ID, expected)
	}
	if node.ParentID == nil {
		return nil
	}
	parentNamespace, _, err := identity.Decode(*node.ParentID)
	if err != nil {
		return fmt.Errorf("parentId %q is not a namespace-aware canonical ID", *node.ParentID)
	}
	if parentNamespace != *wire.Namespace {
		return fmt.Errorf("parentId %q crosses namespace %q -> %q", *node.ParentID, *wire.Namespace, parentNamespace)
	}
	return nil
}
