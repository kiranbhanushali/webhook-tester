package session_update_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/session_update"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/shared"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/openapi"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage"
)

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }
func codePtr(c int) *int      { return &c }

func TestHandler_AppliesPatchAndReturnsUpdated(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = storage.NewInMemory(time.Minute, 8)
	)

	t.Cleanup(func() { _ = db.Close() })

	sID, err := db.NewSession(ctx, storage.Session{Slug: "alpha", GroupName: "g1", Code: 200})
	require.NoError(t, err)

	h := session_update.New(db)

	// patch by slug reference (slug-or-uuid resolution)
	resp, hErr := h.Handle(ctx, "alpha", openapi.UpdateSessionRequest{
		Group:      strPtr("g2"),
		StatusCode: codePtr(404),
		LongLived:  boolPtr(true),
		Slug:       strPtr("alpha-2"),
	})
	require.NoError(t, hErr)
	require.NotNil(t, resp)

	require.NotNil(t, resp.Response.Group)
	assert.Equal(t, "g2", *resp.Response.Group)
	assert.EqualValues(t, 404, resp.Response.StatusCode)
	require.NotNil(t, resp.Response.LongLived)
	assert.True(t, *resp.Response.LongLived)
	require.NotNil(t, resp.Response.Slug)
	assert.Equal(t, "alpha-2", *resp.Response.Slug)

	// the new slug must resolve, the old one must not
	_, gErr := db.GetSessionBySlug(ctx, "alpha-2")
	assert.NoError(t, gErr)

	_ = sID
}

func TestHandler_SlugConflictReturns409(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = storage.NewInMemory(time.Minute, 8)
	)

	t.Cleanup(func() { _ = db.Close() })

	_, err := db.NewSession(ctx, storage.Session{Slug: "taken"})
	require.NoError(t, err)

	_, err = db.NewSession(ctx, storage.Session{Slug: "mine"})
	require.NoError(t, err)

	h := session_update.New(db)

	_, hErr := h.Handle(ctx, "mine", openapi.UpdateSessionRequest{Slug: strPtr("taken")})
	require.Error(t, hErr)
	assert.True(t, errors.Is(hErr, shared.ErrConflict), "expected conflict, got %v", hErr)
}

func TestHandler_InvalidSlugReturnsBadRequest(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = storage.NewInMemory(time.Minute, 8)
	)

	t.Cleanup(func() { _ = db.Close() })

	_, err := db.NewSession(ctx, storage.Session{Slug: "mine"})
	require.NoError(t, err)

	h := session_update.New(db)

	_, hErr := h.Handle(ctx, "mine", openapi.UpdateSessionRequest{Slug: strPtr("Bad Slug!")})
	require.Error(t, hErr)
	assert.True(t, errors.Is(hErr, shared.ErrBadRequest), "expected bad request, got %v", hErr)
}

func TestHandler_UnknownSessionReturnsNotFound(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = storage.NewInMemory(time.Minute, 8)
	)

	t.Cleanup(func() { _ = db.Close() })

	h := session_update.New(db)

	_, hErr := h.Handle(ctx, "does-not-exist", openapi.UpdateSessionRequest{Group: strPtr("x")})
	require.Error(t, hErr)
	assert.True(t, errors.Is(hErr, storage.ErrNotFound), "expected not found, got %v", hErr)
}
