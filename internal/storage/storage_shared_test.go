package storage_test

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gh.tarampamp.am/webhook-tester/v2/internal/storage"
)

func toCloser(s storage.Storage) io.Closer {
	if c, ok := s.(io.Closer); ok {
		return c
	}

	return io.NopCloser(nil)
}

type fakeTime struct{ atomic.Pointer[time.Time] }

func (f *fakeTime) Add(t time.Duration) { newNow := f.Load().Add(t); f.Store(&newNow) }
func (f *fakeTime) Get() time.Time      { return *f.Load() }

func newFakeTime(t *testing.T) *fakeTime {
	t.Helper()

	now, ft := time.Now(), fakeTime{}
	ft.Store(&now)

	return &ft
}

func testSessionCreateReadDelete(
	t *testing.T,
	new func(sessionTTL time.Duration, maxRequests uint32) storage.Storage,
	sleep func(time.Duration),
	now func() time.Time,
) {
	t.Helper()

	var ctx = context.Background()

	t.Run("create, read, delete", func(t *testing.T) {
		t.Parallel()

		var impl = new(time.Minute, 1)
		defer func() { _ = toCloser(impl).Close() }()

		var sessionHeaders = []storage.HttpHeader{{"foo", "bar"}, {"bar", "baz"}}

		const (
			code  uint16 = 201
			delay        = time.Second * 123
		)

		// create
		var sID, newErr = impl.NewSession(ctx, storage.Session{
			Code:    code,
			Headers: sessionHeaders,
			Delay:   delay,
		})

		require.NoError(t, newErr)
		require.NotEmpty(t, sID)

		// read
		got, getErr := impl.GetSession(ctx, sID)
		require.NoError(t, getErr)
		require.Equal(t, code, got.Code)
		require.Equal(t, sessionHeaders, got.Headers)
		require.Equal(t, delay, got.Delay)
		require.Equal(t, sID, got.ID) // output-only ID is populated on read
		assert.NotZero(t, got.CreatedAtUnixMilli)

		// delete
		require.NoError(t, impl.DeleteSession(ctx, sID))                      // success
		require.ErrorIs(t, impl.DeleteSession(ctx, sID), storage.ErrNotFound) // already deleted
		require.ErrorIs(t, impl.DeleteSession(ctx, sID), storage.ErrSessionNotFound)

		// read again
		got, getErr = impl.GetSession(ctx, sID)
		require.Nil(t, got)
		require.ErrorIs(t, getErr, storage.ErrNotFound)
		require.ErrorIs(t, getErr, storage.ErrSessionNotFound)
	})

	t.Run("inbound auth config round-trips", func(t *testing.T) {
		t.Parallel()

		var impl = new(time.Minute, 1)
		defer func() { _ = toCloser(impl).Close() }()

		// create with inbound auth configured
		sID, err := impl.NewSession(ctx, storage.Session{
			Code:              200,
			InboundAuthHeader: "X-Webhook-Token",
			InboundAuthValue:  "s3cr3t-value",
		})
		require.NoError(t, err)

		got, err := impl.GetSession(ctx, sID)
		require.NoError(t, err)
		require.Equal(t, "X-Webhook-Token", got.InboundAuthHeader)
		require.Equal(t, "s3cr3t-value", got.InboundAuthValue)

		// update can change and then clear it (empty header disables inbound auth)
		var (
			newHeader = "Authorization"
			newValue  = "Bearer abc"
		)
		require.NoError(t, impl.UpdateSession(ctx, sID, storage.SessionPatch{
			InboundAuthHeader: &newHeader,
			InboundAuthValue:  &newValue,
		}))

		got, err = impl.GetSession(ctx, sID)
		require.NoError(t, err)
		require.Equal(t, "Authorization", got.InboundAuthHeader)
		require.Equal(t, "Bearer abc", got.InboundAuthValue)

		var empty string
		require.NoError(t, impl.UpdateSession(ctx, sID, storage.SessionPatch{
			InboundAuthHeader: &empty,
			InboundAuthValue:  &empty,
		}))

		got, err = impl.GetSession(ctx, sID)
		require.NoError(t, err)
		require.Empty(t, got.InboundAuthHeader, "empty header clears inbound auth")
		require.Empty(t, got.InboundAuthValue)
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()

		var impl = new(time.Minute, 1)
		defer func() { _ = toCloser(impl).Close() }()

		got, err := impl.GetSession(ctx, "foo")
		require.Nil(t, got)
		require.ErrorIs(t, err, storage.ErrSessionNotFound)
	})

	t.Run("delete not existing", func(t *testing.T) {
		t.Parallel()

		var impl = new(time.Minute, 1)
		defer func() { _ = toCloser(impl).Close() }()

		require.ErrorIs(t, impl.DeleteSession(ctx, "foo"), storage.ErrSessionNotFound)
	})

	t.Run("expired", func(t *testing.T) {
		t.Parallel()

		const sessionTTL = time.Millisecond

		var impl = new(sessionTTL, 1)
		defer func() { _ = toCloser(impl).Close() }()

		sID, err := impl.NewSession(ctx, storage.Session{})
		require.NoError(t, err)
		require.NotEmpty(t, sID)

		sleep(sessionTTL * 2) // wait for expiration

		_, err = impl.GetSession(ctx, sID)

		require.ErrorIs(t, err, storage.ErrSessionNotFound)
	})

	t.Run("add session TTL", func(t *testing.T) {
		t.Parallel()

		const sessionTTL = time.Millisecond * 20

		var impl = new(sessionTTL, 2)
		defer func() { _ = toCloser(impl).Close() }()

		var before = now()

		// create session with TTL
		sID, err := impl.NewSession(ctx, storage.Session{})
		require.NoError(t, err)
		require.NotEmpty(t, sID)

		// get it (ensure it exists)
		sess, err := impl.GetSession(ctx, sID)
		require.NoError(t, err)

		{ // check the created and expiration time
			require.GreaterOrEqual(t, sess.CreatedAtUnixMilli, before.UnixMilli())
			require.LessOrEqual(t, sess.CreatedAtUnixMilli, now().UnixMilli())
			require.True(t, sess.ExpiresAt.After(time.UnixMilli(sess.CreatedAtUnixMilli)))
		}

		var ( // store the original values
			originalCreatedAt = sess.CreatedAtUnixMilli
			originalExpiresAt = sess.ExpiresAt
		)

		// reload the session
		sess, err = impl.GetSession(ctx, sID)
		require.NoError(t, err)
		require.Equal(t, originalCreatedAt, sess.CreatedAtUnixMilli) // should be the same

		// add TTL
		require.NoError(t, impl.AddSessionTTL(ctx, sID, sessionTTL*2)) // current ttl = x + 2x = 3x

		// wait for expiration (2x)
		sleep(sessionTTL * 2)

		// the session should be still alive
		sess, err = impl.GetSession(ctx, sID)
		require.NoError(t, err)
		require.Equal(t, originalCreatedAt, sess.CreatedAtUnixMilli)
		require.True(t, sess.ExpiresAt.After(originalExpiresAt)) // TTL was extended

		// wait for expiration (2x)
		sleep(sessionTTL * 2)

		// check again
		sess, err = impl.GetSession(ctx, sID)
		require.ErrorIs(t, err, storage.ErrSessionNotFound)
		require.Nil(t, sess)
	})
}

func testRequestCreateReadDelete(
	t *testing.T,
	new func(sessionTTL time.Duration, maxRequests uint32) storage.Storage,
	sleep func(time.Duration),
) {
	t.Helper()

	var ctx = context.Background()

	const someUrl = "https://example.com/foo/bar"

	t.Run("create, read, delete", func(t *testing.T) {
		t.Parallel()

		var impl = new(time.Minute, 1)
		defer func() { _ = toCloser(impl).Close() }()

		// create session
		sID, newErr := impl.NewSession(ctx, storage.Session{
			Code:    201,
			Headers: []storage.HttpHeader{{"foo", "bar"}, {"bar", "baz"}},
			Delay:   time.Second,
		})
		require.NoError(t, newErr)
		require.NotEmpty(t, sID)

		const (
			clientAddr = "127.0.0.1"
			method     = "GET"
			body       = " \nfoo bar\n\t \nbaz"
		)

		var requestHeaders = []storage.HttpHeader{{"foo", "bar"}, {"bar", "baz"}}

		// create (the common no-auth / auth-passed case is captured Authorized=true)
		rID, newReqErr := impl.NewRequest(ctx, sID, storage.Request{
			ClientAddr: clientAddr,
			Method:     method,
			Body:       []byte(body),
			Headers:    requestHeaders,
			URL:        someUrl,
			Authorized: true,
		})
		require.NoError(t, newReqErr)
		require.NotEmpty(t, rID)

		// read
		got, getErr := impl.GetRequest(ctx, sID, rID)
		require.NoError(t, getErr)
		require.Equal(t, clientAddr, got.ClientAddr)
		require.Equal(t, method, got.Method)
		require.Equal(t, []byte(body), got.Body)
		require.Equal(t, requestHeaders, got.Headers)
		require.Equal(t, someUrl, got.URL)
		assert.NotZero(t, got.CreatedAtUnixMilli)
		require.True(t, got.Authorized, "Authorized=true must round-trip")

		{ // read all
			all, err := impl.GetAllRequests(ctx, sID)
			require.NoError(t, err)
			require.Len(t, all, 1)
			require.Equal(t, all, map[string]storage.Request{rID: *got})
		}

		// delete
		require.NoError(t, impl.DeleteRequest(ctx, sID, rID))                      // success
		require.ErrorIs(t, impl.DeleteRequest(ctx, sID, rID), storage.ErrNotFound) // already deleted
		require.ErrorIs(t, impl.DeleteRequest(ctx, sID, rID), storage.ErrRequestNotFound)

		// read again
		got, getErr = impl.GetRequest(ctx, sID, rID)
		require.Nil(t, got)
		require.ErrorIs(t, getErr, storage.ErrNotFound)
		require.ErrorIs(t, getErr, storage.ErrRequestNotFound)
	})

	t.Run("authorized flag round-trips (true and false)", func(t *testing.T) {
		t.Parallel()

		var impl = new(time.Minute, 10)
		defer func() { _ = toCloser(impl).Close() }()

		sID, err := impl.NewSession(ctx, storage.Session{})
		require.NoError(t, err)

		// an auth-passed (or no-auth) request is captured Authorized=true
		okID, err := impl.NewRequest(ctx, sID, storage.Request{Method: "POST", Authorized: true})
		require.NoError(t, err)

		okReq, err := impl.GetRequest(ctx, sID, okID)
		require.NoError(t, err)
		require.True(t, okReq.Authorized, "Authorized=true must round-trip on GetRequest")

		// a rejected inbound-auth request is STILL captured, flagged Authorized=false
		noID, err := impl.NewRequest(ctx, sID, storage.Request{Method: "POST", Authorized: false})
		require.NoError(t, err)

		noReq, err := impl.GetRequest(ctx, sID, noID)
		require.NoError(t, err)
		require.False(t, noReq.Authorized, "Authorized=false must round-trip on GetRequest")

		// GetAllRequests carries the flag too
		all, err := impl.GetAllRequests(ctx, sID)
		require.NoError(t, err)
		require.True(t, all[okID].Authorized)
		require.False(t, all[noID].Authorized)
	})

	t.Run("new request - limit exceeded", func(t *testing.T) {
		t.Parallel()

		var impl = new(time.Minute, 2) // limit is 2
		defer func() { _ = toCloser(impl).Close() }()

		// create session
		sID, err := impl.NewSession(ctx, storage.Session{})
		require.NoError(t, err)
		require.NotEmpty(t, sID)

		// create request #1
		rID1, err := impl.NewRequest(ctx, sID, storage.Request{ClientAddr: "req1"})
		require.NoError(t, err)
		require.NotEmpty(t, rID1)

		sleep(time.Millisecond) // the accuracy is one millisecond

		// create request #2
		rID2, err := impl.NewRequest(ctx, sID, storage.Request{ClientAddr: "req2"})
		require.NoError(t, err)
		require.NotEmpty(t, rID2)

		// now, the session has 2 requests and the limit is reached

		{ // check made requests
			requests, _ := impl.GetAllRequests(ctx, sID)
			require.Len(t, requests, 2)
			_, ok := requests[rID1]
			require.True(t, ok)
			_, ok = requests[rID2]
			require.True(t, ok)

			req, _ := impl.GetRequest(ctx, sID, rID1)
			require.NotNil(t, req)

			req, _ = impl.GetRequest(ctx, sID, rID2)
			require.NotNil(t, req)
		}

		sleep(time.Millisecond)

		// create request #3
		rID3, err := impl.NewRequest(ctx, sID, storage.Request{ClientAddr: "req3"})
		require.NoError(t, err)
		require.NotEmpty(t, rID3)

		// now, the request #1 should be deleted because the limit is reached (the storage should keep the requests
		// with numbers 2 and 3)

		{ // check made requests again
			requests, _ := impl.GetAllRequests(ctx, sID)
			require.Len(t, requests, 2) // still 2
			_, ok := requests[rID2]
			require.True(t, ok)
			_, ok = requests[rID3]
			require.True(t, ok)

			req, reqErr := impl.GetRequest(ctx, sID, rID1) // not found
			require.Nil(t, req)
			require.Error(t, reqErr)

			req, _ = impl.GetRequest(ctx, sID, rID2) // ok
			require.NotNil(t, req)

			req, _ = impl.GetRequest(ctx, sID, rID3) // ok
			require.NotNil(t, req)
		}

		// and now add one more request - after that, the request #2 should be deleted (the storage should keep the
		// requests with numbers 3 and 4)

		sleep(time.Millisecond)

		// create request #4
		rID4, err := impl.NewRequest(ctx, sID, storage.Request{})
		require.NoError(t, err)
		require.NotEmpty(t, rID4)

		{ // check made requests again
			requests, _ := impl.GetAllRequests(ctx, sID)
			require.Len(t, requests, 2) // still 2

			req, reqErr := impl.GetRequest(ctx, sID, rID1) // not found
			require.Nil(t, req)
			require.Error(t, reqErr)

			req, reqErr = impl.GetRequest(ctx, sID, rID2) // not found
			require.Nil(t, req)
			require.Error(t, reqErr)

			req, _ = impl.GetRequest(ctx, sID, rID3) // ok
			require.NotNil(t, req)

			req, _ = impl.GetRequest(ctx, sID, rID4) // ok
			require.NotNil(t, req)
		}

		// and now delete all the requests
		require.NoError(t, impl.DeleteAllRequests(ctx, sID))

		_, err = impl.GetAllRequests(ctx, sID)
		require.NoError(t, err)

		// and the session
		require.NoError(t, impl.DeleteSession(ctx, sID))

		_, err = impl.GetAllRequests(ctx, sID)
		require.ErrorIs(t, err, storage.ErrSessionNotFound)
	})

	t.Run("delete all", func(t *testing.T) {
		t.Parallel()

		var impl = new(time.Minute, 1)
		defer func() { _ = toCloser(impl).Close() }()

		// create session
		sID, err := impl.NewSession(ctx, storage.Session{})
		require.NoError(t, err)
		require.NotEmpty(t, sID)

		// create request
		rID, err := impl.NewRequest(ctx, sID, storage.Request{})
		require.NoError(t, err)
		require.NotEmpty(t, rID)

		// delete all
		require.NoError(t, impl.DeleteAllRequests(ctx, sID))

		// check
		all, err := impl.GetAllRequests(ctx, sID)
		require.NoError(t, err)
		require.Empty(t, all)
	})

	t.Run("delete all - no session", func(t *testing.T) {
		t.Parallel()

		var impl = new(time.Minute, 1)
		defer func() { _ = toCloser(impl).Close() }()

		err := impl.DeleteAllRequests(ctx, "foo")
		require.ErrorIs(t, err, storage.ErrNotFound)
		require.ErrorIs(t, err, storage.ErrSessionNotFound)
	})

	t.Run("get all - empty", func(t *testing.T) {
		t.Parallel()

		var impl = new(time.Minute, 1)
		defer func() { _ = toCloser(impl).Close() }()

		// create session
		sID, err := impl.NewSession(ctx, storage.Session{})
		require.NoError(t, err)
		require.NotEmpty(t, sID)

		all, err := impl.GetAllRequests(ctx, sID)
		require.NoError(t, err)
		require.Empty(t, all)
	})

	t.Run("get all - no session", func(t *testing.T) {
		t.Parallel()

		var impl = new(time.Minute, 1)
		defer func() { _ = toCloser(impl).Close() }()

		all, err := impl.GetAllRequests(ctx, "foo")
		require.Nil(t, all)
		require.ErrorIs(t, err, storage.ErrNotFound)
		require.ErrorIs(t, err, storage.ErrSessionNotFound)
	})

	t.Run("new request - session not found", func(t *testing.T) {
		t.Parallel()

		var impl = new(time.Minute, 1)
		defer func() { _ = toCloser(impl).Close() }()

		_, err := impl.NewRequest(ctx, "foo", storage.Request{})
		require.ErrorIs(t, err, storage.ErrNotFound)
		require.ErrorIs(t, err, storage.ErrSessionNotFound)
	})

	t.Run("get request - session not found", func(t *testing.T) {
		t.Parallel()

		var impl = new(time.Minute, 1)
		defer func() { _ = toCloser(impl).Close() }()

		got, err := impl.GetRequest(ctx, "foo", "bar")
		require.Nil(t, got)
		require.ErrorIs(t, err, storage.ErrNotFound)
		require.ErrorIs(t, err, storage.ErrSessionNotFound)
	})

	t.Run("get request - request not found", func(t *testing.T) {
		t.Parallel()

		var impl = new(time.Minute, 1)
		defer func() { _ = toCloser(impl).Close() }()

		// create session
		sID, newErr := impl.NewSession(ctx, storage.Session{})
		require.NoError(t, newErr)
		require.NotEmpty(t, sID)

		got, err := impl.GetRequest(ctx, sID, "foo")
		require.Nil(t, got)
		require.ErrorIs(t, err, storage.ErrNotFound)
		require.ErrorIs(t, err, storage.ErrRequestNotFound)
	})

	t.Run("delete request - session not found", func(t *testing.T) {
		t.Parallel()

		var impl = new(time.Minute, 1)
		defer func() { _ = toCloser(impl).Close() }()

		err := impl.DeleteRequest(ctx, "foo", "bar")
		require.ErrorIs(t, err, storage.ErrNotFound)
		require.ErrorIs(t, err, storage.ErrSessionNotFound)
	})

	t.Run("delete request - request not found", func(t *testing.T) {
		t.Parallel()

		var impl = new(time.Minute, 1)
		defer func() { _ = toCloser(impl).Close() }()

		// create session
		sID, newErr := impl.NewSession(ctx, storage.Session{})
		require.NoError(t, newErr)
		require.NotEmpty(t, sID)

		err := impl.DeleteRequest(ctx, sID, "foo")
		require.ErrorIs(t, err, storage.ErrNotFound)
		require.ErrorIs(t, err, storage.ErrRequestNotFound)
	})
}

// testExtendedSessionOps tests the four new interface methods added in Task 2:
// GetSessionBySlug, UpdateSession, ListSessions, and SearchRequests.
// It is called for every local driver (inmemory, fs) that performs a full scan.
func testExtendedSessionOps(
	t *testing.T,
	newImpl func(sessionTTL time.Duration, maxRequests uint32) storage.Storage,
) {
	t.Helper()

	var ctx = context.Background()

	t.Run("GetSessionBySlug - found and not found", func(t *testing.T) {
		t.Parallel()

		var impl = newImpl(time.Minute, 10)
		defer func() { _ = toCloser(impl).Close() }()

		// create session with a slug
		sID, err := impl.NewSession(ctx, storage.Session{Code: 201, Slug: "my-slug"})
		require.NoError(t, err)

		// look it up by slug
		got, err := impl.GetSessionBySlug(ctx, "my-slug")
		require.NoError(t, err)
		require.NotNil(t, got)
		require.Equal(t, "my-slug", got.Slug)
		require.Equal(t, uint16(201), got.Code)

		// the output-only ID field must be populated and consistent across both read paths
		bySID, err := impl.GetSession(ctx, sID)
		require.NoError(t, err)
		require.Equal(t, sID, got.ID, "GetSessionBySlug must populate Session.ID")
		require.Equal(t, got.ID, bySID.ID, "GetSessionBySlug(slug).ID == GetSession(id).ID")

		// empty slug must return ErrNotFound
		_, err = impl.GetSessionBySlug(ctx, "")
		require.ErrorIs(t, err, storage.ErrNotFound)

		// nonexistent slug must return ErrNotFound
		_, err = impl.GetSessionBySlug(ctx, "does-not-exist")
		require.ErrorIs(t, err, storage.ErrNotFound)
	})

	t.Run("UpdateSession - mutates a field", func(t *testing.T) {
		t.Parallel()

		var impl = newImpl(time.Minute, 10)
		defer func() { _ = toCloser(impl).Close() }()

		sID, err := impl.NewSession(ctx, storage.Session{Code: 200})
		require.NoError(t, err)

		var newCode uint16 = 404
		var newSlug = "updated-slug"

		require.NoError(t, impl.UpdateSession(ctx, sID, storage.SessionPatch{
			Code: &newCode,
			Slug: &newSlug,
		}))

		got, err := impl.GetSession(ctx, sID)
		require.NoError(t, err)
		require.Equal(t, uint16(404), got.Code)
		require.Equal(t, "updated-slug", got.Slug)

		// update non-existent session
		require.ErrorIs(t, impl.UpdateSession(ctx, "nonexistent", storage.SessionPatch{}), storage.ErrSessionNotFound)
	})

	t.Run("ListSessions - returns summaries with request count", func(t *testing.T) {
		t.Parallel()

		var impl = newImpl(time.Minute, 10)
		defer func() { _ = toCloser(impl).Close() }()

		sID, err := impl.NewSession(ctx, storage.Session{Code: 200, GroupName: "grp-test"})
		require.NoError(t, err)

		// add two requests
		_, err = impl.NewRequest(ctx, sID, storage.Request{ClientAddr: "1.2.3.4"})
		require.NoError(t, err)
		_, err = impl.NewRequest(ctx, sID, storage.Request{ClientAddr: "5.6.7.8"})
		require.NoError(t, err)

		// list all sessions – at least our session should appear
		list, err := impl.ListSessions(ctx, storage.SessionFilter{})
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(list), 1)

		var found *storage.SessionSummary

		for i := range list {
			if list[i].ID == sID {
				found = &list[i]

				break
			}
		}

		require.NotNil(t, found, "our session should appear in ListSessions")
		require.Equal(t, 2, found.RequestsCount)
		assert.NotZero(t, found.LastRequestUnixMilli)
		assert.NotZero(t, found.CreatedAtUnixMilli)
		assert.NotZero(t, found.ExpiresAtUnixMilli)

		// group filter: matching group
		filtered, err := impl.ListSessions(ctx, storage.SessionFilter{Group: "grp-test"})
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(filtered), 1)

		var filteredFound bool

		for _, s := range filtered {
			if s.ID == sID {
				filteredFound = true

				break
			}
		}

		require.True(t, filteredFound)

		// group filter: non-matching group
		none, err := impl.ListSessions(ctx, storage.SessionFilter{Group: "no-such-group"})
		require.NoError(t, err)
		require.Empty(t, none)
	})

	t.Run("SearchRequests - no error on empty store", func(t *testing.T) {
		t.Parallel()

		var impl = newImpl(time.Minute, 10)
		defer func() { _ = toCloser(impl).Close() }()

		matches, err := impl.SearchRequests(ctx, storage.IdentifierQuery{
			Key:   "X-Trace-ID",
			Value: "abc",
			Match: storage.IdentifierMatchExact,
			Limit: 10,
		})
		require.NoError(t, err)
		require.Empty(t, matches)
	})

	t.Run("SearchRequests - exact header match", func(t *testing.T) {
		t.Parallel()

		var impl = newImpl(time.Minute, 10)
		defer func() { _ = toCloser(impl).Close() }()

		sID, err := impl.NewSession(ctx, storage.Session{Code: 200, Slug: "search-sess"})
		require.NoError(t, err)

		_, err = impl.NewRequest(ctx, sID, storage.Request{
			Headers: []storage.HttpHeader{
				{Name: "X-Trace-ID", Value: "abc123"},
				{Name: "Content-Type", Value: "application/json"},
			},
			Body: []byte(`{"event":"user.created","userId":"u-99"}`),
		})
		require.NoError(t, err)

		// exact match on a header
		matches, err := impl.SearchRequests(ctx, storage.IdentifierQuery{
			Key:   "X-Trace-ID",
			Value: "abc123",
			Match: storage.IdentifierMatchExact,
			Limit: 10,
		})
		require.NoError(t, err)
		require.Len(t, matches, 1)
		require.Equal(t, "X-Trace-ID", matches[0].Key)
		require.Equal(t, "abc123", matches[0].Value)
		require.Equal(t, sID, matches[0].SessionID)
		require.Equal(t, "search-sess", matches[0].SessionSlug)
		require.NotEmpty(t, matches[0].RequestID)

		// prefix match on a header value
		prefixMatches, err := impl.SearchRequests(ctx, storage.IdentifierQuery{
			Key:   "X-Trace-ID",
			Value: "abc",
			Match: storage.IdentifierMatchPrefix,
			Limit: 10,
		})
		require.NoError(t, err)
		require.Len(t, prefixMatches, 1)

		// no match
		noMatch, err := impl.SearchRequests(ctx, storage.IdentifierQuery{
			Key:   "X-Trace-ID",
			Value: "xyz",
			Match: storage.IdentifierMatchExact,
			Limit: 10,
		})
		require.NoError(t, err)
		require.Empty(t, noMatch)
	})
}

func testRaceProvocation(
	t *testing.T,
	new func(sessionTTL time.Duration, maxRequests uint32) storage.Storage,
) {
	t.Helper()

	var ctx = context.Background()

	var impl = new(time.Minute, 1000)
	defer func() { _ = toCloser(impl).Close() }()

	var wg sync.WaitGroup

	for range 20 {
		wg.Go(func() {
			sID, err := impl.NewSession(ctx, storage.Session{})
			require.NoError(t, err)

			_, err = impl.GetSession(ctx, sID)
			require.NoError(t, err)

			var rID string

			for range 20 {
				rID, err = impl.NewRequest(ctx, sID, storage.Request{})
				require.NoError(t, err)

				_, err = impl.GetRequest(ctx, sID, rID)
				require.NoError(t, err)

				all, aErr := impl.GetAllRequests(ctx, sID)
				require.NoError(t, aErr)
				require.NotEmpty(t, all)
			}

			require.NoError(t, impl.AddSessionTTL(ctx, sID, time.Minute))

			require.NoError(t, impl.DeleteRequest(ctx, sID, rID))

			require.NoError(t, impl.DeleteAllRequests(ctx, sID))
		})
	}

	wg.Wait()
}
