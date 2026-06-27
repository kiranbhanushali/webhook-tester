// Package requests_list implements GET /api/session/{session_uuid}/requests: a cursor-paginated,
// NEWEST-first page of a session's captured requests anchored by the durable capture sequence
// (seq). A consumer fetches the newest page (omit before / before=0), reads the returned items
// plus next_before, then requests again with before=next_before to fetch the next (older) page.
// Because seq is strictly increasing and never reused, paging is no-skip and no-duplicate. This
// is the core scalability fix: the default page is the newest `limit` requests, NOT all of them.
package requests_list

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
	defaultLimit = 50
	// maxLimit caps the page size; larger requests are clamped down to it.
	maxLimit = 200
)

func New(db storage.Storage) *Handler { return &Handler{db: db} }

func (h *Handler) Handle(
	ctx context.Context,
	ref sID,
	p openapi.ApiSessionListRequestsParams,
) (*openapi.CapturedRequestsListResponse, error) {
	// Resolve the slug-or-uuid reference to a concrete session (and its canonical ID).
	// An unknown reference surfaces storage.ErrNotFound -> 404 at the dispatch layer.
	sess, err := shared.ResolveSession(ctx, h.db, ref)
	if err != nil {
		return nil, err
	}

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

	reqs, err := h.db.ListRequestsPage(ctx, sess.ID, before, limit)
	if err != nil {
		return nil, err
	}

	var (
		items      = make([]openapi.CapturedRequest, 0, len(reqs))
		nextBefore = before // when nothing older is returned, echo the request cursor
	)

	for _, r := range reqs {
		items = append(items, toCapturedRequest(r))
		nextBefore = r.Seq // results are seq-descending, so the last one is the oldest (lowest)
	}

	// has_more is true when the page was full: the limit may have hidden older requests.
	return &openapi.CapturedRequestsListResponse{
		Items:      items,
		NextBefore: nextBefore,
		HasMore:    len(reqs) == limit,
	}, nil
}

// toCapturedRequest maps a stored request to its OpenAPI representation (base64 payload,
// upper-case method, inbound-auth flag) plus the seq pagination cursor.
func toCapturedRequest(r storage.Request) openapi.CapturedRequest {
	rUUID, _ := uuid.Parse(r.ID) // request IDs are UUIDs; zero value on the unexpected path

	headers := make([]openapi.HttpHeader, len(r.Headers))
	for i, header := range r.Headers {
		headers[i].Name, headers[i].Value = header.Name, header.Value
	}

	return openapi.CapturedRequest{
		Seq:                  r.Seq,
		CapturedAtUnixMilli:  r.CreatedAtUnixMilli,
		ClientAddress:        r.ClientAddr,
		Headers:              headers,
		Method:               strings.ToUpper(r.Method),
		RequestPayloadBase64: base64.StdEncoding.EncodeToString(r.Body),
		Url:                  r.URL,
		Uuid:                 rUUID,
		Authorized:           r.Authorized,
	}
}
