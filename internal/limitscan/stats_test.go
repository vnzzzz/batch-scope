package limitscan

import (
	"context"
	"fmt"
	"testing"

	"batchscope/internal/traversal"
)

func TestPrepareScopeRootsResolvesDeepHierarchyLinearly(t *testing.T) {
	const depth = 500

	reachedNodes := make([]traversal.Reached, 0, depth+1)
	scopeEdges := make([]traversal.ScopeEdge, 0, depth)
	rootID := "NET-000"
	reachedNodes = append(reachedNodes, traversal.Reached{
		Node:       traversal.Node{ID: rootID, Type: "job_network", Name: rootID},
		Membership: traversal.MembershipDownstream,
	})
	parentID := rootID
	for index := 1; index <= depth; index++ {
		id := fmt.Sprintf("NET-%03d", index)
		reachedNodes = append(reachedNodes, traversal.Reached{
			Node:       traversal.Node{ID: id, Type: "job_network", Name: id},
			Membership: traversal.MembershipDownstream,
		})
		scopeEdges = append(scopeEdges, traversal.ScopeEdge{ParentID: parentID, ChildID: id})
		parentID = id
	}

	result := traversal.Result{Nodes: reachedNodes, ScopeEdges: scopeEdges}
	state := newScanState(context.Background(), nil, result)
	if err := state.prepare(result); err != nil {
		t.Fatal(err)
	}
	if got := state.scopeParentReferenceCount; got < 1 {
		t.Errorf("scope parent references = %d, want at least 1", got)
	}
	// 解決済みpathを再利用すれば親参照はedge数に対して線形に収まる。
	// exactな回数には固定せず、各nodeからrootまで独立に辿る二次退行だけを検出する。
	if got, maxLinearReferences := state.scopeParentReferenceCount, 2*len(scopeEdges); got > maxLinearReferences {
		t.Errorf("scope parent references = %d, want at most %d for %d scope edges", got, maxLinearReferences, len(scopeEdges))
	}
	if root := state.scopeRoots[parentID]; root.ID != rootID {
		t.Errorf("deepest scope root = %q, want %q", root.ID, rootID)
	}
}
