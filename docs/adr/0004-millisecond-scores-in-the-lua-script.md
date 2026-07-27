# ADR-0004: Millisecond scores and caller-supplied members in the Lua script

Status: Accepted
Date: 2026-07-26

## Context

The sliding window is a Redis sorted set. Each accepted request is one member, scored by
when it happened, and the script trims anything older than the window before counting.

The first version scored by nanosecond timestamp and used that same timestamp as the
member:

```lua
redis.call('ZADD', key, now, now)   -- now = time.Now().UnixNano()
```

Redis runs Lua 5.1, where every number is a float64. Exact integers only go up to 2^53,
which is about 9.0e15. A nanosecond timestamp is around 1.8e18, so it does not survive
the round trip. Worse, Lua formats large numbers with `%.14g`, so `now` becomes a string
like `1.7717e+18`. Many distinct timestamps produce the same string.

`ZADD` treats a repeated member as an update, not an insertion. So requests silently
overwrote each other instead of accumulating, and the set held a handful of entries no
matter how much traffic arrived.

Measured against Redis 5.0.14, a burst of 50 accepted requests left 4 in the window, and
the limiter then allowed a request that should have been refused. The configured limit
was passing roughly twelve times the intended rate while reporting healthy.

## Decision

Score in milliseconds, and pass the member in from Go as a separate random string:

```lua
redis.call('ZADD', key, now, member)   -- now = UnixMilli, member = ARGV[4]
```

Millisecond timestamps are about 1.8e12, comfortably inside the exact integer range, and
they render without scientific notation. The member no longer derives from the score, so
two requests in the same millisecond cannot collide. Uniqueness comes from
`rand.Uint64()`, which also holds across gateway instances sharing one Redis.

See `internal/ratelimit/redis.go`.

## Consequences

Window resolution drops from nanoseconds to milliseconds, which does not matter for
limits measured in requests per minute. `millis()` floors at 1ms so a sub-millisecond
window cannot round to zero.

Sorted set members are now opaque random strings rather than readable timestamps, so
inspecting the key by hand tells you less. The score still carries the time.

`TestRedisStore_BurstInSameMillisecondCountsIndividually` pins the behaviour: 50 rapid
requests must leave 50 entries. It fails against the old version.

## What we rejected

Keeping nanosecond precision by passing the timestamp as a string and doing arithmetic
on it in Lua. Possible, but every comparison becomes a string parse, and sorted set
scores are float64 regardless, so the precision was never available where it mattered.

Calling `redis.call('TIME')` inside the script instead of passing the clock in. Removes
one argument, but makes the script's result depend on the server clock in a way the
tests cannot control.
