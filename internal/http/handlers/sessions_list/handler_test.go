package sessions_list_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/sessions_list"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/openapi"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage"
)

func strPtr(s string) *string { return &s }

func TestHandler_ListsAndMaps(t *testing.T) {
	t.Parallel()

	// Inject a deterministic, manually-advanced clock so activity timestamps never
	// tie at millisecond resolution (the inmemory store iterates a sync.Map in
	// random order, so ties would make the expected ordering non-deterministic).
	var (
		ctx    = context.Background()
		mu     sync.Mutex
		nowVal = time.Now()
		clock  = func() time.Time { mu.Lock(); defer mu.Unlock(); return nowVal }
		adv    = func(d time.Duration) { mu.Lock(); nowVal = nowVal.Add(d); mu.Unlock() }
		db     = storage.NewInMemory(time.Hour, 8, storage.WithInMemoryTimeNow(clock))
	)

	t.Cleanup(func() { _ = db.Close() })

	// Session A: no requests, group team-a (created first / oldest activity).
	aID, err := db.NewSession(ctx, storage.Session{Slug: "alpha", GroupName: "team-a", Code: 201})
	require.NoError(t, err)

	adv(time.Second)

	// Session B: one request (so it has newer activity), group team-b, long-lived.
	bID, err := db.NewSession(ctx, storage.Session{Slug: "bravo", GroupName: "team-b", Code: 200, LongLived: true})
	require.NoError(t, err)

	adv(time.Second)

	_, err = db.NewRequest(ctx, bID, storage.Request{Method: "POST"})
	require.NoError(t, err)

	h := sessions_list.New(db)

	t.Run("all sessions newest-activity-first", func(t *testing.T) {
		t.Parallel()

		resp, hErr := h.Handle(ctx, openapi.ApiSessionsListParams{})
		require.NoError(t, hErr)
		require.NotNil(t, resp)
		require.Len(t, *resp, 2)

		// B has a request → newer activity → must come first.
		assert.Equal(t, "bravo", (*resp)[0].Slug)
		assert.Equal(t, "alpha", (*resp)[1].Slug)

		b := (*resp)[0]
		assert.EqualValues(t, 200, b.StatusCode)
		assert.EqualValues(t, 1, b.RequestsCount)
		assert.True(t, b.LongLived)
		require.NotNil(t, b.Group)
		assert.Equal(t, "team-b", *b.Group)
		require.NotNil(t, b.LastRequestUnixMilli)
		assert.Positive(t, *b.LastRequestUnixMilli)
		assert.Positive(t, b.ExpiresAtUnixMilli)
		assert.Equal(t, uuid.MustParse(bID), b.Uuid, "summary for bravo must carry the session uuid")

		a := (*resp)[1]
		assert.EqualValues(t, 0, a.RequestsCount)
		assert.Nil(t, a.LastRequestUnixMilli, "session with no requests reports nil last-request time")
		assert.Equal(t, uuid.MustParse(aID), a.Uuid, "summary for alpha must carry the session uuid")
	})

	t.Run("group filter", func(t *testing.T) {
		t.Parallel()

		resp, hErr := h.Handle(ctx, openapi.ApiSessionsListParams{Group: strPtr("team-a")})
		require.NoError(t, hErr)
		require.Len(t, *resp, 1)
		assert.Equal(t, "alpha", (*resp)[0].Slug)
	})

	t.Run("substring query filter", func(t *testing.T) {
		t.Parallel()

		resp, hErr := h.Handle(ctx, openapi.ApiSessionsListParams{Q: strPtr("brav")})
		require.NoError(t, hErr)
		require.Len(t, *resp, 1)
		assert.Equal(t, "bravo", (*resp)[0].Slug)
	})
}
