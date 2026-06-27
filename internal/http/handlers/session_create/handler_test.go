package session_create_test

import (
	"context"
	"errors"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/session_create"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/shared"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/openapi"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage"
)

var slugFormat = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,48}$`)

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }

func TestHandler_AutoGeneratesValidSlug(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = storage.NewInMemory(time.Minute, 8)
	)

	t.Cleanup(func() { _ = db.Close() })

	h := session_create.New(db)

	resp, err := h.Handle(ctx, openapi.CreateSessionRequest{
		StatusCode:         200,
		ResponseBodyBase64: "",
		Group:              strPtr("team-a"),
		ForwardUrl:         strPtr("https://example.com/hook"),
		LongLived:          boolPtr(true),
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.Response.Slug)
	assert.Truef(t, slugFormat.MatchString(*resp.Response.Slug), "auto slug %q invalid", *resp.Response.Slug)

	require.NotNil(t, resp.Response.Group)
	assert.Equal(t, "team-a", *resp.Response.Group)
	require.NotNil(t, resp.Response.ForwardUrl)
	assert.Equal(t, "https://example.com/hook", *resp.Response.ForwardUrl)
	require.NotNil(t, resp.Response.LongLived)
	assert.True(t, *resp.Response.LongLived)

	// generated slug must resolve in storage
	_, gErr := db.GetSessionBySlug(ctx, *resp.Response.Slug)
	assert.NoError(t, gErr)
}

func TestHandler_AcceptsUserSlug(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = storage.NewInMemory(time.Minute, 8)
	)

	t.Cleanup(func() { _ = db.Close() })

	h := session_create.New(db)

	resp, err := h.Handle(ctx, openapi.CreateSessionRequest{StatusCode: 200, Slug: strPtr("my-custom-slug")})
	require.NoError(t, err)
	require.NotNil(t, resp.Response.Slug)
	assert.Equal(t, "my-custom-slug", *resp.Response.Slug)
}

func TestHandler_DuplicateUserSlugReturns409(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = storage.NewInMemory(time.Minute, 8)
	)

	t.Cleanup(func() { _ = db.Close() })

	h := session_create.New(db)

	_, err := h.Handle(ctx, openapi.CreateSessionRequest{StatusCode: 200, Slug: strPtr("dup")})
	require.NoError(t, err)

	_, err = h.Handle(ctx, openapi.CreateSessionRequest{StatusCode: 200, Slug: strPtr("dup")})
	require.Error(t, err)
	assert.True(t, errors.Is(err, shared.ErrConflict), "expected conflict, got %v", err)
}

// SQLite is the production default storage; this proves a duplicate user slug on
// create surfaces a conflict end-to-end through the real backend (the dispatch
// layer maps it to HTTP 409 — see TestStatusForError in the http package).
func TestHandler_DuplicateUserSlugReturns409_SQLite(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	dsn := filepath.Join(t.TempDir(), "create.sqlite")
	db, err := storage.NewSQLite(ctx, dsn, time.Minute, 8)
	require.NoError(t, err)

	t.Cleanup(func() { _ = db.Close() })

	h := session_create.New(db)

	_, err = h.Handle(ctx, openapi.CreateSessionRequest{StatusCode: 200, Slug: strPtr("dup")})
	require.NoError(t, err)

	_, err = h.Handle(ctx, openapi.CreateSessionRequest{StatusCode: 200, Slug: strPtr("dup")})
	require.Error(t, err)
	assert.True(t, errors.Is(err, shared.ErrConflict), "expected conflict, got %v", err)
}

func TestHandler_PersistsInboundAuth(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = storage.NewInMemory(time.Minute, 8)
	)

	t.Cleanup(func() { _ = db.Close() })

	h := session_create.New(db)

	resp, err := h.Handle(ctx, openapi.CreateSessionRequest{
		StatusCode:        200,
		Slug:              strPtr("auth-create"),
		InboundAuthHeader: strPtr("X-Webhook-Token"),
		InboundAuthValue:  strPtr("super-secret"),
	})
	require.NoError(t, err)
	require.NotNil(t, resp.Response.InboundAuthHeader)
	assert.Equal(t, "X-Webhook-Token", *resp.Response.InboundAuthHeader)
	require.NotNil(t, resp.Response.InboundAuthValue)
	assert.Equal(t, "super-secret", *resp.Response.InboundAuthValue)

	// persisted in storage
	sess, gErr := db.GetSessionBySlug(ctx, "auth-create")
	require.NoError(t, gErr)
	assert.Equal(t, "X-Webhook-Token", sess.InboundAuthHeader)
	assert.Equal(t, "super-secret", sess.InboundAuthValue)
}

func TestHandler_InboundAuthHeaderWithoutValue_BadRequest(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = storage.NewInMemory(time.Minute, 8)
	)

	t.Cleanup(func() { _ = db.Close() })

	h := session_create.New(db)

	// header set but value omitted → 400
	_, err := h.Handle(ctx, openapi.CreateSessionRequest{
		StatusCode:        200,
		InboundAuthHeader: strPtr("X-Webhook-Token"),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, shared.ErrBadRequest), "expected bad request, got %v", err)

	// header set but value explicitly empty → 400
	_, err = h.Handle(ctx, openapi.CreateSessionRequest{
		StatusCode:        200,
		InboundAuthHeader: strPtr("X-Webhook-Token"),
		InboundAuthValue:  strPtr(""),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, shared.ErrBadRequest), "expected bad request, got %v", err)

	// both empty (inbound auth disabled) → ok
	_, err = h.Handle(ctx, openapi.CreateSessionRequest{
		StatusCode:        200,
		InboundAuthHeader: strPtr(""),
		InboundAuthValue:  strPtr(""),
	})
	require.NoError(t, err)
}

func TestHandler_InvalidUserSlugReturnsBadRequest(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = storage.NewInMemory(time.Minute, 8)
	)

	t.Cleanup(func() { _ = db.Close() })

	h := session_create.New(db)

	_, err := h.Handle(ctx, openapi.CreateSessionRequest{StatusCode: 200, Slug: strPtr("Bad Slug!")})
	require.Error(t, err)
	assert.True(t, errors.Is(err, shared.ErrBadRequest), "expected bad request, got %v", err)
}
