package storage_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite" // register the "sqlite" driver for the raw-DB migration test

	"gh.tarampamp.am/webhook-tester/v2/internal/storage"
)

// testListRequestsAfterAndSeq exercises the FIFO events-fetch contract (ListRequestsAfter)
// and the durable, never-reused Seq guarantee. It runs for every driver that supports a
// monotonic sequence (sqlite, inmemory, fs).
func testListRequestsAfterAndSeq(
	t *testing.T,
	newImpl func(sessionTTL time.Duration, maxRequests uint32) storage.Storage,
	sleep func(time.Duration),
) {
	t.Helper()

	var ctx = context.Background()

	t.Run("FIFO order, after cursor and limit", func(t *testing.T) {
		t.Parallel()

		var impl = newImpl(time.Minute, 100)
		defer func() { _ = toCloser(impl).Close() }()

		sID, err := impl.NewSession(ctx, storage.Session{})
		require.NoError(t, err)

		const n = 5
		for i := range n {
			_, rErr := impl.NewRequest(ctx, sID, storage.Request{Method: "POST", Body: []byte{byte('a' + i)}})
			require.NoError(t, rErr)

			sleep(time.Millisecond) // distinct, ordered capture timestamps
		}

		// page 1: after=0, limit=2 -> the two OLDEST, FIFO (ascending seq)
		page1, err := impl.ListRequestsAfter(ctx, sID, 0, 2)
		require.NoError(t, err)
		require.Len(t, page1, 2)
		require.Less(t, page1[0].Seq, page1[1].Seq)
		require.NotEmpty(t, page1[0].ID, "request ID must be populated on read")
		require.Equal(t, []byte{'a'}, page1[0].Body) // oldest first
		require.Equal(t, []byte{'b'}, page1[1].Body)

		// page 2: poll with the previous page's last seq -> only newer events, no overlap
		page2, err := impl.ListRequestsAfter(ctx, sID, page1[1].Seq, 2)
		require.NoError(t, err)
		require.Len(t, page2, 2)
		require.Greater(t, page2[0].Seq, page1[1].Seq)
		require.Equal(t, []byte{'c'}, page2[0].Body)
		require.Equal(t, []byte{'d'}, page2[1].Body)

		// page 3: the remainder
		page3, err := impl.ListRequestsAfter(ctx, sID, page2[1].Seq, 2)
		require.NoError(t, err)
		require.Len(t, page3, 1)
		require.Equal(t, []byte{'e'}, page3[0].Body)

		// beyond the end -> empty (no skip, no duplicate)
		page4, err := impl.ListRequestsAfter(ctx, sID, page3[0].Seq, 2)
		require.NoError(t, err)
		require.Empty(t, page4)

		// after=0 with a large limit returns everything in strict FIFO order
		all, err := impl.ListRequestsAfter(ctx, sID, 0, 1000)
		require.NoError(t, err)
		require.Len(t, all, n)

		for i := 1; i < len(all); i++ {
			require.Greater(t, all[i].Seq, all[i-1].Seq)
		}
	})

	t.Run("session not found", func(t *testing.T) {
		t.Parallel()

		var impl = newImpl(time.Minute, 100)
		defer func() { _ = toCloser(impl).Close() }()

		_, err := impl.ListRequestsAfter(ctx, "missing", 0, 10)
		require.ErrorIs(t, err, storage.ErrSessionNotFound)
	})

	t.Run("seq is monotonic and never reused across eviction and full delete", func(t *testing.T) {
		t.Parallel()

		const maxReq = 3

		var impl = newImpl(time.Hour, maxReq)
		defer func() { _ = toCloser(impl).Close() }()

		sID, err := impl.NewSession(ctx, storage.Session{})
		require.NoError(t, err)

		// insert more than maxReq so the OLDEST get evicted
		for i := range 6 {
			_, rErr := impl.NewRequest(ctx, sID, storage.Request{Method: "POST", Body: []byte{byte('0' + i)}})
			require.NoError(t, rErr)

			sleep(time.Millisecond)
		}

		remaining, err := impl.ListRequestsAfter(ctx, sID, 0, 1000)
		require.NoError(t, err)
		require.Len(t, remaining, maxReq) // eviction enforced

		var maxSeq int64

		for i, r := range remaining {
			if i > 0 {
				require.Greater(t, r.Seq, remaining[i-1].Seq) // strictly increasing
			}

			require.Greater(t, r.Seq, int64(0))

			maxSeq = r.Seq
		}

		// further inserts keep climbing; the counter is never rewound by eviction
		for i := range 3 {
			_, rErr := impl.NewRequest(ctx, sID, storage.Request{Method: "POST", Body: []byte{byte('x' + i)}})
			require.NoError(t, rErr)

			sleep(time.Millisecond)
		}

		grown, err := impl.ListRequestsAfter(ctx, sID, 0, 1000)
		require.NoError(t, err)
		require.Len(t, grown, maxReq)

		newMax := grown[len(grown)-1].Seq
		require.Greater(t, newMax, maxSeq, "seq must keep increasing across eviction")

		// a fresh request AFTER a full wipe must still have a higher seq (counter durability)
		require.NoError(t, impl.DeleteAllRequests(ctx, sID))

		_, err = impl.NewRequest(ctx, sID, storage.Request{Method: "POST"})
		require.NoError(t, err)

		post, err := impl.ListRequestsAfter(ctx, sID, 0, 10)
		require.NoError(t, err)
		require.Len(t, post, 1)
		require.Greater(t, post[0].Seq, newMax, "seq must keep climbing after a full delete")
	})
}

func TestInMemory_ListRequestsAfterAndSeq(t *testing.T) {
	t.Parallel()

	var ft = newFakeTime(t)

	testListRequestsAfterAndSeq(t,
		func(sTTL time.Duration, maxReq uint32) storage.Storage {
			return storage.NewInMemory(sTTL, maxReq, storage.WithInMemoryTimeNow(ft.Get))
		},
		func(d time.Duration) { ft.Add(d) },
	)
}

func TestFS_ListRequestsAfterAndSeq(t *testing.T) {
	t.Parallel()

	var ft = newFakeTime(t)

	testListRequestsAfterAndSeq(t,
		func(sTTL time.Duration, maxReq uint32) storage.Storage {
			return storage.NewFS(t.TempDir(), sTTL, maxReq, storage.WithFSTimeNow(ft.Get))
		},
		func(d time.Duration) { ft.Add(d) },
	)
}

func TestSQLite_ListRequestsAfterAndSeq(t *testing.T) {
	t.Parallel()

	var ft = newFakeTime(t)

	testListRequestsAfterAndSeq(t,
		func(sTTL time.Duration, maxReq uint32) storage.Storage {
			dsn := "file:" + filepath.Join(t.TempDir(), "seq.db")

			impl, err := storage.NewSQLite(context.Background(), dsn, sTTL, maxReq, storage.WithSQLiteTimeNow(ft.Get))
			require.NoError(t, err)

			return impl
		},
		func(d time.Duration) { ft.Add(d) },
	)
}

// TestRedis_ListRequestsAfter_Unsupported documents that the Redis driver has no durable
// sequence and reports ListRequestsAfter as unsupported (consistent with ListSessions/SearchRequests).
func TestRedis_ListRequestsAfter_Unsupported(t *testing.T) {
	t.Parallel()

	// nil client is fine: the method must short-circuit before touching it.
	s := storage.NewRedis(nil, time.Minute, 100)

	_, err := s.ListRequestsAfter(context.Background(), "any", 0, 10)
	require.ErrorIs(t, err, storage.ErrSearchUnsupported)
}

// testListRequestsPage exercises the cursor-paginated, NEWEST-first requests-list contract
// (ListRequestsPage). It runs for every driver that supports a monotonic sequence (sqlite,
// inmemory, fs).
func testListRequestsPage(
	t *testing.T,
	newImpl func(sessionTTL time.Duration, maxRequests uint32) storage.Storage,
	sleep func(time.Duration),
) {
	t.Helper()

	var ctx = context.Background()

	t.Run("newest-first, before cursor, limit, full paging", func(t *testing.T) {
		t.Parallel()

		var impl = newImpl(time.Minute, 100)
		defer func() { _ = toCloser(impl).Close() }()

		sID, err := impl.NewSession(ctx, storage.Session{})
		require.NoError(t, err)

		const n = 5
		for i := range n {
			_, rErr := impl.NewRequest(ctx, sID, storage.Request{Method: "POST", Body: []byte{byte('a' + i)}})
			require.NoError(t, rErr)

			sleep(time.Millisecond) // distinct, ordered capture timestamps
		}

		// beforeSeq<=0 => the NEWEST page. limit=2 => the two newest, descending seq.
		page1, err := impl.ListRequestsPage(ctx, sID, 0, 2)
		require.NoError(t, err)
		require.Len(t, page1, 2)
		require.Greater(t, page1[0].Seq, page1[1].Seq) // newest first
		require.NotEmpty(t, page1[0].ID, "request ID must be populated on read")
		require.Equal(t, []byte{'e'}, page1[0].Body) // newest
		require.Equal(t, []byte{'d'}, page1[1].Body)

		// page 2: before = seq of the last (oldest) item of page1 -> the next two older, no overlap
		page2, err := impl.ListRequestsPage(ctx, sID, page1[1].Seq, 2)
		require.NoError(t, err)
		require.Len(t, page2, 2)
		require.Less(t, page2[0].Seq, page1[1].Seq)
		require.Equal(t, []byte{'c'}, page2[0].Body)
		require.Equal(t, []byte{'b'}, page2[1].Body)

		// page 3: the remainder
		page3, err := impl.ListRequestsPage(ctx, sID, page2[1].Seq, 2)
		require.NoError(t, err)
		require.Len(t, page3, 1)
		require.Equal(t, []byte{'a'}, page3[0].Body)

		// beyond the end -> empty (no skip, no duplicate)
		page4, err := impl.ListRequestsPage(ctx, sID, page3[0].Seq, 2)
		require.NoError(t, err)
		require.Empty(t, page4)

		// walk every page and assert each request is returned exactly once, strictly descending
		var (
			seen   = make(map[string]int)
			before int64 // 0 = newest
			total  int
		)

		for {
			pg, pErr := impl.ListRequestsPage(ctx, sID, before, 2)
			require.NoError(t, pErr)

			if len(pg) == 0 {
				break
			}

			for i, r := range pg {
				if i > 0 {
					require.Less(t, r.Seq, pg[i-1].Seq) // descending within a page
				}

				seen[r.ID]++
				total++
			}

			before = pg[len(pg)-1].Seq
		}

		require.Equal(t, n, total, "every request returned exactly once across pages (no gaps/dupes)")
		require.Len(t, seen, n)

		for id, c := range seen {
			require.Equalf(t, 1, c, "request %s returned more than once", id)
		}

		// a large limit returns everything newest-first
		all, err := impl.ListRequestsPage(ctx, sID, 0, 1000)
		require.NoError(t, err)
		require.Len(t, all, n)

		for i := 1; i < len(all); i++ {
			require.Less(t, all[i].Seq, all[i-1].Seq)
		}

		// limit is respected (and points at the newest)
		one, err := impl.ListRequestsPage(ctx, sID, 0, 1)
		require.NoError(t, err)
		require.Len(t, one, 1)
		require.Equal(t, []byte{'e'}, one[0].Body)
	})

	t.Run("session not found", func(t *testing.T) {
		t.Parallel()

		var impl = newImpl(time.Minute, 100)
		defer func() { _ = toCloser(impl).Close() }()

		_, err := impl.ListRequestsPage(ctx, "missing", 0, 10)
		require.ErrorIs(t, err, storage.ErrSessionNotFound)
	})
}

func TestInMemory_ListRequestsPage(t *testing.T) {
	t.Parallel()

	var ft = newFakeTime(t)

	testListRequestsPage(t,
		func(sTTL time.Duration, maxReq uint32) storage.Storage {
			return storage.NewInMemory(sTTL, maxReq, storage.WithInMemoryTimeNow(ft.Get))
		},
		func(d time.Duration) { ft.Add(d) },
	)
}

func TestFS_ListRequestsPage(t *testing.T) {
	t.Parallel()

	var ft = newFakeTime(t)

	testListRequestsPage(t,
		func(sTTL time.Duration, maxReq uint32) storage.Storage {
			return storage.NewFS(t.TempDir(), sTTL, maxReq, storage.WithFSTimeNow(ft.Get))
		},
		func(d time.Duration) { ft.Add(d) },
	)
}

func TestSQLite_ListRequestsPage(t *testing.T) {
	t.Parallel()

	var ft = newFakeTime(t)

	testListRequestsPage(t,
		func(sTTL time.Duration, maxReq uint32) storage.Storage {
			dsn := "file:" + filepath.Join(t.TempDir(), "page.db")

			impl, err := storage.NewSQLite(context.Background(), dsn, sTTL, maxReq, storage.WithSQLiteTimeNow(ft.Get))
			require.NoError(t, err)

			return impl
		},
		func(d time.Duration) { ft.Add(d) },
	)
}

// TestRedis_ListRequestsPage_Unsupported documents that the Redis driver has no durable
// sequence and reports ListRequestsPage as unsupported (consistent with ListRequestsAfter).
func TestRedis_ListRequestsPage_Unsupported(t *testing.T) {
	t.Parallel()

	// nil client is fine: the method must short-circuit before touching it.
	s := storage.NewRedis(nil, time.Minute, 100)

	_, err := s.ListRequestsPage(context.Background(), "any", 0, 10)
	require.ErrorIs(t, err, storage.ErrSearchUnsupported)
}

// TestSQLite_RequestSeq_DurableAcrossReopen proves the durable counter survives a full
// process restart (close + reopen of the same database file): a consumer's stored cursor
// never silently rewinds.
func TestSQLite_RequestSeq_DurableAcrossReopen(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		dsn = "file:" + filepath.Join(t.TempDir(), "reopen.db")
		sID = "11111111-1111-1111-1111-111111111111"
	)

	var maxSeq int64

	{ // first "process": create a session and three requests
		s, err := storage.NewSQLite(ctx, dsn, time.Hour, 100)
		require.NoError(t, err)

		_, err = s.NewSession(ctx, storage.Session{}, sID)
		require.NoError(t, err)

		for range 3 {
			_, rErr := s.NewRequest(ctx, sID, storage.Request{Method: "POST"})
			require.NoError(t, rErr)
		}

		reqs, err := s.ListRequestsAfter(ctx, sID, 0, 100)
		require.NoError(t, err)
		require.Len(t, reqs, 3)

		maxSeq = reqs[len(reqs)-1].Seq
		require.Greater(t, maxSeq, int64(0))

		require.NoError(t, s.Close())
	}

	{ // second "process": reopen the SAME file
		s, err := storage.NewSQLite(ctx, dsn, time.Hour, 100)
		require.NoError(t, err)

		defer func() { _ = s.Close() }()

		_, err = s.GetSession(ctx, sID) // data persisted
		require.NoError(t, err)

		_, err = s.NewRequest(ctx, sID, storage.Request{Method: "POST"})
		require.NoError(t, err)

		reqs, err := s.ListRequestsAfter(ctx, sID, maxSeq, 100)
		require.NoError(t, err)
		require.Len(t, reqs, 1)
		require.Greater(t, reqs[0].Seq, maxSeq, "durable counter must not rewind after reopen")
	}
}

// TestSQLite_RequestSeq_MigratesExistingDB proves the idempotent migration upgrades a
// pre-existing database (no seq column / no counters table): existing rows are backfilled
// in capture order and the counter is seeded above the current max.
func TestSQLite_RequestSeq_MigratesExistingDB(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		dsn = "file:" + filepath.Join(t.TempDir(), "old.db")
	)

	{ // build an "old" database WITHOUT the seq column or counters table
		raw, err := sql.Open("sqlite", dsn)
		require.NoError(t, err)

		_, err = raw.ExecContext(ctx, `
CREATE TABLE sessions (
  id TEXT PRIMARY KEY, slug TEXT NOT NULL DEFAULT '', group_name TEXT NOT NULL DEFAULT '',
  code INTEGER NOT NULL DEFAULT 200, headers_json TEXT NOT NULL DEFAULT '[]', response_body BLOB,
  delay_millis INTEGER NOT NULL DEFAULT 0, response_script TEXT NOT NULL DEFAULT '',
  security_headers TEXT NOT NULL DEFAULT '[]', forward_url TEXT NOT NULL DEFAULT '',
  long_lived INTEGER NOT NULL DEFAULT 0, created_at_ms INTEGER NOT NULL, expires_at_ms INTEGER NOT NULL);
CREATE TABLE requests (
  id TEXT PRIMARY KEY, session_id TEXT NOT NULL, method TEXT NOT NULL, body BLOB,
  headers_json TEXT NOT NULL DEFAULT '[]', url TEXT NOT NULL, client_addr TEXT NOT NULL, created_at_ms INTEGER NOT NULL);`)
		require.NoError(t, err)

		now := time.Now().UnixMilli()

		_, err = raw.ExecContext(ctx,
			`INSERT INTO sessions (id, created_at_ms, expires_at_ms) VALUES (?,?,?)`, "s1", now, now+3_600_000)
		require.NoError(t, err)

		const insReq = `INSERT INTO requests (id, session_id, method, url, client_addr, created_at_ms) VALUES (?,?,?,?,?,?)`

		_, err = raw.ExecContext(ctx, insReq, "r-old-1", "s1", "GET", "/a", "1.1.1.1", now)
		require.NoError(t, err)
		_, err = raw.ExecContext(ctx, insReq, "r-old-2", "s1", "GET", "/b", "1.1.1.1", now+1)
		require.NoError(t, err)

		require.NoError(t, raw.Close())
	}

	// open with the real driver -> migration runs
	s, err := storage.NewSQLite(ctx, dsn, time.Hour, 100)
	require.NoError(t, err)

	defer func() { _ = s.Close() }()

	reqs, err := s.ListRequestsAfter(ctx, "s1", 0, 100)
	require.NoError(t, err)
	require.Len(t, reqs, 2)

	// backfilled in created_at order: r-old-1 (older) gets the lower seq
	require.Equal(t, "r-old-1", reqs[0].ID)
	require.Equal(t, "r-old-2", reqs[1].ID)
	require.Less(t, reqs[0].Seq, reqs[1].Seq)
	require.Greater(t, reqs[0].Seq, int64(0))

	maxOld := reqs[1].Seq

	// new requests must continue above the backfilled max (counter seeded correctly)
	_, err = s.NewRequest(ctx, "s1", storage.Request{Method: "POST"})
	require.NoError(t, err)

	after, err := s.ListRequestsAfter(ctx, "s1", maxOld, 100)
	require.NoError(t, err)
	require.Len(t, after, 1)
	require.Greater(t, after[0].Seq, maxOld)
}
