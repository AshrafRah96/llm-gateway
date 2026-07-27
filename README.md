# LLM Gateway

[![CI](https://github.com/ashrafrah96/llm-gateway/actions/workflows/ci.yml/badge.svg)](https://github.com/ashrafrah96/llm-gateway/actions/workflows/ci.yml)

An OpenAI gateway focused on the engineering around an LLM call: distributed quotas,
semantic reuse, streaming cancellation and cost tracking.

Your app sends a prompt here instead of to OpenAI. The gateway checks the caller's
API key, makes sure they aren't sending too many requests, looks for a cached answer,
picks a model, calls OpenAI, and records what it cost.

> This is a reference implementation and CV project, not a production-ready commercial
> gateway. The [threat model](docs/THREAT-MODEL.md) lists the controls that exist today,
> and the [roadmap](docs/ROADMAP.md) lists the work still missing.

### What is implemented

| Engineering problem | What this project demonstrates | Evidence |
|---|---|---|
| Distributed rate limits | One atomic Redis Lua decision; rejected requests do not consume quota | [ADR-0003](docs/adr/0003-the-store-decides-the-rate-limit.md), [bug write-up](docs/ENGINEERING-NOTES.md#1-the-rate-limiter-that-allowed-twelve-times-its-limit) |
| Interrupted SSE streams | Cancellation stops upstream work while partial usage is estimated and labelled | [ADR-0006](docs/adr/0006-estimate-and-bill-abandoned-streams.md), [regression test](internal/handler/abandoned_test.go) |
| Safe semantic reuse | Answers are reused only inside the same tenant, routed model and cache schema | [architecture](docs/ARCHITECTURE.md), [integration tests](internal/cache/semantic_integration_test.go) |

The repository includes a [versioned 40-case corpus](docs/evaluation/cases-v1.json) and
an opt-in evaluator for cache precision, recall, hit rate and lookup latency. There is
no published score yet because the evaluator has not been run in a controlled
environment. The [evaluation guide](docs/EVALUATION.md) explains how to run it.

```text
client → auth → sliding-window limit → route model
                                      ↓
                            tenant-safe semantic cache
                                      │ miss
                                      ▼
                                    OpenAI
                                      ↓
                           meter + cache + respond
```

Stack: Go, Redis/Redis Search, Lua, Server-Sent Events, Docker and GitHub Actions.

## What it does

The semantic cache treats reworded questions as potential matches. "What is the capital
of France?" and "France's capital city?" are different strings, but should produce the
same answer. The gateway embeds each prompt and accepts a stored answer at 95%
similarity or above. Searches stay within the caller, routed model and cache schema;
entries expire after 24 hours by default.

Short prompts use `gpt-3.5-turbo`. Prompts over 500 characters, or prompts containing
words such as "analyze", "compare" or "debug", use `gpt-4`. This is a local keyword
check, so routing does not add another model call.

Each API key gets 10 requests per minute by default, counted over a sliding window.
Rejected attempts do not consume quota. A client that retries too soon will recover
when its earlier accepted requests age out.

The gateway records tokens and USD cost per API key. Streaming requests count too,
including requests abandoned before the final usage chunk arrives.

`/chat/stream` emits content-delta server-sent events. The OpenAI adapter decodes the
provider's wire format before completion policy sees it. Complete streamed answers use
the same cache and billing path as non-streamed answers.

## How a request flows

```text
your app
   │  X-API-Key
   ▼
┌──────────────┐
│ is the key   │──── no ──▶ 401
│ valid?       │
└──────┬───────┘
       ▼
┌──────────────┐
│ under the    │──── no ──▶ 429 + Retry-After
│ rate limit?  │
└──────┬───────┘
       ▼
┌──────────────┐
│ pick a model │
└──────┬───────┘
       ▼
┌──────────────┐
│ similar      │──── yes ─▶ return the cached answer (X-Cache: HIT)
│ prompt seen  │
│ for tenant + │
│ model?       │
└──────┬───────┘
       ▼
┌──────────────┐
│ call OpenAI  │
└──────┬───────┘
       ▼
  record cost, cache the answer, return it
```

## Endpoints

| Method | Path           | Needs a key | What it does                          |
|--------|----------------|-------------|---------------------------------------|
| POST   | `/chat`        | yes         | Send a prompt, get an answer          |
| POST   | `/chat/stream` | yes         | Same, streamed as it arrives          |
| GET    | `/usage`       | yes         | Tokens and cost so far for your key   |
| GET    | `/limits`      | yes         | How many requests you have left       |
| GET    | `/models`      | no          | Which models exist and what they cost |
| GET    | `/health`      | no          | Is the server up                      |

Send your key in the `X-API-Key` header.

### Response headers

- `X-Cache`: `HIT` if the answer came from the cache, `MISS` if it came from OpenAI
- `X-Model`: which model was called (not set on a cache hit because this request made
  no model call)
- `Retry-After`: on a 429, how many seconds to wait

If OpenAI returns an error, the gateway passes its status code through unchanged, so a
429 from OpenAI reaches you as a 429. Both `/chat` and `/chat/stream` behave the same way.

### Abandoned streams

If you stop reading a stream part way through, the gateway drops the connection to
OpenAI, which stops generating. That saves money, but it also means OpenAI never sends
its final token count. It arrives at the very end or not at all.

Those tokens were still used. Your prompt was charged in full, and so was whatever got
generated before the cut. Recording nothing would quietly write off a real cost, so the
gateway estimates instead, from your prompt and the part of the answer it received.

Estimates are approximate, and they are labelled. `/usage` reports how many of your
requests were estimated:

```json
{
  "requests": 1000,
  "tokens_in": 45000,
  "tokens_out": 90000,
  "cost_usd": 12.34,
  "estimated_requests": 3
}
```

The request log marks them too, with `"estimated": true`. If a charge looks wrong, that
tells you whether the number was measured or inferred.

Normal streams settle their cache, usage and log records as soon as the provider
finishes. The handler's deferred close uses the same idempotent settlement path only
when a caller disconnects before exhaustion.

## Running it

The quickest way is compose, which brings up redis-stack alongside the gateway:

```bash
export OPENAI_API_KEY=sk-your-key
docker compose up
```

The server listens on port 8080. RedisInsight is on 8001 if you want to look at what
got cached.

To run it directly you need Go 1.25+, an OpenAI key, and Redis:

```bash
git clone https://github.com/ashrafrah96/llm-gateway
cd llm-gateway
export OPENAI_API_KEY=sk-your-key
export REDIS_ADDR=localhost:6379
go run main.go
```

Use the redis-stack image rather than plain Redis. The cache needs vector search
commands that only the stack has, and the gateway will not start without them.
Everything else works on plain Redis.

### Adding an API key

Keys live in a Redis set called `api_keys`. There is no admin endpoint, so add them
directly:

```bash
redis-cli SADD api_keys some-secret-key
redis-cli SREM api_keys some-secret-key   # to revoke
```

### Docker

```bash
docker build -t llm-gateway .
docker run -p 8080:8080 \
  -e OPENAI_API_KEY=sk-your-key \
  -e REDIS_ADDR=host.docker.internal:6379 \
  llm-gateway
```

### Settings

| Variable         | What it is               | Default          |
|------------------|--------------------------|------------------|
| `OPENAI_API_KEY` | Your OpenAI key (needed) | required         |
| `REDIS_ADDR`     | Where Redis is           | `localhost:6379` |
| `CACHE_TTL`      | How long answers remain reusable | `24h` |

`internal/application` validates these values before constructing the adapter graph.
Redis connectivity and semantic-cache index creation share a bounded startup context,
so a broken dependency cannot leave startup waiting forever.

## Trying it out

```bash
# ask something
curl -X POST http://localhost:8080/chat \
  -H "Content-Type: application/json" \
  -H "X-API-Key: some-secret-key" \
  -d '{"prompt": "What is the capital of France?"}'

# ask it a different way; this one should come back as a cache hit
curl -i -X POST http://localhost:8080/chat \
  -H "Content-Type: application/json" \
  -H "X-API-Key: some-secret-key" \
  -d '{"prompt": "Which city is the capital of France?"}'

# stream an answer
curl -X POST http://localhost:8080/chat/stream \
  -H "Content-Type: application/json" \
  -H "X-API-Key: some-secret-key" \
  -d '{"prompt": "Explain quantum computing"}'

# see what you have spent
curl http://localhost:8080/usage -H "X-API-Key: some-secret-key"
```

## Running the tests

```bash
go test ./...
```

Most tests use fakes and need nothing running. The ones covering Redis skip themselves
when no Redis is reachable, so to run everything:

```bash
docker run -d -p 6379:6379 redis/redis-stack-server:latest
REDIS_ADDR=localhost:6379 go test -race ./...
```

CI runs Redis Stack and fails the build if Redis-backed tests skip. A skipped test is
how one of the bugs in
[docs/ENGINEERING-NOTES.md](docs/ENGINEERING-NOTES.md) survived as long as it did.

The real-embedding evaluation is separate from CI because it makes paid OpenAI calls.
Follow [docs/EVALUATION.md](docs/EVALUATION.md) to run it.

## Reviewing this

For a code review, start at the boundaries where provider data becomes gateway policy.

Start with [internal/provider/openai.go](internal/provider/openai.go), where OpenAI's
SSE wire format becomes provider-neutral content, usage and completion events. Then read
[internal/completion/stream.go](internal/completion/stream.go), which applies caching
and billing policy without knowing OpenAI's JSON framing.

Then [internal/ratelimit/redis.go](internal/ratelimit/redis.go), specifically the Lua
script and the comment explaining why the timestamps are milliseconds. That comment is
the fix for a bug that let roughly twelve times the configured rate through.

[docs/adr/](docs/adr/) records the decisions and rejected alternatives.
[docs/ENGINEERING-NOTES.md](docs/ENGINEERING-NOTES.md) covers two bugs, why the tests
missed them, and the regression tests that now catch them.

For the tests, [internal/handler/abandoned_test.go](internal/handler/abandoned_test.go)
is the one I would look at. It cancels a request mid stream against a real provider and
checks the caller still gets billed.

## Known limitations

These limitations are still open:

- Provider tests use local fakes. Their request and response shapes follow OpenAI's
  documentation, but the end-to-end smoke test has not run against a real account.
- Abandoned streams use a rough estimate of four bytes per token. It has not been
  reconciled against an OpenAI invoice and will undercount some non-English text.
- The 0.95 similarity threshold remains a hypothesis until the committed evaluator
  meets its precision target. Isolation tests prove the mechanics, not the quality of
  every possible match.
- The routing keyword list has not been checked against real traffic.
- Rate limits, similarity threshold and model prices are compile-time constants. Only
  cache lifetime is configurable.

## How the code is laid out

| Package                  | What lives there                                   |
|--------------------------|----------------------------------------------------|
| `internal/application`   | Configuration, concrete composition and owned resources |
| `internal/completion`    | What a chat request does, start to finish          |
| `internal/handler`       | HTTP: reads the request, writes the response       |
| `internal/middleware`    | Key checking and rate limiting                     |
| `internal/provider`      | Talking to OpenAI                                  |
| `internal/router`        | The model list, their prices, and which one to use |
| `internal/cache`         | Prompt embeddings and the vector lookup            |
| `internal/ratelimit`     | The sliding window, in Redis or in memory          |
| `internal/usage`         | Per-key token and cost totals                      |
| `internal/observability` | Request logging and token parsing                  |

Both `/chat` and `/chat/stream` go through `internal/completion`. That is deliberate:
when the two had their own copies of the logic, the streaming one quietly stopped
caching and billing, and nobody noticed.

## Why it works this way

The decisions with a real tradeoff behind them are written up in [docs/adr/](docs/adr/),
including what got rejected. The short version:

- The rate limiter uses a sliding window. A fixed window would allow a burst at the end
  of one window and another at the start of the next.
- The cache compares meaning instead of exact text, so reworded prompts can hit.
- Routing checks prompt length and keywords. Asking another model to choose would add a
  paid call before every completion.
- A model's ID, description and price live together in `internal/router`. Billing,
  routing and `/models` all read that catalogue.
