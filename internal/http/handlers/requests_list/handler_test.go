package requests_list_test

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/requests_list"
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
		_, rErr := db.NewRequest(ctx, sID, storage.Request{Method: "POST", Body: []byte{byte('a' + i)}})
		require.NoError(t, rErr)

		time.Sleep(time.Millisecond) // distinct capture timestamps
	}

	return sID
}

func TestHandler_NewestFirst_CursorPagingAndHasMore(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = storage.NewInMemory(time.Minute, 100)
		h   = requests_list.New(db)
	)

	t.Cleanup(func() { _ = db.Close() })

	sID := seed(t, db, "alpha", 3) // bodies a, b, c (oldest -> newest)

	// page 1: before=0, limit=2 -> the two NEWEST (c, b), has_more=true (page is full)
	page1, err := h.Handle(ctx, sID, openapi.ApiSessionListRequestsParams{Before: i64Ptr(0), Limit: intPtr(2)})
	require.NoError(t, err)
	require.NotNil(t, page1)
	require.Len(t, page1.Items, 2)
	assert.True(t, page1.HasMore)

	// newest-first ordering, payload round-trips as base64
	assert.Equal(t, base64.StdEncoding.EncodeToString([]byte("c")), page1.Items[0].RequestPayloadBase64)
	assert.Equal(t, base64.StdEncoding.EncodeToString([]byte("b")), page1.Items[1].RequestPayloadBase64)
	assert.Greater(t, page1.Items[0].Seq, page1.Items[1].Seq)
	assert.NotEqual(t, "00000000-0000-0000-0000-000000000000", page1.Items[0].Uuid.String())
	assert.Equal(t, "POST", page1.Items[0].Method)

	// next_before is the seq of the last (oldest) returned item
	assert.Equal(t, page1.Items[1].Seq, page1.NextBefore)

	// page 2: fetch with next_before -> only the older item (a), has_more=false (page not full)
	page2, err := h.Handle(ctx, sID, openapi.ApiSessionListRequestsParams{Before: i64Ptr(page1.NextBefore), Limit: intPtr(2)})
	require.NoError(t, err)
	require.Len(t, page2.Items, 1)
	assert.False(t, page2.HasMore)
	assert.Equal(t, base64.StdEncoding.EncodeToString([]byte("a")), page2.Items[0].RequestPayloadBase64)
	assert.Less(t, page2.Items[0].Seq, page1.NextBefore)

	// page 3: nothing older -> empty, has_more=false, cursor echoes the request's before
	page3, err := h.Handle(ctx, sID, openapi.ApiSessionListRequestsParams{Before: i64Ptr(page2.NextBefore), Limit: intPtr(2)})
	require.NoError(t, err)
	require.Empty(t, page3.Items)
	assert.False(t, page3.HasMore)
	assert.Equal(t, page2.NextBefore, page3.NextBefore, "next_before must echo before when nothing is returned")
}

func TestHandler_DefaultsAndClamp(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = storage.NewInMemory(time.Minute, 1000)
		h   = requests_list.New(db)
	)

	t.Cleanup(func() { _ = db.Close() })

	// default limit is 50: seed 51 and expect a full default page + has_more — this is the core
	// fix: NO params must NOT return all requests, only the newest `limit`.
	sID := seed(t, db, "defaults", 51)

	def, err := h.Handle(ctx, sID, openapi.ApiSessionListRequestsParams{})
	require.NoError(t, err)
	require.Len(t, def.Items, 50)
	assert.True(t, def.HasMore)

	// a huge limit is clamped to 200 (we have 51, so all returned, has_more=false)
	clamped, err := h.Handle(ctx, sID, openapi.ApiSessionListRequestsParams{Limit: intPtr(100000)})
	require.NoError(t, err)
	require.Len(t, clamped.Items, 51)
	assert.False(t, clamped.HasMore)
}

func TestHandler_SlugAndUuidRefsBothResolve(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = storage.NewInMemory(time.Minute, 100)
		h   = requests_list.New(db)
	)

	t.Cleanup(func() { _ = db.Close() })

	sID := seed(t, db, "by-slug", 2)

	bySlug, err := h.Handle(ctx, "by-slug", openapi.ApiSessionListRequestsParams{})
	require.NoError(t, err)
	require.Len(t, bySlug.Items, 2)

	byUUID, err := h.Handle(ctx, sID, openapi.ApiSessionListRequestsParams{})
	require.NoError(t, err)
	require.Len(t, byUUID.Items, 2)

	assert.Equal(t, bySlug.Items[0].Uuid, byUUID.Items[0].Uuid)
}

func TestHandler_IncludesAuthorizedFlag(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = storage.NewInMemory(time.Minute, 8)
		h   = requests_list.New(db)
	)

	t.Cleanup(func() { _ = db.Close() })

	sID, err := db.NewSession(ctx, storage.Session{})
	require.NoError(t, err)

	_, err = db.NewRequest(ctx, sID, storage.Request{Method: "POST", Authorized: true})
	require.NoError(t, err)

	_, err = db.NewRequest(ctx, sID, storage.Request{Method: "POST", Authorized: false})
	require.NoError(t, err)

	resp, err := h.Handle(ctx, sID, openapi.ApiSessionListRequestsParams{})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, resp.Items, 2)

	var sawTrue, sawFalse bool

	for _, r := range resp.Items {
		if r.Authorized {
			sawTrue = true
		} else {
			sawFalse = true
		}
	}

	assert.True(t, sawTrue, "an authorized request must surface authorized=true")
	assert.True(t, sawFalse, "a rejected request must surface authorized=false")
}

func TestHandler_UnknownRef_NotFound(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = storage.NewInMemory(time.Minute, 100)
		h   = requests_list.New(db)
	)

	t.Cleanup(func() { _ = db.Close() })

	_, err := h.Handle(ctx, "does-not-exist", openapi.ApiSessionListRequestsParams{})
	require.Error(t, err)
	require.ErrorIs(t, err, storage.ErrNotFound)
}
