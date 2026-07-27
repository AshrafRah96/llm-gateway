# ADR-0003: The store decides the rate limit

Status: Accepted
Date: 2026-07-26
Supersedes: the original `Store.Increment` design

## Context

The first version of the rate limiter had a store that could count and a limiter that
could decide:

```go
type Store interface {
    Increment(ctx, key, window) (count int, err error)
    Count(ctx, key, window) (count int, err error)
}
```

`Limiter.Allow` called `Increment`, got back the new count, and compared it to the
limit. That ordering is forced by the interface: you cannot find out the count without
also recording a hit.

So a rejected request still landed in the window. A client that kept retrying kept
pushing its own lockout forward and never recovered, because every refused attempt reset
the clock it was waiting on.

The tests copied the bug. The mock store incremented a counter the same way as the real
store, then asserted that behavior was correct.

## Decision

Move the decision behind the interface:

```go
type Store interface {
    Allow(ctx, key, limit, window) (allowed bool, retryAfter time.Duration, err error)
    Count(ctx, key, window) (count int, err error)
}
```

The store trims the window, counts, and records the hit only if the request is accepted.
`Limiter` keeps the configuration and forwards.

See `internal/ratelimit/store.go`.

## Consequences

Refused requests stop consuming budget, so a hammering client recovers as soon as its
accepted requests age out.

The Redis adapter makes the decision in one Lua script instead of four pipelined
commands. Concurrent gateway instances therefore cannot accept against the same stale
count.

`retryAfter` is now the real time until a slot frees, computed from the oldest accepted
request, rather than a flat window length.

There is more logic inside Redis, which is harder to debug than Go. `MemoryStore` exists
partly to keep the contract testable without a server, and the same contract tests run
against both adapters.

## What we rejected

Keeping `Increment` and having the limiter compensate, for example by deleting the entry
it just added when it decides to refuse. Two round trips, and a crash between them
leaves the window wrong.

Checking before incrementing with two separate calls. Two clients can both check, both
see room, and both proceed. The whole reason to put this in Redis is atomicity.
