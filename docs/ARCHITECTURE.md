# Architecture

Go HTTP gateway. OpenAI handles embeddings and chat. Redis Stack holds access, vector,
and usage state. All Redis boxes in the diagram represent one configured instance.

[![Current gateway architecture](diagrams/architecture.svg)](diagrams/architecture.svg)

[Interactive Archify viewer](diagrams/architecture.html)
· [Diagram source](diagrams/architecture.json)

## Request lifecycle

1. Authenticate key; carry non-secret project/key identity.
2. Apply key quota, process-wide and project concurrency caps, body limit, and deadline.
3. Route the original question.
4. Retrieve project-filtered chunks; errors or no match continue ungrounded.
5. Embed the question and search the scoped response cache; errors become misses.
6. A hit returns/replays stored content without chat generation or usage increments.
7. A miss builds trusted system policy, encoded reference data, and a separate question.
8. Call the primary model; try one distinct configured fallback on eligible pre-output failures.
9. Meter, log, cache complete success, and return JSON or SSE.

RAG and cache make separate embedding calls, including on cache hits. Usage totals meter
chat completions, not embedding spend.

## Ownership

| Module | Responsibility |
|---|---|
| `application` | Validate environment, create indexes, own Redis, compose server |
| `auth` | HMAC key lookup and project principal |
| `middleware` | Admission gates and request bounds |
| `handler` | HTTP decoding, response metadata, outbound SSE |
| `completion` | Route/retrieve/cache/provider policy, message builder, usage settlement |
| `router` | Model catalogue, prices, deterministic primary/fallback decision |
| `rag` | Validate/chunk/embed reviewed documents; project-filtered retrieval |
| `cache` | Prompt embedding, scoped vector lookup, TTL storage |
| `ratelimit` | Atomic Redis Lua decision; in-memory adapter for tests |
| `provider` | OpenAI HTTP; decode wire SSE into neutral events |
| `usage` | Redis project aggregates |
| `observability` | Parse tokens and emit structured logs |
| `evaluation` / `evidence` | Opt-in measurements, provenance, reports |

`/chat` and `/chat/stream` share one private lifecycle. Normal exhaustion settles streams;
`Close` is the idempotent abandonment fallback. HTTP owns SSE encoding; provider owns wire decoding.

## Identity and cache scope

Authentication returns `ProjectID` and HMAC `KeyID`. Raw caller keys stop at authentication.
Key quotas use `KeyID`. RAG, cache, usage, and project concurrency use `ProjectID`.
Concurrency caps are per process; Redis quotas are shared across instances.
Quota approval precedes concurrency/body validation; later failures can still consume a slot.

Cache queries filter before ranking:

| Field | Value |
|---|---|
| `tenant` | SHA-256 of project ID |
| `model` | Primary deterministic route's model ID |
| `version` | Cache schema `v2`, optional corpus revision, `prompt-policy-v1` |

Document replacement advances corpus revision. Policy/framing changes require a new
`promptPolicyVersion` in `internal/completion/input.go`. Old entries expire normally.
The attempt retains one embedding for lookup and store. Fallback responses use the
original routed namespace; billed usage uses the model that produced the response.

## Prompt trust boundary

`ProviderInput.Messages()` supplies the only system message. Reference identifiers and
content occupy JSON-encoded user data; the original question occupies the final user message.
Forged delimiters cannot create API message roles. JSON, SSE, and fallback calls share this builder.

The model has no tools or gateway credentials. General security discussion remains allowed.
No phrase blocklist, classifier, or output screening exists. Role separation is an application
property; model resistance still needs adversarial evaluation.

## Failures and settlement

| Condition | Behavior |
|---|---|
| Missing/invalid key | 401 |
| Authentication/quota store error | 500; reject |
| Key quota or project concurrency full | 429 |
| Global concurrency full | 503 |
| Invalid body/prompt | 400 or 413 |
| Retrieval failure | Log; continue ungrounded |
| Cache failure | Log; continue as miss |
| Transport error, HTTP 408/429/5xx | One distinct configured fallback before output |
| Cancellation/deadline error | No fallback |
| Provider error after fallback | Preserve status; transport errors become 502 |
| Stream read failure/abandonment | Meter received work; never cache partial content |

Fallback defaults equal the primary, so default routes do not retry. Streams never replay after emitting content.

Without provider usage, input estimates include policy, references, and question. Output estimates use received content.
Estimates use roughly four bytes per token, omit exact message-framing overhead, and carry an estimated flag.
Cancellation-independent settlement has a 5s budget. Usage writes are best effort and use floating-point USD.

## Startup and deployment

`main.go` gives startup 10s and shutdown 30s. `application.New` checks Redis and creates
cache/RAG indexes before serving on `:8080`. `/health` reports liveness only.
Compose exposes Redis/RedisInsight without ACL/TLS and has no declared persistent volume.
No durable production deployment is claimed.

## Diagram evidence

The viewer and SVG reflect the applied working-tree code. Archify validates citations
against a frozen local source snapshot, kept as a Git bundle beside `candidate.json`.
The snapshot is separate from the project's branch. Source links remain local because
that snapshot revision is not a published GitHub commit.

The SVG is the viewer's canonical export. README embeds it; the HTML retains exploration,
source details, themes, and export controls. Regenerate both from the same candidate after code changes.

## Related docs

- [RAG](RAG.md)
- [Threat model](THREAT-MODEL.md)
- [Roadmap](ROADMAP.md)
- [Decisions](adr/README.md)
- [Evaluation](EVALUATION.md)
