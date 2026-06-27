package request_replay_test

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/request_replay"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/shared"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/openapi"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage"
)

func TestHandler_ReplaysToTargetURL(t *testing.T) {
	t.Parallel()

	var (
		ctx       = context.Background()
		db        = storage.NewInMemory(time.Minute, 8)
		gotMethod string
		gotBody   []byte
		gotHeader string
	)

	t.Cleanup(func() { _ = db.Close() })

	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotBody, _ = io.ReadAll(r.Body)
		gotHeader = r.Header.Get("X-Custom")

		w.Header().Set("X-Sink", "pong")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("sink-response"))
	}))
	t.Cleanup(sink.Close)

	sID, err := db.NewSession(ctx, storage.Session{Slug: "alpha"})
	require.NoError(t, err)

	rID, err := db.NewRequest(ctx, sID, storage.Request{
		Method: "POST",
		Body:   []byte("payload-body"),
		Headers: []storage.HttpHeader{
			{Name: "X-Custom", Value: "keep-me"},
			{Name: "Connection", Value: "keep-alive"}, // hop-by-hop, must be stripped
		},
	})
	require.NoError(t, err)

	rUUID := uuid.MustParse(rID)
	h := request_replay.New(db)

	resp, hErr := h.Handle(ctx, "alpha", rUUID, &openapi.ReplayRequest{TargetUrl: sink.URL})
	require.NoError(t, hErr)
	require.NotNil(t, resp)

	assert.EqualValues(t, http.StatusAccepted, resp.StatusCode)

	body, dErr := base64.StdEncoding.DecodeString(resp.BodyBase64)
	require.NoError(t, dErr)
	assert.Equal(t, "sink-response", string(body))

	// the sink saw the original method, body and headers (minus hop-by-hop)
	assert.Equal(t, "POST", gotMethod)
	assert.Equal(t, "payload-body", string(gotBody))
	assert.Equal(t, "keep-me", gotHeader)

	// response headers are surfaced
	var found bool
	for _, hdr := range resp.Headers {
		if hdr.Name == "X-Sink" && hdr.Value == "pong" {
			found = true
		}
	}

	assert.True(t, found, "expected X-Sink response header to be returned")
}

func TestHandler_FallsBackToForwardURL(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = storage.NewInMemory(time.Minute, 8)
	)

	t.Cleanup(func() { _ = db.Close() })

	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(sink.Close)

	sID, err := db.NewSession(ctx, storage.Session{Slug: "beta", ForwardURL: sink.URL})
	require.NoError(t, err)

	rID, err := db.NewRequest(ctx, sID, storage.Request{Method: "GET"})
	require.NoError(t, err)

	h := request_replay.New(db)

	// no body / no target → uses session ForwardURL
	resp, hErr := h.Handle(ctx, "beta", uuid.MustParse(rID), nil)
	require.NoError(t, hErr)
	assert.EqualValues(t, http.StatusTeapot, resp.StatusCode)
}

func TestHandler_DoesNotFollowRedirects(t *testing.T) {
	t.Parallel()

	// secondHit tracks whether the redirect target was ever contacted.
	var secondHit bool

	// redirectTarget is the URL the first server would redirect to.
	// We create it first so we have its URL when building the redirector.
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		secondHit = true

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("should-not-see-this"))
	}))
	t.Cleanup(redirectTarget.Close)

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, redirectTarget.URL, http.StatusFound)
	}))
	t.Cleanup(redirector.Close)

	var (
		ctx = context.Background()
		db  = storage.NewInMemory(time.Minute, 8)
	)
	t.Cleanup(func() { _ = db.Close() })

	sID, err := db.NewSession(ctx, storage.Session{Slug: "redirect-test"})
	require.NoError(t, err)

	rID, err := db.NewRequest(ctx, sID, storage.Request{Method: "GET"})
	require.NoError(t, err)

	h := request_replay.New(db)

	resp, hErr := h.Handle(ctx, "redirect-test", uuid.MustParse(rID), &openapi.ReplayRequest{TargetUrl: redirector.URL})
	require.NoError(t, hErr)
	require.NotNil(t, resp)

	// Must see the 302 from the redirector, not the 200 from the target.
	assert.EqualValues(t, http.StatusFound, resp.StatusCode, "replay must return the redirect response, not follow it")
	assert.False(t, secondHit, "replay must not contact the redirect target")
}

func TestHandler_NoTargetReturnsBadRequest(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = storage.NewInMemory(time.Minute, 8)
	)

	t.Cleanup(func() { _ = db.Close() })

	sID, err := db.NewSession(ctx, storage.Session{Slug: "gamma"})
	require.NoError(t, err)

	rID, err := db.NewRequest(ctx, sID, storage.Request{Method: "GET"})
	require.NoError(t, err)

	h := request_replay.New(db)

	_, hErr := h.Handle(ctx, "gamma", uuid.MustParse(rID), nil)
	require.Error(t, hErr)
	assert.True(t, errors.Is(hErr, shared.ErrBadRequest), "expected bad request, got %v", hErr)
}
