package auth

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"path"
	"strings"

	"go.uber.org/zap"
)

const cookieName = "wh_token"

// New returns an auth middleware that gates every path under /api/* behind a shared token.
//
// When token is "" the middleware is fully pass-through; POST /api/auth/login still returns
// 200 {"ok":true} so the frontend can detect that auth is disabled.
//
// Accepted credentials (constant-time compared):
//   - Authorization: Bearer <token>  header
//   - wh_token=<token>               HttpOnly cookie (for browser WebSocket / cookie-based flows)
//
// Paths that are NEVER gated (always pass to next):
//   - anything that does NOT start with /api/  (SPA shell, /healthz, /ready, /{slug}/…)
//
// Paths handled directly by the middleware (not forwarded to next):
//   - POST /api/auth/login   — validates body {"token":"…"}, sets cookie, 200/401
//   - GET|POST /api/auth/logout — clears cookie, 200
func New(token string, log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Normalize the path before any prefix/exact check. Go's HTTP server does NOT clean
			// r.URL.Path before the handler chain, so a request to e.g. "//api/sessions" or
			// "/api/../api/x" would otherwise skip the gate. The security property must not depend
			// on the router. path.Clean("//api/sessions") -> "/api/sessions"; path.Clean("/") -> "/".
			cleaned := path.Clean(r.URL.Path)

			// Login: always handled here (even when auth disabled so we can return {"ok":true})
			if cleaned == "/api/auth/login" && r.Method == http.MethodPost {
				handleLogin(w, r, token, log)

				return
			}

			// Logout: always handled here; no auth required to log out
			if cleaned == "/api/auth/logout" && (r.Method == http.MethodGet || r.Method == http.MethodPost) {
				handleLogout(w)

				return
			}

			// Only gate /api/* paths — everything else (SPA, health, webhooks) passes through
			if !strings.HasPrefix(cleaned, "/api/") {
				next.ServeHTTP(w, r)

				return
			}

			// Auth disabled — pass through
			if token == "" {
				next.ServeHTTP(w, r)

				return
			}

			// Check credentials
			if isAuthorized(r, token) {
				next.ServeHTTP(w, r)

				return
			}

			respondUnauthorized(w, log)
		})
	}
}

// handleLogin validates the JSON body {"token":"…"} and, on success, sets the wh_token cookie.
func handleLogin(w http.ResponseWriter, r *http.Request, configuredToken string, log *zap.Logger) {
	// Auth disabled
	if configuredToken == "" {
		respondOK(w)

		return
	}

	var body struct {
		Token string `json:"token"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondUnauthorized(w, log)

		return
	}

	if subtle.ConstantTimeCompare([]byte(body.Token), []byte(configuredToken)) != 1 {
		respondUnauthorized(w, log)

		return
	}

	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure flag not forced: tool may run over plain HTTP in local dev
		Name:     cookieName,
		Value:    configuredToken,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	respondOK(w)
}

// handleLogout clears the wh_token cookie.
func handleLogout(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure flag not forced: tool may run over plain HTTP in local dev
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})

	respondOK(w)
}

// isAuthorized returns true if the request carries a valid Bearer token or a valid wh_token cookie.
// All comparisons are constant-time to prevent timing attacks.
func isAuthorized(r *http.Request, token string) bool {
	if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
		given := strings.TrimPrefix(authHeader, "Bearer ")
		if subtle.ConstantTimeCompare([]byte(given), []byte(token)) == 1 {
			return true
		}
	}

	if c, err := r.Cookie(cookieName); err == nil {
		if subtle.ConstantTimeCompare([]byte(c.Value), []byte(token)) == 1 {
			return true
		}
	}

	return false
}

func respondOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func respondUnauthorized(w http.ResponseWriter, _ *zap.Logger) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
}
