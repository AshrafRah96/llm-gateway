# Architecture

Go HTTP gateway. OpenAI processes chat and embeddings. Redis Stack owns disposable vectors,
RAG content, and quotas. A separate persistent Redis process owns keys, money, and budgets.

[![Current gateway architecture](diagrams/architecture.svg)](diagrams/architecture.svg)

[Interactive Archify viewer](diagrams/architecture.html) · [Diagram source](diagrams/architecture.json).

## Request lifecycle

1. Authenticate project key from the ledger; apply quota and bounded request admission.
2. Route the original question. Retrieve project-filtered references.
3. Reserve budget before retrieval/cache embeddings; search the scoped response cache.
4. A hit replays complete content without chat generation; embedding spend still counts.
5. A miss builds fixed system policy, encoded reference data, and a separate question.
6. Reserve before each chat attempt; try one distinct configured fallback before output when eligible.
7. Record usage, log, cache complete success, and return JSON or live SSE.

Ordinary retrieval failures continue ungrounded; cache errors become misses. Budget errors propagate
from both paths. No fallback starts after budget failure, cancellation, or emitted content.

## Ownership

| Module | Responsibility |
|---|---|
| `application` | Validate catalogue/TLS/configuration; own two Redis clients and indexes; readiness |
| `auth` | HMAC lookup, expiry/revocation/rotation, legacy fingerprint migration |
| `middleware` / `ratelimit` | Admission bounds and atomic shared key quotas |
| `handler` | Decode HTTP, emit JSON/SSE and metadata, status endpoints |
| `completion` | Shared lifecycle, trusted messages, per-attempt chat metering |
| `router` / `provider` | Configured prices/caps, route/fallback, bounded OpenAI wire reads |
| `rag` / `cache` | Reviewed ingestion, project vectors, response TTLs, metered embeddings |
| `usage` / `money` | Atomic reservations/events/aggregates, integer charges, migration/reconciliation |
| `redisconfig` | Scoped credentials and verified TLS configuration |
| `observability` | Structured request logs and labelled estimates |
| `evaluation` / `adversarial` / `evidence` | Opt-in gates, provenance and reports |

JSON and SSE share one lifecycle. Provider decoding remains separate from outbound SSE encoding.
Normal exhaustion and idempotent `Close` share settlement; close failures suppress caching and successful terminal output.

## Identity and cache scope

Raw caller keys stop at authentication. HMAC `KeyID` scopes quotas; `ProjectID` scopes RAG,
cache, budgets, usage and project concurrency. Concurrency caps remain per process.
Quota approval precedes body/concurrency validation; later failures may consume a slot.

Cache filters SHA-256 tenant, primary routed model, schema `v2`, corpus revision, and `prompt-policy-v1`
before vector ranking. Document replacement advances corpus revision. Policy/framing changes require
bumping `promptPolicyVersion`. Old entries expire normally. One attempt retains its lookup embedding for storage.
Fallback output uses the original routed namespace; ledger charges each model attempt separately.

## Trust boundaries

`ProviderInput.Messages()` creates the only system message. JSON-encoded reference identifiers and
content occupy user data; the original question occupies a separate final user message.
Forged delimiters cannot create API roles. JSON, SSE and fallback share the builder.

The model has no tools, credentials, or state access. General security discussion remains allowed.
No phrase blocklist, classifier, or output screening exists. Streaming releases content immediately.
Message separation is verified locally; model resistance and answer/cache poisoning remain unmeasured.

## Failures and settlement

| Condition | Behavior |
|---|---|
| Missing, expired or revoked key | 401 |
| Authentication/quota store error | 500; reject |
| Key quota/project concurrency/daily budget full | 429 |
| Global concurrency full or ledger unavailable for paid work | 503 |
| Invalid body/prompt | 400 or 413 |
| Ordinary retrieval/cache error | Ungrounded completion/cache miss |
| Transport error or HTTP 408/429/5xx | One distinct fallback before output; separate reservation |
| Cancellation/deadline/budget error | No fallback |
| Response overflow or malformed usage | Error; retain uncertain hold; no cache |
| Provider error after fallback | Preserve status; transport error becomes 502 |
| Stream failure after headers | SSE error; no successful terminal event or partial cache entry |

Chat JSON/errors are capped at 1 MiB, embedding JSON at 128 KiB, cumulative SSE at 4 MiB,
and each SSE line at 1 MiB. Oversized reads close upstream work.

Admission reserves serialized UTF-8 message bytes plus framing allowance and capped output.
Provider usage releases known reservations. Missing counts produce labelled estimates while full
holds remain pending. Detached settlement has a 5s limit; ledger failure stays visible.
Unique operation IDs make retries idempotent; provider request IDs are retained when available.
See [ledger rules](LEDGER.md) for daily windows, migration, and reconciliation.

## Startup and deployment

Startup has 10s; shutdown 30s. Both Redis processes must connect and have different server identities.
Unmigrated legacy usage or key fingerprints stop startup. Cache/RAG indexes must initialize.
`/health` reports liveness; `/ready` checks ledger connectivity and both Search indexes within 3s.
Neither checks OpenAI credentials or model availability.

Default Compose uses TLS, ACL users, private state networking, pinned images, separate persistent
volumes, no eviction, and resource caps. A development override exposes loopback operator ports.
Runtime users cannot administer keys or budget policy. [Operations](OPERATIONS.md)
· [Backup and restore](LEDGER-RECOVERY.md).

## Diagram evidence

Archify validates citations against a frozen local commit, stored in the source bundle beside the diagram.
That snapshot is separate from the project branch; source links stay local-only.
README embeds the static SVG. HTML retains source details, exploration, themes, and exports.
[Diagram receipts](diagrams/README.md) describe artifact hashes and checks separately.

[Threat model](THREAT-MODEL.md) · [Roadmap](ROADMAP.md)
· [RAG](RAG.md) · [Evaluation](EVALUATION.md).
