package http

import (
	"errors"
	"net/http"

	"gh.tarampamp.am/webhook-tester/v2/internal/http/openapi"
)

// ApiSessionsList is a stub handler for GET /api/sessions.
// The real implementation is added in a later task.
func (o *OpenAPI) ApiSessionsList(w http.ResponseWriter, _ *http.Request, _ openapi.ApiSessionsListParams) {
	o.errorToJson(w, errors.New("not implemented"), http.StatusNotImplemented)
}

// ApiSearch is a stub handler for GET /api/search.
// The real implementation is added in a later task.
func (o *OpenAPI) ApiSearch(w http.ResponseWriter, _ *http.Request, _ openapi.ApiSearchParams) {
	o.errorToJson(w, errors.New("not implemented"), http.StatusNotImplemented)
}

// ApiSessionUpdate is a stub handler for PATCH /api/session/{session_uuid}.
// The real implementation is added in a later task.
func (o *OpenAPI) ApiSessionUpdate(w http.ResponseWriter, _ *http.Request, _ sID) {
	o.errorToJson(w, errors.New("not implemented"), http.StatusNotImplemented)
}

// ApiSessionReplayRequest is a stub handler for POST /api/session/{session_uuid}/requests/{request_uuid}/replay.
// The real implementation is added in a later task.
func (o *OpenAPI) ApiSessionReplayRequest(w http.ResponseWriter, _ *http.Request, _ sID, _ rID) {
	o.errorToJson(w, errors.New("not implemented"), http.StatusNotImplemented)
}
