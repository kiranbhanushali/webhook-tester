package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

type (
	InMemory struct {
		sessionTTL      time.Duration
		maxRequests     uint32
		sessions        syncMap[ /* sID */ string, *sessionData]
		slugs           syncMap[ /* slug */ string, /* sID */ string] // slug → session ID index
		cleanupInterval time.Duration

		// this function returns the current time, it's used to mock the time in tests
		timeNow TimeFunc

		// seq assigns each request a strictly-increasing, never-reused Request.Seq. It is
		// monotonic for the process lifetime (the store is in-memory, so it resets when the
		// process restarts, along with all data). See storage.Request.Seq.
		seq atomic.Int64

		close  chan struct{}
		closed atomic.Bool
	}

	sessionData struct {
		sync.Mutex
		session  Session
		requests syncMap[ /* rID */ string, Request]
	}
)

var ( // ensure interface implementation
	_ Storage   = (*InMemory)(nil)
	_ io.Closer = (*InMemory)(nil)
)

type InMemoryOption func(*InMemory)

// WithInMemoryCleanupInterval sets the cleanup interval for expired sessions.
func WithInMemoryCleanupInterval(v time.Duration) InMemoryOption {
	return func(s *InMemory) { s.cleanupInterval = v }
}

// WithInMemoryTimeNow sets the function that returns the current time.
func WithInMemoryTimeNow(fn TimeFunc) InMemoryOption { return func(s *InMemory) { s.timeNow = fn } }

// NewInMemory creates a new in-memory storage with the given session TTL and the maximum number of stored requests.
// Note that the cleanup goroutine is started automatically if the cleanup interval is greater than zero.
// To stop the cleanup goroutine and close the storage, call the InMemory.Close method.
func NewInMemory(sessionTTL time.Duration, maxRequests uint32, opts ...InMemoryOption) *InMemory {
	var s = InMemory{
		sessionTTL:      sessionTTL,
		maxRequests:     maxRequests,
		close:           make(chan struct{}),
		cleanupInterval: time.Second, // default cleanup interval
		timeNow:         defaultTimeFunc,
	}

	for _, opt := range opts {
		opt(&s)
	}

	if s.cleanupInterval > time.Duration(0) {
		go s.cleanup(context.Background()) // start cleanup goroutine
	}

	return &s
}

// newID generates a new (unique) ID.
func (*InMemory) newID() string { return uuid.New().String() }

func (s *InMemory) cleanup(ctx context.Context) {
	var timer = time.NewTimer(s.cleanupInterval)
	defer timer.Stop()

	defer func() { // cleanup on exit
		s.sessions.Range(func(sID string, _ *sessionData) bool {
			_ = s.DeleteSession(ctx, sID)

			return true
		})
	}()

	for {
		select {
		case <-s.close: // close signal received
			return
		case <-timer.C:
			var now = s.timeNow()

			s.sessions.Range(func(sID string, data *sessionData) bool {
				data.Lock()
				var expiresAt = data.session.ExpiresAt //nolint:wsl_v5
				data.Unlock()

				if expiresAt.Before(now) {
					_ = s.DeleteSession(ctx, sID)
				}

				return true
			})

			timer.Reset(s.cleanupInterval)
		}
	}
}

// isSessionExists checks if the session with the specified ID exists and is not expired.
func (s *InMemory) isSessionExists(sID string) bool {
	data, ok := s.sessions.Load(sID)
	if !ok {
		return false
	}

	data.Lock()
	var expiresAt = data.session.ExpiresAt //nolint:wsl_v5
	data.Unlock()

	// TODO: remove expired sessions automatically?

	return expiresAt.After(s.timeNow())
}

// isOpenAndNotDone checks if the storage is open and the context is not done.
func (s *InMemory) isOpenAndNotDone(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err // context is done
	} else if s.closed.Load() {
		return ErrClosed // storage is closed
	}

	return nil
}

func (s *InMemory) NewSession(ctx context.Context, session Session, id ...string) (sID string, _ error) {
	if err := s.isOpenAndNotDone(ctx); err != nil {
		return "", err // context is done
	}

	var now = s.timeNow()

	if len(id) > 0 { //nolint:nestif // use the specified ID
		if len(id[0]) == 0 {
			return "", errors.New("empty session ID")
		}

		sID = id[0]

		// check if the session with the specified ID already exists
		if data, ok := s.sessions.Load(sID); ok {
			return "", fmt.Errorf("session %s already exists", sID)
		} else if data != nil {
			// check if the session with the specified ID has expired if it exists
			data.Lock()
			expiresAt := data.session.ExpiresAt
			data.Unlock()

			if expiresAt.After(now) {
				if dErr := s.DeleteSession(ctx, sID); dErr != nil {
					return "", dErr
				}
			}
		}
	} else {
		sID = s.newID() // generate a new ID
	}

	session.CreatedAtUnixMilli, session.ExpiresAt = now.UnixMilli(), now.Add(s.sessionTTL)

	s.sessions.Store(sID, &sessionData{session: session})

	if session.Slug != "" {
		s.slugs.Store(session.Slug, sID)
	}

	return
}

func (s *InMemory) GetSession(ctx context.Context, sID string) (*Session, error) {
	if err := s.isOpenAndNotDone(ctx); err != nil {
		return nil, err
	}

	data, ok := s.sessions.Load(sID)
	if !ok {
		return nil, ErrSessionNotFound // not found
	}

	data.Lock()
	var sess = data.session //nolint:wsl_v5 // copy under lock (avoids aliasing the shared struct)
	data.Unlock()

	if sess.ExpiresAt.Before(s.timeNow()) {
		s.sessions.Delete(sID)

		return nil, ErrSessionNotFound // session has been expired
	}

	sess.ID = sID // populate the output-only ID field

	return &sess, nil
}

func (s *InMemory) AddSessionTTL(ctx context.Context, sID string, howMuch time.Duration) error {
	if err := s.isOpenAndNotDone(ctx); err != nil {
		return err
	}

	if !s.isSessionExists(sID) {
		return ErrSessionNotFound // session not found
	}

	data, ok := s.sessions.Load(sID)
	if !ok {
		return ErrSessionNotFound // like a fuse, because we already checked it
	}

	data.Lock()
	data.session.ExpiresAt = data.session.ExpiresAt.Add(howMuch)
	data.Unlock()

	return nil
}

func (s *InMemory) DeleteSession(ctx context.Context, sID string) error {
	if err := s.isOpenAndNotDone(ctx); err != nil {
		return err
	}

	data, ok := s.sessions.LoadAndDelete(sID)
	if !ok {
		return ErrSessionNotFound // session not found
	}

	data.requests.Range(func(rID string, _ Request) bool { // delete all session requests
		data.requests.Delete(rID)

		return true
	})

	// remove slug index entry
	data.Lock()
	slug := data.session.Slug
	data.Unlock()

	if slug != "" {
		s.slugs.Delete(slug)
	}

	return nil
}

func (s *InMemory) NewRequest(ctx context.Context, sID string, r Request) (rID string, _ error) {
	if err := s.isOpenAndNotDone(ctx); err != nil {
		return "", err
	}

	if !s.isSessionExists(sID) {
		return "", ErrSessionNotFound // session not found
	}

	data, ok := s.sessions.Load(sID)
	if !ok {
		return "", ErrSessionNotFound // like a fuse, because we already checked it
	}

	rID, r.CreatedAtUnixMilli = s.newID(), s.timeNow().UnixMilli()

	// ID and Seq are output-only: assign them here (ignoring any caller-provided values).
	r.ID, r.Seq = rID, s.seq.Add(1)

	data.requests.Store(rID, r)

	if s.maxRequests > 0 { // limit stored requests count
		type rq struct { // a runtime representation of the request, used for sorting
			id string
			ts int64
		}

		var all = make([]rq, 0) // a slice for all session requests

		data.requests.Range(func(id string, req Request) bool { // iterate over all session requests and fill the slice
			all = append(all, rq{id, req.CreatedAtUnixMilli})

			return true
		})

		if len(all) > int(s.maxRequests) { // if the number of requests exceeds the limit
			sort.Slice(all, func(i, j int) bool { return all[i].ts > all[j].ts }) // sort requests by creation time

			for i := int(s.maxRequests); i < len(all); i++ { // delete the oldest requests
				data.requests.Delete(all[i].id)
			}
		}
	}

	return
}

func (s *InMemory) GetRequest(ctx context.Context, sID, rID string) (*Request, error) {
	if err := s.isOpenAndNotDone(ctx); err != nil {
		return nil, err
	}

	if !s.isSessionExists(sID) {
		return nil, ErrSessionNotFound // session not found
	}

	session, sessionOk := s.sessions.Load(sID)
	if !sessionOk {
		return nil, ErrSessionNotFound // like a fuse, because we already checked it
	}

	if request, ok := session.requests.Load(rID); ok {
		return &request, nil
	}

	return nil, ErrRequestNotFound // request not found
}

func (s *InMemory) GetAllRequests(ctx context.Context, sID string) (map[string]Request, error) {
	if err := s.isOpenAndNotDone(ctx); err != nil {
		return nil, err
	}

	if !s.isSessionExists(sID) {
		return nil, ErrSessionNotFound // session not found
	}

	session, sessionOk := s.sessions.Load(sID)
	if !sessionOk {
		return nil, ErrSessionNotFound // like a fuse, because we already checked it
	}

	var all = make(map[string]Request)

	session.requests.Range(func(id string, req Request) bool {
		all[id] = req

		return true
	})

	return all, nil
}

// ListRequestsAfter returns the session's requests with Seq > afterSeq, sorted by Seq
// ascending (FIFO), capped at limit. It backs the incremental events-fetch API.
func (s *InMemory) ListRequestsAfter(ctx context.Context, sID string, afterSeq int64, limit int) ([]Request, error) {
	if err := s.isOpenAndNotDone(ctx); err != nil {
		return nil, err
	}

	if !s.isSessionExists(sID) {
		return nil, ErrSessionNotFound // session not found
	}

	session, sessionOk := s.sessions.Load(sID)
	if !sessionOk {
		return nil, ErrSessionNotFound // like a fuse, because we already checked it
	}

	if limit <= 0 {
		limit = defaultListLimit
	}

	var out []Request

	session.requests.Range(func(_ string, req Request) bool {
		if req.Seq > afterSeq {
			out = append(out, req)
		}

		return true
	})

	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })

	if len(out) > limit {
		out = out[:limit]
	}

	return out, nil
}

func (s *InMemory) DeleteRequest(ctx context.Context, sID, rID string) error {
	if err := s.isOpenAndNotDone(ctx); err != nil {
		return err
	}

	if !s.isSessionExists(sID) {
		return ErrSessionNotFound // session not found
	}

	session, sessionOk := s.sessions.Load(sID)
	if !sessionOk {
		return ErrSessionNotFound // like a fuse, because we already checked it
	}

	if _, ok := session.requests.LoadAndDelete(rID); ok {
		return nil
	}

	return ErrRequestNotFound // request not found
}

func (s *InMemory) DeleteAllRequests(ctx context.Context, sID string) error {
	if err := s.isOpenAndNotDone(ctx); err != nil {
		return err
	}

	if !s.isSessionExists(sID) {
		return ErrSessionNotFound // session not found
	}

	session, sessionOk := s.sessions.Load(sID)
	if !sessionOk {
		return ErrSessionNotFound // like a fuse, because we already checked it
	}

	// delete all session requests
	session.requests.Range(func(rID string, _ Request) bool {
		session.requests.Delete(rID)

		return true
	})

	return nil
}

// GetSessionBySlug looks up a session by its human-readable slug using the in-memory slug index.
// Returns ErrNotFound when slug is empty or no session has that slug.
func (s *InMemory) GetSessionBySlug(ctx context.Context, slug string) (*Session, error) {
	if err := s.isOpenAndNotDone(ctx); err != nil {
		return nil, err
	}

	if slug == "" {
		return nil, ErrNotFound
	}

	sID, ok := s.slugs.Load(slug)
	if !ok {
		return nil, ErrNotFound
	}

	return s.GetSession(ctx, sID)
}

// UpdateSession applies the non-nil fields of patch to the session with the given ID.
// Only the fields that are set (non-nil pointer) in patch are updated; others remain unchanged.
// Returns ErrSessionNotFound when the session does not exist.
func (s *InMemory) UpdateSession(ctx context.Context, sID string, patch SessionPatch) error {
	if err := s.isOpenAndNotDone(ctx); err != nil {
		return err
	}

	if !s.isSessionExists(sID) {
		return ErrSessionNotFound
	}

	data, ok := s.sessions.Load(sID)
	if !ok {
		return ErrSessionNotFound
	}

	data.Lock()
	defer data.Unlock()

	oldSlug := data.session.Slug

	if patch.Code != nil {
		data.session.Code = *patch.Code
	}

	if patch.Slug != nil {
		data.session.Slug = *patch.Slug
	}

	if patch.GroupName != nil {
		data.session.GroupName = *patch.GroupName
	}

	if patch.ResponseScript != nil {
		data.session.ResponseScript = *patch.ResponseScript
	}

	if patch.ForwardURL != nil {
		data.session.ForwardURL = *patch.ForwardURL
	}

	if patch.Headers != nil {
		data.session.Headers = *patch.Headers
	}

	if patch.SecurityHeaders != nil {
		data.session.SecurityHeaders = *patch.SecurityHeaders
	}

	if patch.ResponseBody != nil {
		data.session.ResponseBody = *patch.ResponseBody
	}

	if patch.Delay != nil {
		data.session.Delay = *patch.Delay
	}

	if patch.LongLived != nil {
		data.session.LongLived = *patch.LongLived
	}

	if patch.InboundAuthHeader != nil {
		data.session.InboundAuthHeader = *patch.InboundAuthHeader
	}

	if patch.InboundAuthValue != nil {
		data.session.InboundAuthValue = *patch.InboundAuthValue
	}

	newSlug := data.session.Slug

	// maintain slug index: remove old, add new (if changed)
	if oldSlug != newSlug {
		if oldSlug != "" {
			s.slugs.Delete(oldSlug)
		}

		if newSlug != "" {
			s.slugs.Store(newSlug, sID)
		}
	}

	return nil
}

// ListSessions returns a summary of all non-expired sessions, optionally filtered by f.
// SessionFilter.Group performs an exact match on GroupName; SessionFilter.Query is a
// substring match applied to ID, Slug, and GroupName.
func (s *InMemory) ListSessions(ctx context.Context, f SessionFilter) ([]SessionSummary, error) {
	if err := s.isOpenAndNotDone(ctx); err != nil {
		return nil, err
	}

	var (
		now     = s.timeNow()
		results []SessionSummary
	)

	s.sessions.Range(func(sID string, data *sessionData) bool {
		data.Lock()
		sess := data.session
		expiresAt := sess.ExpiresAt
		data.Unlock()

		if expiresAt.Before(now) {
			return true // skip expired
		}

		// apply group filter
		if f.Group != "" && sess.GroupName != f.Group {
			return true
		}

		// apply query (substring) filter
		if f.Query != "" {
			if !strings.Contains(sID, f.Query) &&
				!strings.Contains(sess.Slug, f.Query) &&
				!strings.Contains(sess.GroupName, f.Query) {
				return true
			}
		}

		// count requests and find last-activity time
		var (
			reqCount    int
			lastReqTime int64
		)

		data.requests.Range(func(_ string, req Request) bool {
			reqCount++

			if req.CreatedAtUnixMilli > lastReqTime {
				lastReqTime = req.CreatedAtUnixMilli
			}

			return true
		})

		results = append(results, SessionSummary{
			ID:                   sID,
			Slug:                 sess.Slug,
			GroupName:            sess.GroupName,
			Code:                 sess.Code,
			RequestsCount:        reqCount,
			LastRequestUnixMilli: lastReqTime,
			CreatedAtUnixMilli:   sess.CreatedAtUnixMilli,
			ExpiresAtUnixMilli:   expiresAt.UnixMilli(),
			LongLived:            sess.LongLived,
		})

		return true
	})

	return results, nil
}

// SearchRequests performs a non-indexed linear scan of all stored requests, matching headers and
// JSON body fields against the query key/value pair. For high-volume indexed search, use the
// SQLite driver instead.
func (s *InMemory) SearchRequests(ctx context.Context, q IdentifierQuery) ([]RequestMatch, error) {
	if err := s.isOpenAndNotDone(ctx); err != nil {
		return nil, err
	}

	var (
		now     = s.timeNow()
		results []RequestMatch
		done    bool
	)

	s.sessions.Range(func(sID string, data *sessionData) bool {
		if done {
			return false
		}

		data.Lock()
		sess := data.session
		expiresAt := sess.ExpiresAt
		data.Unlock()

		if expiresAt.Before(now) {
			return true // skip expired
		}

		// apply session-level filters
		if q.SessionID != "" && q.SessionID != sID {
			return true
		}

		if q.Group != "" && q.Group != sess.GroupName {
			return true
		}

		data.requests.Range(func(rID string, req Request) bool {
			if done {
				return false
			}

			// apply time filters
			if q.FromUnixMilli > 0 && req.CreatedAtUnixMilli < q.FromUnixMilli {
				return true
			}

			if q.ToUnixMilli > 0 && req.CreatedAtUnixMilli > q.ToUnixMilli {
				return true
			}

			// scan headers for a matching key/value pair
			for _, h := range req.Headers {
				if identifierMatches(q, h.Name, h.Value) {
					results = append(results, RequestMatch{
						SessionID:           sID,
						SessionSlug:         sess.Slug,
						RequestID:           rID,
						Key:                 h.Name,
						Value:               h.Value,
						CapturedAtUnixMilli: req.CreatedAtUnixMilli,
					})

					if q.Limit > 0 && len(results) >= q.Limit {
						done = true
					}

					return !done
				}
			}

			// scan JSON body for matching top-level string fields
			if len(req.Body) > 0 {
				var m map[string]any

				if err := json.Unmarshal(req.Body, &m); err == nil {
					for k, v := range m {
						if identifierMatches(q, k, fmt.Sprintf("%v", v)) {
							results = append(results, RequestMatch{
								SessionID:           sID,
								SessionSlug:         sess.Slug,
								RequestID:           rID,
								Key:                 k,
								Value:               fmt.Sprintf("%v", v),
								CapturedAtUnixMilli: req.CreatedAtUnixMilli,
							})

							if q.Limit > 0 && len(results) >= q.Limit {
								done = true

								return false
							}
						}
					}
				}
			}

			return true
		})

		return !done
	})

	return results, nil
}

// Close closes the storage and stops the cleanup goroutine. Any further calls to the storage methods will
// return ErrClosed.
func (s *InMemory) Close() error {
	if s.closed.CompareAndSwap(false, true) {
		close(s.close)

		return nil
	}

	return ErrClosed
}

// syncMap is a thread-safe map with strong-typed keys and values.
type syncMap[K comparable, V any] struct{ m sync.Map }

// Delete deletes the value for a key.
func (m *syncMap[K, V]) Delete(key K) { m.m.Delete(key) }

// Load returns the value stored in the map for a key, or nil if no value is present.
// The ok result indicates whether value was found in the map.
func (m *syncMap[K, V]) Load(key K) (value V, ok bool) {
	v, ok := m.m.Load(key)
	if !ok {
		return value, ok
	}

	return v.(V), ok
}

// LoadAndDelete deletes the value for a key, returning the previous value if any.
// The loaded result reports whether the key was present.
func (m *syncMap[K, V]) LoadAndDelete(key K) (value V, loaded bool) {
	v, loaded := m.m.LoadAndDelete(key)
	if !loaded {
		return value, loaded
	}

	return v.(V), loaded
}

// Range calls f sequentially for each key and value present in the map.
// If f returns false, range stops the iteration.
func (m *syncMap[K, V]) Range(f func(key K, value V) bool) {
	m.m.Range(func(key, value any) bool { return f(key.(K), value.(V)) })
}

// Store sets the value for a key.
func (m *syncMap[K, V]) Store(key K, value V) { m.m.Store(key, value) }
