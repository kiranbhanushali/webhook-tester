// Package session_create implements POST /api/session: it creates a session from
// the supplied response options, generating a unique human-readable slug when the
// client does not provide one and persisting the extended fields (group, response
// script, security headers, forward URL, long-lived).
package session_create

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/shared"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/openapi"
	"gh.tarampamp.am/webhook-tester/v2/internal/slug"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage"
)

// maxSlugGenerationAttempts bounds how many times we regenerate an auto slug
// before giving up on a (astronomically unlikely) run of collisions.
const maxSlugGenerationAttempts = 8

type Handler struct{ db storage.Storage }

func New(db storage.Storage) *Handler { return &Handler{db: db} }

func (h *Handler) Handle(ctx context.Context, p openapi.CreateSessionRequest) (*openapi.SessionOptionsResponse, error) {
	responseBody, decErr := base64.StdEncoding.DecodeString(p.ResponseBodyBase64)
	if decErr != nil {
		return nil, fmt.Errorf("%w: cannot decode response body (wrong base64): %w", shared.ErrBadRequest, decErr)
	}

	if err := shared.ValidateInboundAuth(p.InboundAuthHeader, p.InboundAuthValue); err != nil {
		return nil, err
	}

	sessionSlug, slugErr := h.resolveSlug(ctx, p.Slug)
	if slugErr != nil {
		return nil, slugErr
	}

	newSession := storage.Session{
		Code:         uint16(p.StatusCode), //nolint:gosec // bounded by the API validation contract
		Headers:      shared.ToStorageHeaders(p.Headers),
		ResponseBody: responseBody,
		Delay:        time.Second * time.Duration(p.Delay),
		Slug:         sessionSlug,
	}

	if p.Group != nil {
		newSession.GroupName = *p.Group
	}

	if p.ResponseScript != nil {
		newSession.ResponseScript = *p.ResponseScript
	}

	if p.ForwardUrl != nil {
		newSession.ForwardURL = *p.ForwardUrl
	}

	if p.SecurityHeaders != nil {
		newSession.SecurityHeaders = shared.ToStorageHeaders(*p.SecurityHeaders)
	}

	if p.LongLived != nil {
		newSession.LongLived = *p.LongLived
	}

	if p.InboundAuthHeader != nil {
		newSession.InboundAuthHeader = *p.InboundAuthHeader
	}

	if p.InboundAuthValue != nil {
		newSession.InboundAuthValue = *p.InboundAuthValue
	}

	sID, sErr := h.db.NewSession(ctx, newSession)
	if sErr != nil {
		return nil, fmt.Errorf("failed to create a new session: %w", sErr)
	}

	created, gErr := h.db.GetSession(ctx, sID)
	if gErr != nil {
		return nil, fmt.Errorf("failed to get session: %w", gErr)
	}

	resp := shared.SessionResponse(created)

	return &resp, nil
}

// resolveSlug validates a user-supplied slug (400 on bad format, 409 on
// collision) or generates a unique one when none is provided.
//
// NOTE: the check-then-create against GetSessionBySlug carries a small TOCTOU
// window under concurrent creation of the same slug; the storage layer's slug
// index (unique in SQLite) is the ultimate guard.
func (h *Handler) resolveSlug(ctx context.Context, supplied *string) (string, error) {
	if supplied != nil && *supplied != "" {
		if err := shared.ValidateSlug(*supplied); err != nil {
			return "", err
		}

		if _, err := h.db.GetSessionBySlug(ctx, *supplied); err == nil {
			return "", fmt.Errorf("%w: slug %q is already in use", shared.ErrConflict, *supplied)
		}

		return *supplied, nil
	}

	for range maxSlugGenerationAttempts {
		candidate := slug.Generate()

		if _, err := h.db.GetSessionBySlug(ctx, candidate); errors.Is(err, storage.ErrNotFound) {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("failed to generate a unique slug after %d attempts", maxSlugGenerationAttempts)
}
