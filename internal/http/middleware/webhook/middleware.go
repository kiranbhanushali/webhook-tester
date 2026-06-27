package webhook

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"gh.tarampamp.am/webhook-tester/v2/internal/config"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/openapi"
	"gh.tarampamp.am/webhook-tester/v2/internal/identifiers"
	"gh.tarampamp.am/webhook-tester/v2/internal/pubsub"
	"gh.tarampamp.am/webhook-tester/v2/internal/response"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage/hotindex"
)

// webhookPathPrefix is the reserved URL prefix for webhook capture. Only requests
// whose path begins with this prefix are considered for capture; everything else
// (SPA, /api/*, /healthz, /ready) is passed through untouched.
const webhookPathPrefix = "/w/"

// slugPattern validates a human-readable session slug (mirrors the storage/OpenAPI regex).
var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,48}$`)

func New( //nolint:funlen,gocognit,gocyclo
	appCtx context.Context,
	log *zap.Logger,
	db storage.Storage,
	pub pubsub.Publisher[pubsub.RequestEvent],
	cfg *config.AppSettings,
	extractor *identifiers.Extractor,
	hotIndex *hotindex.HotIndex,
	responseScriptTimeout time.Duration,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var ref, doIt = shouldCaptureRequest(r)
			if !doIt {
				next.ServeHTTP(w, r)

				return
			}

			var (
				reqCtx = r.Context()
				isUUID = openapi.IsValidUUID(ref)
			)

			// resolve the session: by slug first, then by id (UUID back-compat)
			sess, sErr := db.GetSessionBySlug(reqCtx, ref) //nolint:contextcheck
			if sErr != nil {
				sess, sErr = db.GetSession(reqCtx, ref) //nolint:contextcheck
			}

			if sErr != nil { //nolint:nestif
				// if the session is not found
				if errors.Is(sErr, storage.ErrNotFound) {
					// auto-creation is supported ONLY for valid UUIDs (never for arbitrary slugs)
					if isUUID && cfg.AutoCreateSessions {
						// create a new session with some default values
						if _, err := db.NewSession(reqCtx, storage.Session{ //nolint:contextcheck
							Code: http.StatusOK,
						}, ref); err != nil {
							respondWithError(w, log, http.StatusInternalServerError, err.Error())

							return
						}

						// and try to get it again
						if sess, sErr = db.GetSession(reqCtx, ref); sErr != nil { //nolint:contextcheck
							respondWithError(w, log, http.StatusInternalServerError, sErr.Error())

							return
						}

						// add the header to indicate that the session has been created automatically
						w.Header().Set("X-Wh-Created-Automatically", "1")
					} else {
						respondWithError(w, log, http.StatusNotFound, "The webhook has not been created yet or may have expired")

						return
					}
				} else {
					respondWithError(w, log, http.StatusInternalServerError, sErr.Error())

					return
				}
			}

			// the resolved session always carries its real ID (UUID), populated by the storage driver
			var sID = sess.ID

			{ // increase the session lifetime
				var delta = time.Now().Add(cfg.SessionTTL).Sub(time.Unix(0, sess.CreatedAtUnixMilli*int64(time.Millisecond)))

				if err := db.AddSessionTTL(reqCtx, sID, delta); err != nil { //nolint:contextcheck
					respondWithError(w, log, http.StatusInternalServerError, err.Error())

					return
				}
			}

			// read the request body
			var body []byte

			if r.Body != nil {
				if b, err := io.ReadAll(r.Body); err == nil {
					body = b
				}
			}

			// check the request body size and respond with an error if it's too large
			if cfg.MaxRequestBodySize > 0 && uint32(len(body)) > cfg.MaxRequestBodySize { //nolint:gosec
				respondWithError(w, log,
					http.StatusRequestEntityTooLarge,
					fmt.Sprintf("The request body is too large (current: %d, max: %d)", len(body), cfg.MaxRequestBodySize),
				)

				return
			}

			// convert request headers into the storage format
			var rHeaders = make([]storage.HttpHeader, 0, len(r.Header))
			for name, value := range r.Header {
				rHeaders = append(rHeaders, storage.HttpHeader{Name: name, Value: strings.Join(value, "; ")})
			}

			// sort headers by name
			slices.SortFunc(rHeaders, func(i, j storage.HttpHeader) int { return strings.Compare(i.Name, j.Name) })

			var fullURL = extractFullUrl(r)

			// evaluate inbound auth BEFORE capture so the stored request carries the flag. A
			// request that fails inbound auth is still captured fully (below); it is just flagged
			// Authorized=false and answered with a 401 (the response script is skipped).
			var authorized = inboundAuthorized(r, sess)

			// and save the request to the storage
			rID, rErr := db.NewRequest(reqCtx, sID, storage.Request{ //nolint:contextcheck
				ClientAddr: extractRealIP(r),
				Method:     r.Method,
				Body:       body,
				Headers:    rHeaders,
				URL:        fullURL,
				Authorized: authorized,
			})
			if rErr != nil {
				respondWithError(w, log, http.StatusInternalServerError, rErr.Error())

				return
			}

			w.Header().Set("X-Wh-Request-Id", rID)

			var now = time.Now()

			// feed captured identifiers into the in-memory hot index (best-effort). The durable
			// index is written separately by the storage driver's own injected extractor (Task 10);
			// this double extraction is intentional and keeps the two indexes consistent.
			if extractor != nil && hotIndex != nil {
				for _, id := range extractor.Extract(body, rHeaders, fullURL) {
					hotIndex.Add(id.Key, id.Value, hotindex.Ref{
						SessionID:           sID,
						SessionSlug:         sess.Slug,
						RequestID:           rID,
						CapturedAtUnixMilli: now.UnixMilli(),
					})
				}
			}

			// publish the captured request to the pub/sub. important note - we should use the app ctx instead of the req ctx
			// because the request context can be canceled before the goroutine finishes (and moreover - before the
			// subscribers will receive the event - in this case the event will be lost)
			go func() {
				// read the actual data from the storage (the main point is the time of creation)
				captured, dbErr := db.GetRequest(appCtx, sID, rID)
				if dbErr != nil {
					log.Error("failed to get a captured request", zap.Error(dbErr))

					return
				}

				var headers = make([]pubsub.HttpHeader, len(captured.Headers))
				for i, h := range captured.Headers {
					headers[i] = pubsub.HttpHeader{Name: h.Name, Value: h.Value}
				}

				if err := pub.Publish(appCtx, sID, pubsub.RequestEvent{
					Action: pubsub.RequestActionCreate,
					Request: &pubsub.Request{
						ID:                 rID,
						ClientAddr:         captured.ClientAddr,
						Method:             captured.Method,
						Headers:            headers,
						URL:                captured.URL,
						CreatedAtUnixMilli: captured.CreatedAtUnixMilli,
						// carry the inbound-auth flag on the per-session event too, so the live UI can flag
						// a rejected (401) capture (the Unauthorized badge) without re-fetching the request.
						Authorized: captured.Authorized,
					},
				}); err != nil {
					log.Error("failed to publish a captured request", zap.Error(err))
				}

				// ALSO publish to the global firehose topic so a single cross-session subscriber sees
				// every capture. The firehose event additionally carries the session slug+uuid and the
				// authorized flag (the per-session event above is intentionally left unchanged). Only the
				// "create" action is published cross-session; per-session delete/clear stay session-scoped.
				if err := pub.Publish(appCtx, pubsub.FirehoseTopic, pubsub.RequestEvent{
					Action:      pubsub.RequestActionCreate,
					SessionUUID: sID,
					SessionSlug: sess.Slug,
					Request: &pubsub.Request{
						ID:         rID,
						ClientAddr: captured.ClientAddr,
						Method:     captured.Method,
						// Headers are intentionally omitted: convertEvent never puts them on the
						// FirehoseEventRequest wire, so carrying them here would only serialize and ship
						// full request headers (incl. any inbound-auth secret) over redis to be discarded.
						URL:                captured.URL,
						CreatedAtUnixMilli: captured.CreatedAtUnixMilli,
						Authorized:         captured.Authorized,
					},
				}); err != nil {
					log.Error("failed to publish a captured request to the firehose", zap.Error(err))
				}
			}()

			// inbound auth failed: the request was captured above (flagged Authorized=false); answer
			// with a 401 and skip the response script / static response (and the configured delay).
			// Security headers are still applied, consistent with the success paths.
			if !authorized {
				for _, h := range sess.SecurityHeaders {
					w.Header().Set(h.Name, h.Value)
				}

				respondUnauthorized(w, log)

				return
			}

			// wait for the delay if it's set
			if sess.Delay > 0 {
				sleep(reqCtx, sess.Delay) //nolint:contextcheck
			}

			// set the header to allow CORS requests from any origin and method
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "*")
			w.Header().Set("Access-Control-Allow-Headers", "*")

			// set the session (custom) headers
			for _, h := range sess.Headers {
				w.Header().Set(h.Name, h.Value)
			}

			// security headers are applied on EVERY response path (script or static)
			for _, h := range sess.SecurityHeaders {
				w.Header().Set(h.Name, h.Value)
			}

			// resolve the static response: the session default status code, optionally overridden by a
			// numeric segment in the URL path AFTER /w/{ref}/...
			var (
				statusCode   = statusFromPath(r.URL.Path, int(sess.Code))
				responseBody = sess.ResponseBody
			)

			// if a response script is configured, try to render it; on ANY error fall back to the static response
			if sess.ResponseScript != "" {
				res, err := response.Render( //nolint:contextcheck
					reqCtx, sess.ResponseScript, buildScriptRequest(r, sess.Slug, body, now), responseScriptTimeout,
				)
				if err != nil {
					log.Warn("response script failed; falling back to the static response", zap.Error(err))
				} else {
					if res.Status != 0 { // a non-zero @status directive takes precedence
						statusCode = res.Status
					}

					responseBody = res.Body
				}
			}

			// set the status code
			w.WriteHeader(statusCode)

			// write the response body
			if _, err := w.Write(responseBody); err != nil {
				log.Error("failed to write the response body", zap.Error(err))
			}
		})
	}
}

// shouldCaptureRequest reports whether the request targets the webhook-capture prefix
// (/w/) and, if so, returns the session reference (the first path segment after /w/).
// The reference is accepted when it is a valid slug or a valid UUID.
func shouldCaptureRequest(r *http.Request) (string, bool) {
	if r.URL == nil {
		return "", false
	}

	if !strings.HasPrefix(r.URL.Path, webhookPathPrefix) {
		return "", false
	}

	// the session reference is the segment between the /w/ prefix and the next slash
	var ref = r.URL.Path[len(webhookPathPrefix):]
	if i := strings.IndexByte(ref, '/'); i >= 0 {
		ref = ref[:i]
	}

	if ref == "" {
		return "", false
	}

	if openapi.IsValidUUID(ref) || slugPattern.MatchString(ref) {
		return ref, true
	}

	return "", false
}

// statusFromPath returns the HTTP status code requested via the URL path. Only segments
// AFTER /w/{ref}/ are scanned (so a numeric slug is never mistaken for a status code); the
// last valid numeric segment (100–599) wins. When none is found, def is returned.
func statusFromPath(path string, def int) int {
	var rest = strings.TrimPrefix(path, webhookPathPrefix)

	// drop the {ref} segment; only the tail after it may carry a status override
	i := strings.IndexByte(rest, '/')
	if i < 0 {
		return def
	}

	var statusCode = def

	for _, part := range strings.Split(strings.Trim(rest[i+1:], "/"), "/") {
		if code, err := strconv.Atoi(part); err == nil && code >= 100 && code <= 599 {
			statusCode = code // last numeric segment wins
		}
	}

	return statusCode
}

// buildScriptRequest converts the incoming HTTP request into the response-engine input.
func buildScriptRequest(r *http.Request, slug string, body []byte, now time.Time) response.Request {
	var query = make(map[string]string, len(r.URL.Query()))
	for k, v := range r.URL.Query() {
		if len(v) > 0 {
			query[k] = v[0]
		}
	}

	var header = make(map[string]string, len(r.Header))
	for k, v := range r.Header {
		if len(v) > 0 {
			header[http.CanonicalHeaderKey(k)] = v[0]
		}
	}

	return response.Request{
		Method: r.Method,
		Path:   r.URL.Path,
		Slug:   slug,
		Body:   string(body),
		Query:  query,
		Header: header,
		Now:    now,
	}
}

// TODO: add supporting of format requested by the user (json, html, plain text, etc).
func respondWithError(w http.ResponseWriter, log *zap.Logger, code int, msg string) {
	var s strings.Builder

	s.Grow(1024) //nolint:mnd

	s.WriteString(`<!doctype html>
<html lang="en">
<head>
    <meta charset="utf-8"/>
    <meta http-equiv="X-UA-Compatible" content="IE=edge"/>
    <meta name="viewport" content="width=device-width, initial-scale=1"/>
    <title>`)
	s.WriteString(http.StatusText(code))
	s.WriteString(`</title>
    <style>
        html,body {width:100%; height:100%; margin:0; padding:0; background-color: #2b2b2b; color: #efeffa}
        body {display:flex; justify-content:center; align-items:center; font-family:sans-serif}
        .container {text-align:center}
    </style>
</head>
<body>
    <div class="container">
        <h1>WebHook: `)
	s.WriteString(http.StatusText(code))
	s.WriteString(`</h1>
        <h3>`)
	s.WriteString(msg)
	s.WriteString(`</h3>
    </div>
</body>
</html>`)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(s.Len()))
	w.WriteHeader(code)

	if _, err := w.Write([]byte(s.String())); err != nil {
		log.Error("failed to respond with an error", zap.Error(err), zap.Int("code", code), zap.String("msg", msg))
	}
}

// inboundAuthorized reports whether the incoming webhook request satisfies the session's
// inbound-auth configuration. An empty InboundAuthHeader means inbound auth is disabled (the
// endpoint is public), so the request is always authorized. Otherwise the incoming header value
// (looked up case-insensitively via http.Header.Get) must equal the configured value; the
// comparison uses crypto/subtle.ConstantTimeCompare to avoid leaking the secret via timing.
//
// It FAILS CLOSED: when a header is configured, a missing/empty incoming value never authorizes.
// This also guards a misconfigured session (header set, value empty) — without the guard a
// header-less request would match ConstantTimeCompare("","")==1 and silently bypass auth; here
// such a session rejects every request instead (the API also refuses to create that config).
func inboundAuthorized(r *http.Request, sess *storage.Session) bool {
	if sess.InboundAuthHeader == "" {
		return true
	}

	var got = r.Header.Get(sess.InboundAuthHeader)
	if got == "" {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(got), []byte(sess.InboundAuthValue)) == 1
}

// respondUnauthorized writes the small JSON 401 used when an inbound-auth-protected webhook is
// posted without a valid token. The request has already been captured (flagged Authorized=false);
// the response script and static response are intentionally skipped.
func respondUnauthorized(w http.ResponseWriter, log *zap.Logger) {
	const body = `{"error":"unauthorized webhook"}`

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusUnauthorized)

	if _, err := w.Write([]byte(body)); err != nil {
		log.Error("failed to write the unauthorized webhook response", zap.Error(err))
	}
}

// extractFullUrl returns the full URL from the request.
func extractFullUrl(r *http.Request) string {
	var scheme = "http"
	if r.TLS != nil {
		scheme = "https"
	}

	return fmt.Sprintf("%s://%s%s", scheme, r.Host, r.RequestURI)
}

// we will trust following HTTP headers for the real ip extracting (priority low -> high).
var trustHeaders = [...]string{"X-Forwarded-For", "X-Real-IP", "CF-Connecting-IP"} //nolint:gochecknoglobals

func extractRealIP(r *http.Request) string {
	var ip string

	for _, name := range trustHeaders {
		if value := r.Header.Get(name); value != "" {
			// `X-Forwarded-For` can be `10.0.0.1, 10.0.0.2, 10.0.0.3`
			if strings.Contains(value, ",") {
				parts := strings.Split(value, ",")

				if len(parts) > 0 {
					ip = strings.TrimSpace(parts[0])
				}
			} else {
				ip = strings.TrimSpace(value)
			}
		}
	}

	if net.ParseIP(ip) != nil {
		return ip
	}

	return strings.Split(r.RemoteAddr, ":")[0]
}

func sleep(ctx context.Context, d time.Duration) {
	var timer = time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
