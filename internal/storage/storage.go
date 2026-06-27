package storage

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	ErrNotFound        = errors.New("not found")
	ErrSessionNotFound = fmt.Errorf("session %w", ErrNotFound)
	ErrRequestNotFound = fmt.Errorf("request %w", ErrNotFound)

	ErrClosed = errors.New("closed")

	// ErrSearchUnsupported is returned by drivers (e.g. Redis) that do not implement
	// ListSessions or SearchRequests. The SQLite driver is the documented default for
	// those operations.
	ErrSearchUnsupported = errors.New("search is not supported by this storage backend")
)

// Storage manages Session and Request data.
type Storage interface {
	// NewSession creates a new session and returns a session ID on success.
	// The Session.CreatedAt field will be set to the current time.
	NewSession(_ context.Context, _ Session, id ...string) (sID string, _ error)

	// GetSession retrieves session data.
	// If the session is not found, ErrSessionNotFound will be returned.
	GetSession(_ context.Context, sID string) (*Session, error)

	// GetSessionBySlug retrieves session data by its human-readable slug.
	// If the slug is empty or no session with that slug exists, ErrNotFound will be returned.
	GetSessionBySlug(_ context.Context, slug string) (*Session, error)

	// AddSessionTTL adds the specified TTL to the session (and all its requests) with the specified ID.
	AddSessionTTL(_ context.Context, sID string, howMuch time.Duration) error

	// UpdateSession applies the non-nil fields of patch to the session with the given ID.
	// If the session is not found, ErrSessionNotFound will be returned.
	UpdateSession(_ context.Context, sID string, patch SessionPatch) error

	// DeleteSession removes the session with the specified ID.
	// If the session is not found, ErrSessionNotFound will be returned.
	DeleteSession(_ context.Context, sID string) error

	// NewRequest creates a new request for the session with the specified ID and returns a request ID on success.
	// The session with the specified ID must exist. The Request.CreatedAtUnixMilli field will be set to the
	// current time. The storage may limit the number of requests per session - in this case the oldest request
	// will be removed.
	// If the session is not found, ErrSessionNotFound will be returned.
	NewRequest(_ context.Context, sID string, _ Request) (rID string, _ error)

	// GetRequest retrieves request data.
	// If the request or session is not found, ErrNotFound (ErrSessionNotFound or ErrRequestNotFound) will be returned.
	GetRequest(_ context.Context, sID, rID string) (*Request, error)

	// GetAllRequests returns all requests for the session with the specified ID.
	// If the session is not found, ErrSessionNotFound will be returned. If there are no requests, an empty map
	// will be returned.
	GetAllRequests(_ context.Context, sID string) (map[string]Request, error)

	// DeleteRequest removes the request with the specified ID.
	// If the request or session is not found, ErrNotFound (ErrSessionNotFound or ErrRequestNotFound) will be returned.
	DeleteRequest(_ context.Context, sID, rID string) error

	// DeleteAllRequests removes all requests for the session with the specified ID.
	// If the session is not found, ErrSessionNotFound will be returned.
	DeleteAllRequests(_ context.Context, sID string) error

	// ListSessions returns a summary of all non-expired sessions, optionally filtered by f.
	// SessionFilter.Group performs an exact match on GroupName; SessionFilter.Query is a
	// substring match applied to ID, Slug, and GroupName.
	ListSessions(_ context.Context, f SessionFilter) ([]SessionSummary, error)

	// SearchRequests scans stored requests for identifier key/value matches.
	// Scanning is non-indexed for inmemory/fs drivers (linear scan); SQLite is the
	// documented default for high-volume indexed search.
	// Drivers that do not implement search return ErrSearchUnsupported.
	SearchRequests(_ context.Context, q IdentifierQuery) ([]RequestMatch, error)

	// ListRequestsAfter returns the session's captured requests whose Seq is strictly
	// greater than afterSeq, ordered by Seq ascending (FIFO, oldest first), capped at
	// limit (a non-positive limit applies defaultListLimit). Each returned Request has
	// its output-only ID and Seq populated so callers can build an incremental,
	// no-skip/no-duplicate cursor (use the last returned Seq as the next afterSeq).
	// If the session is not found, ErrSessionNotFound is returned. Drivers without a
	// durable monotonic sequence (Redis) return ErrSearchUnsupported.
	ListRequestsAfter(_ context.Context, sID string, afterSeq int64, limit int) ([]Request, error)

	// ListRequestsPage returns the session's captured requests with Seq strictly less than
	// beforeSeq, ordered by Seq descending (NEWEST first), capped at limit (a non-positive
	// limit applies defaultListLimit). When beforeSeq is non-positive the newest page is
	// returned (no upper bound). Each returned Request has its output-only ID and Seq
	// populated so callers can page backwards through history with no skips/duplicates: use
	// the Seq of the last (oldest) returned request as the next beforeSeq. It backs the
	// cursor-paginated requests-list API. If the session is not found, ErrSessionNotFound is
	// returned. Drivers without a durable monotonic sequence (Redis) return ErrSearchUnsupported.
	ListRequestsPage(_ context.Context, sID string, beforeSeq int64, limit int) ([]Request, error)
}

// defaultListLimit caps ListRequestsAfter results when the caller passes a non-positive limit.
const defaultListLimit = 1000

type (
	// Session describes session settings (like response data and any additional information).
	Session struct {
		// ID is the session's unique identifier (UUID). It is an output-only field:
		// it is populated on reads (GetSession and GetSessionBySlug) and is ignored by
		// NewSession, whose id comes from the variadic argument or a generated value.
		ID                 string        `json:"-"`                     // session ID (populated on reads)
		Code               uint16        `json:"code"`                  // default server response code
		Headers            []HttpHeader  `json:"headers"`               // server response headers
		ResponseBody       []byte        `json:"body"`                  // server response body (payload)
		Delay              time.Duration `json:"delay"`                 // delay before response sending
		CreatedAtUnixMilli int64         `json:"created_at_unit_milli"` // creation time
		ExpiresAt          time.Time     `json:"-"`                     // expiration time
		Slug               string        `json:"slug"`                  // human-readable URL slug
		GroupName          string        `json:"group_name"`            // logical group for multi-tenant use
		ResponseScript     string        `json:"response_script"`       // go-template response script
		SecurityHeaders    []HttpHeader  `json:"security_headers"`      // extra security response headers
		ForwardURL         string        `json:"forward_url"`           // upstream URL for request forwarding
		LongLived          bool          `json:"long_lived"`            // if true, session does not expire

		// InboundAuthHeader is the name of the HTTP header an incoming webhook POST must carry to
		// be authorized on the public /w/{slug} path. An empty value disables inbound auth (the
		// endpoint is public — current behavior). The lookup is case-insensitive.
		InboundAuthHeader string `json:"inbound_auth_header"`
		// InboundAuthValue is the expected value of InboundAuthHeader (a secret). It is compared to
		// the incoming header value in constant time. Ignored when InboundAuthHeader is empty.
		InboundAuthValue string `json:"inbound_auth_value"`
	}

	// Request describes recorded request and additional meta-data.
	Request struct {
		ClientAddr         string       `json:"client_addr"`           // client hostname or IP address
		Method             string       `json:"method"`                // HTTP method name (i.e., 'GET', 'POST')
		Body               []byte       `json:"body"`                  // request body (payload)
		Headers            []HttpHeader `json:"headers"`               // HTTP request headers
		URL                string       `json:"url"`                   // Uniform Resource Identifier
		CreatedAtUnixMilli int64        `json:"created_at_unit_milli"` // creation time

		// ID is the request's unique identifier (UUID). It is an output-only field:
		// it is populated on reads (GetRequest, GetAllRequests, ListRequestsAfter) and
		// is ignored by NewRequest, whose id is generated by the storage driver.
		ID string `json:"-"` // request ID (populated on reads)

		// Seq is the durable, strictly-increasing, never-reused capture sequence used by
		// the FIFO events-fetch cursor. It is an output-only field: populated on reads and
		// assigned by NewRequest (any caller-provided value is ignored). The SQLite driver
		// backs it with a durable counter that survives eviction and full request wipes; the
		// inmemory/fs drivers use a per-driver atomic counter (monotonic for the process
		// lifetime, reset on restart). The Redis driver has no sequence (best-effort 0).
		Seq int64 `json:"seq"`

		// Authorized reports whether the captured request satisfied the session's inbound-auth
		// configuration. It is true for sessions with no inbound auth and for requests that
		// presented the correct header/value; it is false for requests rejected by inbound auth
		// (which are still captured). It is an output field, set by the caller (the webhook
		// middleware) at capture time and persisted/returned by every driver.
		Authorized bool `json:"authorized"`
	}

	HttpHeader struct {
		Name  string `json:"name"`  // the name of the header, e.g. "Content-Type"
		Value string `json:"value"` // the value of the header, e.g. "application/json"
	}

	// SessionPatch holds optional overrides applied by UpdateSession.
	// Only non-nil pointer fields are written; nil fields are left unchanged.
	SessionPatch struct {
		Slug              *string
		GroupName         *string
		ResponseScript    *string
		ForwardURL        *string
		Code              *uint16
		Headers           *[]HttpHeader
		SecurityHeaders   *[]HttpHeader
		ResponseBody      *[]byte
		Delay             *time.Duration
		LongLived         *bool
		InboundAuthHeader *string // empty string clears (disables) inbound auth
		InboundAuthValue  *string
	}

	// SessionFilter restricts which sessions are returned by ListSessions.
	SessionFilter struct {
		// Group is an exact match on Session.GroupName (empty = no filter).
		Group string
		// Query is a case-sensitive substring match applied to ID, Slug, and GroupName (empty = no filter).
		Query string
	}

	// SessionSummary is the lightweight listing representation of a session.
	SessionSummary struct {
		ID                   string
		Slug                 string
		GroupName            string
		Code                 uint16
		RequestsCount        int
		LastRequestUnixMilli int64
		CreatedAtUnixMilli   int64
		ExpiresAtUnixMilli   int64
		LongLived            bool
	}

	// IdentifierMatch controls how the Value field of an IdentifierQuery is compared.
	IdentifierMatch string

	// IdentifierQuery is the search query passed to SearchRequests.
	IdentifierQuery struct {
		Key           string          // identifier key (header name or JSON field name)
		Value         string          // value to match
		Match         IdentifierMatch // exact or prefix
		SessionID     string          // restrict to a specific session (empty = all)
		Group         string          // restrict to sessions in a group (empty = all)
		FromUnixMilli int64           // lower bound on request capture time (0 = no bound)
		ToUnixMilli   int64           // upper bound on request capture time (0 = no bound)
		Limit         int             // maximum number of matches to return (0 = no limit)
	}

	// RequestMatch is a single result returned by SearchRequests.
	RequestMatch struct {
		SessionID           string
		SessionSlug         string
		RequestID           string
		Key                 string
		Value               string
		CapturedAtUnixMilli int64
	}

	// IdentifierRef is a single captured identifier joined to its session and request.
	// It is a neutral storage-package type so the durable→hot-index warm-up path can
	// be expressed without storage importing the hotindex package (avoiding a cycle):
	// the CLI converts these refs into the hot index's composite-keyed map. Drivers
	// that can serve a recent back-fill expose it via ListRecentIdentifiers.
	IdentifierRef struct {
		Key                 string
		Value               string
		SessionID           string
		SessionSlug         string
		RequestID           string
		CapturedAtUnixMilli int64
	}
)

const (
	// IdentifierMatchExact requires the value to be an exact match.
	IdentifierMatchExact IdentifierMatch = "exact"
	// IdentifierMatchPrefix requires the value to start with the query value.
	IdentifierMatchPrefix IdentifierMatch = "prefix"
)

// TimeFunc is a function that returns the current time.
type TimeFunc func() time.Time

// defaultTimeFunc is the default TimeFunc implementation, which returns the current time rounded to milliseconds.
func defaultTimeFunc() time.Time { return time.Now().Round(time.Millisecond) }
