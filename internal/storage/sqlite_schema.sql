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
  expires_at_ms    INTEGER NOT NULL                 -- ignored when long_lived = 1
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
  created_at_ms INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_requests_session_time ON requests(session_id, created_at_ms DESC);

CREATE TABLE IF NOT EXISTS request_identifiers (
  request_id    TEXT NOT NULL REFERENCES requests(id) ON DELETE CASCADE,
  session_id    TEXT NOT NULL,
  key           TEXT NOT NULL,                       -- normalized lower-case, e.g. "trackingid"
  value         TEXT NOT NULL,
  created_at_ms INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_ident_kv  ON request_identifiers(key, value);
CREATE INDEX IF NOT EXISTS idx_ident_skv ON request_identifiers(session_id, key, value);
