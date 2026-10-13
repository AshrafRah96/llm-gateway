# ADR-0003: The store decides the rate limit

Status: Accepted
Date: 2026-07-26
Supersedes: the original `Store.Increment` design

## Original seam

```go
type Store interface {
    Increment(ctx, key, window) (count int, err error)
    Count(ctx, key, window) (count int, err error)
}
```

Counting forced recording first. Refused attempts entered the window and extended lockout;
retrying callers could prevent their own recovery. The mock copied the same bug.

## Decision

```go
type Store interface {
    Allow(ctx, key, limit, window) (allowed bool, retryAfter time.Duration, err error)
    Count(ctx, key, window) (count int, err error)
}
```

The store trims, counts, decides, and records only accepted requests.
`Limiter` owns configuration and forwards. Redis uses one atomic Lua script;
`MemoryStore` keeps the same contract testable without a server.
`retryAfter` derives from the oldest accepted request rather than a flat window.

## Tradeoff

Refused requests no longer consume quota. More admission logic lives in Redis, which is harder to debug than Go.
Both adapters face the same contract tests. See `internal/ratelimit/store.go`.

Rejected: compensating deletion after `Increment`, because crashes leave wrong state;
separate check/increment calls, because concurrent callers can both see room.
