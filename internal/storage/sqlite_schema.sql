-- SQLite schema for the webhook-tester storage driver (design spec §5).
--
-- Connection pragmas are applied via the DSN by NewSQLite (every pooled
-- connection gets them), not here, because PRAGMA journal_mode/foreign_keys are
-- per-connection / cannot run inside the implicit transaction of a batch exec:
--     PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;
--
-- This file is embedded via //go:embed and executed once at startup. All objects
-- use IF NOT EXISTS so re-opening an existing database is a no-op.

CREATE TABLE IF NOT EXISTS sessions (
  id               TEXT PRIMARY KEY,                -- internal stable id (uuid)
  slug             TEXT NOT NULL DEFAULT '',        -- public identifier used in URLs ('' = none)
  group_name       TEXT NOT NULL DEFAULT '',
  code             INTEGER NOT NULL DEFAULT 200,
  headers_json     TEXT NOT NULL DEFAULT '[]',      -- []HttpHeader (static response headers)
  response_body    BLOB,
  delay_millis     INTEGER NOT NULL DEFAULT 0,
  response_script  TEXT NOT NULL DEFAULT '',        -- text/template; empty = static response
  security_headers TEXT NOT NULL DEFAULT '[]',      -- []HttpHeader added to every webhook response
  forward_url      TEXT NOT NULL DEFAULT '',        -- default replay target
  long_lived       INTEGER NOT NULL DEFAULT 0,      -- 1 = never auto-expire
  created_at_ms    INTEGER NOT NULL,
  expires_at_ms    INTEGER NOT NULL,                -- ignored when long_lived = 1
  inbound_auth_header TEXT NOT NULL DEFAULT '',     -- inbound-auth header name ('' = public, no inbound auth)
  inbound_auth_value  TEXT NOT NULL DEFAULT ''      -- expected inbound-auth header value (secret)
);

CREATE INDEX IF NOT EXISTS idx_sessions_group ON sessions(group_name);

-- Partial unique index: slugs are unique when present, but many sessions may have
-- no slug (UUID back-compat / auto-created), so empty slugs are exempt.
CREATE UNIQUE INDEX IF NOT EXISTS idx_sessions_slug ON sessions(slug) WHERE slug <> '';

CREATE TABLE IF NOT EXISTS requests (
  id            TEXT PRIMARY KEY,
  session_id    TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  method        TEXT NOT NULL,
  body          BLOB,
  headers_json  TEXT NOT NULL DEFAULT '[]',
  url           TEXT NOT NULL,
  client_addr   TEXT NOT NULL,
  created_at_ms INTEGER NOT NULL,
  seq           INTEGER NOT NULL DEFAULT 0,      -- durable FIFO sequence (see counters / migrateRequestSeq)
  authorized    INTEGER NOT NULL DEFAULT 1       -- 0 = rejected by inbound auth (still captured); 1 = authorized
);

CREATE INDEX IF NOT EXISTS idx_requests_session_time ON requests(session_id, created_at_ms DESC);

-- NOTE: idx_requests_session_seq is created by the Go migration (migrateRequestSeq), not here,
-- because on a pre-existing database the requests table may not yet have the seq column when
-- this batch runs (CREATE TABLE IF NOT EXISTS is a no-op for an existing table).

-- Durable, never-reused monotonic counters. The 'request_seq' row backs Request.Seq: it is
-- bumped inside NewRequest's transaction and read back, so the sequence survives max-requests
-- eviction (which deletes low-seq rows) and full request wipes (this table is independent).
CREATE TABLE IF NOT EXISTS counters (
  name  TEXT PRIMARY KEY,
  value INTEGER NOT NULL
);

INSERT OR IGNORE INTO counters (name, value) VALUES ('request_seq', 0);

CREATE TABLE IF NOT EXISTS request_identifiers (
  request_id    TEXT NOT NULL REFERENCES requests(id) ON DELETE CASCADE,
  session_id    TEXT NOT NULL,
  key           TEXT NOT NULL,                       -- normalized lower-case, e.g. "trackingid"
  value         TEXT NOT NULL,
  created_at_ms INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_ident_kv  ON request_identifiers(key, value);
CREATE INDEX IF NOT EXISTS idx_ident_skv ON request_identifiers(session_id, key, value);
