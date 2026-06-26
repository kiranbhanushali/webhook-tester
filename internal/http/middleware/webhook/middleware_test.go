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
