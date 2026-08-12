package snapshot_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"batchscope/internal/identity"
	"batchscope/internal/snapshot"
	"batchscope/internal/store"
	"batchscope/internal/target"
	"batchscope/internal/traversal"
)

func TestNamespacedSnapshotKeepsDuplicateLocalIDsAndCrossNamespaceDependency(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	mainNetwork := identity.Encode("main", "NET")
	mainJob := identity.Encode("main", "JOB-A")
	sharedFile := identity.Encode("shared", "/exchange/ready.flag")
	drNetwork := identity.Encode("dr", "NET")
	drJob := identity.Encode("dr", "JOB-A")

	extracted := writeNamespacedFixture(t, directory,
		[]string{
			nodeJSON("job_network", mainNetwork, "main", "NET", "Main Network", ""),
			nodeJSON("job", mainJob, "main", "JOB-A", "Main JOB-A", mainNetwork),
			nodeJSON("file", sharedFile, "shared", "/exchange/ready.flag", "ready.flag", ""),
			nodeJSON("job_network", drNetwork, "dr", "NET", "DR Network", ""),
			nodeJSON("job", drJob, "dr", "JOB-A", "DR JOB-A", drNetwork),
		},
		[]string{
			fmt.Sprintf(`{"fromId":%q,"toId":%q,"kind":"produces","origin":"deterministic_analysis","certainty":"confirmed"}`, mainJob, sharedFile),
			fmt.Sprintf(`{"fromId":%q,"toId":%q,"kind":"observed_by","origin":"deterministic_analysis","certainty":"confirmed"}`, sharedFile, drJob),
		},
	)
	validated, err := snapshot.Validate(ctx, extracted)
	if err != nil {
		t.Fatal(err)
	}
	storage, err := store.New(filepath.Join(directory, "store"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	operation, err := storage.BeginImport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Load(ctx, operation.DB(), extracted, validated); err != nil {
		t.Fatal(err)
	}
	if err := operation.Complete(ctx, store.Generation{
		SnapshotID: validated.SnapshotID, GeneratedAt: validated.GeneratedAt, SchemaVersion: validated.SchemaVersion,
		NodeCount: validated.NodeCount, RelationCount: validated.RelationCount, LimitCount: validated.LimitCount,
		MaxSCCNodes: validated.MaxSCCNodes, MaxJobNetworkDepth: validated.MaxJobNetworkDepth, Fingerprint: validated.Fingerprint,
	}); err != nil {
		t.Fatal(err)
	}

	db, _, release, err := storage.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	result, err := target.SearchLocalID(ctx, db, "JOB-A", nil, []string{"job"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("JOB-A matches = %d, want 2: %#v", len(result.Items), result.Items)
	}
	if result.Items[0].Namespace != "dr" || result.Items[1].Namespace != "main" {
		t.Fatalf("namespaces = %q, %q, want dr, main", result.Items[0].Namespace, result.Items[1].Namespace)
	}
	for _, item := range result.Items {
		if item.LocalID != "JOB-A" || len(item.MatchedBy) != 1 || item.MatchedBy[0] != "localId" {
			t.Fatalf("search item = %#v", item)
		}
	}
	mainOnly, err := target.SearchLocalID(ctx, db, "JOB-A", stringPointer("main"), []string{"job"})
	if err != nil {
		t.Fatal(err)
	}
	if len(mainOnly.Items) != 1 || mainOnly.Items[0].ID != mainJob {
		t.Fatalf("main JOB-A = %#v", mainOnly.Items)
	}

	traversed, err := traversal.Traverse(ctx, db, mainJob)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := traversed.Nodes[drJob]; !ok {
		t.Fatalf("cross-namespace downstream job %q was not reached", drJob)
	}
	if _, ok := traversed.Nodes[sharedFile]; !ok {
		t.Fatalf("shared resource %q was not reached", sharedFile)
	}
}

func TestNamespacedSnapshotRejectsCrossNamespaceParent(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	mainNetwork := identity.Encode("main", "NET")
	drJob := identity.Encode("dr", "JOB-A")
	extracted := writeNamespacedFixture(t, directory,
		[]string{
			nodeJSON("job_network", mainNetwork, "main", "NET", "Main Network", ""),
			nodeJSON("job", drJob, "dr", "JOB-A", "DR JOB-A", mainNetwork),
		}, nil,
	)
	validated, err := snapshot.Validate(ctx, extracted)
	if err != nil {
		t.Fatal(err)
	}
	storage, err := store.New(filepath.Join(directory, "store"))
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	operation, err := storage.BeginImport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = snapshot.Load(ctx, operation.DB(), extracted, validated)
	if err == nil {
		t.Fatal("Load succeeded with cross-namespace parent")
	}
	var snapshotErr *snapshot.Error
	if !asSnapshotError(err, &snapshotErr) || snapshotErr.Kind != snapshot.ErrorInvalidIdentity || snapshotErr.Pointer != "/parentId" {
		t.Fatalf("Load error = %v", err)
	}
	_ = operation.Abort()
}

func writeNamespacedFixture(t *testing.T, directory string, nodes, relations []string) snapshot.Extracted {
	t.Helper()
	manifestPath := filepath.Join(directory, "manifest.json")
	nodesPath := filepath.Join(directory, "nodes.ndjson")
	relationsPath := filepath.Join(directory, "relations.ndjson")
	manifest := fmt.Sprintf(`{"schemaVersion":"0.6","snapshotId":"namespace-test","generatedAt":%q,"nodeCount":%d,"relationCount":%d,"producer":{"name":"test","version":"0.6.0"}}`, time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC).Format(time.RFC3339), len(nodes), len(relations))
	writeFile(t, manifestPath, manifest+"\n")
	writeFile(t, nodesPath, joinLines(nodes))
	writeFile(t, relationsPath, joinLines(relations))
	return snapshot.Extracted{Directory: directory, Manifest: manifestPath, Nodes: nodesPath, Relations: relationsPath}
}

func nodeJSON(kind, id, namespace, localID, name, parentID string) string {
	parent := ""
	if parentID != "" {
		parent = fmt.Sprintf(`,"parentId":%q`, parentID)
	}
	return fmt.Sprintf(`{"type":%q,"id":%q,"namespace":%q,"localId":%q,"name":%q%s}`, kind, id, namespace, localID, name, parent)
}

func joinLines(lines []string) string {
	result := ""
	for _, line := range lines {
		result += line + "\n"
	}
	return result
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func stringPointer(value string) *string { return &value }

func asSnapshotError(err error, target **snapshot.Error) bool {
	for err != nil {
		if current, ok := err.(*snapshot.Error); ok {
			*target = current
			return true
		}
		type unwrapper interface{ Unwrap() error }
		unwrapped, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = unwrapped.Unwrap()
	}
	return false
}
