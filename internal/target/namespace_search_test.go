package target

import (
	"context"
	"reflect"
	"testing"

	"batchscope/internal/identity"
)

func TestSearchByLocalIDReturnsEveryNamespace(t *testing.T) {
	mainID := identity.Canonical("main", "JOB-A")
	drID := identity.Canonical("dr", "JOB-A")
	nodes := []testNode{
		{id: mainID, typeName: "job", name: "Main A"},
		{id: drID, typeName: "job", name: "DR A"},
		{id: identity.Canonical("main", "JOB-B"), typeName: "job", name: "Main B"},
	}
	db := openSearchDB(t, "namespaces", nodes, nil)

	result, err := SearchByLocalID(context.Background(), db, "JOB-A", "", []string{"job"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Truncated {
		t.Fatal("unexpected truncated result")
	}
	if len(result.Items) != 2 {
		t.Fatalf("items = %#v", result.Items)
	}
	if got := []string{result.Items[0].Namespace, result.Items[1].Namespace}; !reflect.DeepEqual(got, []string{"dr", "main"}) {
		t.Fatalf("namespaces = %v", got)
	}
	for _, item := range result.Items {
		if item.LocalID != "JOB-A" || !reflect.DeepEqual(item.MatchedBy, []string{"localId"}) {
			t.Fatalf("item = %#v", item)
		}
	}
}

func TestSearchByLocalIDCanSelectNamespace(t *testing.T) {
	mainID := identity.Canonical("main", "JOB-A")
	drID := identity.Canonical("dr", "JOB-A")
	db := openSearchDB(t, "namespace-filter", []testNode{
		{id: mainID, typeName: "job", name: "Main A"},
		{id: drID, typeName: "job", name: "DR A"},
	}, nil)

	result, err := SearchByLocalID(context.Background(), db, "JOB-A", "main", []string{"job"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].ID != mainID || result.Items[0].Namespace != "main" {
		t.Fatalf("items = %#v", result.Items)
	}
}

func TestSearchByLocalIDKeepsLegacyDefaultNamespace(t *testing.T) {
	db := openSearchDB(t, "legacy-local-id", []testNode{{id: "JOB-A", typeName: "job", name: "A"}}, nil)
	result, err := SearchByLocalID(context.Background(), db, "JOB-A", "", []string{"job"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].Namespace != "default" || result.Items[0].LocalID != "JOB-A" {
		t.Fatalf("items = %#v", result.Items)
	}
}
