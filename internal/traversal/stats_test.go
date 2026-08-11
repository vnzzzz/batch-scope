package traversal

import (
	"context"
	"testing"
)

func TestTraverseRecordsRelationQueries(t *testing.T) {
	result, err := Traverse(
		context.Background(),
		openTestDB(t, jobs("START", "END"), []testRelation{relation("R-1", "START", "END", "precedes")}),
		"START",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Stats.RelationQueries; got < 1 {
		t.Errorf("RelationQueries = %d, want at least 1", got)
	}
}
