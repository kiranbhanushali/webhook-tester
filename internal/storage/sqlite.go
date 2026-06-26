package storage

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

//go:embed sqlite_schema.sql
var sqliteSchema string

// ErrSlugConflict is returned when a session is created or updated with a slug that
// is already in use by another (non-expired) session.
var ErrSlugConflict = errors.New("slug already in use")

const (
	// defaultSearchLimit caps SearchRequests results when the query sets no Limit.
	defaultSearchLimit = 1000
	// defaultSQLiteCleanup is how often the janitor purges expired sessions.
	defaultSQLiteCleanup = time.Minute
)

// Identifier is a single searchable key/value pair extracted from a captured
// request (JSON body field, header, or query parameter). Keys are normalized to
// lower-case before they are written to the request_identifiers index.
type Identifier struct {
	Key   string
	Value string
}

// IdentifierExtractor extracts searchable identifiers from a captured request.
// The real implementation (the configurable JSON/header/query walker) is provided
// by Task 5 and wired in Task 10; the SQLite driver only depends on this seam.
// It is invoked by NewRequest, inside the same transaction as the request insert,
// when set. A nil extractor means no identifiers are recorded.
type IdentifierExtractor func(body []byte, headers []HttpHeader, rawURL string) []Identifier

// SQLite is the pure-Go (modernc.org/sqlite) storage driver. It is the project's
// default backend and the only driver with an indexed identifier search.
type SQLite struct {
	db          *sql.DB
	sessionTTL  time.Duration
	maxRequests uint32
	timeNow     TimeFunc

	// Extractor, when non-nil, is called inside NewRequest's transaction to populate
	// the request_identifiers search index. Defaults to nil (no identifiers recorded).
	Extractor IdentifierExtractor

	cleanupInterval time.Duration
	close           chan struct{}
	closed          atomic.Bool
	wg              sync.WaitGroup
}

var ( // ensure interface implementation
	_ Storage   = (*SQLite)(nil)
	_ io.Closer = (*SQLite)(nil)
)

// SQLiteOption customizes a SQLite driver at construction time.
type SQLiteOption func(*SQLite)

// WithSQLiteTimeNow sets the function that returns the current time (used in tests).
func WithSQLiteTimeNow(fn TimeFunc) SQLiteOption { return func(s *SQLite) { s.timeNow = fn } }

// WithSQLiteCleanupInterval sets how often the expired-session janitor runs.
// A non-positive value disables the janitor (read-time expiry still applies).
func WithSQLiteCleanupInterval(v time.Duration) SQLiteOption {
	return func(s *SQLite) { s.cleanupInterval = v }
}

// WithSQLiteExtractor sets the identifier extractor used to populate the search index.
func WithSQLiteExtractor(fn IdentifierExtractor) SQLiteOption {
	return func(s *SQLite) { s.Extractor = fn }
}

// NewSQLite opens (or creates) a SQLite database at dsn and applies the schema.
// sessionTTL is the time-to-live for non-long-lived sessions; maxRequests caps the
// number of stored requests per session (0 = unlimited). Call Close to release it.
func NewSQLite(
	ctx context.Context,
	dsn string,
	sessionTTL time.Duration,
	maxRequests uint32,
	opts ...SQLiteOption,
) (*SQLite, error) {
	db, err := sql.Open("sqlite", ensurePragmas(dsn))
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	if err = db.PingContext(ctx); err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	if _, err = db.ExecContext(ctx, sqliteSchema); err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("init schema: %w", err)
	}

	var s = SQLite{
		db:              db,
		sessionTTL:      sessionTTL,
		maxRequests:     maxRequests,
		timeNow:         defaultTimeFunc,
		cleanupInterval: defaultSQLiteCleanup,
		close:           make(chan struct{}),
	}

	for _, opt := range opts {
		opt(&s)
	}

	if s.cleanupInterval > 0 {
		//nolint:contextcheck // janitor is intentionally detached from the constructor ctx
		s.startJanitor()
	}

	return &s, nil
}

// startJanitor launches the background expired-session sweeper. It lives in its own
// (ctx-free) method so the long-lived goroutine is not tied to a request context.
func (s *SQLite) startJanitor() {
	s.wg.Add(1)

	go s.cleanup()
}

// ensurePragmas appends the required connection pragmas to the DSN when absent.
func ensurePragmas(dsn string) string {
	var required = []struct{ name, param string }{
		{"busy_timeout", "_pragma=busy_timeout(5000)"},
		{"journal_mode", "_pragma=journal_mode(WAL)"},
		{"foreign_keys", "_pragma=foreign_keys(ON)"},
	}

	var add []string

	for _, p := range required {
		if !strings.Contains(dsn, "_pragma="+p.name) {
			add = append(add, p.param)
		}
	}

	if len(add) == 0 {
		return dsn
	}

	var sep = "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}

	return dsn + sep + strings.Join(add, "&")
}

// newID generates a new (unique) ID.
func (*SQLite) newID() string { return uuid.New().String() }

// isOpenAndNotDone checks the storage is open and the context is not done.
func (s *SQLite) isOpenAndNotDone(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	} else if s.closed.Load() {
		return ErrClosed
	}

	return nil
}

// cleanup periodically deletes expired (non-long-lived) sessions; CASCADE removes
// their requests and identifiers. It runs for the lifetime of the driver (not tied
// to any request-scoped context) and is stopped by Close.
func (s *SQLite) cleanup() {
	defer s.wg.Done()

	var t = time.NewTicker(s.cleanupInterval)
	defer t.Stop()

	for {
		select {
		case <-s.close:
			return
		case <-t.C:
			_, _ = s.db.ExecContext(context.Background(),
				`DELETE FROM sessions WHERE long_lived = 0 AND expires_at_ms <= ?`, s.timeNow().UnixMilli())
		}
	}
}

// Close stops the janitor and closes the database. Subsequent calls return ErrClosed.
func (s *SQLite) Close() error {
	if !s.closed.CompareAndSwap(false, true) {
		return ErrClosed
	}

	close(s.close)
	s.wg.Wait()

	return s.db.Close()
}

const sessionCols = `id, slug, group_name, code, headers_json, response_body, delay_millis, ` +
	`response_script, security_headers, forward_url, long_lived, created_at_ms, expires_at_ms`

const requestCols = `id, method, body, headers_json, url, client_addr, created_at_ms`

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface{ Scan(dest ...any) error }

func (s *SQLite) NewSession(ctx context.Context, session Session, id ...string) (string, error) {
	if err := s.isOpenAndNotDone(ctx); err != nil {
		return "", err
	}

	var sID string
	if len(id) > 0 {
		if id[0] == "" {
			return "", errors.New("empty session ID")
		}

		sID = id[0]
	} else {
		sID = s.newID()
	}

	var now = s.timeNow()

	session.CreatedAtUnixMilli, session.ExpiresAt = now.UnixMilli(), now.Add(s.sessionTTL)

	headers, hErr := marshalHeaders(session.Headers)
	if hErr != nil {
		return "", hErr
	}

	security, sErr := marshalHeaders(session.SecurityHeaders)
	if sErr != nil {
		return "", sErr
	}

	const q = `INSERT INTO sessions (` + sessionCols + `) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`

	_, err := s.db.ExecContext(ctx, q,
		sID, session.Slug, session.GroupName, session.Code, headers, session.ResponseBody,
		session.Delay.Milliseconds(), session.ResponseScript, security, session.ForwardURL,
		boolToInt(session.LongLived), session.CreatedAtUnixMilli, session.ExpiresAt.UnixMilli())
	if err != nil {
		if isUniqueViolation(err) {
			if session.Slug != "" && strings.Contains(err.Error(), "slug") {
				return "", fmt.Errorf("%w: %q", ErrSlugConflict, session.Slug)
			}

			return "", fmt.Errorf("session %s already exists", sID)
		}

		return "", fmt.Errorf("insert session: %w", err)
	}

	return sID, nil
}

func (s *SQLite) GetSession(ctx context.Context, sID string) (*Session, error) {
	if err := s.isOpenAndNotDone(ctx); err != nil {
		return nil, err
	}

	const q = `SELECT ` + sessionCols + ` FROM sessions WHERE id = ? AND (long_lived = 1 OR expires_at_ms > ?)`

	sess, err := scanSession(s.db.QueryRowContext(ctx, q, sID, s.timeNow().UnixMilli()))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionNotFound
	}

	return sess, err
}

func (s *SQLite) GetSessionBySlug(ctx context.Context, slug string) (*Session, error) {
	if err := s.isOpenAndNotDone(ctx); err != nil {
		return nil, err
	}

	if slug == "" {
		return nil, ErrNotFound
	}

	const q = `SELECT ` + sessionCols +
		` FROM sessions WHERE slug = ? AND slug <> '' AND (long_lived = 1 OR expires_at_ms > ?)`

	sess, err := scanSession(s.db.QueryRowContext(ctx, q, slug, s.timeNow().UnixMilli()))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}

	return sess, err
}

func (s *SQLite) AddSessionTTL(ctx context.Context, sID string, howMuch time.Duration) error {
	if err := s.isOpenAndNotDone(ctx); err != nil {
		return err
	}

	const q = `UPDATE sessions SET expires_at_ms = expires_at_ms + ? ` +
		`WHERE id = ? AND (long_lived = 1 OR expires_at_ms > ?)`

	res, err := s.db.ExecContext(ctx, q, howMuch.Milliseconds(), sID, s.timeNow().UnixMilli())
	if err != nil {
		return fmt.Errorf("add session ttl: %w", err)
	}

	if n, _ := res.RowsAffected(); n == 0 {
		return ErrSessionNotFound
	}

	return nil
}

func (s *SQLite) UpdateSession(ctx context.Context, sID string, patch SessionPatch) error {
	if err := s.isOpenAndNotDone(ctx); err != nil {
		return err
	}

	sets, args, err := buildSessionPatch(patch)
	if err != nil {
		return err
	}

	if len(sets) == 0 {
		return s.ensureSessionExists(ctx, sID)
	}

	//nolint:gosec // G202: only constant SQL fragments are concatenated; values are parameterized
	var q = `UPDATE sessions SET ` + strings.Join(sets, ", ") +
		` WHERE id = ? AND (long_lived = 1 OR expires_at_ms > ?)`

	args = append(args, sID, s.timeNow().UnixMilli())

	res, execErr := s.db.ExecContext(ctx, q, args...)
	if execErr != nil {
		if isUniqueViolation(execErr) && patch.Slug != nil {
			return fmt.Errorf("%w: %q", ErrSlugConflict, *patch.Slug)
		}

		return fmt.Errorf("update session: %w", execErr)
	}

	if n, _ := res.RowsAffected(); n == 0 {
		return ErrSessionNotFound
	}

	return nil
}

func (s *SQLite) DeleteSession(ctx context.Context, sID string) error {
	if err := s.isOpenAndNotDone(ctx); err != nil {
		return err
	}

	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sID)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}

	if n, _ := res.RowsAffected(); n == 0 {
		return ErrSessionNotFound
	}

	return nil
}

func (s *SQLite) NewRequest(ctx context.Context, sID string, r Request) (string, error) {
	if err := s.isOpenAndNotDone(ctx); err != nil {
		return "", err
	}

	if err := s.ensureSessionExists(ctx, sID); err != nil {
		return "", err
	}

	var rID = s.newID()

	r.CreatedAtUnixMilli = s.timeNow().UnixMilli()

	headers, hErr := marshalHeaders(r.Headers)
	if hErr != nil {
		return "", hErr
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once committed

	const insReq = `INSERT INTO requests (id, session_id, method, body, headers_json, url, client_addr, ` +
		`created_at_ms) VALUES (?,?,?,?,?,?,?,?)`

	if _, err = tx.ExecContext(ctx, insReq,
		rID, sID, r.Method, r.Body, headers, r.URL, r.ClientAddr, r.CreatedAtUnixMilli); err != nil {
		return "", fmt.Errorf("insert request: %w", err)
	}

	if err = s.insertIdentifiers(ctx, tx, sID, rID, r); err != nil {
		return "", err
	}

	if err = s.evictOldRequests(ctx, tx, sID); err != nil {
		return "", err
	}

	if err = tx.Commit(); err != nil {
		return "", fmt.Errorf("commit request: %w", err)
	}

	return rID, nil
}

// insertIdentifiers extracts and writes request_identifiers rows within tx.
func (s *SQLite) insertIdentifiers(ctx context.Context, tx *sql.Tx, sID, rID string, r Request) error {
	if s.Extractor == nil {
		return nil
	}

	const ins = `INSERT INTO request_identifiers (request_id, session_id, "key", value, created_at_ms) ` +
		`VALUES (?,?,?,?,?)`

	for _, ident := range s.Extractor(r.Body, r.Headers, r.URL) {
		var key = strings.ToLower(ident.Key)
		if key == "" {
			continue
		}

		if _, err := tx.ExecContext(ctx, ins, rID, sID, key, ident.Value, r.CreatedAtUnixMilli); err != nil {
			return fmt.Errorf("insert identifier: %w", err)
		}
	}

	return nil
}

// evictOldRequests removes the oldest requests beyond maxRequests within tx.
func (s *SQLite) evictOldRequests(ctx context.Context, tx *sql.Tx, sID string) error {
	if s.maxRequests == 0 {
		return nil
	}

	const q = `DELETE FROM requests WHERE session_id = ? AND id NOT IN (` +
		`SELECT id FROM requests WHERE session_id = ? ORDER BY created_at_ms DESC, id DESC LIMIT ?)`

	if _, err := tx.ExecContext(ctx, q, sID, sID, s.maxRequests); err != nil {
		return fmt.Errorf("evict old requests: %w", err)
	}

	return nil
}

func (s *SQLite) GetRequest(ctx context.Context, sID, rID string) (*Request, error) {
	if err := s.isOpenAndNotDone(ctx); err != nil {
		return nil, err
	}

	if err := s.ensureSessionExists(ctx, sID); err != nil {
		return nil, err
	}

	const q = `SELECT ` + requestCols + ` FROM requests WHERE id = ? AND session_id = ?`

	_, req, err := scanRequest(s.db.QueryRowContext(ctx, q, rID, sID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRequestNotFound
	}

	return req, err
}

func (s *SQLite) GetAllRequests(ctx context.Context, sID string) (map[string]Request, error) {
	if err := s.isOpenAndNotDone(ctx); err != nil {
		return nil, err
	}

	if err := s.ensureSessionExists(ctx, sID); err != nil {
		return nil, err
	}

	const q = `SELECT ` + requestCols + ` FROM requests WHERE session_id = ? ORDER BY created_at_ms DESC`

	rows, err := s.db.QueryContext(ctx, q, sID)
	if err != nil {
		return nil, fmt.Errorf("query requests: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var all = make(map[string]Request)

	for rows.Next() {
		rID, req, sErr := scanRequest(rows)
		if sErr != nil {
			return nil, sErr
		}

		all[rID] = *req
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate requests: %w", err)
	}

	return all, nil
}

func (s *SQLite) DeleteRequest(ctx context.Context, sID, rID string) error {
	if err := s.isOpenAndNotDone(ctx); err != nil {
		return err
	}

	if err := s.ensureSessionExists(ctx, sID); err != nil {
		return err
	}

	res, err := s.db.ExecContext(ctx, `DELETE FROM requests WHERE id = ? AND session_id = ?`, rID, sID)
	if err != nil {
		return fmt.Errorf("delete request: %w", err)
	}

	if n, _ := res.RowsAffected(); n == 0 {
		return ErrRequestNotFound
	}

	return nil
}

func (s *SQLite) DeleteAllRequests(ctx context.Context, sID string) error {
	if err := s.isOpenAndNotDone(ctx); err != nil {
		return err
	}

	if err := s.ensureSessionExists(ctx, sID); err != nil {
		return err
	}

	if _, err := s.db.ExecContext(ctx, `DELETE FROM requests WHERE session_id = ?`, sID); err != nil {
		return fmt.Errorf("delete all requests: %w", err)
	}

	return nil
}

func (s *SQLite) ListSessions(ctx context.Context, f SessionFilter) ([]SessionSummary, error) {
	if err := s.isOpenAndNotDone(ctx); err != nil {
		return nil, err
	}

	var (
		conds = []string{"(s.long_lived = 1 OR s.expires_at_ms > ?)"}
		args  = []any{s.timeNow().UnixMilli()}
	)

	if f.Group != "" {
		conds = append(conds, "s.group_name = ?")
		args = append(args, f.Group)
	}

	if f.Query != "" {
		conds = append(conds, "(instr(s.id, ?) > 0 OR instr(s.slug, ?) > 0 OR instr(s.group_name, ?) > 0)")
		args = append(args, f.Query, f.Query, f.Query)
	}

	//nolint:gosec // G202: only constant SQL fragments are concatenated; values are parameterized
	var q = `SELECT s.id, s.slug, s.group_name, s.code, s.created_at_ms, s.expires_at_ms, s.long_lived, ` +
		`COUNT(r.id), COALESCE(MAX(r.created_at_ms), 0) ` +
		`FROM sessions s LEFT JOIN requests r ON r.session_id = s.id ` +
		`WHERE ` + strings.Join(conds, " AND ") +
		` GROUP BY s.id ORDER BY s.created_at_ms DESC`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []SessionSummary

	for rows.Next() {
		var (
			sum       SessionSummary
			longLived int
		)

		if err = rows.Scan(&sum.ID, &sum.Slug, &sum.GroupName, &sum.Code, &sum.CreatedAtUnixMilli,
			&sum.ExpiresAtUnixMilli, &longLived, &sum.RequestsCount, &sum.LastRequestUnixMilli); err != nil {
			return nil, fmt.Errorf("scan session summary: %w", err)
		}

		sum.LongLived = longLived != 0
		out = append(out, sum)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sessions: %w", err)
	}

	return out, nil
}

func (s *SQLite) SearchRequests(ctx context.Context, query IdentifierQuery) ([]RequestMatch, error) {
	if err := s.isOpenAndNotDone(ctx); err != nil {
		return nil, err
	}

	var q, args = buildSearchQuery(query, s.timeNow().UnixMilli())

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("search requests: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []RequestMatch

	for rows.Next() {
		var m RequestMatch

		if err = rows.Scan(&m.SessionID, &m.SessionSlug, &m.RequestID, &m.Key, &m.Value,
			&m.CapturedAtUnixMilli); err != nil {
			return nil, fmt.Errorf("scan match: %w", err)
		}

		out = append(out, m)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate matches: %w", err)
	}

	return out, nil
}

// buildSearchQuery assembles the parameterized SearchRequests statement and args.
func buildSearchQuery(query IdentifierQuery, now int64) (string, []any) {
	var (
		conds = []string{"(s.long_lived = 1 OR s.expires_at_ms > ?)"}
		args  = []any{now}
	)

	if query.Key != "" {
		conds = append(conds, `ri."key" = ?`)
		args = append(args, strings.ToLower(query.Key))
	}

	if query.Match == IdentifierMatchPrefix {
		// Wildcard-free, BINARY (case-sensitive) prefix match: '_' and '%' in the value
		// are matched literally, unlike LIKE. Bind the prefix value twice.
		conds = append(conds, "substr(ri.value, 1, length(?)) = ?")
		args = append(args, query.Value, query.Value)
	} else {
		conds = append(conds, "ri.value = ?")
		args = append(args, query.Value)
	}

	if query.SessionID != "" {
		conds = append(conds, "ri.session_id = ?")
		args = append(args, query.SessionID)
	}

	if query.Group != "" {
		conds = append(conds, "s.group_name = ?")
		args = append(args, query.Group)
	}

	if query.FromUnixMilli > 0 {
		conds = append(conds, "ri.created_at_ms >= ?")
		args = append(args, query.FromUnixMilli)
	}

	if query.ToUnixMilli > 0 {
		conds = append(conds, "ri.created_at_ms <= ?")
		args = append(args, query.ToUnixMilli)
	}

	var limit = query.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}

	args = append(args, limit)

	// NOTE: q concatenates only constant SQL fragments; all values flow through args.
	var q = `SELECT ri.session_id, s.slug, ri.request_id, ri."key", ri.value, ri.created_at_ms ` +
		`FROM request_identifiers ri ` +
		`JOIN requests r ON r.id = ri.request_id ` +
		`JOIN sessions s ON s.id = ri.session_id ` +
		`WHERE ` + strings.Join(conds, " AND ") +
		` ORDER BY ri.created_at_ms DESC LIMIT ?`

	return q, args
}

// ensureSessionExists returns ErrSessionNotFound when the session is missing or expired.
func (s *SQLite) ensureSessionExists(ctx context.Context, sID string) error {
	const q = `SELECT 1 FROM sessions WHERE id = ? AND (long_lived = 1 OR expires_at_ms > ?)`

	var one int

	err := s.db.QueryRowContext(ctx, q, sID, s.timeNow().UnixMilli()).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSessionNotFound
	}

	return err
}

// buildSessionPatch turns a SessionPatch into SET clauses and their args.
func buildSessionPatch(patch SessionPatch) (sets []string, args []any, _ error) {
	var add = func(col string, v any) {
		sets = append(sets, col+" = ?")
		args = append(args, v)
	}

	if patch.Code != nil {
		add("code", *patch.Code)
	}

	if patch.Slug != nil {
		add("slug", *patch.Slug)
	}

	if patch.GroupName != nil {
		add("group_name", *patch.GroupName)
	}

	if patch.ResponseScript != nil {
		add("response_script", *patch.ResponseScript)
	}

	if patch.ForwardURL != nil {
		add("forward_url", *patch.ForwardURL)
	}

	if patch.ResponseBody != nil {
		add("response_body", *patch.ResponseBody)
	}

	if patch.Delay != nil {
		add("delay_millis", patch.Delay.Milliseconds())
	}

	if patch.LongLived != nil {
		add("long_lived", boolToInt(*patch.LongLived))
	}

	if patch.Headers != nil {
		h, err := marshalHeaders(*patch.Headers)
		if err != nil {
			return nil, nil, err
		}

		add("headers_json", h)
	}

	if patch.SecurityHeaders != nil {
		h, err := marshalHeaders(*patch.SecurityHeaders)
		if err != nil {
			return nil, nil, err
		}

		add("security_headers", h)
	}

	return sets, args, nil
}

// scanSession reads a full sessions row into a Session (the id column is discarded).
func scanSession(sc rowScanner) (*Session, error) {
	var (
		sess                                               Session
		id, slug, group, headersJSON, script, secJSON, fwd string
		code                                               uint16
		body                                               []byte
		delayMs, createdMs, expiresMs                      int64
		longLived                                          int
	)

	if err := sc.Scan(&id, &slug, &group, &code, &headersJSON, &body, &delayMs,
		&script, &secJSON, &fwd, &longLived, &createdMs, &expiresMs); err != nil {
		return nil, err
	}

	headers, err := unmarshalHeaders(headersJSON)
	if err != nil {
		return nil, err
	}

	security, err := unmarshalHeaders(secJSON)
	if err != nil {
		return nil, err
	}

	sess.ID = id // populate the output-only ID field (read from the id column)
	sess.Code = code
	sess.Headers = headers
	sess.ResponseBody = body
	sess.Delay = time.Duration(delayMs) * time.Millisecond
	sess.CreatedAtUnixMilli = createdMs
	sess.ExpiresAt = time.UnixMilli(expiresMs)
	sess.Slug = slug
	sess.GroupName = group
	sess.ResponseScript = script
	sess.SecurityHeaders = security
	sess.ForwardURL = fwd
	sess.LongLived = longLived != 0

	return &sess, nil
}

// scanRequest reads a full requests row, returning its id and the Request.
func scanRequest(sc rowScanner) (string, *Request, error) {
	var (
		id, method, headersJSON, reqURL, clientAddr string
		body                                        []byte
		createdMs                                   int64
	)

	if err := sc.Scan(&id, &method, &body, &headersJSON, &reqURL, &clientAddr, &createdMs); err != nil {
		return "", nil, err
	}

	headers, err := unmarshalHeaders(headersJSON)
	if err != nil {
		return "", nil, err
	}

	return id, &Request{
		ClientAddr:         clientAddr,
		Method:             method,
		Body:               body,
		Headers:            headers,
		URL:                reqURL,
		CreatedAtUnixMilli: createdMs,
	}, nil
}

// marshalHeaders encodes headers as a JSON array, never producing NULL.
func marshalHeaders(h []HttpHeader) (string, error) {
	if len(h) == 0 {
		return "[]", nil
	}

	b, err := json.Marshal(h)
	if err != nil {
		return "", fmt.Errorf("marshal headers: %w", err)
	}

	return string(b), nil
}

// unmarshalHeaders decodes a JSON header array, returning nil for the empty case.
func unmarshalHeaders(s string) ([]HttpHeader, error) {
	if s == "" || s == "[]" {
		return nil, nil
	}

	var h []HttpHeader
	if err := json.Unmarshal([]byte(s), &h); err != nil {
		return nil, fmt.Errorf("unmarshal headers: %w", err)
	}

	return h, nil
}

// isUniqueViolation reports whether err is a SQLite UNIQUE/PRIMARY KEY constraint failure.
func isUniqueViolation(err error) bool {
	var se *sqlite.Error
	if errors.As(err, &se) {
		return se.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE ||
			se.Code() == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY
	}

	return false
}

func boolToInt(b bool) int {
	if b {
		return 1
	}

	return 0
}
