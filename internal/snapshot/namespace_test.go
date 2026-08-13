package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"batchscope/internal/identity"
)

func TestNodeInputNamespaceIdentity(t *testing.T) {
	t.Parallel()
	id := identity.Canonical("main", "JOB-A")
	input := `{"type":"job","id":"` + id + `","namespace":"main","localId":"JOB-A","name":"A"}`
	var node nodeInput
	if err := json.Unmarshal([]byte(input), &node); err != nil {
		t.Fatal(err)
	}
	if node.ID != id {
		t.Fatalf("node ID = %q", node.ID)
	}
}

func TestNodeInputRejectsMismatchedCanonicalIdentity(t *testing.T) {
	t.Parallel()
	input := `{"type":"job","id":"JOB-A","namespace":"main","localId":"JOB-A","name":"A"}`
	var node nodeInput
	if err := json.Unmarshal([]byte(input), &node); err == nil || !strings.Contains(err.Error(), "does not match namespace/localId") {
		t.Fatalf("error = %v", err)
	}
}

func TestNodeInputRejectsCrossNamespaceParent(t *testing.T) {
	t.Parallel()
	childID := identity.Canonical("main", "JOB-A")
	parentID := identity.Canonical("dr", "NET-A")
	input := `{"type":"job","id":"` + childID + `","namespace":"main","localId":"JOB-A","name":"A","parentId":"` + parentID + `"}`
	var node nodeInput
	if err := json.Unmarshal([]byte(input), &node); err == nil || !strings.Contains(err.Error(), "crosses namespace") {
		t.Fatalf("error = %v", err)
	}
}

func TestNodeInputRejectsExplicitDefaultNamespace(t *testing.T) {
	t.Parallel()
	id := identity.Canonical("default", "JOB-A")
	input := `{"type":"job","id":"` + id + `","namespace":"default","localId":"JOB-A","name":"A"}`
	var node nodeInput
	if err := json.Unmarshal([]byte(input), &node); err == nil || !strings.Contains(err.Error(), "reserved for legacy nodes") {
		t.Fatalf("error = %v", err)
	}
}

func TestNodeInputKeepsLegacyCompatibility(t *testing.T) {
	t.Parallel()
	var node nodeInput
	if err := json.Unmarshal([]byte(`{"type":"job","id":"JOB-A","name":"A"}`), &node); err != nil {
		t.Fatal(err)
	}
	if node.ID != "JOB-A" {
		t.Fatalf("node ID = %q", node.ID)
	}
}

func TestValidateRejectsLegacyChildWithNamespacedParent(t *testing.T) {
	parentID := identity.Canonical("main", "NET-A")
	extracted := writeExtractedSnapshot(t, []string{
		`{"type":"job_network","id":"` + parentID + `","namespace":"main","localId":"NET-A","name":"Main network"}`,
		`{"type":"job","id":"LEGACY-JOB","name":"Legacy job","parentId":"` + parentID + `"}`,
	}, nil)

	_, err := Validate(context.Background(), extracted)
	assertParentNamespaceError(t, err)
}

func TestValidateRejectsNamespacedChildWithCanonicalLookingLegacyParent(t *testing.T) {
	parentID := identity.Canonical("main", "NET-A")
	childID := identity.Canonical("main", "JOB-A")
	extracted := writeExtractedSnapshot(t, []string{
		`{"type":"job_network","id":"` + parentID + `","name":"Legacy-looking network"}`,
		`{"type":"job","id":"` + childID + `","namespace":"main","localId":"JOB-A","name":"Main job","parentId":"` + parentID + `"}`,
	}, nil)

	_, err := Validate(context.Background(), extracted)
	assertParentNamespaceError(t, err)
}

func assertParentNamespaceError(t *testing.T, err error) {
	t.Helper()
	var snapshotErr *Error
	if !errors.As(err, &snapshotErr) || snapshotErr.Kind != ErrorInvalidParentType || snapshotErr.Pointer != "/parentId" {
		t.Fatalf("error = %#v, want invalid_parent_type at /parentId", err)
	}
}
