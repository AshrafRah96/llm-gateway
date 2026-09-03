# Architecture

This gateway sits between an application and OpenAI. It centralises model policy,
project-scoped retrieval, semantic reuse, quotas and usage tracking while remaining a
focused reference implementation rather than a complete production service.

## Request flow

```text
HTTP client (X-API-Key)
  |
  v
HMAC project authentication -- invalid --> 401
  |
  v
rate limit + global/project concurrency caps -- full --> 429 / 503
  |
  v
project-filtered vector retrieval
  |                         \
  |                          \-- no match: continue ungrounded
  v
deterministic primary model route
  |
  v
project + model + corpus-revision semantic response cache -- hit --> response
  |
  v
OpenAI primary -- transient pre-output failure --> one configured fallback
  |
  v
completion or SSE stream --> meter --> cache complete success --> respond
```

Retrieved documents are untrusted reference data, not executable instructions. The
gateway does not expose tools or downstream actions to the model. A retrieval miss still
calls the model but marks the response as ungrounded; a successful retrieval returns
bounded source metadata.

## Modules and seams

| Module | Responsibility |
|---|---|
| `application` | Validates configuration, owns Redis, and composes the HTTP server. |
| `auth` | Looks up HMAC-fingerprinted project keys and returns a non-secret principal. |
| `rag` | Validates, chunks, embeds and retrieves operator-reviewed project documents. |
| `completion` | Coordinates retrieval, routing, cache, provider calls, metering and streaming. |
| `cache` | Performs semantic response caching within a project/model/corpus namespace. |
| `ratelimit` | Makes distributed sliding-window decisions in Redis. |
| `provider` | Translates OpenAI HTTP and SSE wire formats into provider-neutral events. |
| `handler` | Decodes HTTP requests and emits JSON or stable SSE responses. |

Both `/chat` and `/chat/stream` use the same completion lifecycle. Normal stream
exhaustion settles cache, usage and logs; `Close` is an idempotent fallback for client
abandonment.

## Project identity, RAG and cache isolation

Authentication derives a project principal from an HMAC fingerprint of the caller key.
Raw keys do not enter completion, rate-limit, usage or cache state.

RAG stores each chunk with a project fingerprint and filters that field before Redis
Search ranks vectors. Each response-cache namespace contains:

- `tenant`: SHA-256 of the authenticated project ID;
- `model`: the deterministic route's selected model;
- `version`: the cache schema and current project corpus revision.

Changing a project's documents advances its corpus revision, so a response generated
from prior context cannot be replayed. Cache operations retain one prompt embedding for
both lookup and store; cache failures are logged and treated as misses.

## Resource and failure policy

Requests have bounded bodies, prompts, deadlines, output-token caps, and global and
per-project concurrency limits before they reach an embedding or completion provider.
Authentication and limits fail closed. Cache and retrieval failures fail open as
ungrounded completion requests because they are optimisations, not authorization rules.

The configured fallback is attempted once only for a transient transport failure or an
HTTP 408, 429, or 5xx before any output is delivered. A stream is never replayed after
it emits content. Complete successful responses may be cached; partial streams are
metered with a labelled token estimate and are never cached.

## Further reading

- [Project-scoped RAG](RAG.md)
- [Threat model](THREAT-MODEL.md)
- [Roadmap](ROADMAP.md)
- [Decision records](adr/README.md)
- [Evaluation method](EVALUATION.md)
