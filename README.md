# LLM Gateway

[![CI](https://github.com/ashrafrah96/llm-gateway/actions/workflows/ci.yml/badge.svg)](https://github.com/ashrafrah96/llm-gateway/actions/workflows/ci.yml)

Go gateway for OpenAI: project authentication, Redis quotas, project RAG, semantic reuse,
JSON/SSE delivery, and project usage totals.

Reference implementation and portfolio project. See [implemented controls](docs/THREAT-MODEL.md)
and [remaining work](docs/ROADMAP.md).

## Architecture

[![LLM Gateway architecture: admission, completion, retrieval, prompt boundaries, provider, and Redis state](docs/diagrams/architecture.svg)](docs/diagrams/architecture.svg)

[Interactive Archify diagram](docs/diagrams/architecture.html)
· [Architecture details](docs/ARCHITECTURE.md)
· [Diagram source](docs/diagrams/architecture.json)

The diagram includes the applied prompt-injection defenses. Its source evidence uses a
frozen local snapshot of the current code. Download the HTML and open it in a browser;
GitHub displays the SVG but does not run the interactive viewer.

## Request path

1. Authenticate `X-API-Key`; derive project and key identity.
2. Apply key quota, global/project concurrency caps, body limit, and deadline.
3. Route the original question by length and keywords.
4. Retrieve project-filtered reference material.
5. Search cache by project, primary routed model, corpus revision, schema, and prompt policy.
6. On a miss, send fixed system policy, encoded references, and separate user question to OpenAI.
7. Settle usage and logs. Cache complete successful answers. Return JSON or SSE.

Cache hits skip chat generation and usage increments. They still make embedding calls.
Retrieval/cache errors allow an ungrounded completion or cache miss. Authentication/quota errors reject the request.

## Behavior

| Capability | Rule | Evidence |
|---|---|---|
| Quotas | Default 10 quota approvals per key per sliding minute; Redis Lua makes one atomic decision | [ADR-0003](docs/adr/0003-the-store-decides-the-rate-limit.md) |
| Routing | Over 500 **bytes**, or a complexity keyword, selects the powerful tier | [ADR-0001](docs/adr/0001-keyword-routing-instead-of-a-classifier.md) |
| RAG | Reviewed text only; authenticated project filter; up to four chunks at similarity ≥0.80 | [RAG](docs/RAG.md) |
| Semantic cache | Similarity ≥0.95; namespace filters precede ranking; default TTL 24h | [ADR-0007](docs/adr/0007-isolate-semantic-cache-by-tenant-and-model.md) |
| Prompt boundaries | One fixed system policy; references and question remain user data; shared JSON/SSE/fallback builder | [ADR-0009](docs/adr/0009-separate-gateway-policy-from-untrusted-input.md) |
| Failover | One distinct configured fallback on transport errors, HTTP 408/429/5xx, before output | [Architecture](docs/ARCHITECTURE.md) |
| Usage | Project totals for completion requests, tokens, USD cost, and estimated requests | [ADR-0006](docs/adr/0006-estimate-and-bill-abandoned-streams.md) |

Default routes: `gpt-3.5-turbo` and `gpt-4`. Fallback defaults equal the primary model,
so retry occurs only when a distinct fallback is configured. Routing makes no classifier call.

JSON and SSE share `internal/completion`. OpenAI wire framing stays in `internal/provider`;
HTTP decoding and outbound SSE framing stay in `internal/handler`.

## Run

Docker Compose starts the gateway and Redis Stack. Go 1.25+ is needed for operator commands.

```bash
export OPENAI_API_KEY=sk-your-key
export API_KEY_PEPPER='a-long-random-secret'
export REDIS_ADDR=localhost:6379
docker compose up -d --build
export PROJECT_API_KEY="$(go run ./cmd/project-key -project demo)"
```

The key command prints the raw key once. Redis stores its HMAC fingerprint and project binding.
Keep the pepper stable and store the key securely.

Gateway: port 8080. Redis: 6379. RedisInsight: 8001.
Compose exposes Redis without ACL/TLS and declares no persistent volume. Use it for local development.

Direct Go startup, after starting Redis Stack and setting the same environment:

```bash
go run .
```

Plain Redis cannot serve the cache/RAG vector indexes. Redis connectivity and index creation
share a 10s startup budget. Shutdown has a 30s budget.

Import reviewed UTF-8 text or Markdown:

```bash
go run ./cmd/rag-import -project demo -file docs/ARCHITECTURE.md
```

Standalone Docker image:

```bash
docker build -t llm-gateway .
docker run -p 8080:8080 \
  -e OPENAI_API_KEY \
  -e API_KEY_PEPPER \
  -e REDIS_ADDR=host.docker.internal:6379 \
  llm-gateway
```

`host.docker.internal` assumes Docker Desktop. Use the Redis host reachable from your container.

## Endpoints

| Method | Path | Key required | Result |
|---|---|---|---|
| POST | `/chat` | yes | JSON completion with additive grounding metadata on success |
| POST | `/chat/stream` | yes | Content-delta SSE; initial `sources` event when grounded |
| GET | `/usage` | yes | Project completion usage totals |
| GET | `/limits` | yes | Current key's quota status |
| GET | `/models` | no | Compiled model catalogue and prices |
| GET | `/health` | no | Liveness response; not dependency readiness |

All protected endpoints consume quota and pass request limits, including `/usage` and `/limits`.
Quota approval happens before handler validation and concurrency checks; later rejection can still consume a slot.
Requests contain only `{"prompt":"..."}`. Empty prompts, unknown fields, and oversized prompts are rejected.

```bash
curl -i http://localhost:8080/chat \
  -H 'Content-Type: application/json' \
  -H "X-API-Key: $PROJECT_API_KEY" \
  -d '{"prompt":"What is the capital of France?"}'

curl -N http://localhost:8080/chat/stream \
  -H 'Content-Type: application/json' \
  -H "X-API-Key: $PROJECT_API_KEY" \
  -d '{"prompt":"Explain quantum computing"}'

curl http://localhost:8080/usage -H "X-API-Key: $PROJECT_API_KEY"
```

Repeating the first prompt can hit the cache. Reworded prompts hit only when similarity
and namespace checks pass.

Response headers:

- `X-Cache`: `HIT` or `MISS`.
- `X-Model`: model used on a miss; absent on hits.
- `X-RAG-Grounded`: `true` or `false` when retrieval is enabled.
- `X-Model-Fallback`: fallback marker. Grounded SSE may flush headers before this marker is set.
- `Retry-After`: integer seconds on gateway quota rejection.

Provider error statuses propagate after any configured fallback. Transport errors become 502.

## Streaming settlement

Provider exhaustion settles usage, logs, and cache. Deferred `Close` uses the same
idempotent path when the caller leaves early.

Cancellation closes upstream work. Without final provider usage, estimate input from
system policy, encoded references, and question; estimate output from received content.
The estimate uses roughly four bytes per token and excludes exact chat-framing overhead.
It is unreconciled and can undercount non-English text.

Settlement outlives request cancellation but stops after 5s. Partial streams are never cached.
Measured provider usage takes precedence. Estimates appear as `"estimated": true` in logs
and increment `estimated_requests` in `/usage`:

```json
{
  "requests": 1000,
  "tokens_in": 45000,
  "tokens_out": 90000,
  "cost_usd": 12.34,
  "estimated_requests": 3
}
```

Usage is best effort, uses floating-point USD, and excludes embedding spend.

## Settings

| Variable | Default | Meaning |
|---|---|---|
| `OPENAI_API_KEY` | required | Provider key |
| `API_KEY_PEPPER` | required | Project-key HMAC secret |
| `REDIS_ADDR` | `localhost:6379` | Redis Stack address |
| `CACHE_TTL` | `24h` | Response-cache lifetime |
| `REQUEST_TIMEOUT` | `60s` | Request context deadline |
| `MAX_REQUEST_BODY_BYTES` | `65536` | JSON body limit |
| `MAX_CONCURRENT_REQUESTS` | `16` | In-flight cap per gateway process |
| `MAX_CONCURRENT_PER_PROJECT` | `4` | Project in-flight cap per gateway process |
| `CHEAP_MODEL` | `gpt-3.5-turbo` | Simple-question model ID |
| `POWERFUL_MODEL` | `gpt-4` | Complex-question model ID |
| `CHEAP_FALLBACK_MODEL` | cheap primary ID | Simple-tier fallback ID |
| `POWERFUL_FALLBACK_MODEL` | powerful primary ID | Complex-tier fallback ID |

Prompt limit: 32768 bytes. Model prices, output caps, quota defaults, and similarity thresholds
remain compiled constants. Model-ID overrides inherit the tier's compiled prices/caps;
`/models` still shows the compiled catalogue. Validate overrides before real billing.

Compose passes the provider key, pepper, Redis address, and cache TTL. Add other settings
to Compose or run directly when overriding them. `internal/application` validates configuration
and owns Redis resources.

## Verify

```bash
go test ./...
```

Redis-backed tests skip when Redis/Search is unavailable. Run all checks with Redis Stack:

```bash
docker run --rm -d --name llm-gateway-test-redis \
  -p 6379:6379 redis/redis-stack-server:latest
go build ./...
go vet ./...
REDIS_ADDR=localhost:6379 go test -race -count=1 ./...
docker stop llm-gateway-test-redis
```

Use a free port or an existing Redis Stack instance if Compose already owns 6379.
CI builds, vets, checks formatting, runs race tests, and rejects skipped Redis-backed tests.

[The evaluator](docs/EVALUATION.md) measures a committed 40-case English corpus with paid embeddings.
Targets: precision ≥95%, recall ≥80%. The opt-in smoke command checks real JSON, cache,
SSE, cancellation, and usage paths. No measured evaluation or smoke report is committed yet.

## Code map

| Package | Responsibility |
|---|---|
| `internal/application` | Configuration, composition, owned resources |
| `internal/auth` | Project-key fingerprints and lookup |
| `internal/middleware` | Authentication, quotas, body/deadline/concurrency limits |
| `internal/handler` | HTTP decoding, JSON, outbound SSE, read endpoints |
| `internal/completion` | Shared lifecycle, trusted prompt builder, settlement |
| `internal/provider` | OpenAI HTTP and provider-neutral SSE events |
| `internal/router` | Model catalogue, pricing, deterministic route |
| `internal/rag` | Reviewed document ingestion and project retrieval |
| `internal/cache` | Embeddings, semantic lookup, response storage |
| `internal/ratelimit` | Redis/in-memory sliding-window stores |
| `internal/usage` | Project token/cost totals |
| `internal/observability` | Token parsing and structured request logs |
| `internal/evaluation` | Corpus validation and classification metrics |
| `internal/evidence` | Evaluation provenance and report writes |

Operator commands: `project-key`, `rag-import`, `cache-eval`, `provider-smoke` under `cmd/`.
Review [provider events](internal/provider/openai.go), [settlement](internal/completion/stream.go),
[prompt boundaries](internal/completion/input.go), and [atomic quota decisions](internal/ratelimit/redis.go).

## Limits and decisions

- Structural prompt defenses reduce risk; no classifier, phrase blocklist, or output screening exists.
- Payload tests prove message boundaries, not real-model attack resistance.
- Semantic quality and keyword routing remain corpus/traffic dependent.
- Spend budgets, durable accounting, key rotation, and production Redis hardening remain open.

[Decision records](docs/adr/README.md) preserve tradeoffs.
[Engineering notes](docs/ENGINEERING-NOTES.md) explain the quota and streaming billing regressions.
