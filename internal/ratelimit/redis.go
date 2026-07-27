package ratelimit

import (
	"context"
	"math/rand/v2"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// allowScript trims the window, counts what is left, and records the request only if it
// is under the limit. Doing all of it in one script keeps the decision atomic across
// instances and collapses four round trips into one.
//
//	KEYS[1] = the key           ARGV[1] = now (unix millis)
//	ARGV[2] = window (millis)   ARGV[3] = limit
//	ARGV[4] = unique member
//
// Returns {allowed, retryAfterMillis}.
//
// Times are milliseconds, not nanoseconds: Redis Lua numbers are float64, so a
// nanosecond timestamp (~1.8e18) exceeds the 2^53 exact-integer range and stringifies
// in scientific notation. Members are supplied by the caller rather than derived from
// the score, so two requests in the same millisecond cannot collapse into one ZADD.
var allowScript = redis.NewScript(`
local key    = KEYS[1]
local now    = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local limit  = tonumber(ARGV[3])
local member = ARGV[4]

redis.call('ZREMRANGEBYSCORE', key, '-inf', now - window)
local count = redis.call('ZCARD', key)

if count >= limit then
  local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
  local retry = window
  if oldest[2] then
    retry = tonumber(oldest[2]) + window - now
    if retry < 0 then retry = 0 end
  end
  return {0, retry}
end

redis.call('ZADD', key, now, member)
redis.call('PEXPIRE', key, window)
return {1, 0}
`)

type RedisStore struct {
	client *redis.Client
}

func NewRedisStore(client *redis.Client) *RedisStore {
	return &RedisStore{client: client}
}

func (s *RedisStore) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error) {
	res, err := allowScript.Run(ctx, s.client,
		[]string{key},
		time.Now().UnixMilli(),
		millis(window),
		limit,
		// The score orders the window; the member only has to be unique, including
		// across gateway instances sharing this Redis.
		strconv.FormatUint(rand.Uint64(), 36),
	).Int64Slice()
	if err != nil {
		return false, 0, err
	}
	if len(res) != 2 {
		return false, 0, nil
	}

	return res[0] == 1, time.Duration(res[1]) * time.Millisecond, nil
}

func (s *RedisStore) Count(ctx context.Context, key string, window time.Duration) (int, error) {
	cutoff := time.Now().UnixMilli() - millis(window)
	count, err := s.client.ZCount(ctx, key, strconv.FormatInt(cutoff, 10), "+inf").Result()
	if err != nil {
		return 0, err
	}
	return int(count), nil
}

// millis floors at 1: Redis scores and PEXPIRE cannot express a sub-millisecond window.
func millis(window time.Duration) int64 {
	if ms := window.Milliseconds(); ms > 0 {
		return ms
	}
	return 1
}
