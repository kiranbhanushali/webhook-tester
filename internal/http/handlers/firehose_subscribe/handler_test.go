package firehose_subscribe_test

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
	"go.uber.org/zap"

	"gh.tarampamp.am/webhook-tester/v2/internal/config"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/firehose_subscribe"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/middleware/webhook"
	"gh.tarampamp.am/webhook-tester/v2/internal/http/openapi"
	"gh.tarampamp.am/webhook-tester/v2/internal/pubsub"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage"
)

// newFirehoseWS starts an httptest server that serves the firehose subscribe handler over the given
// pub/sub and returns a connected WebSocket client (closed via t.Cleanup).
func newFirehoseWS(t *testing.T, ps pubsub.PubSub[pubsub.RequestEvent]) *websocket.Conn {
	t.Helper()

	var h = firehose_subscribe.New(ps)

	var srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = h.Handle(r.Context(), w, r)
	}))
	t.Cleanup(srv.Close)

	var wsURL = "ws" + strings.TrimPrefix(srv.URL, "http")

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)

	if resp != nil {
		_ = resp.Body.Close()
	}

	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

// readFirehoseEvent reads exactly one JSON firehose event from the connection (with a deadline).
func readFirehoseEvent(t *testing.T, conn *websocket.Conn) openapi.FirehoseEvent {
	t.Helper()

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(3*time.Second)))

	_, data, err := conn.ReadMessage()
	require.NoError(t, err, "failed to read a firehose event from the websocket")

	var ev openapi.FirehoseEvent
	require.NoError(t, json.Unmarshal(data, &ev))

	return ev
}

func newMemDB(t *testing.T) storage.Storage {
	t.Helper()

	var db = storage.NewInMemory(time.Minute, 16)

	t.Cleanup(func() { _ = db.Close() })

	return db
}

// the firehose handler upgrades to a WebSocket and forwards events published to the global firehose
// topic, mapping them to the OpenAPI FirehoseEvent shape (session metadata + request summary).
func TestFirehoseSubscribe_StreamsDirectlyPublishedEvent(t *testing.T) {
	t.Parallel()

	var (
		ctx  = context.Background()
		ps   = pubsub.NewInMemory[pubsub.RequestEvent]()
		conn = newFirehoseWS(t, ps)
	)

	const (
		sessUUID = "00000000-0000-4000-8000-000000000001"
		reqUUID  = "11111111-1111-4111-8111-111111111111"
	)

	var event = pubsub.RequestEvent{
		Action:      pubsub.RequestActionCreate,
		SessionUUID: sessUUID,
		SessionSlug: "fh-direct",
		Request: &pubsub.Request{
			ID:                 reqUUID,
			ClientAddr:         "203.0.113.7",
			Method:             http.MethodPost,
			URL:                "https://example.com/w/fh-direct/path",
			CreatedAtUnixMilli: time.Now().UnixMilli(),
			Authorized:         true,
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
				_ = ps.Publish(ctx, pubsub.FirehoseTopic, event)
			}
		}
	}()

	var ev = readFirehoseEvent(t, conn)

	require.Equal(t, openapi.FirehoseEventActionCreate, ev.Action)
	require.Equal(t, sessUUID, ev.SessionUuid.String())
	require.Equal(t, "fh-direct", ev.SessionSlug)

	require.NotNil(t, ev.Request)
	require.Equal(t, reqUUID, ev.Request.Uuid.String())
	require.Equal(t, http.MethodPost, ev.Request.Method)
	require.Equal(t, "203.0.113.7", ev.Request.ClientAddress)
	require.Equal(t, "https://example.com/w/fh-direct/path", ev.Request.Url)
	require.True(t, ev.Request.Authorized)
}

// end-to-end: a webhook captured through the real middleware (sharing one pub/sub) reaches a firehose
// WebSocket subscriber, carrying the session slug and the authorized=false flag for a rejected capture.
func TestFirehoseSubscribe_EndToEndUnauthorizedCapture(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		db  = newMemDB(t)
		ps  = pubsub.NewInMemory[pubsub.RequestEvent]()
	)

	_, err := db.NewSession(ctx, storage.Session{
		Code:              200,
		Slug:              "fh-e2e",
		InboundAuthHeader: "X-Webhook-Token",
		InboundAuthValue:  "s3cr3t",
	})
	require.NoError(t, err)

	// the webhook capture middleware, wired to the SAME pub/sub as the firehose handler
	var capture = webhook.New(ctx, zap.NewNop(), db, ps,
		&config.AppSettings{SessionTTL: time.Minute}, nil, nil, time.Second,
	)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }))

	var conn = newFirehoseWS(t, ps)

	// retry the capture until the subscriber (connected above) observes it — each POST without the
	// required inbound-auth header is rejected (401) but still captured and published.
	var stop = make(chan struct{})
	defer close(stop)

	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(25 * time.Millisecond):
				var rec = httptest.NewRecorder()

				capture.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/w/fh-e2e", strings.NewReader(`{}`)))
			}
		}
	}()

	var ev = readFirehoseEvent(t, conn)

	require.Equal(t, openapi.FirehoseEventActionCreate, ev.Action)
	require.Equal(t, "fh-e2e", ev.SessionSlug)
	require.NotEmpty(t, ev.SessionUuid.String())
	require.NotNil(t, ev.Request)
	require.False(t, ev.Request.Authorized, "a rejected capture must reach the firehose with authorized=false")
}
