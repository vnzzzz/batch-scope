package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"batchscope/internal/importer"
)

// TestGenerateDemoDownstreamLimitAnalysisResponse は公開DTO変更時のgolden再生成用。
// 通常testではskipし、BATCHSCOPE_UPDATE_DEMO=1を明示した場合だけrepository内のfixtureを更新する。
func TestGenerateDemoDownstreamLimitAnalysisResponse(t *testing.T) {
	if os.Getenv("BATCHSCOPE_UPDATE_DEMO") != "1" {
		t.Skip("set BATCHSCOPE_UPDATE_DEMO=1 to regenerate demo response")
	}

	a := newTestApp(t)
	workspace := t.TempDir()
	archive := demoSnapshotArchive(t)
	if _, err := importer.Run(context.Background(), workspace, bytes.NewReader(archive), a.store); err != nil {
		t.Fatalf("import demo snapshot: %v", err)
	}

	recorder := serveRequest(a, "/v1/downstream-limit-analysis?targetId=JOB-A")
	assertStatus(t, recorder, 200)
	actual := decodeObject(t, recorder)
	actual["bootId"] = demoBootID
	formatted, err := json.MarshalIndent(actual, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	formatted = append(formatted, '\n')

	responsePath := filepath.Join("..", "..", "examples", "demo", "responses", "downstream-limit-analysis.json")
	if err := os.WriteFile(responsePath, formatted, 0o644); err != nil {
		t.Fatal(err)
	}
}
