// Package sessions_list implements GET /api/sessions: a filtered, newest-activity-first
// listing of non-expired sessions.
package sessions_list

import (
	"context"
	"fmt"
	"sort"

	"gh.tarampamp.am/webhook-tester/v2/internal/http/openapi"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage"
)

type Handler struct{ db storage.Storage }

func New(db storage.Storage) *Handler { return &Handler{db: db} }

// Handle lists sessions, optionally filtered by group (exact) and q (substring on
// id/slug/group), and returns them sorted newest-activity-first.
func (h *Handler) Handle(ctx context.Context, p openapi.ApiSessionsListParams) (*openapi.SessionsListResponse, error) {
	var f storage.SessionFilter

	if p.Group != nil {
		f.Group = *p.Group
	}

	if p.Q != nil {
		f.Query = *p.Q
	}

	summaries, err := h.db.ListSessions(ctx, f)
	if err != nil {
		return nil, fmt.Errorf("failed to list sessions: %w", err)
	}

	// Sort newest-activity-first regardless of the backend's native ordering:
	// primary key is the last request time (falling back to creation time when a
	// session has no requests), tie-broken by creation time. Both descending.
	sort.SliceStable(summaries, func(i, j int) bool {
		ai, aj := activity(summaries[i]), activity(summaries[j])
		if ai != aj {
			return ai > aj
		}

		return summaries[i].CreatedAtUnixMilli > summaries[j].CreatedAtUnixMilli
	})

	out := make(openapi.SessionsListResponse, 0, len(summaries))

	for _, s := range summaries {
		item := openapi.SessionSummary{
			Slug:               s.Slug,
			StatusCode:         openapi.StatusCode(s.Code),
			RequestsCount:      uint32(s.RequestsCount), //nolint:gosec
			CreatedAtUnixMilli: s.CreatedAtUnixMilli,
			ExpiresAtUnixMilli: s.ExpiresAtUnixMilli,
			LongLived:          s.LongLived,
		}

		if s.GroupName != "" {
			group := s.GroupName
			item.Group = &group
		}

		if s.LastRequestUnixMilli > 0 {
			last := s.LastRequestUnixMilli
			item.LastRequestUnixMilli = &last
		}

		out = append(out, item)
	}

	return &out, nil
}

// activity returns the timestamp used to rank a session: the last request time
// when present, otherwise the creation time.
func activity(s storage.SessionSummary) int64 {
	if s.LastRequestUnixMilli > 0 {
		return s.LastRequestUnixMilli
	}

	return s.CreatedAtUnixMilli
}
