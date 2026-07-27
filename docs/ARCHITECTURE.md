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
| `application` | Load configuration or build the application | Validation, concrete adapter graph, Redis ownership and HTTP server |
| `completion` | Complete or stream one request | Routing, caching, provider calls, metering and logging |
| `cache` | Begin one attempt within a namespace | One reusable embedding, Redis Search, similarity policy, TTL and schema versioning |
| `ratelimit` | Allow and status | Sliding-window state and atomic Redis Lua execution |
| `provider` | Complete or stream semantic events | OpenAI HTTP, SSE scanning and response decoding |
| `handler` | HTTP routes | Request JSON, stable response SSE and headers |

`completion` is the central module. Both `/chat` and `/chat/stream` cross the same seam
so their caching and billing behavior cannot drift apart. The streaming implementation
accumulates provider-neutral content and usage events while applying cache and billing
policy. OpenAI framing stays inside the provider adapter; outbound SSE framing stays
inside the HTTP handler.

A private request lifecycle owns routing, namespace construction, the cache attempt,
metering, logging and store eligibility for both delivery modes. A normal stream settles
that lifecycle when its provider ends. `Close` crosses the same idempotent settlement
path only as the fallback for an abandoned stream.

The cache and rate limiter each have real adapter seams. Production uses Redis and
OpenAI; tests use deterministic adapters without external calls.

## Startup and shutdown

`internal/application` parses the three environment settings, validates configuration,
constructs the concrete adapter graph and returns the HTTP server. Redis uses RESP2,
connectivity is checked before serving, and semantic-cache index creation receives the
same bounded startup context. The application owns and closes the Redis client.

`main` owns only process lifecycle: a ten-second startup budget, signal handling,
listening, a thirty-second graceful shutdown budget and application cleanup. Startup
errors return through the application interface before `main` decides to exit.

## Semantic-cache namespace

Every cache operation carries:

- `tenant`: SHA-256 of the caller's API key;
- `model`: the model selected by the router;
- `version`: the cache schema version.

Redis Search applies these as hard filters before ranking vectors. Semantic similarity
can select only within that namespace. Redis keys contain hashes rather than raw keys or
prompts, and entries expire after `CACHE_TTL` (`24h` by default).

A request begins one cache attempt. The attempt embeds and validates the prompt once,
then retains the encoded vector for both lookup and a later store. A cacheable miss
therefore does not pay for a second embedding call.

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
