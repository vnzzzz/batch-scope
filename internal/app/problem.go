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
