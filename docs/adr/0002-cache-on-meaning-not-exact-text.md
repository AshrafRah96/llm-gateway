# ADR-0002: Cache on meaning, not exact text

Status: Superseded in part by ADR-0007
Date: 2026-07-26

## Context

A cache keyed on the prompt string is easy to build and almost useless here. "What is
the capital of France?" and "France's capital city?" hash differently, so an exact-match
cache misses. People reword constantly, and the whole value of caching an LLM response
is avoiding a call that costs real money.

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

The 0.95 threshold is a guess that has never been tuned against real traffic. Too low
and users get answers to questions they did not ask, which is worse than a cache miss.
Too high and it degrades to exact matching. It sits in one constant, so it is easy to
move once there is data to move it with.

## What we rejected

Exact-match caching on a hash of the prompt. Cheap, but it misses almost everything that
matters.

Caching per API key. This was later adopted in ADR-0007: confidentiality matters more
than the cross-tenant hit rate, and a semantic score is not an authorization check.
