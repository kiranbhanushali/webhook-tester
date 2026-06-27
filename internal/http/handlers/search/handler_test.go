package search_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/search"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/openapi"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage/hotindex"
)

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }
func i64Ptr(i int64) *int64   { return &i }

func TestHandler_StoragePath_MapsMatches(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = storage.NewInMemory(time.Minute, 8)
	)

	t.Cleanup(func() { _ = db.Close() })

	sID, err := db.NewSession(ctx, storage.Session{Slug: "alpha"})
	require.NoError(t, err)

	_, err = db.NewRequest(ctx, sID, storage.Request{
		Method:  "POST",
		Headers: []storage.HttpHeader{{Name: "trackingId", Value: "abc-123"}},
	})
	require.NoError(t, err)

	// nil hot index forces the durable-storage (SQLite-equivalent) path.
	h := search.New(db, nil, 0)

	resp, hErr := h.Handle(ctx, openapi.ApiSearchParams{
		Value: "abc-123",
		Key:   strPtr("trackingId"),
	})
	require.NoError(t, hErr)
	require.NotNil(t, resp)
	require.Len(t, *resp, 1)

	item := (*resp)[0]
	assert.Equal(t, "alpha", item.SessionSlug)
	assert.Equal(t, "trackingId", item.Key)
	assert.Equal(t, "abc-123", item.Value)
	assert.Positive(t, item.CapturedAtUnixMilli)
	assert.NotEqual(t, uuid.Nil, item.RequestUuid)
}

func TestHandler_HotPath_WarmedRecentExactMatches(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = storage.NewInMemory(time.Minute, 8) // intentionally empty: proves the hot path is used
		hot = hotindex.New(168 * time.Hour)
	)

	t.Cleanup(func() { _ = db.Close() })

	var (
		now        = time.Now().UnixMilli()
		newRID     = uuid.NewString()
		oldRID     = uuid.NewString()
		recentFrom = time.Now().Add(-time.Hour).UnixMilli()
	)

	hot.Add("trackingId", "v-1", hotindex.Ref{
		SessionID: "s1", SessionSlug: "alpha", RequestID: oldRID, CapturedAtUnixMilli: now - 5000,
	})
	hot.Add("trackingId", "v-1", hotindex.Ref{
		SessionID: "s1", SessionSlug: "alpha", RequestID: newRID, CapturedAtUnixMilli: now - 1000,
	})
	hot.MarkWarm() // only a warmed index may be trusted

	h := search.New(db, hot, 168*time.Hour)

	resp, hErr := h.Handle(ctx, openapi.ApiSearchParams{
		Value: "v-1",
		Key:   strPtr("trackingId"),
		From:  i64Ptr(recentFrom), // explicit, recent lower bound ⇒ hot path eligible
	})
	require.NoError(t, hErr)
	require.NotNil(t, resp)
	require.Len(t, *resp, 2)

	// newest-first
	assert.Equal(t, newRID, (*resp)[0].RequestUuid.String())
	assert.Equal(t, oldRID, (*resp)[1].RequestUuid.String())
	assert.Equal(t, "trackingId", (*resp)[0].Key)
	assert.Equal(t, "v-1", (*resp)[0].Value)

	t.Run("limit honored", func(t *testing.T) {
		t.Parallel()

		limited, lErr := h.Handle(ctx, openapi.ApiSearchParams{
			Value: "v-1", Key: strPtr("trackingId"), From: i64Ptr(recentFrom), Limit: intPtr(1),
		})
		require.NoError(t, lErr)
		require.Len(t, *limited, 1)
		assert.Equal(t, newRID, (*limited)[0].RequestUuid.String())
	})
}

// A LIVE but never-warm-started hot index must never be trusted: a default
// (from==0) exact query must be answered from durable storage, not the empty index.
func TestHandler_LiveButUnwarmedIndex_UsesStorage(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = storage.NewInMemory(time.Minute, 8)
		hot = hotindex.New(168 * time.Hour) // live, but NOT warmed and empty
	)

	t.Cleanup(func() { _ = db.Close() })

	sID, err := db.NewSession(ctx, storage.Session{Slug: "from-storage"})
	require.NoError(t, err)

	_, err = db.NewRequest(ctx, sID, storage.Request{
		Headers: []storage.HttpHeader{{Name: "trackingId", Value: "v-1"}},
	})
	require.NoError(t, err)

	require.False(t, hot.Warmed(), "precondition: index must be live but not warmed")

	h := search.New(db, hot, 168*time.Hour)

	// Default (unbounded) exact query: if it wrongly trusted the empty hot index it
	// would return 0 results; using storage returns the captured request.
	resp, hErr := h.Handle(ctx, openapi.ApiSearchParams{Value: "v-1", Key: strPtr("trackingId")})
	require.NoError(t, hErr)
	require.Len(t, *resp, 1)
	assert.Equal(t, "from-storage", (*resp)[0].SessionSlug)

	t.Run("even a recent-from query stays on storage until warmed", func(t *testing.T) {
		t.Parallel()

		recentFrom := time.Now().Add(-time.Hour).UnixMilli()

		r, e := h.Handle(ctx, openapi.ApiSearchParams{Value: "v-1", Key: strPtr("trackingId"), From: &recentFrom})
		require.NoError(t, e)
		require.Len(t, *r, 1)
		assert.Equal(t, "from-storage", (*r)[0].SessionSlug)
	})
}

// An unbounded (from==0) exact query is an all-time query the bounded hot index
// cannot answer completely, so even a warmed index falls back to storage.
func TestHandler_UnboundedQueryFallsBackToStorage(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = storage.NewInMemory(time.Minute, 8)
		hot = hotindex.New(168 * time.Hour)
	)

	t.Cleanup(func() { _ = db.Close() })

	sID, err := db.NewSession(ctx, storage.Session{Slug: "delta"})
	require.NoError(t, err)

	_, err = db.NewRequest(ctx, sID, storage.Request{
		Headers: []storage.HttpHeader{{Name: "trackingId", Value: "all-time"}},
	})
	require.NoError(t, err)

	hot.MarkWarm() // warmed, but the query is unbounded

	h := search.New(db, hot, 168*time.Hour)

	resp, hErr := h.Handle(ctx, openapi.ApiSearchParams{Value: "all-time", Key: strPtr("trackingId")})
	require.NoError(t, hErr)
	require.Len(t, *resp, 1)
	assert.Equal(t, "delta", (*resp)[0].SessionSlug)
}

func TestHandler_OlderFromFallsBackToStorage(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = storage.NewInMemory(time.Minute, 8)
		hot = hotindex.New(24 * time.Hour)
	)

	t.Cleanup(func() { _ = db.Close() })

	sID, err := db.NewSession(ctx, storage.Session{Slug: "beta"})
	require.NoError(t, err)

	_, err = db.NewRequest(ctx, sID, storage.Request{
		Headers: []storage.HttpHeader{{Name: "trackingId", Value: "old-value"}},
	})
	require.NoError(t, err)

	hot.MarkWarm() // warmed, so this isolates the older-than-window condition

	// A `from` older than the 24h window must route to durable storage even though
	// the index is warmed (the hot index cannot be complete that far back).
	old := time.Now().Add(-72 * time.Hour).UnixMilli()

	h := search.New(db, hot, 24*time.Hour)

	resp, hErr := h.Handle(ctx, openapi.ApiSearchParams{
		Value: "old-value",
		Key:   strPtr("trackingId"),
		From:  &old,
	})
	require.NoError(t, hErr)
	require.Len(t, *resp, 1)
	assert.Equal(t, "beta", (*resp)[0].SessionSlug)
}
