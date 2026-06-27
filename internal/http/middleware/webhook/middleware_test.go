package webhook_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"gh.tarampamp.am/webhook-tester/v2/internal/config"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/middleware/webhook"
	"gh.tarampamp.am/webhook-tester/v2/internal/identifiers"
	"gh.tarampamp.am/webhook-tester/v2/internal/pubsub"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage/hotindex"
)

// passThroughMarker is set by the test "next" handler so we can detect when the
// middleware did NOT capture the request (i.e. it fell through to next).
const passThroughMarker = "X-Passed-Through"

// newTestHandler builds the webhook middleware wrapping a distinctive next handler.
func newTestHandler(
	t *testing.T,
	db storage.Storage,
	cfg *config.AppSettings,
	ext *identifiers.Extractor,
	hi *hotindex.HotIndex,
) http.Handler {
	t.Helper()

	var (
		pub = pubsub.NewInMemory[pubsub.RequestEvent]()
		mw  = webhook.New(context.Background(), zap.NewNop(), db, pub, cfg, ext, hi, time.Second)
	)

	var next = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(passThroughMarker, "1")
		w.WriteHeader(http.StatusTeapot) // 418 — a code the middleware never produces on its own
		_, _ = w.Write([]byte("passed-through"))
	})

	return mw(next)
}

func newMemDB(t *testing.T) storage.Storage {
	t.Helper()

	var db = storage.NewInMemory(time.Minute, 16)

	t.Cleanup(func() { _ = db.Close() })

	return db
}

// (a) slug resolution + identifier hot-indexing + template response + security header.
func TestCapture_SlugScript_IndexedAndSecured(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = newMemDB(t)
	)

	const script = "@status 202\n{\"ok\":true,\"slug\":\"{{ .Slug }}\"}"

	sID, err := db.NewSession(ctx, storage.Session{
		Code:            200,
		Slug:            "my-app",
		ResponseScript:  script,
		SecurityHeaders: []storage.HttpHeader{{Name: "X-Frame-Options", Value: "DENY"}},
	})
	require.NoError(t, err)

	var (
		ext = identifiers.NewExtractor([]string{"trackingId"}, nil, true)
		hi  = hotindex.New(168 * time.Hour)
		h   = newTestHandler(t, db, &config.AppSettings{SessionTTL: time.Minute}, ext, hi)
	)

	const body = `{"trackingId":"ABC123","nested":{"trackingId":"ABC123"}}`

	var (
		r = httptest.NewRequest(http.MethodPost, "/w/my-app/foo", strings.NewReader(body))
		w = httptest.NewRecorder()
	)

	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(w, r)

	// response built from the session's ResponseScript (@status 202 wins)
	require.Equal(t, http.StatusAccepted, w.Code)
	require.Contains(t, w.Body.String(), `"slug":"my-app"`)

	// security header always applied
	require.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))

	// request stored, not passed through
	require.NotEmpty(t, w.Header().Get("X-Wh-Request-Id"))
	require.Empty(t, w.Header().Get(passThroughMarker))

	// hot index now returns a ref for the captured trackingId (deduped → exactly one)
	var refs = hi.Lookup("trackingId", "ABC123", storage.IdentifierMatchExact)
	require.Len(t, refs, 1)
	require.Equal(t, sID, refs[0].SessionID)
	require.Equal(t, "my-app", refs[0].SessionSlug)
	require.Equal(t, w.Header().Get("X-Wh-Request-Id"), refs[0].RequestID)
}

// (b) empty script → static code/body, security headers still applied; nil ext/hi tolerated.
func TestCapture_NoScript_StaticWithSecurityHeaders(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = newMemDB(t)
	)

	_, err := db.NewSession(ctx, storage.Session{
		Code:            201,
		ResponseBody:    []byte("static-body"),
		Slug:            "static-slug",
		Headers:         []storage.HttpHeader{{Name: "X-Custom", Value: "yes"}},
		SecurityHeaders: []storage.HttpHeader{{Name: "Strict-Transport-Security", Value: "max-age=63072000"}},
	})
	require.NoError(t, err)

	// nil extractor + nil hot index must be tolerated gracefully
	var h = newTestHandler(t, db, &config.AppSettings{SessionTTL: time.Minute}, nil, nil)

	var (
		r = httptest.NewRequest(http.MethodPost, "/w/static-slug", nil)
		w = httptest.NewRecorder()
	)

	h.ServeHTTP(w, r)

	require.Equal(t, http.StatusCreated, w.Code)
	require.Equal(t, "static-body", w.Body.String())
	require.Equal(t, "yes", w.Header().Get("X-Custom"))
	require.Equal(t, "max-age=63072000", w.Header().Get("Strict-Transport-Security"))
	require.NotEmpty(t, w.Header().Get("X-Wh-Request-Id"))
	require.Empty(t, w.Header().Get(passThroughMarker))
}

// (b2) script execution error → static fallback, security headers still applied.
func TestCapture_ScriptError_FallsBackToStatic(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = newMemDB(t)
	)

	_, err := db.NewSession(ctx, storage.Session{
		Code:            418,
		ResponseBody:    []byte("fallback-body"),
		Slug:            "broken",
		ResponseScript:  "{{ .Nope.Missing }}", // execution error
		SecurityHeaders: []storage.HttpHeader{{Name: "X-Content-Type-Options", Value: "nosniff"}},
	})
	require.NoError(t, err)

	var h = newTestHandler(t, db, &config.AppSettings{SessionTTL: time.Minute}, nil, nil)

	var (
		r = httptest.NewRequest(http.MethodPost, "/w/broken", nil)
		w = httptest.NewRecorder()
	)

	h.ServeHTTP(w, r)

	require.Equal(t, http.StatusTeapot, w.Code) // 418 from static sess.Code
	require.Equal(t, "fallback-body", w.Body.String())
	require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
}

// (c) unknown ref under /w/ with auto-create off → 404 (no SPA conflict under /w/).
func TestCapture_UnknownSlug_AutoCreateOff_404(t *testing.T) {
	t.Parallel()

	var (
		db = newMemDB(t)
		h  = newTestHandler(t, db, &config.AppSettings{SessionTTL: time.Minute}, nil, nil)
		r  = httptest.NewRequest(http.MethodPost, "/w/no-such-slug/foo", nil)
		w  = httptest.NewRecorder()
	)

	h.ServeHTTP(w, r)

	require.Equal(t, http.StatusNotFound, w.Code)
	require.Empty(t, w.Header().Get(passThroughMarker)) // 404'd by middleware, not passed through
}

// (d) uuid path still works (back-compat).
func TestCapture_UUIDBackCompat(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = newMemDB(t)
	)

	const id = "00000000-0000-4000-8000-000000000001"

	sID, err := db.NewSession(ctx, storage.Session{Code: 200, ResponseBody: []byte("uuid-body")}, id)
	require.NoError(t, err)
	require.Equal(t, id, sID)

	var (
		h = newTestHandler(t, db, &config.AppSettings{SessionTTL: time.Minute}, nil, nil)
		r = httptest.NewRequest(http.MethodPost, "/w/"+id, nil)
		w = httptest.NewRecorder()
	)

	h.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "uuid-body", w.Body.String())
	require.NotEmpty(t, w.Header().Get("X-Wh-Request-Id"))
}

// (d2) uuid auto-create when enabled.
func TestCapture_UUIDAutoCreate(t *testing.T) {
	t.Parallel()

	var (
		db = newMemDB(t)
		h  = newTestHandler(t, db, &config.AppSettings{SessionTTL: time.Minute, AutoCreateSessions: true}, nil, nil)
	)

	const id = "11111111-1111-4111-8111-111111111111"

	var (
		r = httptest.NewRequest(http.MethodPost, "/w/"+id, nil)
		w = httptest.NewRecorder()
	)

	h.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code) // default code 200 on auto-create
	require.Equal(t, "1", w.Header().Get("X-Wh-Created-Automatically"))
	require.NotEmpty(t, w.Header().Get("X-Wh-Request-Id"))
}

// (d3) unknown uuid with auto-create off → 404 (existing behavior preserved).
func TestCapture_UnknownUUID_AutoCreateOff_404(t *testing.T) {
	t.Parallel()

	var (
		db = newMemDB(t)
		h  = newTestHandler(t, db, &config.AppSettings{SessionTTL: time.Minute}, nil, nil)
		r  = httptest.NewRequest(http.MethodPost, "/w/22222222-2222-4222-8222-222222222222", nil)
		w  = httptest.NewRecorder()
	)

	h.ServeHTTP(w, r)

	require.Equal(t, http.StatusNotFound, w.Code)
}

// (e) non-/w/ paths pass through untouched (SPA, /api, health, /s/{slug}, assets).
func TestCapture_NonWebhookPaths_PassThrough(t *testing.T) {
	t.Parallel()

	var (
		db = newMemDB(t)
		h  = newTestHandler(t, db, &config.AppSettings{SessionTTL: time.Minute, AutoCreateSessions: true}, nil, nil)
	)

	for _, p := range []string{
		"/api/sessions", "/foo-404", "/", "/healthz", "/ready",
		"/s/some-slug", "/assets/index-abc.js", "/robots.txt", "/w", "/w/",
	} {
		t.Run(p, func(t *testing.T) {
			t.Parallel()

			var (
				r = httptest.NewRequest(http.MethodGet, p, nil)
				w = httptest.NewRecorder()
			)

			h.ServeHTTP(w, r)

			require.Equal(t, "1", w.Header().Get(passThroughMarker), "path %q must pass through", p)
			require.Equal(t, http.StatusTeapot, w.Code, "path %q", p)
		})
	}
}

// inbound-auth helpers/tests ------------------------------------------------

const (
	inboundAuthHeader = "X-Webhook-Token"
	inboundAuthValue  = "s3cr3t-token"
	// authScript renders a distinctive body so we can prove the response script runs on the
	// authorized path and is skipped on the rejected (401) path.
	authScript = "@status 202\nSCRIPT-RAN:{{ .Slug }}"
)

// onlyRequest returns the single captured request for a session, failing if not exactly one.
func onlyRequest(t *testing.T, db storage.Storage, sID string) storage.Request {
	t.Helper()

	all, err := db.GetAllRequests(context.Background(), sID)
	require.NoError(t, err)
	require.Len(t, all, 1, "exactly one request must have been captured")

	for _, r := range all {
		return r
	}

	return storage.Request{}
}

// (f) inbound auth configured, request WITHOUT the header → 401, captured (authorized=false),
// response script NOT run.
func TestCapture_InboundAuth_MissingHeader_401_CapturedUnauthorized(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = newMemDB(t)
	)

	sID, err := db.NewSession(ctx, storage.Session{
		Code:              200,
		Slug:              "guarded",
		ResponseScript:    authScript,
		SecurityHeaders:   []storage.HttpHeader{{Name: "X-Frame-Options", Value: "DENY"}},
		InboundAuthHeader: inboundAuthHeader,
		InboundAuthValue:  inboundAuthValue,
	})
	require.NoError(t, err)

	var (
		h = newTestHandler(t, db, &config.AppSettings{SessionTTL: time.Minute}, nil, nil)
		r = httptest.NewRequest(http.MethodPost, "/w/guarded/foo", strings.NewReader(`{"x":1}`))
		w = httptest.NewRecorder()
	)

	h.ServeHTTP(w, r)

	// 401 with the JSON error body; the response script must NOT have run
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Contains(t, w.Body.String(), `"error":"unauthorized webhook"`)
	require.NotContains(t, w.Body.String(), "SCRIPT-RAN", "response script must not run on a rejected request")

	// the request is STILL captured and flagged authorized=false
	require.NotEmpty(t, w.Header().Get("X-Wh-Request-Id"))
	require.Empty(t, w.Header().Get(passThroughMarker))

	got := onlyRequest(t, db, sID)
	require.False(t, got.Authorized, "rejected request must be captured with authorized=false")
}

// (g) inbound auth configured, request with the WRONG value → 401, captured (authorized=false).
func TestCapture_InboundAuth_WrongValue_401_CapturedUnauthorized(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = newMemDB(t)
	)

	sID, err := db.NewSession(ctx, storage.Session{
		Code:              200,
		Slug:              "guarded2",
		ResponseScript:    authScript,
		InboundAuthHeader: inboundAuthHeader,
		InboundAuthValue:  inboundAuthValue,
	})
	require.NoError(t, err)

	var (
		h = newTestHandler(t, db, &config.AppSettings{SessionTTL: time.Minute}, nil, nil)
		r = httptest.NewRequest(http.MethodPost, "/w/guarded2", strings.NewReader(`{"x":1}`))
		w = httptest.NewRecorder()
	)

	r.Header.Set(inboundAuthHeader, "wrong-value")
	h.ServeHTTP(w, r)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Contains(t, w.Body.String(), `"error":"unauthorized webhook"`)
	require.NotContains(t, w.Body.String(), "SCRIPT-RAN")

	got := onlyRequest(t, db, sID)
	require.False(t, got.Authorized)
}

// (h) inbound auth configured, request with the CORRECT value → 200, script runs, authorized=true.
func TestCapture_InboundAuth_CorrectValue_RunsScript_Authorized(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = newMemDB(t)
	)

	sID, err := db.NewSession(ctx, storage.Session{
		Code:              200,
		Slug:              "guarded3",
		ResponseScript:    authScript,
		InboundAuthHeader: inboundAuthHeader,
		InboundAuthValue:  inboundAuthValue,
	})
	require.NoError(t, err)

	var (
		h = newTestHandler(t, db, &config.AppSettings{SessionTTL: time.Minute}, nil, nil)
		r = httptest.NewRequest(http.MethodPost, "/w/guarded3", strings.NewReader(`{"x":1}`))
		w = httptest.NewRecorder()
	)

	// header-name lookup is case-insensitive: send a differently-cased name on purpose
	r.Header.Set("x-webhook-token", inboundAuthValue)
	h.ServeHTTP(w, r)

	require.Equal(t, http.StatusAccepted, w.Code) // @status 202 from the script
	require.Contains(t, w.Body.String(), "SCRIPT-RAN:guarded3", "the response script must run on the authorized path")

	got := onlyRequest(t, db, sID)
	require.True(t, got.Authorized, "authorized request must be captured with authorized=true")
}

// (i) no inbound auth configured → behaves exactly as before, captured authorized=true.
func TestCapture_NoInboundAuth_Authorized(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = newMemDB(t)
	)

	sID, err := db.NewSession(ctx, storage.Session{
		Code:         201,
		Slug:         "public-ep",
		ResponseBody: []byte("ok-body"),
	})
	require.NoError(t, err)

	var (
		h = newTestHandler(t, db, &config.AppSettings{SessionTTL: time.Minute}, nil, nil)
		r = httptest.NewRequest(http.MethodPost, "/w/public-ep", strings.NewReader(`{"x":1}`))
		w = httptest.NewRecorder()
	)

	h.ServeHTTP(w, r)

	require.Equal(t, http.StatusCreated, w.Code)
	require.Equal(t, "ok-body", w.Body.String())

	got := onlyRequest(t, db, sID)
	require.True(t, got.Authorized, "a request to a public endpoint must be authorized=true")
}

// (j) misconfigured at the storage layer: header set but the configured value is EMPTY. This
// must FAIL CLOSED — a request that omits the header must NOT be authorized (the silent-bypass
// regression: ConstantTimeCompare("","") used to return 1 → authorized).
func TestCapture_InboundAuth_EmptyConfiguredValue_FailsClosed(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = newMemDB(t)
	)

	sID, err := db.NewSession(ctx, storage.Session{
		Code:              200,
		Slug:              "misconfig",
		InboundAuthHeader: inboundAuthHeader,
		InboundAuthValue:  "", // empty value must never authorize a header-less request
	})
	require.NoError(t, err)

	var (
		h = newTestHandler(t, db, &config.AppSettings{SessionTTL: time.Minute}, nil, nil)
		r = httptest.NewRequest(http.MethodPost, "/w/misconfig", strings.NewReader(`{}`))
		w = httptest.NewRecorder()
	)

	// the request OMITS the header entirely — must still be rejected
	h.ServeHTTP(w, r)

	require.Equal(t, http.StatusUnauthorized, w.Code, "empty configured value must fail closed, not authorize")

	got := onlyRequest(t, db, sID)
	require.False(t, got.Authorized)
}

// (k) header + value configured, request presents an EMPTY header value → 401 (captured false).
func TestCapture_InboundAuth_EmptyIncomingValue_401(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = newMemDB(t)
	)

	sID, err := db.NewSession(ctx, storage.Session{
		Code:              200,
		Slug:              "guarded-empty",
		InboundAuthHeader: inboundAuthHeader,
		InboundAuthValue:  inboundAuthValue,
	})
	require.NoError(t, err)

	var (
		h = newTestHandler(t, db, &config.AppSettings{SessionTTL: time.Minute}, nil, nil)
		r = httptest.NewRequest(http.MethodPost, "/w/guarded-empty", strings.NewReader(`{}`))
		w = httptest.NewRecorder()
	)

	r.Header.Set(inboundAuthHeader, "") // present but empty
	h.ServeHTTP(w, r)

	require.Equal(t, http.StatusUnauthorized, w.Code)

	got := onlyRequest(t, db, sID)
	require.False(t, got.Authorized)
}

// status-code-from-URL-path override is scoped to segments AFTER /w/{ref}.
func TestCapture_StatusOverrideInTail(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = newMemDB(t)
	)

	_, err := db.NewSession(ctx, storage.Session{Code: 200, ResponseBody: []byte("ok"), Slug: "ovr"})
	require.NoError(t, err)

	var (
		h = newTestHandler(t, db, &config.AppSettings{SessionTTL: time.Minute}, nil, nil)
		r = httptest.NewRequest(http.MethodPost, "/w/ovr/foo/503", nil)
		w = httptest.NewRecorder()
	)

	h.ServeHTTP(w, r)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// a numeric slug must NOT be misread as a status-code override (guards the ref boundary).
func TestCapture_NumericSlug_NotStatusOverride(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = newMemDB(t)
	)

	_, err := db.NewSession(ctx, storage.Session{Code: 200, ResponseBody: []byte("ok"), Slug: "404"})
	require.NoError(t, err)

	var (
		h = newTestHandler(t, db, &config.AppSettings{SessionTTL: time.Minute}, nil, nil)
		r = httptest.NewRequest(http.MethodPost, "/w/404", nil)
		w = httptest.NewRecorder()
	)

	h.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code) // 200, not 404
	require.Equal(t, "ok", w.Body.String())
}
