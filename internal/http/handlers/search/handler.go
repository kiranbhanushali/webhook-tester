// Package search implements GET /api/search: identifier key/value lookups across
// captured requests.
//
// # Fast path vs. fallback (controller decision)
//
// Two backends can answer a search:
//
//   - The in-memory hot index (hotindex.HotIndex) is an O(1) exact-match lookup
//     over the most-recent retention window (default 168h). It is fast but only
//     covers recent captures and cannot filter by group (its Ref carries no group)
//     nor match a value by prefix.
//   - Durable storage (storage.SearchRequests) is authoritative: full history,
//     accurate stored key/value casing, prefix matching, and group filtering.
//
// The hot path is taken only when ALL of the following hold; otherwise the query
// falls back to durable storage:
//
//   - a hot index was injected (non-nil) AND it has been warm-started
//     (Warmed()); an empty, never-rebuilt index served right after a restart
//     would silently return incomplete results, so it must not be trusted, and
//   - match == exact (prefix needs a full scan), and
//   - a concrete key is supplied (the hot index is keyed; "any key" needs a scan), and
//   - no group filter is set (the hot index cannot filter by group), and
//   - an explicit, recent lower bound is set: `from` > 0 AND `from >= now - window`.
//     An unbounded query (`from == 0`) is an ALL-TIME query that the bounded
//     (e.g. 7-day) hot index cannot answer completely, so it goes to storage.
//
// A session filter is honored on either path (the hot index Ref carries a
// SessionID). When the hot index is nil or not yet warmed (e.g. before Task 10
// wires the warm-up) every query uses durable storage — correct and
// restart-safe.
package search

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/shared"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/openapi"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage/hotindex"
)

// defaultHotWindow mirrors the hot index's default retention window. It is used
// when the caller injects a non-positive window.
const defaultHotWindow = 168 * time.Hour // 7 days

type Handler struct {
	db     storage.Storage
	hot    *hotindex.HotIndex // optional; nil ⇒ always use durable storage
	window time.Duration
}

// New builds a search handler. hot may be nil and window may be zero (both
// tolerated: the handler then serves every query from durable storage).
func New(db storage.Storage, hot *hotindex.HotIndex, window time.Duration) *Handler {
	if window <= 0 {
		window = defaultHotWindow
	}

	return &Handler{db: db, hot: hot, window: window}
}

func (h *Handler) Handle(ctx context.Context, p openapi.ApiSearchParams) (*openapi.SearchResponse, error) {
	if p.Value == "" {
		return nil, fmt.Errorf("%w: search value is required", shared.ErrBadRequest)
	}

	q := storage.IdentifierQuery{
		Value: p.Value,
		Match: storage.IdentifierMatchExact,
	}

	if p.Key != nil {
		q.Key = *p.Key
	}

	if p.Match != nil && *p.Match == openapi.ApiSearchParamsMatchPrefix {
		q.Match = storage.IdentifierMatchPrefix
	}

	if p.Group != nil {
		q.Group = *p.Group
	}

	if p.From != nil {
		q.FromUnixMilli = *p.From
	}

	if p.To != nil {
		q.ToUnixMilli = *p.To
	}

	if p.Limit != nil {
		q.Limit = *p.Limit
	}

	// Resolve a slug-or-uuid session reference to its canonical ID for filtering.
	if p.Session != nil && *p.Session != "" {
		sess, err := shared.ResolveSession(ctx, h.db, *p.Session)
		if err != nil {
			return nil, fmt.Errorf("%w: unknown session %q", shared.ErrBadRequest, *p.Session)
		}

		q.SessionID = sess.ID
	}

	if h.useHotPath(q) {
		return h.fromHotIndex(q), nil
	}

	return h.fromStorage(ctx, q)
}

// useHotPath reports whether the in-memory hot index can fully and accurately
// answer q (see the package doc for the rationale behind each condition).
func (h *Handler) useHotPath(q storage.IdentifierQuery) bool {
	switch {
	case h.hot == nil || !h.hot.Warmed():
		return false
	case q.Match != storage.IdentifierMatchExact:
		return false
	case q.Key == "":
		return false
	case q.Group != "":
		return false
	case q.FromUnixMilli <= 0:
		// Unbounded (all-time) query: the bounded hot index cannot be complete.
		return false
	case q.FromUnixMilli < time.Now().Add(-h.window).UnixMilli():
		// Lower bound reaches back beyond the retained window.
		return false
	default:
		return true
	}
}

// fromHotIndex answers q from the hot index. Results arrive newest-first; the
// optional session/from/to bounds and limit are applied here.
func (h *Handler) fromHotIndex(q storage.IdentifierQuery) *openapi.SearchResponse {
	refs := h.hot.Lookup(q.Key, q.Value, storage.IdentifierMatchExact)

	out := make(openapi.SearchResponse, 0, len(refs))

	for _, ref := range refs {
		if q.SessionID != "" && ref.SessionID != q.SessionID {
			continue
		}

		if q.FromUnixMilli > 0 && ref.CapturedAtUnixMilli < q.FromUnixMilli {
			continue
		}

		if q.ToUnixMilli > 0 && ref.CapturedAtUnixMilli > q.ToUnixMilli {
			continue
		}

		rUUID, _ := uuid.Parse(ref.RequestID)

		out = append(out, openapi.SearchResultItem{
			SessionSlug:         ref.SessionSlug,
			RequestUuid:         rUUID,
			Key:                 q.Key, // exact match ⇒ the query key/value are the stored ones
			Value:               q.Value,
			CapturedAtUnixMilli: ref.CapturedAtUnixMilli,
		})

		if q.Limit > 0 && len(out) >= q.Limit {
			break
		}
	}

	return &out
}

// fromStorage answers q from durable storage and returns the matches sorted
// newest-first for a stable, consistent ordering with the hot path.
func (h *Handler) fromStorage(ctx context.Context, q storage.IdentifierQuery) (*openapi.SearchResponse, error) {
	matches, err := h.db.SearchRequests(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("failed to search requests: %w", err)
	}

	sort.SliceStable(matches, func(i, j int) bool {
		return matches[i].CapturedAtUnixMilli > matches[j].CapturedAtUnixMilli
	})

	out := make(openapi.SearchResponse, 0, len(matches))

	for _, m := range matches {
		rUUID, _ := uuid.Parse(m.RequestID)

		out = append(out, openapi.SearchResultItem{
			SessionSlug:         m.SessionSlug,
			RequestUuid:         rUUID,
			Key:                 m.Key,
			Value:               m.Value,
			CapturedAtUnixMilli: m.CapturedAtUnixMilli,
		})
	}

	return &out, nil
}
