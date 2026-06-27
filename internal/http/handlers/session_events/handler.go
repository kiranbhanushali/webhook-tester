// Package session_events implements GET /api/session/{session_uuid}/events: an
// incremental, FIFO (oldest-first) fetch of a session's captured requests, anchored by a
// durable offset cursor (seq). A consumer starts with after=0, reads the returned events
// plus next_cursor, then polls again with after=next_cursor to receive only newer events.
// Because seq is strictly increasing and never reused (even across max-requests eviction and
// full request wipes on the SQLite driver), the cursor never silently rewinds: delivery is
// no-skip and no-duplicate as long as the cursor is honored.
package session_events

import (
	"context"
	"encoding/base64"
	"strings"

	"github.com/google/uuid"

	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/shared"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/openapi"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage"
)

type (
	// sID is the session reference (UUID or slug) from the path.
	sID = openapi.SessionUUIDInPath

	Handler struct{ db storage.Storage }
)

const (
	// defaultLimit is the page size when the caller omits limit.
	defaultLimit = 100
	// maxLimit caps the page size; larger requests are clamped down to it.
	maxLimit = 1000
)

func New(db storage.Storage) *Handler { return &Handler{db: db} }

func (h *Handler) Handle(
	ctx context.Context,
	ref sID,
	p openapi.ApiSessionEventsParams,
) (*openapi.EventsResponse, error) {
	// Resolve the slug-or-uuid reference to a concrete session (and its canonical ID).
	// An unknown reference surfaces storage.ErrNotFound -> 404 at the dispatch layer.
	sess, err := shared.ResolveSession(ctx, h.db, ref)
	if err != nil {
		return nil, err
	}

	var after int64
	if p.After != nil && *p.After > 0 {
		after = *p.After
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

	reqs, err := h.db.ListRequestsAfter(ctx, sess.ID, after, limit)
	if err != nil {
		return nil, err
	}

	var (
		events     = make([]openapi.EventItem, 0, len(reqs))
		nextCursor = after // when nothing newer is returned, echo the request cursor
	)

	for _, r := range reqs {
		events = append(events, toEventItem(r))
		nextCursor = r.Seq // results are seq-ascending, so the last one is the highest
	}

	// has_more is true when the page was full: the limit may have hidden newer events.
	return &openapi.EventsResponse{
		Events:     events,
		NextCursor: nextCursor,
		HasMore:    len(reqs) == limit,
	}, nil
}

// toEventItem maps a stored request to its OpenAPI event representation, mirroring the
// requests_list/request_get handlers (base64 payload, upper-case method) plus the seq cursor.
func toEventItem(r storage.Request) openapi.EventItem {
	rUUID, _ := uuid.Parse(r.ID) // request IDs are UUIDs; zero value on the unexpected path

	headers := make([]openapi.HttpHeader, len(r.Headers))
	for i, hdr := range r.Headers {
		headers[i].Name, headers[i].Value = hdr.Name, hdr.Value
	}

	return openapi.EventItem{
		Seq:                  r.Seq,
		Uuid:                 rUUID,
		ClientAddress:        r.ClientAddr,
		Method:               strings.ToUpper(r.Method),
		RequestPayloadBase64: base64.StdEncoding.EncodeToString(r.Body),
		Headers:              headers,
		Url:                  r.URL,
		CapturedAtUnixMilli:  r.CreatedAtUnixMilli,
		Authorized:           r.Authorized,
	}
}
