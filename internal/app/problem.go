package app

import (
	"errors"
	"net/http"

	"batchscope/internal/importer"
	"batchscope/internal/snapshot"
	"batchscope/internal/store"

	"github.com/danielgtaylor/huma/v2"
)

type problemDetails struct {
	Type    string `json:"type"`
	Title   string `json:"title"`
	Status  int    `json:"status"`
	Detail  string `json:"detail"`
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
	Pointer string `json:"pointer,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

func newProblem(problemType string, status int, detail string) *huma.ErrorModel {
	return &huma.ErrorModel{
		Type: problemType, Title: http.StatusText(status), Status: status, Detail: detail,
	}
}

func InvalidRequestProblem(detail string) *huma.ErrorModel {
	return newProblem("/problems/invalid-request", http.StatusBadRequest, detail)
}

func InternalErrorProblem(detail string) *huma.ErrorModel {
	return newProblem("/problems/internal-error", http.StatusInternalServerError, detail)
}

func SnapshotNotLoadedProblem(detail string) *huma.ErrorModel {
	return newProblem("/problems/snapshot-not-loaded", http.StatusServiceUnavailable, detail)
}

func AnalysisTimeoutProblem(detail string) *huma.ErrorModel {
	return newProblem("/problems/analysis-timeout", http.StatusServiceUnavailable, detail)
}

func TargetNotFoundProblem(detail string) *huma.ErrorModel {
	return newProblem("/problems/target-not-found", http.StatusNotFound, detail)
}

func SnapshotImportInProgressProblem(detail string) *huma.ErrorModel {
	return newProblem("/problems/snapshot-import-in-progress", http.StatusConflict, detail)
}

func SnapshotIDConflictProblem(detail string) *huma.ErrorModel {
	return newProblem("/problems/snapshot-id-conflict", http.StatusConflict, detail)
}

func SnapshotTooLargeProblem(detail string) *huma.ErrorModel {
	return newProblem("/problems/snapshot-too-large", http.StatusRequestEntityTooLarge, detail)
}

func SnapshotCapacityExceededProblem(detail string) *huma.ErrorModel {
	return newProblem("/problems/snapshot-capacity-exceeded", http.StatusUnprocessableEntity, detail)
}

func InvalidSnapshotProblem(detail string) *huma.ErrorModel {
	return newProblem("/problems/invalid-snapshot", http.StatusUnprocessableEntity, detail)
}

func ImportNotFoundProblem(detail string) *huma.ErrorModel {
	return newProblem("/problems/import-not-found", http.StatusNotFound, detail)
}

func mappedImportProblem(err error) problemDetails {
	model := InternalErrorProblem("snapshot import failed")
	var snapshotErr *snapshot.Error
	switch {
	case errors.Is(err, store.ErrImportInProgress):
		model = SnapshotImportInProgressProblem("another snapshot import is in progress")
	case errors.Is(err, importer.ErrSnapshotIDConflict):
		model = SnapshotIDConflictProblem("snapshot ID is already active with different content")
	case errors.As(err, &snapshotErr):
		switch snapshotErr.Kind {
		case snapshot.ErrorCompressedLimit, snapshot.ErrorExtractedLimit:
			model = SnapshotTooLargeProblem("snapshot exceeds the supported size")
		case snapshot.ErrorCapacityExceeded:
			model = SnapshotCapacityExceededProblem("snapshot exceeds the supported capacity")
		case snapshot.ErrorIO:
			model = InternalErrorProblem("snapshot import failed")
		default:
			model = InvalidSnapshotProblem("snapshot validation failed")
		}
	}

	problem := problemDetails{
		Type: model.Type, Title: model.Title, Status: model.Status, Detail: model.Detail,
	}
	if snapshotErr != nil && (problem.Type == "/problems/invalid-snapshot" || problem.Type == "/problems/snapshot-capacity-exceeded") {
		problem.File = snapshotErr.File
		problem.Line = snapshotErr.Line
		problem.Pointer = snapshotErr.Pointer
		problem.Reason = string(snapshotErr.Kind)
	}
	return problem
}
