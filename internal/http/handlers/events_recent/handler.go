// Package events_recent implements GET /api/events: a cursor-paginated, NEWEST-first page of the
// most-recently captured requests across ALL sessions (optionally narrowed to a single session
// and/or group), anchored by the global durable capture sequence (seq). It backs the unified
// dashboard event viewer: the frontend fetches the newest page on load (recent backfill) and pages
// backwards through history with before=next_before. Because seq is strictly increasing and never
// reused, paging is no-skip and no-duplicate.
package events_recent

import (
	"context"
	"encoding/base64"
	"strings"

	"github.com/google/uuid"

	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/shared"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/openapi"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage"
)

type Handler struct{ db storage.Storage }

const (
	// defaultLimit is the page size when the caller omits limit.
	defaultLimit = 50
	// maxLimit caps the page size; larger requests are clamped down to it.
	maxLimit = 200
)

func New(db storage.Storage) *Handler { return &Handler{db: db} }

func (h *Handler) Handle(ctx context.Context, p openapi.ApiEventsParams) (*openapi.RecentEventsResponse, error) {
	var before int64
	if p.Before != nil && *p.Before > 0 {
		before = *p.Before
	}

	var limit = defaultLimit
	if p.Limit != nil {
		limit = *p.Limit
	}

	if limit <= 0 {
		limit = defaultLimit
	}

	if limit > maxLimit {
		limit = maxLimit
	}

	var filter storage.RecentRequestsFilter

	// Resolve an optional session reference (UUID or slug) to its canonical ID. An unknown
	// reference surfaces storage.ErrNotFound -> 404 at the dispatch layer.
	if p.Session != nil && *p.Session != "" {
		sess, err := shared.ResolveSession(ctx, h.db, *p.Session)
		if err != nil {
			return nil, err
		}

		filter.Session = sess.ID
	}

	if p.Group != nil {
		filter.Group = *p.Group
	}

	reqs, err := h.db.ListRecentRequests(ctx, filter, before, limit)
	if err != nil {
		return nil, err
	}

	var (
		items      = make([]openapi.RecentEventItem, 0, len(reqs))
		nextBefore = before // when nothing older is returned, echo the request cursor
	)

	for _, r := range reqs {
		items = append(items, toRecentEventItem(r))
		nextBefore = r.Seq // results are seq-descending, so the last one is the oldest (lowest)
	}

	// has_more is true when the page was full: the limit may have hidden older events.
	return &openapi.RecentEventsResponse{
		Items:      items,
		NextBefore: nextBefore,
		HasMore:    len(reqs) == limit,
	}, nil
}

// toRecentEventItem maps a stored recent request to its OpenAPI representation, mirroring the
// requests_list/session_events handlers (base64 payload, upper-case method) plus the originating
// session (uuid + slug) and the global seq cursor.
func toRecentEventItem(r storage.RecentRequest) openapi.RecentEventItem {
	rUUID, _ := uuid.Parse(r.ID)        // request IDs are UUIDs; zero value on the unexpected path
	sUUID, _ := uuid.Parse(r.SessionID) // session IDs are UUIDs; zero value on the unexpected path

	headers := make([]openapi.HttpHeader, len(r.Headers))
	for i, hdr := range r.Headers {
		headers[i].Name, headers[i].Value = hdr.Name, hdr.Value
	}

	return openapi.RecentEventItem{
		Seq:                  r.Seq,
		Uuid:                 rUUID,
		SessionUuid:          sUUID,
		SessionSlug:          r.SessionSlug,
		ClientAddress:        r.ClientAddr,
		Method:               strings.ToUpper(r.Method),
		RequestPayloadBase64: base64.StdEncoding.EncodeToString(r.Body),
		Headers:              headers,
		Url:                  r.URL,
		CapturedAtUnixMilli:  r.CreatedAtUnixMilli,
		Authorized:           r.Authorized,
	}
}
