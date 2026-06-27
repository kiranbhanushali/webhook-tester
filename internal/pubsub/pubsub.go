package pubsub

import (
	"context"
)

type (
	Publisher[T any] interface {
		// Publish an event into the topic.
		Publish(_ context.Context, topic string, event T) error
	}

	Subscriber[T any] interface {
		// Subscribe to the topic. The returned channel will receive events.
		// The returned function should be called to unsubscribe.
		Subscribe(_ context.Context, topic string) (_ <-chan T, unsubscribe func(), _ error)
	}
)

type PubSub[T any] interface {
	Publisher[T]
	Subscriber[T]
}

type (
	RequestEvent struct {
		Action  RequestAction `json:"action"`
		Request *Request      `json:"request"`

		// Session metadata is populated ONLY for events published to the global firehose topic
		// (see FirehoseTopic); per-session events leave these empty. They let a cross-session
		// subscriber attribute each event to its originating session without a storage lookup.
		SessionUUID string `json:"session_uuid,omitempty"`
		SessionSlug string `json:"session_slug,omitempty"`
	}

	Request struct {
		ID                 string       `json:"id"`
		ClientAddr         string       `json:"client_addr"`
		Method             string       `json:"method"`
		Headers            []HttpHeader `json:"headers"`
		URL                string       `json:"url"`
		CreatedAtUnixMilli int64        `json:"created_at_unix_milli"`

		// Authorized is false when the request was rejected by inbound auth (it is still captured).
		// It is carried on firehose events so a cross-session dashboard can flag rejected captures.
		Authorized bool `json:"authorized,omitempty"`
	}

	HttpHeader struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}

	RequestAction = string
)

const (
	RequestActionCreate RequestAction = "create" // create a request
	RequestActionDelete RequestAction = "delete" // delete a request
	RequestActionClear  RequestAction = "clear"  // delete all requests
)

// FirehoseTopic is the single GLOBAL pub/sub topic that carries EVERY captured webhook event
// across ALL sessions, in addition to the per-session topic (keyed by session UUID). It is a
// reserved sentinel that can never collide with a session UUID, so the same PubSub instance (and
// driver — in-memory or redis) serves both the per-session and the cross-session "firehose" feeds.
const FirehoseTopic = "__firehose__"
