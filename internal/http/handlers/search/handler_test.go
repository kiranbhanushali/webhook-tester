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

func TestHandler_HotPath_RecentExactMatches(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = storage.NewInMemory(time.Minute, 8) // not consulted on the hot path
		hot = hotindex.New(168 * time.Hour)
	)

	t.Cleanup(func() { _ = db.Close() })

	var (
		now    = time.Now().UnixMilli()
		newRID = uuid.NewString()
		oldRID = uuid.NewString()
	)

	hot.Add("trackingId", "v-1", hotindex.Ref{
		SessionID: "s1", SessionSlug: "alpha", RequestID: oldRID, CapturedAtUnixMilli: now - 5000,
	})
	hot.Add("trackingId", "v-1", hotindex.Ref{
		SessionID: "s1", SessionSlug: "alpha", RequestID: newRID, CapturedAtUnixMilli: now - 1000,
	})

	h := search.New(db, hot, 168*time.Hour)

	resp, hErr := h.Handle(ctx, openapi.ApiSearchParams{
		Value: "v-1",
		Key:   strPtr("trackingId"),
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
			Value: "v-1", Key: strPtr("trackingId"), Limit: intPtr(1),
		})
		require.NoError(t, lErr)
		require.Len(t, *limited, 1)
		assert.Equal(t, newRID, (*limited)[0].RequestUuid.String())
	})
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

	// The hot index does NOT contain this value; a `from` older than the 24h window
	// must route the query to durable storage, which finds the request.
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

func intPtr(i int) *int { return &i }
