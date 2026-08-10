package graphgen

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"batchscope/internal/snapshot"
)

func TestSmallIsDeterministicAndHasExactProfile(t *testing.T) {
	first := Small()
	second := Small()
	if !reflect.DeepEqual(first, second) {
		t.Fatal("Small() returned different datasets")
	}
	if len(first.Nodes) != SmallNodeCount || len(first.Relations) != SmallRelationCount {
		t.Fatalf("Small() size = %d nodes, %d relations", len(first.Nodes), len(first.Relations))
	}
	firstArchive, err := first.Archive(false)
	if err != nil {
		t.Fatal(err)
	}
	secondArchive, err := second.Archive(false)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstArchive, secondArchive) {
		t.Fatal("Small() archives differ")
	}
}

func TestCustomHasRequestedSizeAndTwoTargets(t *testing.T) {
	dataset := Custom(10_000, 27_500)
	if got := len(dataset.Nodes); got != 10_000 {
		t.Fatalf("nodes = %d, want 10000", got)
	}
	if got := len(dataset.Relations); got != 27_500 {
		t.Fatalf("relations = %d, want 27500", got)
	}
	if got := len(dataset.Expectations); got != 2 {
		t.Fatalf("expectations = %d, want 2", got)
	}
}

func TestSmallHasDeepLayeredShapeAndTwoTargets(t *testing.T) {
	dataset := Small()
	if len(dataset.Expectations) != 2 {
		t.Fatalf("expectations = %d, want 2", len(dataset.Expectations))
	}

	compressionCount := SmallNodeCount / 100
	regularCount := SmallNodeCount - (1 + 5 + 3) - SmallNodeCount/10 - compressionCount
	relations := make(map[string]struct{}, len(dataset.Relations))
	outgoing := make(map[string]int)
	incoming := make(map[string]int)
	for _, relation := range dataset.Relations {
		relations[relation.FromID+"\x00"+relation.ToID] = struct{}{}
		outgoing[relation.FromID]++
		incoming[relation.ToID]++
	}
	if len(relations) != len(dataset.Relations) {
		t.Fatalf("unique relation pairs = %d, want %d for constructive tree count", len(relations), len(dataset.Relations))
	}
	assertConnection := func(fromID, toID string) {
		t.Helper()
		if _, ok := relations[fromID+"\x00"+toID]; !ok {
			t.Errorf("missing constructed path connection %s -> %s", fromID, toID)
		}
	}
	assertConnection("JOB-TARGET", "COMPRESS-0000000")
	for index := 0; index+1 < compressionCount; index++ {
		assertConnection(fmt.Sprintf("COMPRESS-%07d", index), fmt.Sprintf("COMPRESS-%07d", index+1))
	}
	assertConnection(fmt.Sprintf("COMPRESS-%07d", compressionCount-1), "JOB-0000000")
	for index := 0; index+1 < regularCount; index++ {
		assertConnection(fmt.Sprintf("JOB-%07d", index), fmt.Sprintf("JOB-%07d", index+1))
	}
	assertConnection(fmt.Sprintf("JOB-%07d", regularCount-1), "RESOURCE-0000000")
	branching, merging := 0, 0
	for _, count := range outgoing {
		if count > 1 {
			branching++
		}
	}
	for _, count := range incoming {
		if count > 1 {
			merging++
		}
	}
	if branching < regularCount/2 || merging < regularCount/2 {
		t.Errorf("branching/merging nodes = %d/%d, want at least %d each", branching, merging, regularCount/2)
	}

	resourceTypes := make(map[string]int)
	parents := make(map[string]string)
	for _, node := range dataset.Nodes {
		if strings.HasPrefix(node.ID, "RESOURCE-") {
			resourceTypes[node.Type]++
		}
		if node.ParentID != nil {
			parents[node.ID] = *node.ParentID
		}
	}
	for _, nodeType := range []string{"file", "file_pattern", "job_status", "external_event"} {
		if resourceTypes[nodeType] == 0 {
			t.Errorf("resource type %q is missing", nodeType)
		}
	}
	if parents["NET-TARGET-NESTED"] != "NET-TARGET" || parents["NET-DOWNSTREAM-NESTED"] != "NET-DOWNSTREAM" {
		t.Errorf("nested network parents = %q/%q", parents["NET-TARGET-NESTED"], parents["NET-DOWNSTREAM-NESTED"])
	}

	wants := []struct {
		targetID         string
		reachedNodes     int
		treeNodes        int
		uncoveredRoutes  int
		membershipLimits LimitsExpectation
	}{
		{
			targetID: "NET-TARGET", reachedNodes: 9_998, treeNodes: 24_906, uncoveredRoutes: 1,
			membershipLimits: limitCounts(0, 0, 0, 1, 1, 1, 16, 15, 15),
		},
		{
			targetID: "JOB-TARGET", reachedNodes: 9_995, treeNodes: 24_902, uncoveredRoutes: 0,
			membershipLimits: limitCounts(1, 1, 1, 0, 0, 0, 16, 15, 15),
		},
	}
	for index, want := range wants {
		got := dataset.Expectations[index]
		if got.TargetID != want.targetID || len(got.ReachedNodes) != want.reachedNodes {
			t.Errorf("expectation[%d] target/reached = %s/%d, want %s/%d", index, got.TargetID, len(got.ReachedNodes), want.targetID, want.reachedNodes)
		}
		if got.TreeNodeCount != want.treeNodes || len(got.HiddenConnections) != compressionCount+1 {
			t.Errorf("expectation[%d] tree/hidden = %d/%d, want %d/%d", index, got.TreeNodeCount, len(got.HiddenConnections), want.treeNodes, compressionCount+1)
		}
		if len(got.SCCs) != 3 || len(got.UncoveredRoutes) != want.uncoveredRoutes {
			t.Errorf("expectation[%d] SCC/uncovered = %d/%d, want 3/%d", index, len(got.SCCs), len(got.UncoveredRoutes), want.uncoveredRoutes)
		}
		if !reflect.DeepEqual(limitCountShape(got.Limits), want.membershipLimits) {
			t.Errorf("expectation[%d] limit counts = %#v, want %#v", index, limitCountShape(got.Limits), want.membershipLimits)
		}
	}
}

func limitCounts(targetFinish, targetElapsed, targetRaw, containedFinish, containedElapsed, containedRaw, downstreamFinish, downstreamElapsed, downstreamRaw int) LimitsExpectation {
	return LimitsExpectation{
		Target:     LimitIDs{FinishBy: make([]string, targetFinish), MaxElapsed: make([]string, targetElapsed), Raw: make([]string, targetRaw)},
		Contained:  LimitIDs{FinishBy: make([]string, containedFinish), MaxElapsed: make([]string, containedElapsed), Raw: make([]string, containedRaw)},
		Downstream: LimitIDs{FinishBy: make([]string, downstreamFinish), MaxElapsed: make([]string, downstreamElapsed), Raw: make([]string, downstreamRaw)},
	}
}

func limitCountShape(limits LimitsExpectation) LimitsExpectation {
	return limitCounts(
		len(limits.Target.FinishBy), len(limits.Target.MaxElapsed), len(limits.Target.Raw),
		len(limits.Contained.FinishBy), len(limits.Contained.MaxElapsed), len(limits.Contained.Raw),
		len(limits.Downstream.FinishBy), len(limits.Downstream.MaxElapsed), len(limits.Downstream.Raw),
	)
}

func TestPathologicalCasesAreIndependentValidSnapshots(t *testing.T) {
	for _, kind := range PathologicalCases() {
		t.Run(string(kind), func(t *testing.T) {
			dataset := Pathological(kind)
			if dataset.Name != string(kind) {
				t.Fatalf("name = %q", dataset.Name)
			}
			files, err := dataset.WriteFiles(t.TempDir(), false)
			if err != nil {
				t.Fatal(err)
			}
			extracted := snapshot.Extracted{Directory: files.Directory, Manifest: files.Manifest, Nodes: files.Nodes, Relations: files.Relations}
			got, err := snapshot.Validate(context.Background(), extracted)
			if err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if got.NodeCount != len(dataset.Nodes) || got.RelationCount != len(dataset.Relations) {
				t.Fatalf("Validate() = %#v, want %d nodes and %d relations", got, len(dataset.Nodes), len(dataset.Relations))
			}
		})
	}
}

func TestMediumAndScaleProfiles(t *testing.T) {
	if testing.Short() || os.Getenv("BATCHSCOPE_RUN_SCALE_DATASETS") == "" {
		t.Skip("set BATCHSCOPE_RUN_SCALE_DATASETS to generate Medium and Scale")
	}
	for _, test := range []struct {
		name          string
		generate      func() Dataset
		nodeCount     int
		relationCount int
	}{
		{name: "Medium", generate: Medium, nodeCount: MediumNodeCount, relationCount: MediumRelationCount},
		{name: "Scale", generate: Scale, nodeCount: ScaleNodeCount, relationCount: ScaleRelationCount},
	} {
		t.Run(test.name, func(t *testing.T) {
			dataset := test.generate()
			if len(dataset.Nodes) != test.nodeCount || len(dataset.Relations) != test.relationCount {
				t.Fatalf("size = %d nodes, %d relations", len(dataset.Nodes), len(dataset.Relations))
			}
		})
	}
}
