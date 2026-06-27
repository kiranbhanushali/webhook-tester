package storage_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"gh.tarampamp.am/webhook-tester/v2/internal/storage"
)

// jsonStubExtractor is a test stand-in for the real identifier extractor (Task 5).
// It pulls every top-level JSON string field out of the request body as an identifier,
// so the SQLite search tests exercise the real request_identifiers index.
func jsonStubExtractor(body []byte, headers []storage.HttpHeader, _ string) []storage.Identifier {
	var ids []storage.Identifier

	// headers become identifiers too (mirrors the inmemory header scan)
	for _, h := range headers {
		ids = append(ids, storage.Identifier{Key: h.Name, Value: h.Value})
	}

	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return ids
	}

	for k, v := range m {
		if str, ok := v.(string); ok {
			ids = append(ids, storage.Identifier{Key: k, Value: str})
		}
	}

	return ids
}

// newSQLite spins up a SQLite driver backed by a throwaway temp file, registered for cleanup.
func newSQLite(t *testing.T, ttl time.Duration, maxReq uint32, opts ...storage.SQLiteOption) *storage.SQLite {
	t.Helper()

	dsn := "file:" + filepath.Join(t.TempDir(), "test.db")

	s, err := storage.NewSQLite(context.Background(), dsn, ttl, maxReq, opts...)
	require.NoError(t, err)

	t.Cleanup(func() { _ = s.Close() })

	return s
}

func TestSQLite_RoundTrip(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		ft  = newFakeTime(t)
		s   = newSQLite(t, 7*24*time.Hour, 128,
			storage.WithSQLiteTimeNow(ft.Get),
			storage.WithSQLiteExtractor(jsonStubExtractor),
		)
	)

	sID, err := s.NewSession(ctx, storage.Session{
		Code:      200,
		Slug:      "my-callback",
		GroupName: "bob",
		Headers:   []storage.HttpHeader{{Name: "X-A", Value: "1"}},
	})
	require.NoError(t, err)
	require.NotEmpty(t, sID)

	rID, err := s.NewRequest(ctx, sID, storage.Request{
		Method: "POST",
		Body:   []byte(`{"trackingId":"ABC","note":"hi"}`),
		URL:    "/my-callback",
	})
	require.NoError(t, err)
	require.NotEmpty(t, rID)

	// indexed identifier search
	matches, err := s.SearchRequests(ctx, storage.IdentifierQuery{
		Key:   "trackingId",
		Value: "ABC",
		Match: storage.IdentifierMatchExact,
	})
	require.NoError(t, err)
	require.Len(t, matches, 1)
	require.Equal(t, "ABC", matches[0].Value)
	require.Equal(t, "trackingid", matches[0].Key) // normalized to lower-case
	require.Equal(t, sID, matches[0].SessionID)
	require.Equal(t, rID, matches[0].RequestID)
	require.Equal(t, "my-callback", matches[0].SessionSlug)

	// listing with request count
	list, err := s.ListSessions(ctx, storage.SessionFilter{})
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, sID, list[0].ID)
	require.Equal(t, "my-callback", list[0].Slug)
	require.Equal(t, 1, list[0].RequestsCount)
	require.NotZero(t, list[0].LastRequestUnixMilli)

	// slug lookup
	bySlug, err := s.GetSessionBySlug(ctx, "my-callback")
	require.NoError(t, err)
	require.Equal(t, "bob", bySlug.GroupName)
	require.Equal(t, sID, bySlug.ID, "GetSessionBySlug must populate Session.ID")

	// mutate via patch
	newGroup := "acme"
	require.NoError(t, s.UpdateSession(ctx, sID, storage.SessionPatch{GroupName: &newGroup}))

	got, err := s.GetSession(ctx, sID)
	require.NoError(t, err)
	require.Equal(t, "acme", got.GroupName)
}

func TestSQLite_SearchExactAndPrefix(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		ft  = newFakeTime(t)
		s   = newSQLite(t, time.Hour, 128,
			storage.WithSQLiteTimeNow(ft.Get),
			storage.WithSQLiteExtractor(jsonStubExtractor),
		)
	)

	sID, err := s.NewSession(ctx, storage.Session{Slug: "s1", GroupName: "g1"})
	require.NoError(t, err)

	_, err = s.NewRequest(ctx, sID, storage.Request{Body: []byte(`{"trackingId":"ABC123"}`)})
	require.NoError(t, err)

	ft.Add(time.Millisecond) // ensure distinct, ordered timestamps

	rID2, err := s.NewRequest(ctx, sID, storage.Request{Body: []byte(`{"trackingId":"ABZ999"}`)})
	require.NoError(t, err)

	// values exercising literal '_' handling in prefix search
	for _, v := range []string{"TXN_123", "TXNX123", "TXNA45"} {
		ft.Add(time.Millisecond)

		_, rErr := s.NewRequest(ctx, sID, storage.Request{Body: []byte(`{"refId":"` + v + `"}`)})
		require.NoError(t, rErr)
	}

	t.Run("exact match", func(t *testing.T) {
		m, sErr := s.SearchRequests(ctx, storage.IdentifierQuery{Key: "trackingId", Value: "ABC123"})
		require.NoError(t, sErr)
		require.Len(t, m, 1)
		require.Equal(t, "ABC123", m[0].Value)
	})

	t.Run("exact is case sensitive", func(t *testing.T) {
		m, sErr := s.SearchRequests(ctx, storage.IdentifierQuery{Key: "trackingId", Value: "abc123"})
		require.NoError(t, sErr)
		require.Empty(t, m)
	})

	t.Run("prefix narrow", func(t *testing.T) {
		m, sErr := s.SearchRequests(ctx, storage.IdentifierQuery{
			Key: "trackingId", Value: "ABC", Match: storage.IdentifierMatchPrefix,
		})
		require.NoError(t, sErr)
		require.Len(t, m, 1)
		require.Equal(t, "ABC123", m[0].Value)
	})

	t.Run("prefix wide ordered newest first", func(t *testing.T) {
		m, sErr := s.SearchRequests(ctx, storage.IdentifierQuery{
			Key: "trackingId", Value: "AB", Match: storage.IdentifierMatchPrefix,
		})
		require.NoError(t, sErr)
		require.Len(t, m, 2)
		require.Equal(t, "ABZ999", m[0].Value) // newest first
		require.Equal(t, rID2, m[0].RequestID)
	})

	t.Run("prefix is case sensitive", func(t *testing.T) {
		// lower-case prefix must NOT match the stored upper-case "ABC123"
		m, sErr := s.SearchRequests(ctx, storage.IdentifierQuery{
			Key: "trackingId", Value: "abc", Match: storage.IdentifierMatchPrefix,
		})
		require.NoError(t, sErr)
		require.Empty(t, m)
	})

	t.Run("prefix underscore is literal not wildcard", func(t *testing.T) {
		// "TXN_1" matches only "TXN_123"; "TXNX123" must NOT match ('_' is literal now)
		m, sErr := s.SearchRequests(ctx, storage.IdentifierQuery{
			Key: "refId", Value: "TXN_1", Match: storage.IdentifierMatchPrefix,
		})
		require.NoError(t, sErr)
		require.Len(t, m, 1)
		require.Equal(t, "TXN_123", m[0].Value)

		// "TXN_" matches "TXN_123" but not "TXNA45"
		m, sErr = s.SearchRequests(ctx, storage.IdentifierQuery{
			Key: "refId", Value: "TXN_", Match: storage.IdentifierMatchPrefix,
		})
		require.NoError(t, sErr)
		require.Len(t, m, 1)
		require.Equal(t, "TXN_123", m[0].Value)
	})

	t.Run("limit caps results", func(t *testing.T) {
		m, sErr := s.SearchRequests(ctx, storage.IdentifierQuery{
			Key: "trackingId", Value: "AB", Match: storage.IdentifierMatchPrefix, Limit: 1,
		})
		require.NoError(t, sErr)
		require.Len(t, m, 1)
	})

	t.Run("wrong key no match", func(t *testing.T) {
		m, sErr := s.SearchRequests(ctx, storage.IdentifierQuery{Key: "referenceId", Value: "ABC123"})
		require.NoError(t, sErr)
		require.Empty(t, m)
	})

	t.Run("time window upper bound excludes all", func(t *testing.T) {
		m, sErr := s.SearchRequests(ctx, storage.IdentifierQuery{
			Key: "trackingId", Value: "AB", Match: storage.IdentifierMatchPrefix,
			FromUnixMilli: ft.Get().Add(time.Hour).UnixMilli(),
		})
		require.NoError(t, sErr)
		require.Empty(t, m)
	})

	t.Run("group filter", func(t *testing.T) {
		m, sErr := s.SearchRequests(ctx, storage.IdentifierQuery{Key: "trackingId", Value: "ABC123", Group: "nope"})
		require.NoError(t, sErr)
		require.Empty(t, m)

		m, sErr = s.SearchRequests(ctx, storage.IdentifierQuery{Key: "trackingId", Value: "ABC123", Group: "g1"})
		require.NoError(t, sErr)
		require.Len(t, m, 1)
	})
}

func TestSQLite_ListSessionsCounts(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		ft  = newFakeTime(t)
		s   = newSQLite(t, time.Hour, 128, storage.WithSQLiteTimeNow(ft.Get))
	)

	a, err := s.NewSession(ctx, storage.Session{Slug: "alpha", GroupName: "team-a"})
	require.NoError(t, err)
	b, err := s.NewSession(ctx, storage.Session{Slug: "beta", GroupName: "team-b"})
	require.NoError(t, err)

	for range 3 {
		ft.Add(time.Millisecond)

		_, rErr := s.NewRequest(ctx, a, storage.Request{ClientAddr: "1.1.1.1"})
		require.NoError(t, rErr)
	}

	all, err := s.ListSessions(ctx, storage.SessionFilter{})
	require.NoError(t, err)
	require.Len(t, all, 2)

	counts := map[string]int{}
	for _, ss := range all {
		counts[ss.ID] = ss.RequestsCount
	}

	require.Equal(t, 3, counts[a])
	require.Equal(t, 0, counts[b])

	// group filter
	g, err := s.ListSessions(ctx, storage.SessionFilter{Group: "team-a"})
	require.NoError(t, err)
	require.Len(t, g, 1)
	require.Equal(t, a, g[0].ID)

	// substring query filter (case sensitive)
	q, err := s.ListSessions(ctx, storage.SessionFilter{Query: "bet"})
	require.NoError(t, err)
	require.Len(t, q, 1)
	require.Equal(t, b, q[0].ID)

	none, err := s.ListSessions(ctx, storage.SessionFilter{Query: "BET"})
	require.NoError(t, err)
	require.Empty(t, none)
}

func TestSQLite_SlugLookupAndConflict(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		s   = newSQLite(t, time.Hour, 16)
	)

	_, err := s.NewSession(ctx, storage.Session{Slug: "dup"})
	require.NoError(t, err)

	// duplicate slug -> conflict
	_, err = s.NewSession(ctx, storage.Session{Slug: "dup"})
	require.Error(t, err)
	require.ErrorIs(t, err, storage.ErrSlugConflict)

	// two slug-less sessions both succeed (partial unique index ignores empty slugs)
	_, err = s.NewSession(ctx, storage.Session{})
	require.NoError(t, err)
	_, err = s.NewSession(ctx, storage.Session{})
	require.NoError(t, err)

	// lookup
	got, err := s.GetSessionBySlug(ctx, "dup")
	require.NoError(t, err)
	require.Equal(t, "dup", got.Slug)

	_, err = s.GetSessionBySlug(ctx, "")
	require.ErrorIs(t, err, storage.ErrNotFound)

	_, err = s.GetSessionBySlug(ctx, "missing")
	require.ErrorIs(t, err, storage.ErrNotFound)
}

func TestSQLite_UpdateSession(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		s   = newSQLite(t, time.Hour, 16)
	)

	sID, err := s.NewSession(ctx, storage.Session{Code: 200, Slug: "orig"})
	require.NoError(t, err)

	var (
		newCode   uint16 = 404
		newSlug          = "renamed"
		newGroup         = "grp"
		newScript        = "{{.Method}}"
		longLived        = true
		newSec           = []storage.HttpHeader{{Name: "X-Frame-Options", Value: "DENY"}}
	)

	require.NoError(t, s.UpdateSession(ctx, sID, storage.SessionPatch{
		Code:            &newCode,
		Slug:            &newSlug,
		GroupName:       &newGroup,
		ResponseScript:  &newScript,
		SecurityHeaders: &newSec,
		LongLived:       &longLived,
	}))

	got, err := s.GetSession(ctx, sID)
	require.NoError(t, err)
	require.Equal(t, uint16(404), got.Code)
	require.Equal(t, "renamed", got.Slug)
	require.Equal(t, "grp", got.GroupName)
	require.Equal(t, "{{.Method}}", got.ResponseScript)
	require.Equal(t, newSec, got.SecurityHeaders)
	require.True(t, got.LongLived)

	// old slug no longer resolves; new one does
	_, err = s.GetSessionBySlug(ctx, "orig")
	require.ErrorIs(t, err, storage.ErrNotFound)
	_, err = s.GetSessionBySlug(ctx, "renamed")
	require.NoError(t, err)

	// empty patch on missing session -> not found
	require.ErrorIs(t, s.UpdateSession(ctx, "missing", storage.SessionPatch{}), storage.ErrSessionNotFound)

	// slug conflict on update
	_, err = s.NewSession(ctx, storage.Session{Slug: "taken"})
	require.NoError(t, err)

	conflictSlug := "taken"
	require.ErrorIs(t, s.UpdateSession(ctx, sID, storage.SessionPatch{Slug: &conflictSlug}), storage.ErrSlugConflict)
}

func TestSQLite_ExpiryVsLongLived(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		ft  = newFakeTime(t)
		s   = newSQLite(t, time.Minute, 16,
			storage.WithSQLiteTimeNow(ft.Get),
			storage.WithSQLiteExtractor(jsonStubExtractor),
		)
	)

	mortal, err := s.NewSession(ctx, storage.Session{Slug: "mortal"})
	require.NoError(t, err)
	immortal, err := s.NewSession(ctx, storage.Session{Slug: "immortal", LongLived: true})
	require.NoError(t, err)

	_, err = s.NewRequest(ctx, mortal, storage.Request{Body: []byte(`{"trackingId":"M1"}`)})
	require.NoError(t, err)
	_, err = s.NewRequest(ctx, immortal, storage.Request{Body: []byte(`{"trackingId":"I1"}`)})
	require.NoError(t, err)

	ft.Add(2 * time.Minute) // both past the 1-minute TTL

	// mortal is gone everywhere
	_, err = s.GetSession(ctx, mortal)
	require.ErrorIs(t, err, storage.ErrSessionNotFound)
	_, err = s.GetSessionBySlug(ctx, "mortal")
	require.ErrorIs(t, err, storage.ErrNotFound)

	// immortal survives
	got, err := s.GetSession(ctx, immortal)
	require.NoError(t, err)
	require.True(t, got.LongLived)

	// listing only shows the long-lived session
	list, err := s.ListSessions(ctx, storage.SessionFilter{})
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, immortal, list[0].ID)

	// search skips expired sessions' identifiers
	m, err := s.SearchRequests(ctx, storage.IdentifierQuery{Key: "trackingId", Value: "M1"})
	require.NoError(t, err)
	require.Empty(t, m)

	m, err = s.SearchRequests(ctx, storage.IdentifierQuery{Key: "trackingId", Value: "I1"})
	require.NoError(t, err)
	require.Len(t, m, 1)
}

func TestSQLite_MaxRequestsEviction(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		ft  = newFakeTime(t)
		s   = newSQLite(t, time.Hour, 2,
			storage.WithSQLiteTimeNow(ft.Get),
			storage.WithSQLiteExtractor(jsonStubExtractor),
		)
	)

	sID, err := s.NewSession(ctx, storage.Session{Slug: "evict"})
	require.NoError(t, err)

	r1, err := s.NewRequest(ctx, sID, storage.Request{Body: []byte(`{"trackingId":"R1"}`)})
	require.NoError(t, err)

	ft.Add(time.Millisecond)

	r2, err := s.NewRequest(ctx, sID, storage.Request{Body: []byte(`{"trackingId":"R2"}`)})
	require.NoError(t, err)

	ft.Add(time.Millisecond)

	r3, err := s.NewRequest(ctx, sID, storage.Request{Body: []byte(`{"trackingId":"R3"}`)})
	require.NoError(t, err)

	all, err := s.GetAllRequests(ctx, sID)
	require.NoError(t, err)
	require.Len(t, all, 2) // limit enforced
	require.NotContains(t, all, r1)
	require.Contains(t, all, r2)
	require.Contains(t, all, r3)

	// evicted request's identifiers must be gone too (CASCADE)
	m, err := s.SearchRequests(ctx, storage.IdentifierQuery{Key: "trackingId", Value: "R1"})
	require.NoError(t, err)
	require.Empty(t, m)

	m, err = s.SearchRequests(ctx, storage.IdentifierQuery{Key: "trackingId", Value: "R3"})
	require.NoError(t, err)
	require.Len(t, m, 1)
}

func TestSQLite_Close(t *testing.T) {
	t.Parallel()

	var ctx = context.Background()

	dsn := "file:" + filepath.Join(t.TempDir(), "close.db")
	impl, err := storage.NewSQLite(ctx, dsn, time.Minute, 1)
	require.NoError(t, err)

	require.NoError(t, impl.Close())
	require.ErrorIs(t, impl.Close(), storage.ErrClosed) // second close

	_, err = impl.NewSession(ctx, storage.Session{})
	require.ErrorIs(t, err, storage.ErrClosed)

	_, err = impl.GetSession(ctx, "foo")
	require.ErrorIs(t, err, storage.ErrClosed)

	_, err = impl.NewRequest(ctx, "foo", storage.Request{})
	require.ErrorIs(t, err, storage.ErrClosed)
}

func TestSQLite_Session_CreateReadDelete(t *testing.T) {
	t.Parallel()

	var ft = newFakeTime(t)

	testSessionCreateReadDelete(t,
		func(sTTL time.Duration, maxReq uint32) storage.Storage {
			dsn := "file:" + filepath.Join(t.TempDir(), "s.db")
			impl, err := storage.NewSQLite(context.Background(), dsn, sTTL, maxReq, storage.WithSQLiteTimeNow(ft.Get))
			require.NoError(t, err)

			return impl
		},
		func(d time.Duration) { ft.Add(d) },
		ft.Get,
	)
}

func TestSQLite_Request_CreateReadDelete(t *testing.T) {
	t.Parallel()

	var ft = newFakeTime(t)

	testRequestCreateReadDelete(t,
		func(sTTL time.Duration, maxReq uint32) storage.Storage {
			dsn := "file:" + filepath.Join(t.TempDir(), "r.db")
			impl, err := storage.NewSQLite(context.Background(), dsn, sTTL, maxReq, storage.WithSQLiteTimeNow(ft.Get))
			require.NoError(t, err)

			return impl
		},
		func(d time.Duration) { ft.Add(d) },
	)
}

func TestSQLite_RaceProvocation(t *testing.T) {
	t.Parallel()

	testRaceProvocation(t, func(sTTL time.Duration, maxReq uint32) storage.Storage {
		dsn := "file:" + filepath.Join(t.TempDir(), "race.db")
		impl, err := storage.NewSQLite(context.Background(), dsn, sTTL, maxReq,
			storage.WithSQLiteCleanupInterval(time.Millisecond))
		require.NoError(t, err)

		return impl
	})
}

func TestSQLite_ListRecentIdentifiers(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		ft  = newFakeTime(t)
		s   = newSQLite(t, 7*24*time.Hour, 128,
			storage.WithSQLiteTimeNow(ft.Get),
			storage.WithSQLiteExtractor(jsonStubExtractor),
		)
	)

	sID, err := s.NewSession(ctx, storage.Session{Code: 200, Slug: "my-app"})
	require.NoError(t, err)

	captureT := ft.Get()

	rID, err := s.NewRequest(ctx, sID, storage.Request{
		Method: "POST",
		Body:   []byte(`{"trackingId":"ABC123"}`),
		URL:    "/my-app",
	})
	require.NoError(t, err)

	// a cutoff one hour before the capture includes the identifier
	refs, err := s.ListRecentIdentifiers(ctx, captureT.Add(-time.Hour).UnixMilli())
	require.NoError(t, err)
	require.Len(t, refs, 1)
	require.Equal(t, "trackingid", refs[0].Key) // normalized to lower-case
	require.Equal(t, "ABC123", refs[0].Value)
	require.Equal(t, sID, refs[0].SessionID)
	require.Equal(t, "my-app", refs[0].SessionSlug)
	require.Equal(t, rID, refs[0].RequestID)
	require.Equal(t, captureT.UnixMilli(), refs[0].CapturedAtUnixMilli)

	// a cutoff after the capture time excludes anything older than the window
	none, err := s.ListRecentIdentifiers(ctx, captureT.Add(time.Hour).UnixMilli())
	require.NoError(t, err)
	require.Empty(t, none)
}

// TestSQLite_PragmaSynchronousNormal verifies that every connection opened by the
// SQLite driver has synchronous=NORMAL (value 1) applied. With WAL this means
// commits skip the per-transaction fsync — durable across app crashes, appropriate
// for a webhook-testing tool.
func TestSQLite_PragmaSynchronousNormal(t *testing.T) {
	t.Parallel()

	s := newSQLite(t, time.Hour, 0)

	var syncVal int

	err := s.DB().QueryRowContext(context.Background(), `PRAGMA synchronous`).Scan(&syncVal)
	require.NoError(t, err)
	require.Equal(t, 1, syncVal, "expected synchronous=NORMAL (1), got %d", syncVal)
}
