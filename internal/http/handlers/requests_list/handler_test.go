package requests_list_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/requests_list"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage"
)

func TestHandler_IncludesAuthorizedFlag(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = storage.NewInMemory(time.Minute, 8)
		h   = requests_list.New(db)
	)

	t.Cleanup(func() { _ = db.Close() })

	sID, err := db.NewSession(ctx, storage.Session{})
	require.NoError(t, err)

	_, err = db.NewRequest(ctx, sID, storage.Request{Method: "POST", Authorized: true})
	require.NoError(t, err)

	_, err = db.NewRequest(ctx, sID, storage.Request{Method: "POST", Authorized: false})
	require.NoError(t, err)

	resp, err := h.Handle(ctx, sID)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, *resp, 2)

	var sawTrue, sawFalse bool

	for _, r := range *resp {
		if r.Authorized {
			sawTrue = true
		} else {
			sawFalse = true
		}
	}

	assert.True(t, sawTrue, "an authorized request must surface authorized=true")
	assert.True(t, sawFalse, "a rejected request must surface authorized=false")
}
