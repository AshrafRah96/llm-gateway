# ADR-0002: Cache on meaning, not exact text

Status: Superseded in part by ADR-0007
Date: 2026-07-26

## Context

A prompt-string cache is easy to build but misses ordinary rewordings. "What is the
capital of France?" and "France's capital city?" hash differently even though the
answer should be the same. Those misses still trigger a paid completion.

## Decision

Embed each prompt with `text-embedding-3-small` and store the answer against that
vector in Redis. On lookup, find the nearest stored vector and return its answer if the
cosine similarity is at least 0.95.

See `internal/cache/semantic.go`.

## Consequences

Rewordings hit the cache, which is the point.

Every cache attempt costs an embedding call, so a cache miss is more expensive than an
exact-key lookup. The attempt retains that embedding for a later store, so a cacheable
miss embeds once rather than repeating the external call. Embeddings are cheap compared
to completions, but semantic caching still stops paying for itself if most traffic is
unique.

Redis has to be the redis-stack image. Plain Redis has no vector search, so the gateway
will not start against it.

The 0.95 threshold has not been tuned against real traffic. Too low, and users receive
answers to questions they did not ask. Too high, and useful rewordings miss. The value
lives in one constant so measured evaluation can change it later.

## What we rejected

Exact-match caching on a hash of the prompt. Cheap, but it misses almost everything that
matters.

Caching per API key. This was later adopted in ADR-0007: confidentiality matters more
than the cross-tenant hit rate, and a semantic score is not an authorization check.
