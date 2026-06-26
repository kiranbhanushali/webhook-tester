package auth_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"gh.tarampamp.am/webhook-tester/v2/internal/http/middleware/auth"
)

// okHandler always returns 200 OK so we can assert pass-through.
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { //nolint:gochecknoglobals
	w.WriteHeader(http.StatusOK)
})

func middleware(token string) func(http.Handler) http.Handler {
	return auth.New(token, zap.NewNop())
}

// -----------------------------------------------------------------------
// Token unset (empty string) — everything passes through
// -----------------------------------------------------------------------

func TestNoToken_AllRequestsPassThrough(t *testing.T) {
	t.Parallel()

	mw := middleware("")
	handler := mw(okHandler)

	paths := []string{"/api/sessions", "/api/whatever", "/healthz", "/ready", "/", "/some-uuid-slug/foo"}
	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequest(http.MethodGet, p, nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, r)

			assert.Equal(t, http.StatusOK, w.Code, "path %s should pass through when token is empty", p)
		})
	}
}

func TestNoToken_LoginEndpointReturns200(t *testing.T) {
	t.Parallel()

	mw := middleware("")
	handler := mw(okHandler)

	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"token":"anything"}`))
	r.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"ok":true`)
}

// -----------------------------------------------------------------------
// Token set — Bearer header
// -----------------------------------------------------------------------

func TestTokenSet_CorrectBearer_Passes(t *testing.T) {
	t.Parallel()

	mw := middleware("secret123")
	handler := mw(okHandler)

	r := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	r.Header.Set("Authorization", "Bearer secret123")

	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestTokenSet_WrongBearer_Returns401(t *testing.T) {
	t.Parallel()

	mw := middleware("secret123")
	handler := mw(okHandler)

	r := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	r.Header.Set("Authorization", "Bearer wrongtoken")

	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")
}

func TestTokenSet_MissingAuth_Returns401(t *testing.T) {
	t.Parallel()

	mw := middleware("secret123")
	handler := mw(okHandler)

	r := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	// no Authorization header, no cookie

	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// -----------------------------------------------------------------------
// Token set — cookie
// -----------------------------------------------------------------------

func TestTokenSet_CorrectCookie_Passes(t *testing.T) {
	t.Parallel()

	mw := middleware("secret123")
	handler := mw(okHandler)

	r := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	r.AddCookie(&http.Cookie{Name: "wh_token", Value: "secret123"}) //nolint:gosec

	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestTokenSet_WrongCookie_Returns401(t *testing.T) {
	t.Parallel()

	mw := middleware("secret123")
	handler := mw(okHandler)

	r := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	r.AddCookie(&http.Cookie{Name: "wh_token", Value: "badvalue"}) //nolint:gosec

	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// -----------------------------------------------------------------------
// Login endpoint behavior
// -----------------------------------------------------------------------

func TestLogin_CorrectToken_SetsCookieAnd200(t *testing.T) {
	t.Parallel()

	mw := middleware("secret123")
	handler := mw(okHandler)

	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"token":"secret123"}`))
	r.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"ok":true`)

	// Check that wh_token cookie is set.
	var found bool

	for _, c := range w.Result().Cookies() {
		if c.Name == "wh_token" {
			assert.Equal(t, "secret123", c.Value)
			assert.True(t, c.HttpOnly, "cookie should be HttpOnly")

			found = true
		}
	}

	assert.True(t, found, "wh_token cookie should be present in response")
}

func TestLogin_WrongToken_Returns401(t *testing.T) {
	t.Parallel()

	mw := middleware("secret123")
	handler := mw(okHandler)

	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"token":"wrong"}`))
	r.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestLogin_EmptyBody_Returns401(t *testing.T) {
	t.Parallel()

	mw := middleware("secret123")
	handler := mw(okHandler)

	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestLogin_MalformedBody_Returns401NoPanic(t *testing.T) {
	t.Parallel()

	mw := middleware("secret123")
	handler := mw(okHandler)

	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`notjson`))
	r.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()

	// Must not panic on malformed JSON.
	assert.NotPanics(t, func() { handler.ServeHTTP(w, r) })
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// -----------------------------------------------------------------------
// Logout endpoint
// -----------------------------------------------------------------------

func TestLogout_ClearsCookie(t *testing.T) {
	t.Parallel()

	mw := middleware("secret123")
	handler := mw(okHandler)

	r := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	r.AddCookie(&http.Cookie{Name: "wh_token", Value: "secret123"}) //nolint:gosec

	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)

	// wh_token cookie should be cleared (MaxAge=-1 or Expires in the past).
	var found bool

	for _, c := range w.Result().Cookies() {
		if c.Name == "wh_token" {
			assert.True(t, c.MaxAge < 0 || c.Value == "", "cookie should be cleared")

			found = true
		}
	}

	assert.True(t, found, "cleared wh_token cookie should be present in Set-Cookie header")
}

// -----------------------------------------------------------------------
// Bypass paths — always pass through regardless of auth
// -----------------------------------------------------------------------

func TestBypass_HealthzPassesThrough(t *testing.T) {
	t.Parallel()

	mw := middleware("secret123")
	handler := mw(okHandler)

	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	// No auth at all

	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestBypass_ReadyPassesThrough(t *testing.T) {
	t.Parallel()

	mw := middleware("secret123")
	handler := mw(okHandler)

	r := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestBypass_WebhookSlugPathPassesThrough(t *testing.T) {
	t.Parallel()

	mw := middleware("secret123")
	handler := mw(okHandler)

	// A UUID-style slug path — must never be gated.
	r := httptest.NewRequest(http.MethodPost, "/550e8400-e29b-41d4-a716-446655440000/foo", nil)
	// No auth at all

	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestBypass_SPAShellPassesThrough(t *testing.T) {
	t.Parallel()

	mw := middleware("secret123")
	handler := mw(okHandler)

	// Root SPA path — must not be gated (SPA shell loads before login).
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
}

// -----------------------------------------------------------------------
// Path normalization — the gate must not be bypassable via uncleaned paths.
// Go's server does NOT clean r.URL.Path before the handler chain, so a request
// to //api/sessions arrives with a raw path that does not literally start with
// "/api/". The middleware must normalize before its prefix/exact checks.
// -----------------------------------------------------------------------

func TestNormalize_DoubleSlashAPI_IsGated(t *testing.T) {
	t.Parallel()

	mw := middleware("secret123")
	handler := mw(okHandler)

	// Raw, uncleaned path that would skip a naive HasPrefix("/api/") check.
	r := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	r.URL.Path = "//api/sessions" // force the uncleaned path the net/http server would pass through
	// No credentials

	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusUnauthorized, w.Code, "//api/sessions must be gated, not bypassed")
}

func TestNormalize_DoubleSlashLogin_Works(t *testing.T) {
	t.Parallel()

	mw := middleware("secret123")
	handler := mw(okHandler)

	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"token":"secret123"}`))
	r.URL.Path = "//api/auth/login" // uncleaned path
	r.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code, "//api/auth/login must be recognized as the login endpoint")
	assert.Contains(t, w.Body.String(), `"ok":true`)
}

// -----------------------------------------------------------------------
// Constant-time compare — verify wrong-prefix tokens all return 401.
// -----------------------------------------------------------------------

func TestConstantTimeCompare_VaryingPrefixLengths(t *testing.T) {
	t.Parallel()

	token := "supersecrettoken"
	mw := middleware(token)
	handler := mw(okHandler)

	// These should all be 401 regardless of how much they share with the token.
	badTokens := []string{"s", "super", "supersecre", "supersecrettoken_extra", ""}
	for _, bt := range badTokens {
		t.Run("wrong_bearer_"+bt, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequest(http.MethodGet, "/api/x", nil)
			r.Header.Set("Authorization", "Bearer "+bt)

			w := httptest.NewRecorder()

			handler.ServeHTTP(w, r)

			assert.Equal(t, http.StatusUnauthorized, w.Code)
		})
	}
}
