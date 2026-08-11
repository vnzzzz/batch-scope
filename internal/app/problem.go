package app

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

func InvalidRequestProblem(detail string) *huma.ErrorModel {
	return &huma.ErrorModel{
		Type:   "/problems/invalid-request",
		Title:  http.StatusText(http.StatusBadRequest),
		Status: http.StatusBadRequest,
		Detail: detail,
	}
}

func InternalErrorProblem(detail string) *huma.ErrorModel {
	return &huma.ErrorModel{
		Type:   "/problems/internal-error",
		Title:  http.StatusText(http.StatusInternalServerError),
		Status: http.StatusInternalServerError,
		Detail: detail,
	}
}

func SnapshotNotLoadedProblem(detail string) *huma.ErrorModel {
	return &huma.ErrorModel{
		Type:   "/problems/snapshot-not-loaded",
		Title:  http.StatusText(http.StatusServiceUnavailable),
		Status: http.StatusServiceUnavailable,
		Detail: detail,
	}
}

func AnalysisTimeoutProblem(detail string) *huma.ErrorModel {
	return &huma.ErrorModel{
		Type:   "/problems/analysis-timeout",
		Title:  http.StatusText(http.StatusServiceUnavailable),
		Status: http.StatusServiceUnavailable,
		Detail: detail,
	}
}

func TargetNotFoundProblem(detail string) *huma.ErrorModel {
	return &huma.ErrorModel{
		Type:   "/problems/target-not-found",
		Title:  http.StatusText(http.StatusNotFound),
		Status: http.StatusNotFound,
		Detail: detail,
	}
}
