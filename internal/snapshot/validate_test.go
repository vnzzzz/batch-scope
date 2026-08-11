package snapshot

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"

	"batchscope/internal/limits"
	"batchscope/internal/testsupport/graphgen"
)

func TestValidateAcceptsParentHierarchies(t *testing.T) {
	tests := []struct {
		name  string
		nodes []string
	}{
		{
			name:  "single root",
			nodes: []string{testNode("management_unit", "ROOT", nil, nil)},
		},
		{
			name: "multiple roots",
			nodes: []string{
				testNode("management_unit", "ROOT-A", nil, nil),
				testNode("management_unit", "ROOT-B", nil, nil),
			},
		},
		{
			name: "nested management units",
			nodes: []string{
				testNode("management_unit", "ROOT", nil, nil),
				testNode("management_unit", "DIVISION", stringPointer("ROOT"), nil),
				testNode("job_network", "NET", stringPointer("DIVISION"), nil),
				testNode("job", "JOB", stringPointer("NET"), nil),
			},
		},
		{
			name: "deep job network hierarchy",
			nodes: []string{
				testNode("management_unit", "ROOT", nil, nil),
				testNode("job_network", "NET-1", stringPointer("ROOT"), nil),
				testNode("job_network", "NET-2", stringPointer("NET-1"), nil),
				testNode("job_network", "NET-3", stringPointer("NET-2"), nil),
				testNode("job", "JOB", stringPointer("NET-3"), nil),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extracted := writeExtractedSnapshot(t, tt.nodes, nil)
			if _, err := Validate(context.Background(), extracted); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestValidateAcceptsDependencyShapes(t *testing.T) {
	job := func(id string) string { return testNode("job", id, nil, nil) }
	file := func(id string) string { return testNode("file", id, nil, nil) }
	pattern := func(id string) string { return testNode("file_pattern", id, nil, nil) }
	status := func(id string) string { return testNode("job_status", id, nil, nil) }
	event := func(id string) string { return testNode("external_event", id, nil, nil) }
	relation := func(from, to, kind string) string {
		return testRelation(from, to, kind, "scheduler", "declared", nil)
	}

	tests := []struct {
		name      string
		nodes     []string
		relations []string
	}{
		{name: "serial predecessor and successor", nodes: []string{job("A"), job("B")}, relations: []string{relation("A", "B", "precedes")}},
		{name: "through file", nodes: []string{job("A"), file("FILE"), job("B")}, relations: []string{relation("A", "FILE", "produces"), relation("FILE", "B", "triggers")}},
		{name: "through file pattern", nodes: []string{job("A"), pattern("PATTERN"), job("B")}, relations: []string{relation("A", "PATTERN", "produces"), relation("PATTERN", "B", "consumed_by")}},
		{name: "observes another job status", nodes: []string{job("A"), status("STATUS"), job("B")}, relations: []string{relation("A", "STATUS", "produces"), relation("STATUS", "B", "observed_by")}},
		{name: "external vendor event", nodes: []string{event("VENDOR"), job("A")}, relations: []string{relation("VENDOR", "A", "triggers")}},
		{name: "serverless completion event", nodes: []string{job("A"), event("SERVERLESS"), job("B")}, relations: []string{relation("A", "SERVERLESS", "produces"), relation("SERVERLESS", "B", "triggers")}},
		{name: "multiple dependencies between same nodes", nodes: []string{job("A"), job("B")}, relations: []string{relation("A", "B", "precedes"), relation("A", "B", "triggers")}},
		{name: "converging paths", nodes: []string{job("A"), job("B"), job("C")}, relations: []string{relation("A", "C", "precedes"), relation("B", "C", "precedes")}},
		{name: "cycle through file", nodes: []string{job("A"), file("FILE"), job("B")}, relations: []string{relation("A", "FILE", "produces"), relation("FILE", "B", "triggers"), relation("B", "A", "precedes")}},
		{name: "independent cycles", nodes: []string{job("A"), job("B"), job("C"), job("D")}, relations: []string{relation("A", "B", "precedes"), relation("B", "A", "precedes"), relation("C", "D", "precedes"), relation("D", "C", "precedes")}},
		{name: "branch outside cycle", nodes: []string{job("A"), job("B"), job("C")}, relations: []string{relation("A", "B", "precedes"), relation("B", "A", "precedes"), relation("A", "C", "precedes")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extracted := writeExtractedSnapshot(t, tt.nodes, tt.relations)
			if _, err := Validate(context.Background(), extracted); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestValidateAcceptsIsolatedAndSameNamedNodesAndInferredWithoutEvidence(t *testing.T) {
	nodes := []string{
		testNodeWithName("job", "A", "same name", nil, nil),
		testNodeWithName("job", "B", "same name", nil, nil),
		testNode("job", "ISOLATED", nil, nil),
	}
	relations := []string{testRelation("A", "B", "precedes", "ai_analysis", "inferred", nil)}
	if _, err := Validate(context.Background(), writeExtractedSnapshot(t, nodes, relations)); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateAcceptsEmptyLimitFactsOutsideJobAndFixedDurations(t *testing.T) {
	limits := []any{
		map[string]any{"id": "WEEK", "kind": "max_elapsed", "duration": "P1W", "origin": "scheduler", "certainty": "declared"},
		map[string]any{"id": "COMPOSITE", "kind": "max_elapsed", "duration": "P1DT2H3M4S", "origin": "scheduler", "certainty": "declared"},
	}
	nodes := []string{
		testNode("management_unit", "ROOT", nil, []any{}),
		testNode("job", "JOB", nil, limits),
	}
	if _, err := Validate(context.Background(), writeExtractedSnapshot(t, nodes, nil)); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateAcceptsDemoSnapshot(t *testing.T) {
	directory := filepath.Join("..", "..", "examples", "demo", "snapshot")
	extracted := Extracted{
		Directory: directory,
		Manifest:  filepath.Join(directory, manifestName),
		Nodes:     filepath.Join(directory, nodesName),
		Relations: filepath.Join(directory, relationsName),
	}
	result, err := Validate(context.Background(), extracted)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if result.NodeCount != 19 || result.RelationCount != 19 || result.LimitCount != 5 {
		t.Fatalf("Validate() result = %#v, want 19 nodes, 19 relations, and 5 limits", result)
	}
}

func TestValidateAppliesManifestInputBoundaries(t *testing.T) {
	t.Run("accepts exact size limit", func(t *testing.T) {
		extracted := writeExtractedSnapshot(t, nil, nil)
		contents, err := os.ReadFile(extracted.Manifest)
		if err != nil {
			t.Fatalf("read manifest: %v", err)
		}
		contents = append(contents, bytes.Repeat([]byte{' '}, (1<<20)-len(contents))...)
		if err := os.WriteFile(extracted.Manifest, contents, 0o600); err != nil {
			t.Fatalf("write manifest: %v", err)
		}
		if _, err := Validate(context.Background(), extracted); err != nil {
			t.Fatalf("Validate() error = %v", err)
		}
	})

	t.Run("size limit before JSON decode", func(t *testing.T) {
		extracted := writeExtractedSnapshot(t, nil, nil)
		contents := bytes.Repeat([]byte{' '}, (1<<20)+1)
		if err := os.WriteFile(extracted.Manifest, contents, 0o600); err != nil {
			t.Fatalf("write manifest: %v", err)
		}
		_, err := Validate(context.Background(), extracted)
		assertValidationError(t, err, ErrorManifestSizeLimit, manifestName, 0, "")
	})

	t.Run("invalid UTF-8 before JSON decode", func(t *testing.T) {
		extracted := writeExtractedSnapshot(t, nil, nil)
		if err := os.WriteFile(extracted.Manifest, []byte{'{', 0xff, '}'}, 0o600); err != nil {
			t.Fatalf("write manifest: %v", err)
		}
		_, err := Validate(context.Background(), extracted)
		assertValidationError(t, err, ErrorInvalidUTF8, manifestName, 0, "")
	})
}

func TestValidateRejectsManifestCapacityBeforeReadingNDJSON(t *testing.T) {
	for _, tt := range []struct {
		name      string
		nodes     int
		relations int
		pointer   string
	}{
		{name: "nodes", nodes: limits.MaxSnapshotNodes + 1, pointer: "/nodeCount"},
		{name: "relations", relations: limits.MaxSnapshotRelations + 1, pointer: "/relationCount"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			extracted := writeExtractedSnapshot(t, nil, nil)
			writeManifest(t, extracted.Manifest, tt.nodes, tt.relations)
			writeTestFile(t, extracted.Nodes, "not-json")
			_, err := Validate(context.Background(), extracted)
			assertValidationError(t, err, ErrorCapacityExceeded, manifestName, 0, tt.pointer)
		})
	}
}

func TestValidateRejectsActualNDJSONCapacityWhileStreaming(t *testing.T) {
	t.Run("nodes", func(t *testing.T) {
		nodes := make([]string, limits.MaxSnapshotNodes+2)
		for index := 0; index <= limits.MaxSnapshotNodes; index++ {
			nodes[index] = fmt.Sprintf(`{"type":"job","id":"JOB-%d","name":"Job"}`, index)
		}
		// 末尾の不正な行は、境界を越えて走査が続いたことを検出するための番兵である。
		// 走査が止まらなければcapacity_exceededではなくinvalid_jsonが返る。
		nodes[len(nodes)-1] = "{"

		extracted := writeExtractedSnapshot(t, nodes, nil)
		writeManifest(t, extracted.Manifest, 1, 0)
		_, err := Validate(context.Background(), extracted)
		assertValidationError(t, err, ErrorCapacityExceeded, nodesName, limits.MaxSnapshotNodes+1, "")
	})

	t.Run("relations", func(t *testing.T) {
		nodes := []string{testNode("job", "JOB", nil, nil)}
		relations := make([]string, limits.MaxSnapshotRelations+2)
		for index := 0; index <= limits.MaxSnapshotRelations; index++ {
			relations[index] = fmt.Sprintf(
				`{"fromId":"JOB","toId":"JOB","kind":"precedes","origin":"scheduler","certainty":"declared","evidence":[{"source":"%d"}]}`,
				index,
			)
		}
		// 末尾の不正な行は、境界を越えて走査が続いたことを検出するための番兵である。
		// 走査が止まらなければcapacity_exceededではなくinvalid_jsonが返る。
		relations[len(relations)-1] = "{"

		extracted := writeExtractedSnapshot(t, nodes, relations)
		writeManifest(t, extracted.Manifest, len(nodes), 0)
		_, err := Validate(context.Background(), extracted)
		assertValidationError(t, err, ErrorCapacityExceeded, relationsName, limits.MaxSnapshotRelations+1, "")
	})
}

func TestValidateRejectsLimitAndJobNetworkDepthCapacity(t *testing.T) {
	t.Run("limits", func(t *testing.T) {
		nodes := make([]string, 0, 6)
		remaining := limits.MaxSnapshotLimits + 1
		for nodeIndex := 0; remaining > 0; nodeIndex++ {
			count := min(remaining, 1000)
			facts := make([]any, count)
			for factIndex := range facts {
				facts[factIndex] = map[string]any{
					"id": fmt.Sprintf("LIMIT-%d-%d", nodeIndex, factIndex), "kind": "raw", "sourceText": "raw",
					"origin": "manual", "certainty": "declared",
				}
			}
			nodes = append(nodes, testNode("job", fmt.Sprintf("JOB-%d", nodeIndex), nil, facts))
			remaining -= count
		}
		_, err := Validate(context.Background(), writeExtractedSnapshot(t, nodes, nil))
		assertValidationError(t, err, ErrorCapacityExceeded, nodesName, 6, "/limitFacts/0")
	})

	t.Run("job network depth", func(t *testing.T) {
		nodes := make([]string, limits.MaxJobNetworkDepth+1)
		var parent *string
		for index := range nodes {
			id := fmt.Sprintf("NET-%02d", index)
			nodes[index] = testNode("job_network", id, parent, nil)
			parent = stringPointer(id)
		}
		_, err := Validate(context.Background(), writeExtractedSnapshot(t, nodes, nil))
		assertValidationError(t, err, ErrorCapacityExceeded, nodesName, 0, "/parentId")
	})
}

func TestValidateStopsAtFirstLimitCapacityExceeded(t *testing.T) {
	const factsPerNode = 1_000
	limit := func(id string) map[string]any {
		return map[string]any{
			"id": id, "kind": "raw", "sourceText": "raw", "origin": "manual", "certainty": "declared",
		}
	}

	nodes := make([]string, 0, limits.MaxSnapshotLimits/factsPerNode+3)
	remaining := limits.MaxSnapshotLimits
	for nodeIndex := 0; remaining > 0; nodeIndex++ {
		count := min(remaining, factsPerNode)
		facts := make([]any, count)
		for factIndex := range facts {
			facts[factIndex] = limit(fmt.Sprintf("LIMIT-%d-%d", nodeIndex, factIndex))
		}
		nodes = append(nodes, testNode("job", fmt.Sprintf("JOB-%d", nodeIndex), nil, facts))
		remaining -= count
	}
	capacityLine := len(nodes) + 1
	overflowFacts := []any{
		limit("OVER-0"), limit("OVER-1"), limit("OVER-2"),
	}
	nodes = append(nodes, testNode("job", "OVER-CAPACITY", nil, overflowFacts))
	// 最初の超過リミットで走査を止め、後続の不正な行を入力として消費しない。
	nodes = append(nodes, "{")

	extracted := writeExtractedSnapshot(t, nodes, nil)
	_, err := Validate(context.Background(), extracted)
	assertValidationError(t, err, ErrorCapacityExceeded, nodesName, capacityLine, "/limitFacts/0")
}

func TestValidateAcceptsJSONNumberFormsForManifestCounts(t *testing.T) {
	extracted := writeExtractedSnapshot(t, []string{testNode("job", "A", nil, nil)}, nil)
	writeTestFile(t, extracted.Manifest, `{"schemaVersion":"0.5","snapshotId":"s","generatedAt":"2026-08-08T00:00:00Z","nodeCount":1.0,"relationCount":0e0,"producer":{"name":"test","version":"1"}}`)
	result, err := Validate(context.Background(), extracted)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if result.NodeCount != 1 || result.RelationCount != 0 {
		t.Fatalf("Validate() result = %#v, want one node and no relations", result)
	}
}

func TestValidateRejectsNonIntegerOrOutOfRangeManifestCounts(t *testing.T) {
	for _, tt := range []struct {
		name      string
		pointer   string
		nodeCount string
		relCount  string
	}{
		{name: "fractional node count", nodeCount: "1.5", relCount: "0", pointer: "/nodeCount"},
		{name: "node count outside int and schema range", nodeCount: "1e100", relCount: "0", pointer: "/nodeCount"},
		{name: "fractional relation count", nodeCount: "1", relCount: "0.5", pointer: "/relationCount"},
		{name: "relation count outside int and schema range", nodeCount: "1", relCount: "1e100", pointer: "/relationCount"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			extracted := writeExtractedSnapshot(t, []string{testNode("job", "A", nil, nil)}, nil)
			contents := fmt.Sprintf(`{"schemaVersion":"0.5","snapshotId":"s","generatedAt":"2026-08-08T00:00:00Z","nodeCount":%s,"relationCount":%s,"producer":{"name":"test","version":"1"}}`, tt.nodeCount, tt.relCount)
			writeTestFile(t, extracted.Manifest, contents)
			_, err := Validate(context.Background(), extracted)
			assertValidationError(t, err, ErrorSchemaViolation, manifestName, 0, tt.pointer)
		})
	}
}

func TestValidateRejectsSchemaViolations(t *testing.T) {
	nodeWithLimitFact := func(fact string) string {
		return fmt.Sprintf(`{"type":"job","id":"A","name":"A","limitFacts":[%s]}`, fact)
	}
	tests := []struct {
		name     string
		kind     ErrorKind
		file     string
		line     int
		pointer  string
		contents string
	}{
		{
			name: "manifest required", kind: ErrorSchemaViolation, file: manifestName,
			contents: `{"schemaVersion":"0.5","snapshotId":"s","generatedAt":"2026-08-08T00:00:00Z","nodeCount":1,"relationCount":0}`,
		},
		{
			name: "manifest type", kind: ErrorSchemaViolation, file: manifestName, pointer: "/snapshotId",
			contents: `{"schemaVersion":"0.5","snapshotId":1,"generatedAt":"2026-08-08T00:00:00Z","nodeCount":1,"relationCount":0,"producer":{"name":"test","version":"1"}}`,
		},
		{
			name: "manifest schemaVersion const", kind: ErrorSchemaViolation, file: manifestName, pointer: "/schemaVersion",
			contents: `{"schemaVersion":"1","snapshotId":"s","generatedAt":"2026-08-08T00:00:00Z","nodeCount":1,"relationCount":0,"producer":{"name":"test","version":"1"}}`,
		},
		{
			name: "manifest additionalProperties", kind: ErrorSchemaViolation, file: manifestName,
			contents: `{"schemaVersion":"0.5","snapshotId":"s","generatedAt":"2026-08-08T00:00:00Z","nodeCount":1,"relationCount":0,"producer":{"name":"test","version":"1"},"extra":true}`,
		},
		{
			name: "manifest nodeCount range", kind: ErrorSchemaViolation, file: manifestName, pointer: "/nodeCount",
			contents: `{"schemaVersion":"0.5","snapshotId":"s","generatedAt":"2026-08-08T00:00:00Z","nodeCount":-1,"relationCount":0,"producer":{"name":"test","version":"1"}}`,
		},
		{name: "node required", kind: ErrorSchemaViolation, file: nodesName, line: 1, contents: `{"type":"job","id":"A"}`},
		{name: "node type enum", kind: ErrorSchemaViolation, file: nodesName, line: 1, pointer: "/type", contents: `{"type":"unknown","id":"A","name":"A"}`},
		{name: "node field type", kind: ErrorSchemaViolation, file: nodesName, line: 1, pointer: "/name", contents: `{"type":"job","id":"A","name":1}`},
		{name: "node additionalProperties", kind: ErrorSchemaViolation, file: nodesName, line: 1, contents: `{"type":"job","id":"A","name":"A","extra":true}`},
		{name: "node name minLength", kind: ErrorSchemaViolation, file: nodesName, line: 1, pointer: "/name", contents: `{"type":"job","id":"A","name":""}`},
		{name: "relation required", kind: ErrorSchemaViolation, file: relationsName, line: 1, contents: `{"toId":"A","kind":"precedes","origin":"scheduler","certainty":"declared"}`},
		{name: "relation kind enum", kind: ErrorSchemaViolation, file: relationsName, line: 1, pointer: "/kind", contents: `{"fromId":"A","toId":"A","kind":"unknown","origin":"scheduler","certainty":"declared"}`},
		{name: "relation origin enum", kind: ErrorSchemaViolation, file: relationsName, line: 1, pointer: "/origin", contents: `{"fromId":"A","toId":"A","kind":"precedes","origin":"unknown","certainty":"declared"}`},
		{name: "relation certainty enum", kind: ErrorSchemaViolation, file: relationsName, line: 1, pointer: "/certainty", contents: `{"fromId":"A","toId":"A","kind":"precedes","origin":"scheduler","certainty":"unknown"}`},
		{name: "relation additionalProperties", kind: ErrorSchemaViolation, file: relationsName, line: 1, contents: `{"fromId":"A","toId":"A","kind":"precedes","origin":"scheduler","certainty":"declared","extra":true}`},
		{name: "relation evidence item type", kind: ErrorSchemaViolation, file: relationsName, line: 1, pointer: "/evidence/0", contents: `{"fromId":"A","toId":"A","kind":"precedes","origin":"scheduler","certainty":"declared","evidence":["invalid"]}`},
		{
			name: "finish_by required", kind: ErrorSchemaViolation, file: nodesName, line: 1, pointer: "/limitFacts/0",
			contents: nodeWithLimitFact(`{"kind":"finish_by","businessDayOffset":0,"localTime":"00:00:00","timeZone":"UTC","origin":"scheduler","certainty":"declared"}`),
		},
		{
			name: "finish_by enum", kind: ErrorSchemaViolation, file: nodesName, line: 1, pointer: "/limitFacts/0/origin",
			contents: nodeWithLimitFact(`{"id":"L","kind":"finish_by","businessDayOffset":0,"localTime":"00:00:00","timeZone":"UTC","origin":"unknown","certainty":"declared"}`),
		},
		{
			name: "finish_by type", kind: ErrorSchemaViolation, file: nodesName, line: 1, pointer: "/limitFacts/0/id",
			contents: nodeWithLimitFact(`{"id":1,"kind":"finish_by","businessDayOffset":0,"localTime":"00:00:00","timeZone":"UTC","origin":"scheduler","certainty":"declared"}`),
		},
		{
			name: "finish_by additionalProperties", kind: ErrorSchemaViolation, file: nodesName, line: 1, pointer: "/limitFacts/0",
			contents: nodeWithLimitFact(`{"id":"L","kind":"finish_by","businessDayOffset":0,"localTime":"00:00:00","timeZone":"UTC","origin":"scheduler","certainty":"declared","extra":true}`),
		},
		{
			name: "max_elapsed required", kind: ErrorSchemaViolation, file: nodesName, line: 1, pointer: "/limitFacts/0",
			contents: nodeWithLimitFact(`{"kind":"max_elapsed","duration":"PT1S","origin":"scheduler","certainty":"declared"}`),
		},
		{
			name: "max_elapsed enum", kind: ErrorSchemaViolation, file: nodesName, line: 1, pointer: "/limitFacts/0/origin",
			contents: nodeWithLimitFact(`{"id":"L","kind":"max_elapsed","duration":"PT1S","origin":"unknown","certainty":"declared"}`),
		},
		{
			name: "max_elapsed type", kind: ErrorSchemaViolation, file: nodesName, line: 1, pointer: "/limitFacts/0/id",
			contents: nodeWithLimitFact(`{"id":1,"kind":"max_elapsed","duration":"PT1S","origin":"scheduler","certainty":"declared"}`),
		},
		{
			name: "max_elapsed additionalProperties", kind: ErrorSchemaViolation, file: nodesName, line: 1, pointer: "/limitFacts/0",
			contents: nodeWithLimitFact(`{"id":"L","kind":"max_elapsed","duration":"PT1S","origin":"scheduler","certainty":"declared","extra":true}`),
		},
		{
			name: "raw required", kind: ErrorSchemaViolation, file: nodesName, line: 1, pointer: "/limitFacts/0",
			contents: nodeWithLimitFact(`{"kind":"raw","sourceText":"raw","origin":"manual","certainty":"declared"}`),
		},
		{
			name: "raw enum", kind: ErrorSchemaViolation, file: nodesName, line: 1, pointer: "/limitFacts/0/origin",
			contents: nodeWithLimitFact(`{"id":"L","kind":"raw","sourceText":"raw","origin":"unknown","certainty":"declared"}`),
		},
		{
			name: "raw type", kind: ErrorSchemaViolation, file: nodesName, line: 1, pointer: "/limitFacts/0/id",
			contents: nodeWithLimitFact(`{"id":1,"kind":"raw","sourceText":"raw","origin":"manual","certainty":"declared"}`),
		},
		{
			name: "raw additionalProperties", kind: ErrorSchemaViolation, file: nodesName, line: 1, pointer: "/limitFacts/0",
			contents: nodeWithLimitFact(`{"id":"L","kind":"raw","sourceText":"raw","origin":"manual","certainty":"declared","extra":true}`),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extracted := writeExtractedSnapshot(t, []string{testNode("job", "A", nil, nil)}, nil)
			switch tt.file {
			case manifestName:
				writeTestFile(t, extracted.Manifest, tt.contents)
			case nodesName:
				writeTestFile(t, extracted.Nodes, tt.contents+"\n")
			case relationsName:
				writeTestFile(t, extracted.Relations, tt.contents+"\n")
			default:
				t.Fatalf("unknown test file %q", tt.file)
			}
			_, err := Validate(context.Background(), extracted)
			assertValidationError(t, err, tt.kind, tt.file, tt.line, tt.pointer)
		})
	}
}

func TestValidateReturnsStableSchemaError(t *testing.T) {
	nodes := []string{`{"type":"job","id":"","name":""}`}
	extracted := writeExtractedSnapshot(t, nodes, nil)

	type errorLocation struct {
		kind    ErrorKind
		file    string
		line    int
		pointer string
	}
	var first errorLocation
	for attempt := 0; attempt < 50; attempt++ {
		_, err := Validate(context.Background(), extracted)
		var validationErr *Error
		if !errors.As(err, &validationErr) {
			t.Fatalf("Validate() attempt %d error = %v, want *Error", attempt, err)
		}
		got := errorLocation{kind: validationErr.Kind, file: validationErr.File, line: validationErr.Line, pointer: validationErr.Pointer}
		if attempt == 0 {
			first = got
			continue
		}
		if got != first {
			t.Fatalf("Validate() attempt %d error = %#v, want %#v", attempt, got, first)
		}
	}
	if first.kind != ErrorSchemaViolation || first.file != nodesName || first.line != 1 || first.pointer != "/id" {
		t.Fatalf("Validate() error = %#v, want stable /id schema violation on node line 1", first)
	}
}

func TestValidateRejectsInvalidJSON(t *testing.T) {
	extracted := writeExtractedSnapshot(t, []string{testNode("job", "A", nil, nil)}, nil)
	writeTestFile(t, extracted.Nodes, "{\n")
	_, err := Validate(context.Background(), extracted)
	assertValidationError(t, err, ErrorInvalidJSON, nodesName, 1, "")
}

func TestValidateRejectsCountMismatches(t *testing.T) {
	tests := []struct {
		name    string
		kind    ErrorKind
		pointer string
		nodes   int
		rels    int
	}{
		{name: "node count", kind: ErrorNodeCountMismatch, pointer: "/nodeCount", nodes: 2},
		{name: "relation count", kind: ErrorRelationCountMismatch, pointer: "/relationCount", nodes: 1, rels: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extracted := writeExtractedSnapshot(t, []string{testNode("job", "A", nil, nil)}, nil)
			writeManifest(t, extracted.Manifest, tt.nodes, tt.rels)
			_, err := Validate(context.Background(), extracted)
			assertValidationError(t, err, tt.kind, manifestName, 0, tt.pointer)
		})
	}
}

func TestValidateRejectsNodeAndParentViolations(t *testing.T) {
	tests := []struct {
		name    string
		kind    ErrorKind
		line    int
		pointer string
		nodes   []string
	}{
		{
			name: "duplicate node ID", kind: ErrorDuplicateNode, line: 2, pointer: "/id",
			nodes: []string{testNode("job", "A", nil, nil), testNode("job", "A", nil, nil)},
		},
		{
			name: "missing parent", kind: ErrorMissingParent, line: 1, pointer: "/parentId",
			nodes: []string{testNode("job", "A", stringPointer("MISSING"), nil)},
		},
		{
			name: "multiple parents", kind: ErrorMultipleParents, line: 4, pointer: "/parentId",
			nodes: []string{
				testNode("job_network", "N1", nil, nil), testNode("job_network", "N2", nil, nil),
				testNode("job", "A", stringPointer("N1"), nil), testNode("job", "A", stringPointer("N2"), nil),
			},
		},
		{
			name: "disallowed parent type", kind: ErrorInvalidParentType, line: 2, pointer: "/parentId",
			nodes: []string{testNode("job", "PARENT", nil, nil), testNode("job_network", "CHILD", stringPointer("PARENT"), nil)},
		},
		{
			name: "parent on dependency-only node", kind: ErrorInvalidParentType, line: 2, pointer: "/parentId",
			nodes: []string{testNode("job_network", "PARENT", nil, nil), testNode("file", "FILE", stringPointer("PARENT"), nil)},
		},
		{
			name: "parent cycle", kind: ErrorParentCycle, line: 1, pointer: "/parentId",
			nodes: []string{testNode("management_unit", "A", stringPointer("B"), nil), testNode("management_unit", "B", stringPointer("A"), nil)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Validate(context.Background(), writeExtractedSnapshot(t, tt.nodes, nil))
			assertValidationError(t, err, tt.kind, nodesName, tt.line, tt.pointer)
		})
	}
}

func TestValidateSelectsNodeErrorsDeterministically(t *testing.T) {
	tests := []struct {
		name        string
		nodes       []string
		wantKind    ErrorKind
		wantLine    int
		wantPointer string
	}{
		{
			name: "earlier parent reference before later duplicate",
			nodes: []string{
				testNode("job", "FIRST", stringPointer("MISSING"), nil),
				testNode("management_unit", "ROOT", nil, nil),
				testNode("job", "OTHER", nil, nil),
				testNode("job", "DUPLICATE", nil, nil),
				testNode("job", "DUPLICATE", nil, nil),
			},
			wantKind: ErrorMissingParent, wantLine: 1, wantPointer: "/parentId",
		},
		{
			name: "earlier duplicate before later parent reference",
			nodes: []string{
				testNode("job", "DUPLICATE", nil, nil),
				testNode("job", "DUPLICATE", nil, nil),
				testNode("management_unit", "ROOT", nil, nil),
				testNode("job", "OTHER", nil, nil),
				testNode("job", "LAST", stringPointer("MISSING"), nil),
			},
			wantKind: ErrorDuplicateNode, wantLine: 2, wantPointer: "/id",
		},
		{
			name: "line validation before node cross-validation",
			nodes: []string{
				testNode("job", "DUPLICATE", nil, nil),
				testNode("job", "DUPLICATE", nil, nil),
				`{"type":"job","id":"INVALID","name":""}`,
			},
			wantKind: ErrorSchemaViolation, wantLine: 3, wantPointer: "/name",
		},
		{
			name: "smallest line in parent cycle",
			nodes: []string{
				testNode("management_unit", "BRANCH", stringPointer("C"), nil),
				testNode("management_unit", "A", stringPointer("C"), nil),
				testNode("management_unit", "B", stringPointer("A"), nil),
				testNode("management_unit", "C", stringPointer("B"), nil),
			},
			wantKind: ErrorParentCycle, wantLine: 2, wantPointer: "/parentId",
		},
	}

	// 行内検査をノード横断検査より先に完了し、横断検査では種別にかかわらず最小行を選ぶ。
	// 親循環も最小行へ揃えることで、同じ入力で取込元が最初に直す位置を変えない。
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Validate(context.Background(), writeExtractedSnapshot(t, test.nodes, nil))
			assertValidationError(t, err, test.wantKind, nodesName, test.wantLine, test.wantPointer)
		})
	}
}

func TestValidateRejectsDuplicateLimitIDs(t *testing.T) {
	limit := func(id string) map[string]any {
		return map[string]any{
			"id": id, "kind": "raw", "sourceText": "raw", "origin": "manual", "certainty": "declared",
		}
	}
	tests := []struct {
		name    string
		line    int
		pointer string
		nodes   []string
	}{
		{
			name: "same node", line: 1, pointer: "/limitFacts/1/id",
			nodes: []string{testNode("job", "A", nil, []any{limit("DUPLICATE"), limit("DUPLICATE")})},
		},
		{
			name: "different nodes", line: 2, pointer: "/limitFacts/0/id",
			nodes: []string{
				testNode("job", "A", nil, []any{limit("DUPLICATE")}),
				testNode("job", "B", nil, []any{limit("DUPLICATE")}),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Validate(context.Background(), writeExtractedSnapshot(t, tt.nodes, nil))
			assertValidationError(t, err, ErrorDuplicateLimit, nodesName, tt.line, tt.pointer)
		})
	}
}

func TestValidateRejectsInvalidBusinessDayOffsetNumbers(t *testing.T) {
	for _, representation := range []string{"1.5", "32", "1e100", "9223372036854775808"} {
		t.Run(representation, func(t *testing.T) {
			fact := fmt.Sprintf(`{"id":"LIMIT","kind":"finish_by","businessDayOffset":%s,"localTime":"00:00:00","timeZone":"UTC","origin":"scheduler","certainty":"declared"}`, representation)
			node := fmt.Sprintf(`{"type":"job","id":"JOB","name":"Job","limitFacts":[%s]}`, fact)
			_, err := Validate(context.Background(), writeExtractedSnapshot(t, []string{node}, nil))
			assertValidationError(t, err, ErrorSchemaViolation, nodesName, 1, "/limitFacts/0/businessDayOffset")
		})
	}
}

func TestValidateRejectsLimitOutsideJob(t *testing.T) {
	limit := map[string]any{"id": "RAW", "kind": "raw", "sourceText": "raw", "origin": "manual", "certainty": "declared"}
	nodes := []string{testNode("job_network", "NET", nil, []any{limit})}
	_, err := Validate(context.Background(), writeExtractedSnapshot(t, nodes, nil))
	assertValidationError(t, err, ErrorInvalidLimitOwner, nodesName, 1, "/limitFacts")
}

func TestValidateRejectsDurationsThatCannotBecomeFixedIntegerSeconds(t *testing.T) {
	accepted := []struct {
		duration string
		seconds  int64
	}{
		{duration: "P0D", seconds: 0},
		{duration: "PT0S", seconds: 0},
		{duration: "P1W", seconds: 604800},
		{duration: "P1DT2H3M4S", seconds: 93784},
		{duration: "PT1.5H", seconds: 5400},
		{duration: "PT0.5M", seconds: 30},
		{duration: "PT1,5H", seconds: 5400},
		{duration: "PT9223372036854775807S", seconds: int64(1<<63 - 1)},
	}
	for _, tt := range accepted {
		t.Run("accept "+tt.duration, func(t *testing.T) {
			seconds, ok := durationSeconds(tt.duration)
			if !ok || seconds != tt.seconds {
				t.Fatalf("durationSeconds(%q) = (%d, %t), want (%d, true)", tt.duration, seconds, ok, tt.seconds)
			}
			limit := map[string]any{"id": "DURATION", "kind": "max_elapsed", "duration": tt.duration, "origin": "scheduler", "certainty": "declared"}
			nodes := []string{testNode("job", "JOB", nil, []any{limit})}
			if _, err := Validate(context.Background(), writeExtractedSnapshot(t, nodes, nil)); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}

	rejected := []string{"P1Y", "P1M", "P", "PT", "P1DT", "PT0.5S", "PT1.5H30M", "PT9223372036854775808S", "P1S"}
	for _, duration := range rejected {
		t.Run("reject "+duration, func(t *testing.T) {
			if seconds, ok := durationSeconds(duration); ok {
				t.Fatalf("durationSeconds(%q) = (%d, true), want rejected", duration, seconds)
			}
			limit := map[string]any{"id": "DURATION", "kind": "max_elapsed", "duration": duration, "origin": "scheduler", "certainty": "declared"}
			nodes := []string{testNode("job", "JOB", nil, []any{limit})}
			_, err := Validate(context.Background(), writeExtractedSnapshot(t, nodes, nil))
			assertValidationError(t, err, ErrorInvalidDuration, nodesName, 1, "/limitFacts/0/duration")
		})
	}
}

func TestValidateRejectsMissingRelationReferences(t *testing.T) {
	tests := []struct {
		name    string
		from    string
		to      string
		pointer string
	}{
		{name: "from", from: "MISSING", to: "A", pointer: "/fromId"},
		{name: "to", from: "A", to: "MISSING", pointer: "/toId"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes := []string{testNode("job", "A", nil, nil)}
			relations := []string{testRelation(tt.from, tt.to, "precedes", "scheduler", "declared", nil)}
			_, err := Validate(context.Background(), writeExtractedSnapshot(t, nodes, relations))
			assertValidationError(t, err, ErrorMissingNode, relationsName, 1, tt.pointer)
		})
	}
}

func TestValidateRejectsDuplicateRelationsAfterCanonicalizingEvidence(t *testing.T) {
	nodes := []string{testNode("job", "A", nil, nil), testNode("job", "B", nil, nil)}
	relations := []string{
		`{"fromId":"A","toId":"B","kind":"precedes","origin":"scheduler","certainty":"declared","evidence":[{"source":"definition","location":{"startLine":1,"endLine":2}}]}`,
		`{"certainty":"declared","origin":"scheduler","kind":"precedes","toId":"B","fromId":"A","evidence":[{"location":{"endLine":2.0,"startLine":1.0},"source":"definition"}]}`,
	}
	_, err := Validate(context.Background(), writeExtractedSnapshot(t, nodes, relations))
	assertValidationError(t, err, ErrorDuplicateRelation, relationsName, 2, "")
}

func TestRelationIDIsDeterministicAndChangesWithEveryIdentityField(t *testing.T) {
	base := relation{
		FromID: "A", ToID: "B", Kind: "precedes", Origin: "scheduler", Certainty: "declared",
		Evidence: json.RawMessage(`[{"source":"definition","location":{"startLine":1,"endLine":2}}]`),
	}
	want, err := relationID(base)
	if err != nil {
		t.Fatalf("relationID() error = %v", err)
	}
	reordered := base
	reordered.Evidence = json.RawMessage(`[{"location":{"endLine":2.0,"startLine":1.0},"source":"definition"}]`)
	got, err := relationID(reordered)
	if err != nil {
		t.Fatalf("relationID() error = %v", err)
	}
	if got != want {
		t.Fatalf("relationID() = %q, want %q for equivalent evidence", hex.EncodeToString(got[:]), hex.EncodeToString(want[:]))
	}

	changes := []relation{
		{FromID: "X", ToID: base.ToID, Kind: base.Kind, Origin: base.Origin, Certainty: base.Certainty, Evidence: base.Evidence},
		{FromID: base.FromID, ToID: "X", Kind: base.Kind, Origin: base.Origin, Certainty: base.Certainty, Evidence: base.Evidence},
		{FromID: base.FromID, ToID: base.ToID, Kind: "triggers", Origin: base.Origin, Certainty: base.Certainty, Evidence: base.Evidence},
		{FromID: base.FromID, ToID: base.ToID, Kind: base.Kind, Origin: "manual", Certainty: base.Certainty, Evidence: base.Evidence},
		{FromID: base.FromID, ToID: base.ToID, Kind: base.Kind, Origin: base.Origin, Certainty: "confirmed", Evidence: base.Evidence},
		{FromID: base.FromID, ToID: base.ToID, Kind: base.Kind, Origin: base.Origin, Certainty: base.Certainty, Evidence: json.RawMessage(`[{"source":"other"}]`)},
	}
	for index, changed := range changes {
		got, err := relationID(changed)
		if err != nil {
			t.Fatalf("relationID(change %d) error = %v", index, err)
		}
		if got == want {
			t.Errorf("relationID(change %d) = unchanged %q", index, hex.EncodeToString(got[:]))
		}
	}
}

func TestValidateReturnsInputCountsWithoutRetainingRelations(t *testing.T) {
	nodes := []string{testNode("job", "A", nil, nil), testNode("job", "B", nil, nil), testNode("job", "C", nil, nil)}
	relations := []string{
		testRelation("A", "B", "precedes", "scheduler", "declared", nil),
		testRelation("B", "C", "precedes", "scheduler", "declared", nil),
	}
	result, err := Validate(context.Background(), writeExtractedSnapshot(t, nodes, relations))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if result.NodeCount != 3 || result.RelationCount != 2 {
		t.Fatalf("Validate() result = %#v, want 3 nodes and 2 relations", result)
	}
}

func TestValidateMeasuresMaximumSCCSize(t *testing.T) {
	tests := []struct {
		name      string
		nodes     []string
		relations []string
		want      int
	}{
		{
			name: "multiple components",
			nodes: []string{
				testNode("job", "A", nil, nil), testNode("job", "B", nil, nil),
				testNode("job", "C", nil, nil), testNode("job", "D", nil, nil),
				testNode("job", "E", nil, nil), testNode("job", "ISOLATED", nil, nil),
			},
			relations: []string{
				testRelation("A", "B", "precedes", "scheduler", "declared", nil),
				testRelation("B", "A", "precedes", "scheduler", "declared", nil),
				testRelation("C", "D", "precedes", "scheduler", "declared", nil),
				testRelation("D", "E", "precedes", "scheduler", "declared", nil),
				testRelation("E", "C", "precedes", "scheduler", "declared", nil),
			},
			want: 3,
		},
		{
			name:      "self loop",
			nodes:     []string{testNode("job", "SELF", nil, nil)},
			relations: []string{testRelation("SELF", "SELF", "precedes", "scheduler", "declared", nil)},
			want:      1,
		},
		{
			name: "scope edge closes cycle",
			nodes: []string{
				testNode("job_network", "NET", nil, nil),
				testNode("job", "JOB", stringPointer("NET"), nil),
			},
			relations: []string{testRelation("JOB", "NET", "precedes", "scheduler", "declared", nil)},
			want:      2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Validate(context.Background(), writeExtractedSnapshot(t, tt.nodes, tt.relations))
			if err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if result.MaxSCCNodes != tt.want {
				t.Fatalf("Validate() MaxSCCNodes = %d, want %d", result.MaxSCCNodes, tt.want)
			}
		})
	}
}

func TestValidateSCCCapacityMatchesTraversableSources(t *testing.T) {
	const sccSize = limits.MaxSCCNodes + 1
	tests := []struct {
		name     string
		nodeType string
		wantMax  int
		wantErr  bool
	}{
		{name: "accepts non-traversable SCC", nodeType: "management_unit", wantMax: 1},
		{name: "rejects traversable SCC", nodeType: "job", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes := make([]string, sccSize)
			relations := make([]string, sccSize)
			for index := range sccSize {
				fromID := fmt.Sprintf("NODE-%04d", index)
				toID := fmt.Sprintf("NODE-%04d", (index+1)%sccSize)
				nodes[index] = testNode(tt.nodeType, fromID, nil, nil)
				relations[index] = testRelation(fromID, toID, "precedes", "scheduler", "declared", nil)
			}

			result, err := Validate(context.Background(), writeExtractedSnapshot(t, nodes, relations))
			if tt.wantErr {
				assertValidationError(t, err, ErrorCapacityExceeded, relationsName, 0, "")
				return
			}
			if err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if result.MaxSCCNodes != tt.wantMax {
				t.Fatalf("Validate() MaxSCCNodes = %d, want %d", result.MaxSCCNodes, tt.wantMax)
			}
		})
	}
}

func TestValidatePreservesContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Validate(ctx, writeExtractedSnapshot(t, nil, nil))
	assertValidationError(t, err, ErrorIO, "", 0, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Validate() error = %v, want context.Canceled", err)
	}
}

func BenchmarkValidate(b *testing.B) {
	benchmarks := []struct {
		name     string
		generate func() graphgen.Dataset
	}{
		{name: "Small", generate: graphgen.Small},
		{name: "Medium", generate: graphgen.Medium},
	}
	for _, benchmark := range benchmarks {
		b.Run(benchmark.name, func(b *testing.B) {
			b.StopTimer()
			dataset := benchmark.generate()
			files, err := dataset.WriteFiles(b.TempDir(), false)
			if err != nil {
				b.Fatal(err)
			}
			extracted := Extracted{Directory: files.Directory, Manifest: files.Manifest, Nodes: files.Nodes, Relations: files.Relations}
			expectedNodes := len(dataset.Nodes)
			expectedRelations := len(dataset.Relations)
			dataset = graphgen.Dataset{}
			runtime.GC()
			debug.FreeOSMemory()
			b.ReportAllocs()
			b.StartTimer()

			for b.Loop() {
				result, err := Validate(context.Background(), extracted)
				if err != nil {
					b.Fatalf("Validate() error = %v", err)
				}
				if result.NodeCount != expectedNodes || result.RelationCount != expectedRelations {
					b.Fatalf("Validate() result = %#v, want %d nodes and %d relations", result, expectedNodes, expectedRelations)
				}
			}
		})
	}
}

func writeExtractedSnapshot(t *testing.T, nodes, relations []string) Extracted {
	t.Helper()
	directory := t.TempDir()
	extracted := Extracted{
		Directory: directory,
		Manifest:  filepath.Join(directory, manifestName),
		Nodes:     filepath.Join(directory, nodesName),
		Relations: filepath.Join(directory, relationsName),
	}
	writeManifest(t, extracted.Manifest, len(nodes), len(relations))
	writeTestFile(t, extracted.Nodes, ndjson(nodes))
	writeTestFile(t, extracted.Relations, ndjson(relations))
	return extracted
}

func writeManifest(t *testing.T, path string, nodeCount, relationCount int) {
	t.Helper()
	contents := fmt.Sprintf(`{"schemaVersion":"0.5","snapshotId":"test","generatedAt":"2026-08-08T00:00:00Z","nodeCount":%d,"relationCount":%d,"producer":{"name":"test","version":"1"}}`, nodeCount, relationCount)
	writeTestFile(t, path, contents)
}

func testNode(kind, id string, parent *string, limits []any) string {
	return testNodeWithName(kind, id, id, parent, limits)
}

func testNodeWithName(kind, id, name string, parent *string, limits []any) string {
	if limits == nil {
		limits = []any{}
	}
	value := map[string]any{"type": kind, "id": id, "name": name, "parentId": parent, "limitFacts": limits}
	contents, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(contents)
}

func testRelation(from, to, kind, origin, certainty string, evidence []any) string {
	value := map[string]any{"fromId": from, "toId": to, "kind": kind, "origin": origin, "certainty": certainty}
	if evidence != nil {
		value["evidence"] = evidence
	}
	contents, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(contents)
}

func stringPointer(value string) *string {
	return &value
}

func ndjson(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func assertValidationError(t *testing.T, err error, kind ErrorKind, file string, line int, pointer string) {
	t.Helper()
	var validationErr *Error
	if !errors.As(err, &validationErr) {
		t.Fatalf("error = %v, want *Error", err)
	}
	if validationErr.Kind != kind || validationErr.File != file || validationErr.Line != line || validationErr.Pointer != pointer {
		t.Fatalf("error detail = %#v, want kind=%q file=%q line=%d pointer=%q", validationErr, kind, file, line, pointer)
	}
}
