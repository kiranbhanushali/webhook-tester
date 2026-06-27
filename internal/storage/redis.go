package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"gh.tarampamp.am/webhook-tester/v2/internal/encoding"
)

type (
	Redis struct {
		sessionTTL  time.Duration
		maxRequests uint32
		client      redis.Cmdable
		encDec      encoding.EncoderDecoder
		timeNow     TimeFunc
	}
)

var _ Storage = (*Redis)(nil) // ensure interface implementation

type RedisOption func(*Redis)

// WithRedisTimeNow sets the function that returns the current time.
func WithRedisTimeNow(fn TimeFunc) RedisOption { return func(s *Redis) { s.timeNow = fn } }

// NewRedis creates a new Redis storage.
// Notes:
//   - sTTL is the session TTL (redis accuracy is in milliseconds)
//   - maxReq is the maximum number of requests to store for the session
func NewRedis(c redis.Cmdable, sTTL time.Duration, maxReq uint32, opts ...RedisOption) *Redis {
	var s = Redis{
		sessionTTL:  sTTL,
		maxRequests: maxReq,
		client:      c,
		encDec:      encoding.JSON{},
		timeNow:     defaultTimeFunc,
	}

	for _, opt := range opts {
		opt(&s)
	}

	return &s
}

// sessionKey returns the key for the session data.
func (*Redis) sessionKey(sID string) string { return "webhook-tester-v2:session:" + sID }

// slugKey returns the Redis key that maps a slug to a session ID.
func (*Redis) slugKey(slug string) string { return "webhook-tester-v2:slug:" + slug }

// requestsKey returns the key for the requests list.
func (s *Redis) requestsKey(sID string) string { return s.sessionKey(sID) + ":requests" }

// requestKey returns the key for the request data.
func (s *Redis) requestKey(sID, rID string) string { return s.sessionKey(sID) + ":requests:" + rID }

// newID generates a new (unique) ID.
func (*Redis) newID() string { return uuid.New().String() }

func (s *Redis) isSessionExists(ctx context.Context, sID string) (bool, error) {
	count, err := s.client.Exists(ctx, s.sessionKey(sID)).Result()
	if err != nil {
		return false, err
	}

	return count == 1, nil
}

func (s *Redis) NewSession(ctx context.Context, session Session, id ...string) (sID string, _ error) {
	if err := ctx.Err(); err != nil {
		return "", err // context is done
	}

	if len(id) > 0 { // use the specified ID
		if len(id[0]) == 0 {
			return "", errors.New("empty session ID")
		}

		sID = id[0]

		// check if the session with the specified ID already exists
		if exists, err := s.isSessionExists(ctx, sID); err != nil {
			return "", err
		} else if exists {
			return "", fmt.Errorf("session %s already exists", sID)
		}
	} else {
		sID = s.newID()
	}

	session.CreatedAtUnixMilli = s.timeNow().UnixMilli()

	data, mErr := s.encDec.Encode(session)
	if mErr != nil {
		return "", mErr
	}

	if err := s.client.Set(ctx, s.sessionKey(sID), data, s.sessionTTL).Err(); err != nil {
		return "", err
	}

	if session.Slug != "" {
		// slug key shares the session TTL so it expires together
		if err := s.client.Set(ctx, s.slugKey(session.Slug), sID, s.sessionTTL).Err(); err != nil {
			return "", err
		}
	}

	return sID, nil
}

func (s *Redis) GetSession(ctx context.Context, sID string) (*Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err // context is done
	}

	data, rErr := s.client.Get(ctx, s.sessionKey(sID)).Bytes()
	if rErr != nil {
		if errors.Is(rErr, redis.Nil) {
			return nil, ErrSessionNotFound
		}

		return nil, rErr
	}

	expire, err := s.client.PTTL(ctx, s.sessionKey(sID)).Result()
	if err != nil {
		return nil, err
	}

	var session Session
	if uErr := s.encDec.Decode(data, &session); uErr != nil {
		return nil, uErr
	}

	session.ExpiresAt = s.timeNow().Add(expire)

	session.ID = sID // populate the output-only ID field

	return &session, nil
}

func (s *Redis) AddSessionTTL(ctx context.Context, sID string, howMuch time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err // context is done
	}

	currentTTL, tErr := s.client.PTTL(ctx, s.sessionKey(sID)).Result()
	if tErr != nil {
		return tErr
	}

	if currentTTL < 0 {
		switch currentTTL { //nolint:exhaustive // https://redis.io/docs/latest/commands/ttl/
		case -2:
			return ErrSessionNotFound
		case -1:
			return fmt.Errorf("no associated expire: %w", ErrSessionNotFound)
		}

		return errors.New("unexpected TTL value")
	}

	// read all stored request UUIDs
	//nolint:goconst
	rIDs, rErr := s.client.ZRangeByScore(ctx, s.requestsKey(sID), &redis.ZRangeBy{Min: "-inf", Max: "+inf"}).Result()
	if rErr != nil {
		return rErr
	}

	// update the expiration date for the session and all requests
	// https://redis.io/docs/latest/commands/expire/
	if _, err := s.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		var newTTL = currentTTL + howMuch
		for _, rID := range rIDs {
			pipe.PExpire(ctx, s.requestKey(sID, rID), newTTL)
		}

		pipe.PExpire(ctx, s.requestsKey(sID), newTTL)
		pipe.PExpire(ctx, s.sessionKey(sID), newTTL)

		return nil
	}); err != nil {
		return err
	}

	return nil
}

func (s *Redis) DeleteSession(ctx context.Context, sID string) error {
	if err := ctx.Err(); err != nil {
		return err // context is done
	}

	// read the session first so we can remove the slug key if one exists
	sess, err := s.GetSession(ctx, sID)
	if err != nil {
		return err // ErrSessionNotFound or other
	}

	if result := s.client.Del(ctx, s.sessionKey(sID)); result.Err() != nil {
		return result.Err()
	} else if count, rErr := result.Result(); rErr != nil {
		return rErr
	} else if count == 0 {
		return ErrSessionNotFound
	}

	if sess.Slug != "" {
		_ = s.client.Del(ctx, s.slugKey(sess.Slug)).Err() // best-effort cleanup
	}

	return nil
}

func (s *Redis) NewRequest(ctx context.Context, sID string, r Request) (rID string, _ error) {
	if err := ctx.Err(); err != nil {
		return "", err // context is done
	}

	// check the session existence
	if exists, err := s.isSessionExists(ctx, sID); err != nil {
		return "", err
	} else if !exists {
		return "", ErrSessionNotFound
	}

	var now = s.timeNow()

	rID, r.CreatedAtUnixMilli = s.newID(), now.UnixMilli()

	// The Redis driver has no durable monotonic sequence (see ListRequestsAfter), so Seq is
	// best-effort 0; ID is output-only and reconstructed from the request key on read.
	r.ID, r.Seq = "", 0

	data, mErr := s.encDec.Encode(r)
	if mErr != nil {
		return "", mErr
	}

	// save the request data
	if _, err := s.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.ZAdd(ctx, s.requestsKey(sID), redis.Z{Score: float64(now.UnixMilli()), Member: rID})
		pipe.Set(ctx, s.requestKey(sID, rID), data, s.sessionTTL)

		return nil
	}); err != nil {
		return "", err
	}

	// read all stored request UUIDs
	ids, rErr := s.client.ZRangeByScore(ctx, s.requestsKey(sID), &redis.ZRangeBy{Min: "-inf", Max: "+inf"}).Result()
	if rErr != nil {
		return "", rErr
	}

	// if we have too many requests - remove unnecessary
	if s.maxRequests > 0 && len(ids) > int(s.maxRequests) {
		if _, err := s.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			for _, id := range ids[:len(ids)-int(s.maxRequests)] {
				pipe.ZRem(ctx, s.requestsKey(sID), id)
				pipe.Del(ctx, s.requestKey(sID, id))
			}

			return nil
		}); err != nil {
			return "", err
		}
	}

	return rID, nil
}

func (s *Redis) GetRequest(ctx context.Context, sID, rID string) (*Request, error) {
	if err := ctx.Err(); err != nil {
		return nil, err // context is done
	}

	// check the session existence
	if exists, err := s.isSessionExists(ctx, sID); err != nil {
		return nil, err
	} else if !exists {
		return nil, ErrSessionNotFound
	}

	data, rErr := s.client.Get(ctx, s.requestKey(sID, rID)).Bytes()
	if rErr != nil {
		if errors.Is(rErr, redis.Nil) {
			return nil, ErrRequestNotFound
		}

		return nil, rErr
	}

	var request Request
	if uErr := s.encDec.Decode(data, &request); uErr != nil {
		return nil, uErr
	}

	return &request, nil
}

func (s *Redis) GetAllRequests(ctx context.Context, sID string) (map[string]Request, error) {
	if err := ctx.Err(); err != nil {
		return nil, err // context is done
	}

	// check the session existence
	if exists, err := s.isSessionExists(ctx, sID); err != nil {
		return nil, err
	} else if !exists {
		return nil, ErrSessionNotFound
	}

	// read all stored request IDs
	ids, rErr := s.client.ZRangeByScore(ctx, s.requestsKey(sID), &redis.ZRangeBy{Min: "-inf", Max: "+inf"}).Result()
	if rErr != nil {
		return nil, rErr
	}

	if len(ids) == 0 {
		return make(map[string]Request), nil
	}

	var (
		all  = make(map[string]Request, len(ids))
		keys = make([]string, len(ids))
	)

	// convert request IDs to keys
	for i, id := range ids {
		keys[i] = s.requestKey(sID, id)
	}

	// read all request data
	data, mErr := s.client.MGet(ctx, keys...).Result()
	if mErr != nil {
		return nil, mErr
	}

	for i, d := range data {
		if d == nil {
			continue
		}

		if str, ok := d.(string); !ok {
			return nil, errors.New("unexpected data type")
		} else {
			var request Request
			if uErr := s.encDec.Decode([]byte(str), &request); uErr != nil {
				return nil, uErr
			}

			all[ids[i]] = request
		}
	}

	return all, nil
}

func (s *Redis) DeleteRequest(ctx context.Context, sID, rID string) error {
	if err := ctx.Err(); err != nil {
		return err // context is done
	}

	// check the session existence
	if exists, err := s.isSessionExists(ctx, sID); err != nil {
		return err
	} else if !exists {
		return ErrSessionNotFound
	}

	var deleted *redis.IntCmd

	// delete the request
	if _, err := s.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.ZRem(ctx, s.requestsKey(sID), rID)
		deleted = pipe.Del(ctx, s.requestKey(sID, rID))

		return nil
	}); err != nil {
		return err
	}

	if deleted.Val() == 0 {
		return ErrRequestNotFound
	}

	return nil
}

func (s *Redis) DeleteAllRequests(ctx context.Context, sID string) error {
	if err := ctx.Err(); err != nil {
		return err // context is done
	}

	// check the session existence
	if exists, err := s.isSessionExists(ctx, sID); err != nil {
		return err
	} else if !exists {
		return ErrSessionNotFound
	}

	// read all stored request IDs
	ids, rErr := s.client.ZRangeByScore(ctx, s.requestsKey(sID), &redis.ZRangeBy{Min: "-inf", Max: "+inf"}).Result()
	if rErr != nil {
		return rErr
	}

	// delete all requests
	if _, err := s.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for _, id := range ids {
			pipe.Del(ctx, s.requestKey(sID, id))
		}

		pipe.Del(ctx, s.requestsKey(sID))

		return nil
	}); err != nil {
		return err
	}

	return nil
}

// GetSessionBySlug retrieves a session by its human-readable slug using the stored slug→ID key.
// Returns ErrNotFound when the slug is empty or no mapping exists.
func (s *Redis) GetSessionBySlug(ctx context.Context, slug string) (*Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if slug == "" {
		return nil, ErrNotFound
	}

	sID, err := s.client.Get(ctx, s.slugKey(slug)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, ErrNotFound
		}

		return nil, err
	}

	return s.GetSession(ctx, sID)
}

// UpdateSession reads the current session data, applies the non-nil patch fields, and stores
// the result back. Returns ErrSessionNotFound when the session does not exist.
func (s *Redis) UpdateSession(ctx context.Context, sID string, patch SessionPatch) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	sess, err := s.GetSession(ctx, sID)
	if err != nil {
		return err
	}

	oldSlug := sess.Slug

	// apply patch
	if patch.Code != nil {
		sess.Code = *patch.Code
	}

	if patch.Slug != nil {
		sess.Slug = *patch.Slug
	}

	if patch.GroupName != nil {
		sess.GroupName = *patch.GroupName
	}

	if patch.ResponseScript != nil {
		sess.ResponseScript = *patch.ResponseScript
	}

	if patch.ForwardURL != nil {
		sess.ForwardURL = *patch.ForwardURL
	}

	if patch.Headers != nil {
		sess.Headers = *patch.Headers
	}

	if patch.SecurityHeaders != nil {
		sess.SecurityHeaders = *patch.SecurityHeaders
	}

	if patch.ResponseBody != nil {
		sess.ResponseBody = *patch.ResponseBody
	}

	if patch.Delay != nil {
		sess.Delay = *patch.Delay
	}

	if patch.LongLived != nil {
		sess.LongLived = *patch.LongLived
	}

	if patch.InboundAuthHeader != nil {
		sess.InboundAuthHeader = *patch.InboundAuthHeader
	}

	if patch.InboundAuthValue != nil {
		sess.InboundAuthValue = *patch.InboundAuthValue
	}

	data, mErr := s.encDec.Encode(sess)
	if mErr != nil {
		return mErr
	}

	// get remaining TTL to preserve it
	currentTTL, tErr := s.client.PTTL(ctx, s.sessionKey(sID)).Result()
	if tErr != nil {
		return tErr
	}

	if currentTTL < 0 {
		return ErrSessionNotFound
	}

	if err := s.client.Set(ctx, s.sessionKey(sID), data, currentTTL).Err(); err != nil {
		return err
	}

	// maintain slug index
	if oldSlug != sess.Slug {
		if oldSlug != "" {
			_ = s.client.Del(ctx, s.slugKey(oldSlug)).Err() // cleanup, best-effort
		}

		if sess.Slug != "" {
			if err := s.client.Set(ctx, s.slugKey(sess.Slug), sID, currentTTL).Err(); err != nil {
				return fmt.Errorf("updating slug index: %w", err)
			}
		}
	}

	return nil
}

// ListSessions is not supported by the Redis driver. Use the SQLite driver for listing sessions.
// Returns ErrSearchUnsupported.
func (s *Redis) ListSessions(_ context.Context, _ SessionFilter) ([]SessionSummary, error) {
	return nil, ErrSearchUnsupported
}

// SearchRequests is not supported by the Redis driver. Use the SQLite driver for indexed search.
// Returns ErrSearchUnsupported.
func (s *Redis) SearchRequests(_ context.Context, _ IdentifierQuery) ([]RequestMatch, error) {
	return nil, ErrSearchUnsupported
}

// ListRequestsAfter is not supported by the Redis driver: it has no durable, never-reused
// monotonic sequence to anchor the FIFO cursor (requests are scored by capture time, which
// is not collision-free and resets nothing on eviction). Use the SQLite driver — the
// documented default — for the incremental events-fetch API. Returns ErrSearchUnsupported.
func (s *Redis) ListRequestsAfter(_ context.Context, _ string, _ int64, _ int) ([]Request, error) {
	return nil, ErrSearchUnsupported
}

// ListRequestsPage is not supported by the Redis driver for the same reason as ListRequestsAfter:
// it has no durable, never-reused monotonic sequence to anchor a stable cursor. Use the SQLite
// driver — the documented default — for the cursor-paginated requests-list API. Returns
// ErrSearchUnsupported.
func (s *Redis) ListRequestsPage(_ context.Context, _ string, _ int64, _ int) ([]Request, error) {
	return nil, ErrSearchUnsupported
}
