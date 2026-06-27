// Package shared holds small helpers reused across the API handlers: resolving
// a session reference (slug or UUID), mapping a storage Session to its OpenAPI
// response shape, header conversion, slug validation, and sentinel errors that
// the OpenAPI dispatch layer maps to HTTP status codes.
package shared

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"

	"github.com/google/uuid"

	"gh.tarampamp.am/webhook-tester/v2/internal/http/openapi"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage"
)

// Sentinel errors returned by handlers. The OpenAPI dispatch layer inspects
// these with errors.Is to choose the HTTP status code (storage.ErrNotFound →
// 404; ErrConflict → 409; ErrBadRequest → 400; anything else → 500).
var (
	// ErrBadRequest signals a client-side validation failure (HTTP 400).
	ErrBadRequest = errors.New("bad request")
	// ErrConflict signals a uniqueness conflict, e.g. a duplicate slug (HTTP 409).
	ErrConflict = errors.New("conflict")
)

// SlugPattern is the contract every session slug must satisfy: a lower-case,
// hyphen-friendly identifier of 2..49 characters.
var SlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,48}$`)

// ValidateSlug returns an ErrBadRequest-wrapped error if s is not a valid slug.
func ValidateSlug(s string) error {
	if !SlugPattern.MatchString(s) {
		return fmt.Errorf("%w: invalid slug %q (must match %s)", ErrBadRequest, s, SlugPattern.String())
	}

	return nil
}

// ValidateInboundAuth enforces that a non-empty inbound_auth_header is accompanied by a
// non-empty inbound_auth_value. An empty value with a configured header would otherwise be a
// foot-gun (the capture middleware fails closed and rejects every request), so it is refused
// at the API boundary with ErrBadRequest. Both nil/empty (inbound auth disabled) is valid, and
// supplying only a value with no header is harmless (auth stays disabled).
func ValidateInboundAuth(header, value *string) error {
	if header == nil || *header == "" {
		return nil
	}

	if value == nil || *value == "" {
		return fmt.Errorf("%w: inbound_auth_value required when inbound_auth_header is set", ErrBadRequest)
	}

	return nil
}

// ResolveSession resolves a slug-or-UUID reference to a session. It tries the
// slug index first, then falls back to a UUID lookup. The returned Session has
// its output-only ID field populated. If neither lookup succeeds, the UUID
// lookup's error (typically storage.ErrSessionNotFound) is returned.
func ResolveSession(ctx context.Context, db storage.Storage, ref string) (*storage.Session, error) {
	if sess, err := db.GetSessionBySlug(ctx, ref); err == nil {
		return sess, nil
	}

	sess, err := db.GetSession(ctx, ref)
	if err != nil {
		return nil, err
	}

	return sess, nil
}

// ToOpenAPIHeaders converts storage headers to their OpenAPI representation.
func ToOpenAPIHeaders(in []storage.HttpHeader) []openapi.HttpHeader {
	out := make([]openapi.HttpHeader, len(in))
	for i, h := range in {
		out[i] = openapi.HttpHeader{Name: h.Name, Value: h.Value}
	}

	return out
}

// ToStorageHeaders converts OpenAPI headers to their storage representation.
func ToStorageHeaders(in []openapi.HttpHeader) []storage.HttpHeader {
	out := make([]storage.HttpHeader, len(in))
	for i, h := range in {
		out[i] = storage.HttpHeader{Name: h.Name, Value: h.Value}
	}

	return out
}

// SessionResponse maps a storage Session to the OpenAPI SessionOptionsResponse,
// populating the optional fields (slug, group, script, security headers,
// forward URL, long-lived, expiry) only when they carry a value.
func SessionResponse(sess *storage.Session) openapi.SessionOptionsResponse {
	sUUID, _ := uuid.Parse(sess.ID) // ID is a UUID for real sessions; zero value otherwise

	resp := openapi.SessionOptionsResponse{
		Uuid:               sUUID,
		CreatedAtUnixMilli: sess.CreatedAtUnixMilli,
		Response: openapi.SessionResponseOptions{
			Delay:              uint16(sess.Delay.Seconds()),
			Headers:            ToOpenAPIHeaders(sess.Headers),
			ResponseBodyBase64: base64.StdEncoding.EncodeToString(sess.ResponseBody),
			StatusCode:         openapi.StatusCode(sess.Code),
		},
	}

	if !sess.ExpiresAt.IsZero() {
		ms := sess.ExpiresAt.UnixMilli()
		resp.ExpiresAtUnixMilli = &ms
	}

	if sess.Slug != "" {
		slug := sess.Slug
		resp.Response.Slug = &slug
	}

	if sess.GroupName != "" {
		group := sess.GroupName
		resp.Response.Group = &group
	}

	if sess.ResponseScript != "" {
		script := sess.ResponseScript
		resp.Response.ResponseScript = &script
	}

	if sess.ForwardURL != "" {
		fwd := sess.ForwardURL
		resp.Response.ForwardUrl = &fwd
	}

	if len(sess.SecurityHeaders) > 0 {
		sh := ToOpenAPIHeaders(sess.SecurityHeaders)
		resp.Response.SecurityHeaders = &sh
	}

	longLived := sess.LongLived
	resp.Response.LongLived = &longLived

	// inbound auth is exposed only when configured (empty header = disabled). The value is a
	// secret, but the dashboard API is auth-gated, so returning it is acceptable (consistent
	// with response_script). It is never logged.
	if sess.InboundAuthHeader != "" {
		h := sess.InboundAuthHeader
		resp.Response.InboundAuthHeader = &h
	}

	if sess.InboundAuthValue != "" {
		v := sess.InboundAuthValue
		resp.Response.InboundAuthValue = &v
	}

	return resp
}
