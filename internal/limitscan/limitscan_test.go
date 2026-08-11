package limitscan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"batchscope/internal/traversal"

	_ "modernc.org/sqlite"
)

type testNode struct {
	id       string
	typeName string
	name     string
}

type testLimit struct {
	id                string
	ownerID           string
	kind              string
	businessDayOffset any
	localTimeSeconds  any
	timeZone          any
	durationSeconds   any
	sourceText        any
	origin            string
	certainty         string
}

func TestScanReturnsEveryLimitByMembershipAndKind(t *testing.T) {
	emptySource := ""
	nodes := []testNode{
		{id: "TARGET", typeName: "job"},
		{id: "CONTAINED-A", typeName: "job"},
		{id: "CONTAINED-B", typeName: "job"},
		{id: "NET-DOWN", typeName: "job_network"},
		{id: "NET-SUB", typeName: "job_network"},
		{id: "SCOPE-JOB", typeName: "job"},
		{id: "DIRECT-A", typeName: "job"},
		{id: "DIRECT-B", typeName: "job"},
	}
	limits := []testLimit{
		rawLimit("TARGET-RAW-2", "TARGET", "10"),
		rawLimit("TARGET-RAW-1", "TARGET", "20"),
		finishLimit("CONTAINED-FINISH", "CONTAINED-B", 0, 0, "UTC"),
		rawLimit("CONTAINED-RAW", "CONTAINED-A", "contained"),
		finishLimit("TOKYO-LATE", "DIRECT-B", 0, 7200, "Asia/Tokyo"),
		finishLimit("TOKYO-PREVIOUS-DAY", "DIRECT-B", -1, 86399, "Asia/Tokyo"),
		finishLimit("TOKYO-TIE-B", "DIRECT-B", 0, 3600, "Asia/Tokyo"),
		finishLimit("TOKYO-TIE-A-2", "DIRECT-A", 0, 3600, "Asia/Tokyo"),
		finishLimit("TOKYO-TIE-A-1", "DIRECT-A", 0, 3600, "Asia/Tokyo"),
		finishLimit("UTC-EARLY", "DIRECT-A", 0, 1, "UTC"),
		elapsedLimit("ELAPSED-LONG", "DIRECT-A", 60, nil),
		elapsedLimit("ELAPSED-TIE-B", "DIRECT-B", 30, &emptySource),
		elapsedLimit("ELAPSED-TIE-A-2", "DIRECT-A", 30, nil),
		elapsedLimit("ELAPSED-TIE-A-1", "DIRECT-A", 30, nil),
		rawLimit("RAW-Z", "DIRECT-A", "2"),
		rawLimit("RAW-A", "DIRECT-A", "100"),
		rawLimit("RAW-SCOPE", "SCOPE-JOB", "scope"),
	}
	result := traversal.Result{
		Nodes: []traversal.Reached{
			reached("TARGET", "job", traversal.MembershipTarget),
			reached("CONTAINED-A", "job", traversal.MembershipContained),
			reached("CONTAINED-B", "job", traversal.MembershipContained),
			reached("NET-DOWN", "job_network", traversal.MembershipDownstream),
			reached("NET-SUB", "job_network", traversal.MembershipDownstream),
			reached("SCOPE-JOB", "job", traversal.MembershipDownstream),
			reached("DIRECT-A", "job", traversal.MembershipDownstream),
			reached("DIRECT-B", "job", traversal.MembershipDownstream),
		},
		ScopeEdges: []traversal.ScopeEdge{
			{ParentID: "NET-DOWN", ChildID: "NET-SUB"},
			{ParentID: "NET-SUB", ChildID: "SCOPE-JOB"},
		},
	}

	got, err := Scan(context.Background(), openTestDB(t, nodes, limits), result)
	if err != nil {
		t.Fatal(err)
	}
	if got.Target.Raw.Total != 2 || len(got.Target.Raw.Items) != 2 {
		t.Fatalf("target raw = %#v, want every item and matching total", got.Target.Raw)
	}
	if ids := factIDs(got.Target.Raw.Items); !slices.Equal(ids, []string{"TARGET-RAW-1", "TARGET-RAW-2"}) {
		t.Errorf("target raw IDs = %v", ids)
	}
	if got.Contained.Raw.Total != 1 || got.Contained.FinishByGroups[0].Total != 1 {
		t.Errorf("contained limits = %#v", got.Contained)
	}

	if zones := finishZones(got.Downstream.FinishByGroups); !slices.Equal(zones, []string{"Asia/Tokyo", "UTC"}) {
		t.Fatalf("finish_by zones = %v, want [Asia/Tokyo UTC]", zones)
	}
	tokyo := got.Downstream.FinishByGroups[0]
	if tokyo.Total != len(tokyo.Items) || tokyo.Total != 5 {
		t.Errorf("Tokyo total = %d, len(items) = %d, want 5", tokyo.Total, len(tokyo.Items))
	}
	if ids := factIDs(tokyo.Items); !slices.Equal(ids, []string{
		"TOKYO-PREVIOUS-DAY", "TOKYO-TIE-A-1", "TOKYO-TIE-A-2", "TOKYO-TIE-B", "TOKYO-LATE",
	}) {
		t.Errorf("Tokyo IDs = %v", ids)
	}
	if gotTime := *tokyo.Items[0].Fact.LocalTime; gotTime != "23:59:59" {
		t.Errorf("restored local time = %q, want 23:59:59", gotTime)
	}
	if gotTime := *got.Downstream.FinishByGroups[1].Items[0].Fact.LocalTime; gotTime != "00:00:01" {
		t.Errorf("restored UTC local time = %q, want 00:00:01", gotTime)
	}

	if ids := factIDs(got.Downstream.MaxElapsed.Items); !slices.Equal(ids, []string{
		"ELAPSED-TIE-A-1", "ELAPSED-TIE-A-2", "ELAPSED-TIE-B", "ELAPSED-LONG",
	}) {
		t.Errorf("max_elapsed IDs = %v", ids)
	}
	if got.Downstream.MaxElapsed.Total != len(got.Downstream.MaxElapsed.Items) {
		t.Errorf("max_elapsed total = %d, len(items) = %d", got.Downstream.MaxElapsed.Total, len(got.Downstream.MaxElapsed.Items))
	}
	if seconds := *got.Downstream.MaxElapsed.Items[0].Fact.DurationSeconds; seconds != 30 {
		t.Errorf("duration seconds = %d, want 30", seconds)
	}
	if got.Downstream.MaxElapsed.Items[0].Fact.SourceText != nil {
		t.Errorf("NULL sourceText = %#v, want nil", got.Downstream.MaxElapsed.Items[0].Fact.SourceText)
	}
	if source := got.Downstream.MaxElapsed.Items[2].Fact.SourceText; source == nil || *source != "" {
		t.Errorf("empty sourceText = %#v, want non-nil empty string", source)
	}

	if ids := factIDs(got.Downstream.Raw.Items); !slices.Equal(ids, []string{"RAW-A", "RAW-Z", "RAW-SCOPE"}) {
		t.Errorf("raw IDs = %v, want owner and limit ID order independent of source value", ids)
	}
	if got.Downstream.Raw.Total != len(got.Downstream.Raw.Items) {
		t.Errorf("raw total = %d, len(items) = %d", got.Downstream.Raw.Total, len(got.Downstream.Raw.Items))
	}
	scopeItem := got.Downstream.Raw.Items[2]
	if scopeItem.LimitOwner.ID != "SCOPE-JOB" || scopeItem.LimitOwner.Type != "job" {
		t.Errorf("LimitOwner = %#v, want SCOPE-JOB job", scopeItem.LimitOwner)
	}
	if scopeItem.ScopeRoot == nil || scopeItem.ScopeRoot.ID != "NET-DOWN" || scopeItem.ScopeRoot.Type != "job_network" {
		t.Errorf("ScopeRoot = %#v, want NET-DOWN job_network", scopeItem.ScopeRoot)
	}
	for _, item := range got.Downstream.FinishByGroups[0].Items {
		if item.ScopeRoot != nil {
			t.Errorf("directly reached owner %s has ScopeRoot %#v, want nil", item.LimitOwner.ID, item.ScopeRoot)
		}
	}
	if got.Contained.Raw.Items[0].ScopeRoot != nil {
		t.Errorf("contained ScopeRoot = %#v, want nil", got.Contained.Raw.Items[0].ScopeRoot)
	}
}

func TestScanBatchesAllReachedIDsAndDeduplicatesLimits(t *testing.T) {
	const ownerCount = 1_201
	nodes := make([]testNode, 0, ownerCount)
	limits := make([]testLimit, 0, ownerCount)
	reachedNodes := make([]traversal.Reached, 0, ownerCount+1)
	for index := 0; index < ownerCount; index++ {
		id := fmt.Sprintf("JOB-%04d", index)
		nodes = append(nodes, testNode{id: id, typeName: "job"})
		limits = append(limits, rawLimit("LIMIT-"+id, id, fmt.Sprintf("raw-%d", index)))
		reachedNodes = append(reachedNodes, reached(id, "job", traversal.MembershipDownstream))
	}
	// 同じ到達先がバッチ境界をまたいでも、同一リミットを重複して返さない。
	reachedNodes = append(reachedNodes, reached("JOB-0000", "job", traversal.MembershipDownstream))

	got, err := Scan(
		context.Background(),
		openTestDB(t, nodes, limits),
		traversal.Result{Nodes: reachedNodes},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.Downstream.Raw.Total != ownerCount || len(got.Downstream.Raw.Items) != ownerCount {
		t.Fatalf("raw = total %d, items %d, want %d without truncation or duplication", got.Downstream.Raw.Total, len(got.Downstream.Raw.Items), ownerCount)
	}
	if first, last := got.Downstream.Raw.Items[0].Fact.ID, got.Downstream.Raw.Items[ownerCount-1].Fact.ID; first != "LIMIT-JOB-0000" || last != "LIMIT-JOB-1200" {
		t.Errorf("ordered range = %q ... %q", first, last)
	}
}

func TestScanFindsScopeRootsForEveryLimitInDeepHierarchy(t *testing.T) {
	const depth = 500

	nodes := make([]testNode, 0, depth*2)
	limits := make([]testLimit, 0, depth)
	reachedNodes := make([]traversal.Reached, 0, depth*2)
	scopeEdges := make([]traversal.ScopeEdge, 0, depth*2-1)

	rootID := "NET-000"
	nodes = append(nodes, testNode{id: rootID, typeName: "job_network"})
	reachedNodes = append(reachedNodes, reached(rootID, "job_network", traversal.MembershipDownstream))
	parentID := rootID
	for index := 0; index < depth; index++ {
		jobID := fmt.Sprintf("JOB-%03d", index)
		nodes = append(nodes, testNode{id: jobID, typeName: "job"})
		limits = append(limits, rawLimit("LIMIT-"+jobID, jobID, "raw"))
		reachedNodes = append(reachedNodes, reached(jobID, "job", traversal.MembershipDownstream))
		scopeEdges = append(scopeEdges, traversal.ScopeEdge{ParentID: parentID, ChildID: jobID})

		if index+1 == depth {
			continue
		}
		networkID := fmt.Sprintf("NET-%03d", index+1)
		nodes = append(nodes, testNode{id: networkID, typeName: "job_network"})
		reachedNodes = append(reachedNodes, reached(networkID, "job_network", traversal.MembershipDownstream))
		scopeEdges = append(scopeEdges, traversal.ScopeEdge{ParentID: parentID, ChildID: networkID})
		parentID = networkID
	}

	result := traversal.Result{Nodes: reachedNodes, ScopeEdges: scopeEdges}
	first, err := Scan(context.Background(), openTestDB(t, nodes, limits), result)
	if err != nil {
		t.Fatal(err)
	}
	if total, count := first.Downstream.Raw.Total, len(first.Downstream.Raw.Items); total != depth || count != depth {
		t.Fatalf("downstream raw total = %d, items = %d, want both %d", total, count, depth)
	}
	for index, item := range first.Downstream.Raw.Items {
		wantOwnerID := fmt.Sprintf("JOB-%03d", index)
		if item.LimitOwner.ID != wantOwnerID {
			t.Errorf("item %d owner = %q, want %q", index, item.LimitOwner.ID, wantOwnerID)
		}
		if item.ScopeRoot == nil || item.ScopeRoot.ID != rootID {
			t.Errorf("item %d ScopeRoot = %#v, want %s", index, item.ScopeRoot, rootID)
		}
	}
	slices.Reverse(nodes)
	slices.Reverse(limits)
	slices.Reverse(reachedNodes)
	slices.Reverse(scopeEdges)
	second, err := Scan(
		context.Background(),
		openTestDB(t, nodes, limits),
		traversal.Result{Nodes: reachedNodes, ScopeEdges: scopeEdges},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("result differs by insertion order:\nfirst  = %#v\nsecond = %#v", first, second)
	}
}

func TestScanOrderDoesNotDependOnInsertionOrder(t *testing.T) {
	nodes := []testNode{
		{id: "A", typeName: "job"},
		{id: "B", typeName: "job"},
	}
	limits := []testLimit{
		finishLimit("F-B", "B", 0, 1, "UTC"),
		finishLimit("F-A", "A", 0, 1, "UTC"),
		elapsedLimit("M-B", "B", 10, nil),
		elapsedLimit("M-A", "A", 10, nil),
		rawLimit("R-B", "B", "1"),
		rawLimit("R-A", "A", "999"),
	}
	reachedNodes := []traversal.Reached{
		reached("B", "job", traversal.MembershipDownstream),
		reached("A", "job", traversal.MembershipDownstream),
	}
	first, err := Scan(context.Background(), openTestDB(t, nodes, limits), traversal.Result{Nodes: reachedNodes})
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(nodes)
	slices.Reverse(limits)
	slices.Reverse(reachedNodes)
	second, err := Scan(context.Background(), openTestDB(t, nodes, limits), traversal.Result{Nodes: reachedNodes})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("result differs by insertion order:\nfirst  = %#v\nsecond = %#v", first, second)
	}
}

func TestScanRejectsNonJobLimitOwner(t *testing.T) {
	nodes := []testNode{{id: "NETWORK", typeName: "job_network"}}
	limits := []testLimit{rawLimit("INVALID", "NETWORK", "raw")}
	result := traversal.Result{Nodes: []traversal.Reached{
		reached("NETWORK", "job_network", traversal.MembershipDownstream),
	}}
	got, err := Scan(context.Background(), openTestDB(t, nodes, limits), result)
	if !errors.Is(err, ErrInvalidLimitOwner) {
		t.Fatalf("error = %v, want ErrInvalidLimitOwner", err)
	}
	if !reflect.DeepEqual(got, Result{}) {
		t.Errorf("result = %#v, want zero result", got)
	}
}

func TestScanRejectsInvalidLimitFreeScope(t *testing.T) {
	tests := []struct {
		name      string
		nodes     []testNode
		result    traversal.Result
		wantError string
	}{
		{
			name: "cycle",
			nodes: []testNode{
				{id: "NET-A", typeName: "job_network"},
				{id: "NET-B", typeName: "job_network"},
			},
			result: traversal.Result{
				Nodes: []traversal.Reached{
					reached("NET-A", "job_network", traversal.MembershipDownstream),
					reached("NET-B", "job_network", traversal.MembershipDownstream),
				},
				ScopeEdges: []traversal.ScopeEdge{
					{ParentID: "NET-A", ChildID: "NET-B"},
					{ParentID: "NET-B", ChildID: "NET-A"},
				},
			},
			wantError: "scope edges contain a cycle",
		},
		{
			name: "multiple parents",
			nodes: []testNode{
				{id: "NET-A", typeName: "job_network"},
				{id: "NET-B", typeName: "job_network"},
				{id: "JOB", typeName: "job"},
			},
			result: traversal.Result{
				Nodes: []traversal.Reached{
					reached("NET-A", "job_network", traversal.MembershipDownstream),
					reached("NET-B", "job_network", traversal.MembershipDownstream),
					reached("JOB", "job", traversal.MembershipDownstream),
				},
				ScopeEdges: []traversal.ScopeEdge{
					{ParentID: "NET-A", ChildID: "JOB"},
					{ParentID: "NET-B", ChildID: "JOB"},
				},
			},
			wantError: `scope child "JOB" has multiple parents`,
		},
		{
			name:  "missing root",
			nodes: []testNode{{id: "JOB", typeName: "job"}},
			result: traversal.Result{
				Nodes: []traversal.Reached{
					reached("JOB", "job", traversal.MembershipDownstream),
				},
				ScopeEdges: []traversal.ScopeEdge{{ParentID: "MISSING", ChildID: "JOB"}},
			},
			wantError: `scope root "MISSING" is not in traversal result`,
		},
		{
			name: "wrong root type",
			nodes: []testNode{
				{id: "ROOT-JOB", typeName: "job"},
				{id: "CHILD-JOB", typeName: "job"},
			},
			result: traversal.Result{
				Nodes: []traversal.Reached{
					reached("ROOT-JOB", "job", traversal.MembershipDownstream),
					reached("CHILD-JOB", "job", traversal.MembershipDownstream),
				},
				ScopeEdges: []traversal.ScopeEdge{{ParentID: "ROOT-JOB", ChildID: "CHILD-JOB"}},
			},
			wantError: `scope root "ROOT-JOB" has type "job", want job_network`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Scan(context.Background(), openTestDB(t, test.nodes, nil), test.result)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want containing %q", err, test.wantError)
			}
			if !reflect.DeepEqual(got, Result{}) {
				t.Errorf("result = %#v, want zero result", got)
			}
		})
	}
}

func TestScanReturnsNoPartialResultOnCancellationOrDatabaseError(t *testing.T) {
	result := traversal.Result{Nodes: []traversal.Reached{
		reached("JOB", "job", traversal.MembershipTarget),
	}}

	t.Run("canceled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		got, err := Scan(ctx, openTestDB(t, []testNode{{id: "JOB", typeName: "job"}}, []testLimit{rawLimit("RAW", "JOB", "raw")}), result)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
		if !reflect.DeepEqual(got, Result{}) {
			t.Errorf("result = %#v, want zero result", got)
		}
	})

	t.Run("database error", func(t *testing.T) {
		db := openIncompleteTestDB(t)
		got, err := Scan(context.Background(), db, result)
		if err == nil {
			t.Fatal("error = nil, want database error")
		}
		if !reflect.DeepEqual(got, Result{}) {
			t.Errorf("result = %#v, want zero result", got)
		}
	})
}

func openTestDB(t *testing.T, nodes []testNode, limits []testLimit) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
CREATE TABLE node (
    node_id TEXT PRIMARY KEY,
    node_type TEXT NOT NULL,
    name TEXT NOT NULL
);
CREATE TABLE limit_fact (
    limit_id TEXT PRIMARY KEY,
    node_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    business_day_offset INTEGER,
    local_time_seconds INTEGER,
    time_zone TEXT,
    finish_sort_seconds INTEGER,
    duration_seconds INTEGER,
    source_text TEXT,
    origin TEXT NOT NULL,
    certainty TEXT NOT NULL
);
CREATE INDEX idx_limit_node ON limit_fact(node_id);`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range nodes {
		name := node.name
		if name == "" {
			name = node.id
		}
		if _, err := tx.Exec(`INSERT INTO node (node_id, node_type, name) VALUES (?, ?, ?)`, node.id, node.typeName, name); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	for _, limit := range limits {
		if _, err := tx.Exec(`INSERT INTO limit_fact (
            limit_id, node_id, kind, business_day_offset, local_time_seconds,
            time_zone, duration_seconds, source_text, origin, certainty
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			limit.id, limit.ownerID, limit.kind, limit.businessDayOffset, limit.localTimeSeconds,
			limit.timeZone, limit.durationSeconds, limit.sourceText, limit.origin, limit.certainty,
		); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return db
}

func openIncompleteTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE node (
        node_id TEXT PRIMARY KEY,
        node_type TEXT NOT NULL,
        name TEXT NOT NULL
    );
    INSERT INTO node (node_id, node_type, name) VALUES ('JOB', 'job', 'JOB');`); err != nil {
		t.Fatal(err)
	}
	return db
}

func reached(id, nodeType string, membership traversal.Membership) traversal.Reached {
	return traversal.Reached{
		Node:       traversal.Node{ID: id, Type: nodeType, Name: id},
		Membership: membership,
	}
}

func finishLimit(id, ownerID string, offset, localTimeSeconds int64, zone string) testLimit {
	return testLimit{
		id: id, ownerID: ownerID, kind: string(KindFinishBy),
		businessDayOffset: offset, localTimeSeconds: localTimeSeconds, timeZone: zone,
		origin: "scheduler", certainty: "declared",
	}
}

func elapsedLimit(id, ownerID string, duration int64, sourceText *string) testLimit {
	return testLimit{
		id: id, ownerID: ownerID, kind: string(KindMaxElapsed),
		durationSeconds: duration, sourceText: sourceText,
		origin: "manual", certainty: "confirmed",
	}
}

func rawLimit(id, ownerID, sourceText string) testLimit {
	return testLimit{
		id: id, ownerID: ownerID, kind: string(KindRaw), sourceText: sourceText,
		origin: "manual", certainty: "candidate",
	}
}

func factIDs(items []Item) []string {
	ids := make([]string, len(items))
	for index, item := range items {
		ids[index] = item.Fact.ID
	}
	return ids
}

func finishZones(groups []FinishByGroup) []string {
	zones := make([]string, len(groups))
	for index, group := range groups {
		zones[index] = group.TimeZone
	}
	return zones
}
