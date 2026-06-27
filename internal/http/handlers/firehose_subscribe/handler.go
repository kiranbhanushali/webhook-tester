// Package firehose_subscribe implements the all-sessions "firehose" WebSocket: a single stream of
// every captured webhook event across ALL sessions, for an operator dashboard. It mirrors the
// per-session requests_subscribe handler's lifecycle (upgrade, subscribe loop, periodic ping,
// client-close handling, context cancel + unsubscribe on disconnect) but subscribes to the global
// pubsub.FirehoseTopic instead of a per-session topic, and emits the richer FirehoseEvent shape
// (session slug + uuid + the authorized flag).
package firehose_subscribe

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"gh.tarampamp.am/webhook-tester/v2/internal/http/openapi"
	"gh.tarampamp.am/webhook-tester/v2/internal/pubsub"
)

type Handler struct {
	sub      pubsub.Subscriber[pubsub.RequestEvent]
	upgrader websocket.Upgrader
}

func New(sub pubsub.Subscriber[pubsub.RequestEvent]) *Handler {
	return &Handler{sub: sub}
}

func (h *Handler) Handle(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	// upgrade the connection to the WebSocket
	ws, upgErr := h.upgrader.Upgrade(w, r, http.Header{})
	if upgErr != nil {
		return fmt.Errorf("failed to upgrade the connection: %w", upgErr)
	}

	defer func() { _ = ws.Close() }()

	// create a new context for the request
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// subscribe to the GLOBAL firehose topic (every captured request, across all sessions)
	sub, unsubscribe, err := h.sub.Subscribe(ctx, pubsub.FirehoseTopic)
	if err != nil {
		return fmt.Errorf("failed to subscribe to the firehose: %w", err)
	}

	defer unsubscribe()

	// read messages from the client in a separate goroutine and cancel the context when the
	// connection is closed or an error occurs
	go func() { defer cancel(); _ = h.reader(ctx, ws) }()

	// run a loop that sends events to the client and pings the client periodically
	return h.writer(ctx, ws, sub)
}

// reader reads (and discards) messages from the client. It must run in a separate goroutine to
// prevent blocking. It exits when the context is canceled, the client closes the connection, or a
// read error occurs.
func (*Handler) reader(ctx context.Context, ws *websocket.Conn) error {
	for {
		if ctx.Err() != nil { // check if the context is canceled
			return nil
		}

		var messageType, msgReader, msgErr = ws.NextReader()
		if msgErr != nil {
			return msgErr
		}

		if msgReader != nil {
			_, _ = io.Copy(io.Discard, msgReader) // ignore the message body but drain it to avoid leaks
		}

		if messageType == websocket.CloseMessage {
			return nil // client closed the connection
		}
	}
}

// writer sends firehose events to the client and pings it periodically. It blocks until the context
// is canceled, the client closes the connection, or a write error occurs.
func (h *Handler) writer(ctx context.Context, ws *websocket.Conn, sub <-chan pubsub.RequestEvent) error {
	const pingInterval, pingDeadline = 10 * time.Second, 5 * time.Second

	var pingTicker = time.NewTicker(pingInterval)
	defer pingTicker.Stop()

	for {
		select {
		case <-ctx.Done(): // check if the context is canceled
			return nil

		case e, isOpened := <-sub: // wait for the next captured request (any session)
			if !isOpened {
				return nil // this should never happen, but just in case
			}

			event, ok := convertEvent(e)
			if !ok {
				continue // skip events we cannot represent (unknown action / unparsable ids)
			}

			if err := ws.WriteJSON(event); err != nil {
				return fmt.Errorf("failed to write the message: %w", err)
			}

		case <-pingTicker.C: // send ping messages to the client
			if err := ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(pingDeadline)); err != nil {
				return fmt.Errorf("failed to send the ping message: %w", err)
			}
		}
	}
}

// convertEvent maps an internal pubsub firehose event to the OpenAPI wire shape. It returns ok=false
// when the event cannot be represented (unknown action or unparsable UUIDs) so the caller can skip it.
func convertEvent(e pubsub.RequestEvent) (openapi.FirehoseEvent, bool) {
	var action openapi.FirehoseEventAction

	switch e.Action {
	case pubsub.RequestActionCreate:
		action = openapi.FirehoseEventActionCreate
	case pubsub.RequestActionDelete:
		action = openapi.FirehoseEventActionDelete
	case pubsub.RequestActionClear:
		action = openapi.FirehoseEventActionClear
	default:
		return openapi.FirehoseEvent{}, false // unknown action
	}

	sessUUID, sErr := uuid.Parse(e.SessionUUID)
	if sErr != nil {
		return openapi.FirehoseEvent{}, false // event is not attributable to a session
	}

	var request *openapi.FirehoseEventRequest

	if e.Request != nil {
		rID, pErr := uuid.Parse(e.Request.ID)
		if pErr != nil {
			return openapi.FirehoseEvent{}, false
		}

		request = &openapi.FirehoseEventRequest{
			Uuid:                rID,
			CapturedAtUnixMilli: e.Request.CreatedAtUnixMilli,
			ClientAddress:       e.Request.ClientAddr,
			Method:              strings.ToUpper(e.Request.Method),
			Url:                 e.Request.URL,
			Authorized:          e.Request.Authorized,
		}
	}

	return openapi.FirehoseEvent{
		Action:      action,
		SessionUuid: sessUUID,
		SessionSlug: e.SessionSlug,
		Request:     request,
	}, true
}
