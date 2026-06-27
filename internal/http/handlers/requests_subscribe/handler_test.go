package requests_subscribe_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/requests_subscribe"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/openapi"
	"gh.tarampamp.am/webhook-tester/v2/internal/pubsub"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage"
)

// the per-session subscribe handler upgrades to a WebSocket and forwards events published to the
// per-session topic (keyed by the session uuid), mapping them to the OpenAPI RequestEvent shape. This
// asserts the inbound-auth `authorized` flag is mapped through, so the live Unauthorized badge works.
func TestRequestsSubscribe_EventCarriesAuthorized(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = storage.NewInMemory(time.Minute, 16)
		ps  = pubsub.NewInMemory[pubsub.RequestEvent]()
	)

	t.Cleanup(func() { _ = db.Close() })

	sID, err := db.NewSession(ctx, storage.Session{Code: 200, Slug: "rs-guarded"})
	require.NoError(t, err)

	var h = requests_subscribe.New(db, ps)

	var srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = h.Handle(r.Context(), w, r, sID)
	}))
	t.Cleanup(srv.Close)

	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	require.NoError(t, err)

	if resp != nil {
		_ = resp.Body.Close()
	}

	t.Cleanup(func() { _ = conn.Close() })

	// a rejected (inbound-auth-failed) capture is published to the per-session topic with authorized=false
	var event = pubsub.RequestEvent{
		Action: pubsub.RequestActionCreate,
		Request: &pubsub.Request{
			ID:                 "11111111-1111-4111-8111-111111111111",
			ClientAddr:         "203.0.113.7",
			Method:             http.MethodPost,
			URL:                "https://example.com/w/rs-guarded/x",
			CreatedAtUnixMilli: time.Now().UnixMilli(),
			Authorized:         false,
		},
	}

	// publish repeatedly to defeat the (tiny) subscribe-after-upgrade race; the client reads one.
	var stop = make(chan struct{})
	defer close(stop)

	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(25 * time.Millisecond):
				_ = ps.Publish(ctx, sID, event)
			}
		}
	}()

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(3*time.Second)))

	_, data, err := conn.ReadMessage()
	require.NoError(t, err, "failed to read a per-session request event from the websocket")

	var ev openapi.RequestEvent
	require.NoError(t, json.Unmarshal(data, &ev))

	require.Equal(t, openapi.RequestEventActionCreate, ev.Action)
	require.NotNil(t, ev.Request)
	require.Equal(t, http.MethodPost, ev.Request.Method)
	require.False(t, ev.Request.Authorized, "the per-session WS event must carry authorized=false for a rejected capture")
}
