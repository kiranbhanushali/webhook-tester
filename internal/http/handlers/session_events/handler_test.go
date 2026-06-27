package session_events_test

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/session_events"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/openapi"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage"
)

func i64Ptr(i int64) *int64 { return &i }
func intPtr(i int) *int     { return &i }

// seed creates a session and n requests (oldest first), returning the session id.
func seed(t *testing.T, db storage.Storage, slug string, n int) string {
	t.Helper()

	var ctx = context.Background()

	sID, err := db.NewSession(ctx, storage.Session{Slug: slug})
	require.NoError(t, err)

	for i := range n {
		_, rErr := db.NewRequest(ctx, sID, storage.Request{
			Method: "POST",
			Body:   []byte{byte('a' + i)},
		})
		require.NoError(t, rErr)

		time.Sleep(time.Millisecond) // distinct capture timestamps
	}

	return sID
}

func TestHandler_PollFromZero_FIFO_WithCursorAndHasMore(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = storage.NewInMemory(time.Minute, 100)
		h   = session_events.New(db)
	)

	t.Cleanup(func() { _ = db.Close() })

	sID := seed(t, db, "alpha", 3)

	// page 1: after=0, limit=2 -> the two OLDEST, FIFO, has_more=true (page is full)
	page1, err := h.Handle(ctx, sID, openapi.ApiSessionEventsParams{After: i64Ptr(0), Limit: intPtr(2)})
	require.NoError(t, err)
	require.NotNil(t, page1)
	require.Len(t, page1.Events, 2)
	assert.True(t, page1.HasMore)

	// oldest-first ordering, payload round-trips as base64
	assert.Equal(t, base64.StdEncoding.EncodeToString([]byte("a")), page1.Events[0].RequestPayloadBase64)
	assert.Equal(t, base64.StdEncoding.EncodeToString([]byte("b")), page1.Events[1].RequestPayloadBase64)
	assert.Less(t, page1.Events[0].Seq, page1.Events[1].Seq)
	assert.NotEqual(t, "00000000-0000-0000-0000-000000000000", page1.Events[0].Uuid.String())
	assert.Equal(t, "POST", page1.Events[0].Method)

	// next_cursor is the last returned seq
	assert.Equal(t, page1.Events[1].Seq, page1.NextCursor)

	// page 2: poll with next_cursor -> only the newer event, has_more=false (page not full)
	page2, err := h.Handle(ctx, sID, openapi.ApiSessionEventsParams{After: i64Ptr(page1.NextCursor), Limit: intPtr(2)})
	require.NoError(t, err)
	require.Len(t, page2.Events, 1)
	assert.False(t, page2.HasMore)
	assert.Equal(t, base64.StdEncoding.EncodeToString([]byte("c")), page2.Events[0].RequestPayloadBase64)
	assert.Greater(t, page2.Events[0].Seq, page1.NextCursor)
	assert.Equal(t, page2.Events[0].Seq, page2.NextCursor)

	// page 3: nothing newer -> empty, has_more=false, cursor echoes the request's after
	page3, err := h.Handle(ctx, sID, openapi.ApiSessionEventsParams{After: i64Ptr(page2.NextCursor), Limit: intPtr(2)})
	require.NoError(t, err)
	require.Empty(t, page3.Events)
	assert.False(t, page3.HasMore)
	assert.Equal(t, page2.NextCursor, page3.NextCursor, "next_cursor must echo after when nothing is returned")
}

func TestHandler_SlugAndUuidRefsBothResolve(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = storage.NewInMemory(time.Minute, 100)
		h   = session_events.New(db)
	)

	t.Cleanup(func() { _ = db.Close() })

	sID := seed(t, db, "by-slug", 2)

	// by slug
	bySlug, err := h.Handle(ctx, "by-slug", openapi.ApiSessionEventsParams{})
	require.NoError(t, err)
	require.Len(t, bySlug.Events, 2)

	// by uuid
	byUUID, err := h.Handle(ctx, sID, openapi.ApiSessionEventsParams{})
	require.NoError(t, err)
	require.Len(t, byUUID.Events, 2)

	assert.Equal(t, bySlug.Events[0].Uuid, byUUID.Events[0].Uuid)
}

func TestHandler_DefaultsAndClamp(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = storage.NewInMemory(time.Minute, 1000)
		h   = session_events.New(db)
	)

	t.Cleanup(func() { _ = db.Close() })

	// default limit is 100: seed 101 and expect a full default page + has_more
	sID := seed(t, db, "defaults", 101)

	// no params at all -> after=0, limit=100
	def, err := h.Handle(ctx, sID, openapi.ApiSessionEventsParams{})
	require.NoError(t, err)
	require.Len(t, def.Events, 100)
	assert.True(t, def.HasMore)

	// a huge limit is clamped to 1000 (we have 101, so all returned, has_more=false)
	clamped, err := h.Handle(ctx, sID, openapi.ApiSessionEventsParams{Limit: intPtr(100000)})
	require.NoError(t, err)
	require.Len(t, clamped.Events, 101)
	assert.False(t, clamped.HasMore)
}

func TestHandler_UnknownRef_NotFound(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = storage.NewInMemory(time.Minute, 100)
		h   = session_events.New(db)
	)

	t.Cleanup(func() { _ = db.Close() })

	_, err := h.Handle(ctx, "does-not-exist", openapi.ApiSessionEventsParams{})
	require.Error(t, err)
	require.ErrorIs(t, err, storage.ErrNotFound)
}
