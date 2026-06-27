package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"gh.tarampamp.am/webhook-tester/v2/internal/config"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/live"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/ready"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/request_delete"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/request_get"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/request_replay"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/requests_delete_all"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/requests_list"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/requests_subscribe"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/search"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/session_check_exists"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/session_create"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/session_delete"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/session_get"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/session_update"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/sessions_list"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/settings_get"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/shared"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/version"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/version_latest"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/openapi"
	"gh.tarampamp.am/webhook-tester/v2/internal/pubsub"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage/hotindex"
	appVersion "gh.tarampamp.am/webhook-tester/v2/internal/version"
)

type ( // type aliases for better readability
	sID  = openapi.SessionUUIDInPath
	rID  = openapi.RequestUUIDInPath
	skip = openapi.ApiSessionRequestsSubscribeParams // it doesn't matter
)

type OpenAPI struct {
	log *zap.Logger

	handlers struct {
		settingsGet        func() openapi.SettingsResponse
		sessionCreate      func(context.Context, openapi.CreateSessionRequest) (*openapi.SessionOptionsResponse, error)
		sessionCheckExists func(ctx context.Context, ids []openapi.UUID) (*openapi.CheckSessionExistsResponse, error)
		sessionGet         func(context.Context, sID) (*openapi.SessionOptionsResponse, error)
		sessionUpdate      func(context.Context, sID, openapi.UpdateSessionRequest) (*openapi.SessionOptionsResponse, error)
		sessionDelete      func(context.Context, sID) (*openapi.SuccessfulOperationResponse, error)
		sessionsList       func(context.Context, openapi.ApiSessionsListParams) (*openapi.SessionsListResponse, error)
		search             func(context.Context, openapi.ApiSearchParams) (*openapi.SearchResponse, error)
		requestsList       func(context.Context, sID) (*openapi.CapturedRequestsListResponse, error)
		requestsDelete     func(context.Context, sID) (*openapi.SuccessfulOperationResponse, error)
		requestsSubscribe  func(context.Context, http.ResponseWriter, *http.Request, sID) error
		requestGet         func(context.Context, sID, rID) (*openapi.CapturedRequestsResponse, error)
		requestDelete      func(context.Context, sID, rID) (*openapi.SuccessfulOperationResponse, error)
		requestReplay      func(context.Context, sID, rID, *openapi.ReplayRequest) (*openapi.ReplayResponse, error)
		appVersion         func() openapi.VersionResponse
		appVersionLatest   func(context.Context, http.ResponseWriter) (*openapi.VersionResponse, error)
		readinessProbe     func(context.Context, http.ResponseWriter, string)
		livenessProbe      func(http.ResponseWriter, string)
	}
}

var _ openapi.ServerInterface = (*OpenAPI)(nil) // verify interface implementation

func NewOpenAPI(
	appCtx context.Context,
	log *zap.Logger,
	rdyChecker func(context.Context) error,
	lastAppVer func(context.Context) (string, error),
	cfg *config.AppSettings,
	db storage.Storage,
	pubSub pubsub.PubSub[pubsub.RequestEvent],
	hotIndex *hotindex.HotIndex, // optional (nil-safe): enables the search fast path
) *OpenAPI {
	var si = &OpenAPI{log: log}

	// The search handler reads the hot index's retention window to decide its
	// fast-path/fallback split; default it when no index is wired (e.g. in tests).
	var searchWindow time.Duration
	if hotIndex != nil {
		searchWindow = hotIndex.Window()
	}

	si.handlers.settingsGet = settings_get.New(cfg).Handle
	si.handlers.sessionCreate = session_create.New(db).Handle
	si.handlers.sessionCheckExists = session_check_exists.New(db).Handle
	si.handlers.sessionGet = session_get.New(db).Handle
	si.handlers.sessionUpdate = session_update.New(db).Handle
	si.handlers.sessionDelete = session_delete.New(db).Handle
	si.handlers.sessionsList = sessions_list.New(db).Handle
	si.handlers.search = search.New(db, hotIndex, searchWindow).Handle
	si.handlers.requestsList = requests_list.New(db).Handle
	si.handlers.requestsDelete = requests_delete_all.New(appCtx, db, pubSub).Handle
	si.handlers.requestsSubscribe = requests_subscribe.New(db, pubSub).Handle
	si.handlers.requestGet = request_get.New(db).Handle
	si.handlers.requestDelete = request_delete.New(appCtx, db, pubSub).Handle
	si.handlers.requestReplay = request_replay.New(db).Handle
	si.handlers.appVersion = version.New(appVersion.Version()).Handle
	si.handlers.appVersionLatest = version_latest.New(lastAppVer).Handle
	si.handlers.readinessProbe = ready.New(rdyChecker).Handle
	si.handlers.livenessProbe = live.New().Handle

	return si
}

func (o *OpenAPI) ApiSettings(w http.ResponseWriter, _ *http.Request) {
	o.respToJson(w, o.handlers.settingsGet())
}

func (o *OpenAPI) ApiSessionCreate(w http.ResponseWriter, r *http.Request) {
	var payload openapi.CreateSessionRequest

	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		o.errorToJson(w, err, http.StatusBadRequest)

		return
	}

	if err := payload.Validate(); err != nil {
		o.errorToJson(w, err, http.StatusBadRequest)

		return
	}

	if resp, err := o.handlers.sessionCreate(r.Context(), payload); err != nil {
		o.errorToJson(w, err, statusForError(err))
	} else {
		o.respToJson(w, resp)
	}
}

func (o *OpenAPI) ApiSessionCheckExists(w http.ResponseWriter, r *http.Request) {
	var payload openapi.CheckSessionExistsRequest

	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		o.errorToJson(w, err, http.StatusBadRequest)

		return
	}

	const minIDsCount, maxIDsCount = 1, 100

	if len(payload) < minIDsCount || len(payload) > maxIDsCount {
		o.errorToJson(w,
			fmt.Errorf("wrong IDs count (should be between %d and %d)", minIDsCount, maxIDsCount),
			http.StatusBadRequest,
		)

		return
	}

	if resp, err := o.handlers.sessionCheckExists(r.Context(), payload); err != nil {
		o.errorToJson(w, err, http.StatusInternalServerError)
	} else {
		o.respToJson(w, resp)
	}
}

func (o *OpenAPI) ApiSessionGet(w http.ResponseWriter, r *http.Request, sID sID) {
	if resp, err := o.handlers.sessionGet(r.Context(), sID); err != nil {
		var statusCode = http.StatusInternalServerError

		if errors.Is(err, storage.ErrNotFound) {
			statusCode = http.StatusNotFound
		}

		o.errorToJson(w, err, statusCode)
	} else {
		o.respToJson(w, resp)
	}
}

func (o *OpenAPI) ApiSessionUpdate(w http.ResponseWriter, r *http.Request, sID sID) {
	var payload openapi.UpdateSessionRequest

	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		o.errorToJson(w, err, http.StatusBadRequest)

		return
	}

	if resp, err := o.handlers.sessionUpdate(r.Context(), sID, payload); err != nil {
		o.errorToJson(w, err, statusForError(err))
	} else {
		o.respToJson(w, resp)
	}
}

func (o *OpenAPI) ApiSessionsList(w http.ResponseWriter, r *http.Request, params openapi.ApiSessionsListParams) {
	if resp, err := o.handlers.sessionsList(r.Context(), params); err != nil {
		o.errorToJson(w, err, statusForError(err))
	} else {
		o.respToJson(w, resp)
	}
}

func (o *OpenAPI) ApiSearch(w http.ResponseWriter, r *http.Request, params openapi.ApiSearchParams) {
	if resp, err := o.handlers.search(r.Context(), params); err != nil {
		o.errorToJson(w, err, statusForError(err))
	} else {
		o.respToJson(w, resp)
	}
}

func (o *OpenAPI) ApiSessionReplayRequest(w http.ResponseWriter, r *http.Request, sID sID, rID rID) {
	// The replay body is optional: an empty body (io.EOF) means "use the session's
	// configured forward URL"; a malformed body is a client error.
	var payload *openapi.ReplayRequest

	var decoded openapi.ReplayRequest
	if err := json.NewDecoder(r.Body).Decode(&decoded); err == nil {
		payload = &decoded
	} else if !errors.Is(err, io.EOF) {
		o.errorToJson(w, err, http.StatusBadRequest)

		return
	}

	if resp, err := o.handlers.requestReplay(r.Context(), sID, rID, payload); err != nil {
		o.errorToJson(w, err, statusForError(err))
	} else {
		o.respToJson(w, resp)
	}
}

func (o *OpenAPI) ApiSessionDelete(w http.ResponseWriter, r *http.Request, sID sID) {
	if resp, err := o.handlers.sessionDelete(r.Context(), sID); err != nil {
		var statusCode = http.StatusInternalServerError

		if errors.Is(err, storage.ErrNotFound) {
			statusCode = http.StatusNotFound
		}

		o.errorToJson(w, err, statusCode)
	} else {
		o.respToJson(w, resp)
	}
}

func (o *OpenAPI) ApiSessionListRequests(w http.ResponseWriter, r *http.Request, sID sID) {
	if resp, err := o.handlers.requestsList(r.Context(), sID); err != nil {
		var statusCode = http.StatusInternalServerError

		if errors.Is(err, storage.ErrNotFound) {
			statusCode = http.StatusNotFound
		}

		o.errorToJson(w, err, statusCode)
	} else {
		o.respToJson(w, resp)
	}
}

func (o *OpenAPI) ApiSessionDeleteAllRequests(w http.ResponseWriter, r *http.Request, sID sID) {
	if resp, err := o.handlers.requestsDelete(r.Context(), sID); err != nil {
		var statusCode = http.StatusInternalServerError

		if errors.Is(err, storage.ErrNotFound) {
			statusCode = http.StatusNotFound
		}

		o.errorToJson(w, err, statusCode)
	} else {
		o.respToJson(w, resp)
	}
}

func (o *OpenAPI) ApiSessionRequestsSubscribe(w http.ResponseWriter, r *http.Request, sID sID, _ skip) {
	if err := o.handlers.requestsSubscribe(r.Context(), w, r, sID); err != nil {
		var statusCode = http.StatusInternalServerError

		if errors.Is(err, storage.ErrNotFound) {
			statusCode = http.StatusNotFound
		}

		o.errorToJson(w, err, statusCode)
	}
}

func (o *OpenAPI) ApiSessionGetRequest(w http.ResponseWriter, r *http.Request, sID sID, rID rID) {
	if resp, err := o.handlers.requestGet(r.Context(), sID, rID); err != nil {
		var statusCode = http.StatusInternalServerError

		if errors.Is(err, storage.ErrNotFound) {
			statusCode = http.StatusNotFound
		}

		o.errorToJson(w, err, statusCode)
	} else {
		o.respToJson(w, resp)
	}
}

func (o *OpenAPI) ApiSessionDeleteRequest(w http.ResponseWriter, r *http.Request, sID sID, rID rID) {
	if resp, err := o.handlers.requestDelete(r.Context(), sID, rID); err != nil {
		var statusCode = http.StatusInternalServerError

		if errors.Is(err, storage.ErrNotFound) {
			statusCode = http.StatusNotFound
		}

		o.errorToJson(w, err, statusCode)
	} else {
		o.respToJson(w, resp)
	}
}

func (o *OpenAPI) ApiAppVersion(w http.ResponseWriter, _ *http.Request) {
	o.respToJson(w, o.handlers.appVersion())
}

func (o *OpenAPI) ApiAppVersionLatest(w http.ResponseWriter, r *http.Request) {
	if resp, err := o.handlers.appVersionLatest(r.Context(), w); err != nil {
		o.errorToJson(w, err, http.StatusInternalServerError)
	} else {
		o.respToJson(w, resp)
	}
}

func (o *OpenAPI) ReadinessProbe(w http.ResponseWriter, r *http.Request) {
	o.handlers.readinessProbe(r.Context(), w, r.Method)
}

func (o *OpenAPI) ReadinessProbeHead(w http.ResponseWriter, r *http.Request) {
	o.handlers.readinessProbe(r.Context(), w, r.Method)
}

func (o *OpenAPI) LivenessProbe(w http.ResponseWriter, r *http.Request) {
	o.handlers.livenessProbe(w, r.Method)
}

func (o *OpenAPI) LivenessProbeHead(w http.ResponseWriter, r *http.Request) {
	o.handlers.livenessProbe(w, r.Method)
}

// -------------------------------------------------- Error handlers --------------------------------------------------

// HandleInternalError is a default error handler for internal server errors (e.g. query parameters binding
// errors, and so on).
func (o *OpenAPI) HandleInternalError(w http.ResponseWriter, _ *http.Request, err error) {
	//	Invalid format for parameter session_uuid: error unmarshaling 'xxxxxx' text as *uuid.UUID: invalid UUID format
	// to
	//	invalid UUID format
	if err != nil && strings.Contains(err.Error(), "invalid UUID") {
		err = errors.New("invalid UUID format")
	}

	o.errorToJson(w, err, http.StatusBadRequest)
}

// HandleNotFoundError is a default error handler for "404: not found" errors.
func (o *OpenAPI) HandleNotFoundError(w http.ResponseWriter, _ *http.Request) {
	o.errorToJson(w, errors.New("not found"), http.StatusNotFound)
}

// ------------------------------------------------- Internal helpers -------------------------------------------------

const (
	contentTypeHeader = "Content-Type"
	contentTypeJSON   = "application/json; charset=utf-8"
)

// statusForError maps a handler error to its HTTP status code. Handlers signal
// client errors via the shared sentinel errors (and storage signals not-found);
// anything else is treated as a 500.
func statusForError(err error) int {
	switch {
	case errors.Is(err, shared.ErrBadRequest):
		return http.StatusBadRequest
	case errors.Is(err, shared.ErrConflict):
		return http.StatusConflict
	case errors.Is(err, storage.ErrNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

func (o *OpenAPI) respToJson(w http.ResponseWriter, resp any) {
	w.Header().Set(contentTypeHeader, contentTypeJSON)
	w.WriteHeader(http.StatusOK)

	if resp == nil {
		return
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		o.log.Error("failed to encode/write response", zap.Error(err))
	}
}

func (o *OpenAPI) errorToJson(w http.ResponseWriter, err error, status int) {
	w.Header().Set(contentTypeHeader, contentTypeJSON)
	w.WriteHeader(status)

	if err == nil {
		return
	}

	if encErr := json.NewEncoder(w).Encode(openapi.ErrorResponse{Error: err.Error()}); encErr != nil {
		o.log.Error("failed to encode/write error response", zap.Error(encErr))
	}
}
