# ADR-0004: Millisecond scores and caller-supplied members in the Lua script

Status: Accepted
Date: 2026-07-26

## Failure

The sliding window used a nanosecond timestamp as both score and member:

```lua
redis.call('ZADD', key, now, now)   -- now = time.Now().UnixNano()
```

Redis Lua numbers use float64. Exact integers stop at 2^53, around 9.0e15;
nanosecond timestamps are around 1.8e18. Precision loss and `%.14g` string formatting
collapse distinct values into members such as `1.7717e+18`.
`ZADD` updates repeated members, so requests disappear from the count.

Measured on Redis 5.0.14: 50 accepted requests left four members; an over-limit request
was then admitted. The quota allowed roughly twelve times its intended rate.

## Decision

```lua
redis.call('ZADD', key, now, member)   -- now = UnixMilli, member = ARGV[4]
```

Millisecond timestamps are around 1.8e12. Go supplies a separate `rand.Uint64()` member,
avoiding deterministic collapse between same-millisecond requests and across gateway instances.
Random collisions remain theoretically possible.

`millis()` floors at 1ms. Quotas measured per minute do not need nanosecond resolution.
Members lose readable timestamps; scores retain time.
`TestRedisStore_BurstInSameMillisecondCountsIndividually` requires 50 burst requests to remain.
See `internal/ratelimit/redis.go`.

Rejected: string arithmetic on nanoseconds, because sorted-set scores still use float64;
server `TIME`, because it makes the clock an uncontrolled test input.
