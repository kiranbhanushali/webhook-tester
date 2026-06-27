// Package session_update implements PATCH /api/session/{session_uuid}: a partial
// update of a session's response options. The path reference may be a slug or a
// UUID; only the fields present in the request body are changed.
package session_update

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/shared"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/openapi"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage"
)

type (
	sID = openapi.SessionUUIDInPath

	Handler struct{ db storage.Storage }
)

func New(db storage.Storage) *Handler { return &Handler{db: db} }

func (h *Handler) Handle(
	ctx context.Context,
	ref sID,
	p openapi.UpdateSessionRequest,
) (*openapi.SessionOptionsResponse, error) {
	sess, err := shared.ResolveSession(ctx, h.db, ref)
	if err != nil {
		return nil, err // storage.ErrNotFound ⇒ 404 at the dispatch layer
	}

	patch, err := buildPatch(p)
	if err != nil {
		return nil, err
	}

	// A changed slug must not collide with a different session.
	if patch.Slug != nil && *patch.Slug != sess.Slug {
		if existing, gErr := h.db.GetSessionBySlug(ctx, *patch.Slug); gErr == nil && existing.ID != sess.ID {
			return nil, fmt.Errorf("%w: slug %q is already in use", shared.ErrConflict, *patch.Slug)
		}
	}

	if uErr := h.db.UpdateSession(ctx, sess.ID, patch); uErr != nil {
		return nil, fmt.Errorf("failed to update session: %w", uErr)
	}

	updated, err := h.db.GetSession(ctx, sess.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to reload session: %w", err)
	}

	resp := shared.SessionResponse(updated)

	return &resp, nil
}

// buildPatch maps the OpenAPI request to a storage.SessionPatch, setting only
// the fields the client provided. It validates a supplied slug and decodes the
// response body, returning shared.ErrBadRequest on malformed input.
//
//nolint:funlen // a flat field-by-field builder; its length scales with the number of session fields
func buildPatch(p openapi.UpdateSessionRequest) (storage.SessionPatch, error) {
	var patch storage.SessionPatch

	if p.Slug != nil {
		if err := shared.ValidateSlug(*p.Slug); err != nil {
			return patch, err
		}

		s := *p.Slug
		patch.Slug = &s
	}

	if p.Group != nil {
		g := *p.Group
		patch.GroupName = &g
	}

	if p.ResponseScript != nil {
		s := *p.ResponseScript
		patch.ResponseScript = &s
	}

	if p.ForwardUrl != nil {
		f := *p.ForwardUrl
		patch.ForwardURL = &f
	}

	if p.StatusCode != nil {
		c := uint16(*p.StatusCode) //nolint:gosec // bounded by the API validation contract
		patch.Code = &c
	}

	if p.Delay != nil {
		d := time.Duration(*p.Delay) * time.Second
		patch.Delay = &d
	}

	if p.LongLived != nil {
		l := *p.LongLived
		patch.LongLived = &l
	}

	if p.Headers != nil {
		hs := shared.ToStorageHeaders(*p.Headers)
		patch.Headers = &hs
	}

	if p.SecurityHeaders != nil {
		sh := shared.ToStorageHeaders(*p.SecurityHeaders)
		patch.SecurityHeaders = &sh
	}

	if p.InboundAuthHeader != nil {
		v := *p.InboundAuthHeader
		patch.InboundAuthHeader = &v
	}

	if p.InboundAuthValue != nil {
		v := *p.InboundAuthValue
		patch.InboundAuthValue = &v
	}

	if p.ResponseBodyBase64 != nil {
		body, err := base64.StdEncoding.DecodeString(*p.ResponseBodyBase64)
		if err != nil {
			return patch, fmt.Errorf("%w: cannot decode response body (wrong base64): %w", shared.ErrBadRequest, err)
		}

		patch.ResponseBody = &body
	}

	return patch, nil
}
