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

// TestSQLite_InboundAuth_MigratesExistingDB proves the idempotent migration upgrades a
// pre-existing database (created before the inbound-auth feature): the new
// sessions.inbound_auth_header / sessions.inbound_auth_value columns and the
// requests.authorized column are added without data loss, and pre-existing requests
// default to authorized=true (DEFAULT 1), matching the "no inbound auth = public" history.
func TestSQLite_InboundAuth_MigratesExistingDB(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		dsn = "file:" + filepath.Join(t.TempDir(), "old-auth.db")
	)

	{ // build an "old" database WITHOUT the inbound-auth columns / authorized column
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

		_, err = raw.ExecContext(ctx,
			`INSERT INTO requests (id, session_id, method, url, client_addr, created_at_ms) VALUES (?,?,?,?,?,?)`,
			"r-old-1", "s1", "GET", "/a", "1.1.1.1", now)
		require.NoError(t, err)

		require.NoError(t, raw.Close())
	}

	// open with the real driver -> migration runs (adds columns)
	s, err := storage.NewSQLite(ctx, dsn, time.Hour, 100)
	require.NoError(t, err)

	defer func() { _ = s.Close() }()

	// the pre-existing session reads back with empty (disabled) inbound auth
	sess, err := s.GetSession(ctx, "s1")
	require.NoError(t, err)
	require.Empty(t, sess.InboundAuthHeader)
	require.Empty(t, sess.InboundAuthValue)

	// the pre-existing request defaults to authorized=true (column DEFAULT 1)
	got, err := s.GetRequest(ctx, "s1", "r-old-1")
	require.NoError(t, err)
	require.True(t, got.Authorized, "pre-existing requests must migrate to authorized=true")

	// inbound auth is fully writable after migration
	hdr, val := "X-Token", "abc"
	require.NoError(t, s.UpdateSession(ctx, "s1", storage.SessionPatch{InboundAuthHeader: &hdr, InboundAuthValue: &val}))

	sess, err = s.GetSession(ctx, "s1")
	require.NoError(t, err)
	require.Equal(t, "X-Token", sess.InboundAuthHeader)
	require.Equal(t, "abc", sess.InboundAuthValue)

	// a newly captured rejected request round-trips authorized=false
	rID, err := s.NewRequest(ctx, "s1", storage.Request{Method: "POST", Authorized: false})
	require.NoError(t, err)

	rejected, err := s.GetRequest(ctx, "s1", rID)
	require.NoError(t, err)
	require.False(t, rejected.Authorized)
}
