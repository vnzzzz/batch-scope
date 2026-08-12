package graphgen

import "testing"

func TestOperationalDatasetKeepsRepresentativeRelationsInsideOneNetwork(t *testing.T) {
	dataset := operationalDataset(21, 8, 2, 3)
	if len(dataset.Nodes) != 21 {
		t.Fatalf("nodes = %d, want 21", len(dataset.Nodes))
	}
	if len(dataset.Relations) != 8 {
		t.Fatalf("relations = %d, want 8", len(dataset.Relations))
	}
	if len(dataset.Expectations) != 2 || dataset.Expectations[0].TargetID != "OPS-ROOT" || dataset.Expectations[1].TargetID != "OPS-NET-0000" {
		t.Fatalf("targets = %#v, want root and representative network", dataset.Expectations)
	}

	parents := make(map[string]string)
	limits := 0
	for _, node := range dataset.Nodes {
		if node.ParentID != nil {
			parents[node.ID] = *node.ParentID
		}
		limits += len(node.LimitFacts)
	}
	if limits != 3 {
		t.Fatalf("limits = %d, want 3", limits)
	}
	for _, relation := range dataset.Relations {
		if parents[relation.FromID] == "" || parents[relation.FromID] != parents[relation.ToID] {
			t.Fatalf("relation %s -> %s crosses network: parents %q -> %q", relation.FromID, relation.ToID, parents[relation.FromID], parents[relation.ToID])
		}
	}
}
