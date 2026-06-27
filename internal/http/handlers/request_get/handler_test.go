package request_get_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/request_get"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage"
)

func TestHandler_IncludesAuthorizedFlag(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = storage.NewInMemory(time.Minute, 8)
		h   = request_get.New(db)
	)

	t.Cleanup(func() { _ = db.Close() })

	sID, err := db.NewSession(ctx, storage.Session{})
	require.NoError(t, err)

	// a rejected (inbound-auth-failed) request is still captured, flagged authorized=false
	rID, err := db.NewRequest(ctx, sID, storage.Request{Method: "POST", Authorized: false})
	require.NoError(t, err)

	rUUID, err := uuid.Parse(rID)
	require.NoError(t, err)

	resp, err := h.Handle(ctx, sID, rUUID)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.False(t, resp.Authorized, "rejected request must surface authorized=false")
}
