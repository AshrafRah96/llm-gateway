# Architecture

This gateway sits between an application and OpenAI. Its purpose is to keep policy,
cost control and reusable infrastructure out of every calling application.

It is a production-minded reference implementation, not a complete production service.
The [threat model](THREAT-MODEL.md) separates implemented controls from open risks.

## Request flow

```text
HTTP client
   │ X-API-Key
   ▼
authentication ── invalid ──▶ 401
   ▼
atomic sliding-window limit ── full ──▶ 429
   ▼
route the prompt to a model
   ▼
tenant + model + schema filtered vector lookup
   ├─ hit ────────────────────────────▶ cached response
   ▼ miss
OpenAI completion or SSE stream
   ▼
meter usage → cache complete successes → respond
```

Routing happens before cache lookup. The routed model is part of the cache namespace,
so an answer produced for one model cannot silently stand in for another.

## Modules and seams

| Module | Interface seen by callers | Complexity hidden behind it |
|---|---|---|
| `completion` | Complete or stream one request | Routing, caching, provider calls, metering and logging |
| `cache` | Get or set within a namespace | Embeddings, Redis Search, similarity policy, TTL and schema versioning |
| `ratelimit` | Allow and status | Sliding-window state and atomic Redis Lua execution |
| `provider` | Complete or stream | OpenAI HTTP request and response shapes |
| `handler` | HTTP routes | JSON/SSE translation and response headers |

`completion` is the central module. Both `/chat` and `/chat/stream` cross the same seam
so their caching and billing behavior cannot drift apart. The streaming implementation
is more complex because it must forward bytes while accumulating content and usage.
That complexity stays behind the `Stream` interface.

The cache and rate limiter each have real adapter seams. Production uses Redis and
OpenAI; tests use deterministic adapters without external calls.

## Semantic-cache namespace

Every cache operation carries:

- `tenant`: SHA-256 of the caller's API key;
- `model`: the model selected by the router;
- `version`: the cache schema version.

Redis Search applies these as hard filters before ranking vectors. Semantic similarity
can select only within that namespace. Redis keys contain hashes rather than raw keys or
prompts, and entries expire after `CACHE_TTL` (`24h` by default).

Schema v2 uses the `cache:v2:` prefix and `prompt_cache_v2` index. Old unscoped entries
remain unreadable and can be removed separately; there is no unsafe migration path.

## Failure behavior

| Failure | Behavior |
|---|---|
| Invalid key | Return 401 without calling OpenAI |
| Local quota full | Return 429 with `Retry-After` |
| Cache embedding/search/write fails | Log it and continue as a cache miss |
| OpenAI transport fails | Return 502 |
| OpenAI returns a status | Preserve the status where the completion interface exposes it |
| Client abandons a stream | Cancel OpenAI, estimate received usage, label the estimate, never cache the partial answer |
| Usage storage fails | Log it; the response is still returned |

Cache failure is fail-open because the cache is an optimization. Authentication and
rate limiting fail closed because they are controls. Best-effort usage storage is an
explicit limitation tracked in the [roadmap](ROADMAP.md).

## Why Redis

One Redis deployment currently holds API keys, quotas, usage totals and vector entries.
Redis sorted sets plus Lua make the rate-limit decision atomic across gateway instances.
Redis Search supports vector ranking with tenant/model metadata filters.

This is economical for a portfolio project but couples data with different durability
needs. A production design should separate disposable cache data from auditable billing
records.

## Further reading

- [Decision records](adr/README.md)
- [Engineering notes](ENGINEERING-NOTES.md)
- [Threat model](THREAT-MODEL.md)
- [Evaluation method](EVALUATION.md)
