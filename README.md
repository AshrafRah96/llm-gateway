# LLM Gateway

[![CI](https://github.com/ashrafrah96/llm-gateway/actions/workflows/ci.yml/badge.svg)](https://github.com/ashrafrah96/llm-gateway/actions/workflows/ci.yml)

Go gateway for OpenAI: project keys, quotas, daily spend budgets, project RAG,
semantic cache, JSON/SSE, and a separate persistent usage ledger.

Reference implementation. [Controls and limits](docs/THREAT-MODEL.md)
· [Remaining work](docs/ROADMAP.md).

## Architecture

[![Gateway: admission, completion, untrusted references, provider, vector cache and persistent ledger](docs/diagrams/architecture.svg)](docs/diagrams/architecture.svg)

[Interactive Archify viewer](docs/diagrams/architecture.html)
· [Architecture](docs/ARCHITECTURE.md) · [Diagram source](docs/diagrams/architecture.json).

GitHub renders the SVG. Download the HTML to explore sources, themes, and exports.
Diagram evidence pins a frozen local source snapshot; it does not claim a published commit.

## Request path

1. Authenticate `X-API-Key`; derive project and fingerprint identity.
2. Apply atomic key quota, process/project concurrency caps, body limit, and deadline.
3. Route the original question. Retrieve project-filtered references and search the scoped cache.
4. Reserve project budget before each embedding or chat attempt.
5. On a cache miss, send fixed system policy, JSON-encoded references, and a separate question.
6. Settle identified usage; cache complete success; return JSON or live SSE.

Cache hits avoid chat charges. Retrieval/cache embeddings still consume budget and appear in `/usage`.
Ordinary retrieval/cache errors permit ungrounded generation or a miss. Budget and ledger failures reject paid work.

| Capability | Rule |
|---|---|
| Key quota | Default 10 approvals per key per sliding minute; shared Redis Lua decision |
| Project budget | Default 100,000 tokens and US$10 per UTC day; conservative per-operation reservation |
| Routing | More than 500 bytes, or a complexity keyword, selects the powerful tier |
| RAG | Reviewed text; authenticated project filter; at most four chunks at similarity ≥0.80 |
| Cache | Similarity ≥0.95; project/model/schema/corpus/policy filter; default TTL 24h |
| Prompt boundaries | Fixed system policy; references and question remain user data; shared JSON/SSE/fallback builder |
| Failover | One distinct fallback on transport errors or HTTP 408/429/5xx before output; each attempt is charged |
| Keys | Existing expiry-free keys preserved; new expiry 90 days; rotation overlap 24h by default |
| Reads | Chat JSON/error 1 MiB; embedding JSON 128 KiB; cumulative SSE 4 MiB; SSE line 1 MiB |

Default model IDs remain `gpt-3.5-turbo` and `gpt-4`. Their aliases are scheduled to shut down
on 23 October 2026. Configure supported IDs with verified prices/output caps before deployment.
Account availability remains untested. [OpenAI deprecations](https://developers.openai.com/api/docs/deprecations).

Fallback defaults equal the primary model; retries require a distinct configured fallback.
No routing classifier call occurs. No injection classifier, phrase blocklist, or output screening exists.

## Run

Go 1.25+, Docker Compose, and Redis Stack are required. Default Compose has private TLS Redis
services, ACL credentials, separate persistent volumes, pinned images, and resource caps.
Only gateway port `127.0.0.1:8080` is published. Operator ports need the development override.

```bash
go run ./cmd/deployment-init
# Supply the provider key securely into .cache/deploy/openai_api_key.
# The generated file is empty; no provider call occurs during initialization.
docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d --build
source .cache/deploy/operator.env
go run ./cmd/project-key -project demo
```

The key command prints the raw key once. Save it securely and set `PROJECT_API_KEY` for requests.
Keep the pepper stable. Changing it invalidates every fingerprint.
Generated certificates are local development material. Production certificates, off-host backups,
secret storage, and HTTPS ingress remain operator responsibilities. [Operations](docs/OPERATIONS.md).

Existing installations must stop old writers, migrate usage and key fingerprints, and verify
balances before cutover. [Ledger and migration](docs/LEDGER.md). Startup rejects unmigrated legacy state.

Direct Go startup uses the same two stores:

```bash
source .cache/deploy/gateway.env
go run .
```

Startup has a 10s budget; shutdown 30s. Cache/RAG index creation is required.
`/ready` checks ledger connectivity and both Search indexes. It does not call OpenAI.

## Endpoints

| Method | Path | Key | Result |
|---|---|---|---|
| POST | `/chat` | yes | JSON completion with grounding metadata |
| POST | `/chat/stream` | yes | Content-delta SSE; initial sources event when grounded |
| GET | `/usage` | yes | Lifetime chat, embedding, and combined project totals |
| GET | `/limits` | yes | Current key's sliding-window quota status |
| GET | `/models` | no | Configured model IDs, USD/1k-token prices, output caps |
| GET | `/health` | no | Process liveness |
| GET | `/ready` | no | Redis/Search readiness |

Protected status endpoints consume key quota. Quota approval precedes handler validation and concurrency checks.
Requests accept only `{"prompt":"..."}`. Empty prompts, unknown fields, and excessive sizes fail.

```bash
curl -i http://localhost:8080/chat \
  -H 'Content-Type: application/json' -H "X-API-Key: $PROJECT_API_KEY" \
  -d '{"prompt":"What is the capital of France?"}'
curl -N http://localhost:8080/chat/stream \
  -H 'Content-Type: application/json' -H "X-API-Key: $PROJECT_API_KEY" \
  -d '{"prompt":"Explain quantum computing"}'
curl http://localhost:8080/usage -H "X-API-Key: $PROJECT_API_KEY"
```

Headers: `X-Cache`, `X-Model` on misses, `X-RAG-Grounded`, `X-Model-Fallback`.
Fallback metadata is set before grounded source events flush. `Retry-After` covers quota rejection
and exhausted daily budget. Budget exhaustion returns 429; ledger failure returns 503.
Other provider errors retain their status after fallback; transport errors become 502.
SSE failures after headers use an error event and omit successful `[DONE]`.

## Accounting

Admission reserves UTF-8 message bytes plus framing allowance and the configured output cap.
Integer nanodollars avoid floating-point ledger arithmetic. Lua updates events, totals, daily
spend, and reservations atomically; duplicate event IDs cannot double-charge.

Provider counts settle known work. Missing usage, cancellation, timeout, or crashes retain the
full reservation until audited reconciliation. Labelled estimates appear in lifetime totals;
estimates can undercount actual tokens. A conservative reservation is separate from that estimate.
Settlement outlives cancellation for at most 5s. Failure remains visible; partial answers never enter cache.

`/usage` preserves `requests`, `tokens_in`, `tokens_out`, `cost_usd`, `estimated_requests`.
It adds `embedding_requests`, `embedding_tokens`, `embedding_cost_usd`, `total_tokens`, `total_cost_usd`.
Chat totals count attempts, including failed attempts with uncertain cost. Display USD fields are derived floats;
ledger money uses integers. Totals describe configured-price accounting, not verified provider invoices.

## Settings

| Variable | Default | Meaning |
|---|---|---|
| `OPENAI_API_KEY` / `_FILE` | required | Provider credential; set one source |
| `API_KEY_PEPPER` / `_FILE` | required | Stable project-key HMAC secret |
| `REDIS_ADDR` | `localhost:6379` | Vector/cache/quota Redis Stack |
| `LEDGER_REDIS_ADDR` | `localhost:6380` | Separate persistent ledger Redis |
| `CACHE_TTL` | `24h` | Cache lifetime |
| `REQUEST_TIMEOUT` | `60s` | Request deadline |
| `MAX_REQUEST_BODY_BYTES` | `65536` | Body bound; prompt bound remains 32768 bytes |
| `MAX_CONCURRENT_REQUESTS` | `16` | Per-process in-flight cap |
| `MAX_CONCURRENT_PER_PROJECT` | `4` | Per-project cap per process |
| `CHEAP_MODEL` / `POWERFUL_MODEL` | `gpt-3.5-turbo` / `gpt-4` | Primary IDs |
| `CHEAP_FALLBACK_MODEL` / `POWERFUL_FALLBACK_MODEL` | primary ID | Fallback IDs |

Both Redis prefixes support `USERNAME`, `PASSWORD` or `PASSWORD_FILE`, `TLS`, `TLS_CA`,
`TLS_SERVER_NAME`, and paired `TLS_CERT`/`TLS_KEY`. Hostname and CA verification stay enabled.

Each route prefix (`CHEAP_`, `POWERFUL_`, `CHEAP_FALLBACK_`, `POWERFUL_FALLBACK_`) supports
`PRICE_IN`, `PRICE_OUT` in USD/1k tokens and `MAX_OUTPUT_TOKENS`. Unknown IDs require all three.
Known IDs use their matching catalogue prices. Contradictory pricing/caps for one ID fail startup.
Operator `usage-admin -action limits` sets daily project limits. [Operations](docs/OPERATIONS.md).

## Verify

CI builds, vets, checks formatting, and runs race tests against digest-pinned Redis Stack.
It rejects integration skips and runs unpaid TLS/ACL/readiness/backup/isolated-restore checks.
Local evidence: [verification record](docs/verification/local-checks.json).

```bash
go build ./...
go vet ./...
REDIS_ADDR=localhost:6379 go test -race -count=1 ./...
```

Redis-backed tests skip without Redis/Search. A skipped test provides no evidence.
[Evaluation](docs/EVALUATION.md) describes paid smoke/cache/adversarial commands.
[Prepared adversarial plan](docs/evaluation/adversarial-prepared-v1.md) is explicitly unmeasured.
No paid calls were authorized for this change; live-model resistance and invoice agreement remain unknown.

## Code map

| Package | Owns |
|---|---|
| `application` | Configuration, composition, two owned Redis clients, readiness |
| `auth` / `middleware` | Fingerprints, key lifecycle, admission and request bounds |
| `handler` | HTTP decoding, JSON, outbound SSE, status endpoints |
| `completion` | Shared lifecycle, trusted messages, per-attempt chat metering |
| `provider` / `router` | OpenAI framing, response bounds, configured catalogue |
| `rag` / `cache` | Reviewed documents, scoped vectors, metered embeddings |
| `usage` / `money` | Reservations, events, reconciliation, exact money |
| `redisconfig` | Scoped credentials and verified TLS options |
| `ratelimit` / `observability` | Sliding quotas and structured request logs |
| `evaluation` / `adversarial` / `evidence` | Opt-in gates, provenance, reports |

Commands: `deployment-init`, `project-key`, `usage-admin`, `rag-import`, `cache-eval`,
`provider-smoke`, `adversarial-eval`. [Engineering notes](docs/ENGINEERING-NOTES.md).
