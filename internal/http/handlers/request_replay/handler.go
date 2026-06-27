// Package request_replay implements POST /api/session/{session_uuid}/requests/{request_uuid}/replay:
// it re-sends a previously captured request to a target URL using the original
// method, body and headers (hop-by-hop headers excluded).
//
// SSRF hardening is intentionally OUT OF SCOPE. This is an operator-facing
// debugging tool: the target URL is supplied by a trusted user, so the
// destination is not validated against an allow/deny-list and no internal-network
// protection is applied. Do not expose this endpoint to untrusted callers.
package request_replay

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/shared"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/openapi"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage"
)

const (
	// replayTimeout bounds the whole downstream round-trip.
	replayTimeout = 10 * time.Second
	// maxResponseBodyBytes caps how much of the downstream response we read back.
	maxResponseBodyBytes = 64 * 1024 // 64 KiB
)

// hopByHopHeaders are connection-scoped headers that must not be forwarded when
// replaying a request (RFC 7230 §6.1), plus Host and Content-Length which the
// net/http client sets itself. Any header with a "Proxy-" prefix is also dropped.
var hopByHopHeaders = map[string]struct{}{ //nolint:gochecknoglobals // static lookup table
	"connection":          {},
	"keep-alive":          {},
	"te":                  {},
	"trailer":             {},
	"transfer-encoding":   {},
	"upgrade":             {},
	"host":                {},
	"content-length":      {},
	"proxy-authenticate":  {},
	"proxy-authorization": {},
}

type (
	sID = openapi.SessionUUIDInPath
	rID = openapi.RequestUUIDInPath

	Handler struct {
		db     storage.Storage
		client *http.Client
	}
)

func New(db storage.Storage) *Handler {
	return &Handler{
		db: db,
		client: &http.Client{
			Timeout: replayTimeout,
			// Never follow redirects: return the 3xx response as-is so the caller
			// sees the actual server reply (correct replay behavior) and a
			// redirected URL cannot bounce the request to an internal host (SSRF).
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (h *Handler) Handle(
	ctx context.Context,
	ref sID,
	reqID rID,
	opts *openapi.ReplayRequest,
) (*openapi.ReplayResponse, error) {
	sess, err := shared.ResolveSession(ctx, h.db, ref)
	if err != nil {
		return nil, err // storage.ErrNotFound ⇒ 404
	}

	captured, err := h.db.GetRequest(ctx, sess.ID, reqID.String())
	if err != nil {
		return nil, err // storage.ErrNotFound ⇒ 404
	}

	target := sess.ForwardURL
	if opts != nil && opts.TargetUrl != "" {
		target = opts.TargetUrl
	}

	if target == "" {
		return nil, fmt.Errorf("%w: no target_url provided and the session has no forward URL", shared.ErrBadRequest)
	}

	method := captured.Method
	if method == "" {
		method = http.MethodGet
	}

	outReq, err := http.NewRequestWithContext(ctx, strings.ToUpper(method), target, bytes.NewReader(captured.Body))
	if err != nil {
		return nil, fmt.Errorf("%w: invalid target URL %q: %w", shared.ErrBadRequest, target, err)
	}

	for _, hdr := range captured.Headers {
		if isHopByHop(hdr.Name) {
			continue
		}

		outReq.Header.Add(hdr.Name, hdr.Value)
	}

	resp, err := h.client.Do(outReq)
	if err != nil {
		return nil, fmt.Errorf("replay request to %q failed: %w", target, err)
	}

	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))

	return &openapi.ReplayResponse{
		StatusCode: resp.StatusCode,
		BodyBase64: base64.StdEncoding.EncodeToString(body),
		Headers:    flattenHeaders(resp.Header),
	}, nil
}

// isHopByHop reports whether a header must not be forwarded to the target.
func isHopByHop(name string) bool {
	lower := strings.ToLower(name)

	if strings.HasPrefix(lower, "proxy-") {
		return true
	}

	_, ok := hopByHopHeaders[lower]

	return ok
}

// flattenHeaders converts an http.Header (name → values) into the flat
// name/value pairs of the OpenAPI response shape.
func flattenHeaders(in http.Header) []openapi.HttpHeader {
	out := make([]openapi.HttpHeader, 0, len(in))

	for name, values := range in {
		for _, v := range values {
			out = append(out, openapi.HttpHeader{Name: name, Value: v})
		}
	}

	return out
}
