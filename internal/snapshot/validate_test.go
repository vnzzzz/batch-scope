package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if len(result.Relations) != 8 {
		t.Fatalf("len(Relations) = %d, want 8", len(result.Relations))
	}
}

func TestValidateRejectsSchemaViolations(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		line    int
		pointer string
		mutate  func(t *testing.T, extracted Extracted)
	}{
		{
			name: "manifest",
			file: manifestName, pointer: "/schemaVersion",
			mutate: func(t *testing.T, extracted Extracted) {
				writeTestFile(t, extracted.Manifest, `{"schemaVersion":"1","snapshotId":"s","generatedAt":"2026-08-08T00:00:00Z","nodeCount":1,"relationCount":0,"producer":{"name":"test","version":"1"}}`)
			},
		},
		{
			name: "node",
			file: nodesName, line: 1, pointer: "/name",
			mutate: func(t *testing.T, extracted Extracted) {
				writeTestFile(t, extracted.Nodes, `{"type":"job","id":"A","name":""}`+"\n")
			},
		},
		{
			name: "relation",
			file: relationsName, line: 1, pointer: "/kind",
			mutate: func(t *testing.T, extracted Extracted) {
				writeTestFile(t, extracted.Relations, `{"fromId":"A","toId":"A","kind":"unknown","origin":"scheduler","certainty":"declared"}`+"\n")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extracted := writeExtractedSnapshot(t, []string{testNode("job", "A", nil, nil)}, nil)
			tt.mutate(t, extracted)
			_, err := Validate(context.Background(), extracted)
			assertValidationError(t, err, ErrorSchemaViolation, tt.file, tt.line, tt.pointer)
		})
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

func TestValidateRejectsLimitOutsideJob(t *testing.T) {
	limit := map[string]any{"id": "RAW", "kind": "raw", "sourceText": "raw", "origin": "manual", "certainty": "declared"}
	nodes := []string{testNode("job_network", "NET", nil, []any{limit})}
	_, err := Validate(context.Background(), writeExtractedSnapshot(t, nodes, nil))
	assertValidationError(t, err, ErrorInvalidLimitOwner, nodesName, 1, "/limitFacts")
}

func TestValidateRejectsDurationsThatCannotBecomeFixedIntegerSeconds(t *testing.T) {
	for _, duration := range []string{"P1Y", "P1M", "P", "PT", "P1DT", "PT0.5S", "P999999999999999999999999999999999999D"} {
		t.Run(duration, func(t *testing.T) {
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
		t.Fatalf("relationID() = %q, want %q for equivalent evidence", got, want)
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
			t.Errorf("relationID(change %d) = unchanged %q", index, got)
		}
	}
}

func TestValidateReturnsRelationIDsInInputOrder(t *testing.T) {
	nodes := []string{testNode("job", "A", nil, nil), testNode("job", "B", nil, nil), testNode("job", "C", nil, nil)}
	input := []relation{
		{FromID: "A", ToID: "B", Kind: "precedes", Origin: "scheduler", Certainty: "declared"},
		{FromID: "B", ToID: "C", Kind: "precedes", Origin: "scheduler", Certainty: "declared"},
	}
	relations := []string{
		testRelation("A", "B", "precedes", "scheduler", "declared", nil),
		testRelation("B", "C", "precedes", "scheduler", "declared", nil),
	}
	result, err := Validate(context.Background(), writeExtractedSnapshot(t, nodes, relations))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	for index, item := range result.Relations {
		want, idErr := relationID(input[index])
		if idErr != nil {
			t.Fatalf("relationID() error = %v", idErr)
		}
		if item.Line != index+1 || item.ID != want {
			t.Errorf("Relations[%d] = %#v, want line %d ID %q", index, item, index+1, want)
		}
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
