# LLM Gateway

[![CI](https://github.com/ashrafrah96/llm-gateway/actions/workflows/ci.yml/badge.svg)](https://github.com/ashrafrah96/llm-gateway/actions/workflows/ci.yml)

An OpenAI gateway project about the engineering around an LLM call:
distributed quotas, semantic reuse, streaming cancellation and auditable cost tracking.

Your app sends a prompt here instead of to OpenAI. The gateway checks the caller's
API key, makes sure they aren't sending too many requests, looks for a cached answer,
picks a model, calls OpenAI, and records what it cost.

> **Project status:** a production-minded reference implementation and CV project, not
> a production-ready commercial gateway. Implemented controls and remaining risks are
> separated in the [threat model](docs/THREAT-MODEL.md) and
> [roadmap](docs/ROADMAP.md).

### Why this is worth reviewing

| Engineering problem | What this project demonstrates | Evidence |
|---|---|---|
| Distributed rate limits | One atomic Redis Lua decision; rejected requests do not consume quota | [ADR-0003](docs/adr/0003-the-store-decides-the-rate-limit.md), [bug write-up](docs/ENGINEERING-NOTES.md#1-the-rate-limiter-that-allowed-twelve-times-its-limit) |
| Interrupted SSE streams | Cancellation stops upstream work while partial usage is estimated and labelled | [ADR-0006](docs/adr/0006-estimate-and-bill-abandoned-streams.md), [regression test](internal/handler/abandoned_test.go) |
| Safe semantic reuse | Answers are reused only inside the same tenant, routed model and cache schema | [architecture](docs/ARCHITECTURE.md), [integration tests](internal/cache/semantic_integration_test.go) |

The cache-quality claims are measurable rather than assumed. The repository includes a
[versioned 40-case corpus](docs/evaluation/cases-v1.json) and an opt-in evaluator for
precision, recall, hit rate and lookup latency. No result is published until that
command has run; see [evaluation](docs/EVALUATION.md).

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

**Stack:** Go, Redis/Redis Search, Lua, Server-Sent Events, Docker and GitHub Actions.

## What it does

**Caches answers to similar questions.** "What is the capital of France?" and
"France's capital city?" are different strings but mean the same thing. The gateway
turns each prompt into a vector and looks for a stored answer that is close enough
(95% similar). A hit costs nothing and returns immediately. Searches are filtered by
a fingerprint of the caller's key, routed model and schema version; entries expire
after 24 hours by default.

**Picks a cheaper model when it can.** Short, simple prompts go to `gpt-3.5-turbo`.
Prompts over 500 characters, or ones containing words like "analyze", "compare" or
"debug", go to `gpt-4`. This is a keyword check, not another model call, so it adds
no delay and no cost.

**Limits how often each key can call.** 10 requests per minute by default, counted
over a sliding window. Requests that get refused do not count against the limit, so
a caller that keeps retrying still recovers as soon as its earlier requests age out.

**Tracks spend per API key.** Every request records its tokens and its cost in USD.
Streamed requests are counted too, including ones the caller abandons — see below.

**Streams when you want it to.** `/chat/stream` forwards OpenAI's response as
server-sent events. Streamed answers are cached and billed exactly like normal ones.

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

- `X-Cache` — `HIT` if the answer came from the cache, `MISS` if it came from OpenAI
- `X-Model` — which model was called (not set on a cache hit because this request made
  no model call)
- `Retry-After` — on a 429, how many seconds to wait

If OpenAI returns an error, the gateway passes its status code through unchanged, so a
429 from OpenAI reaches you as a 429. Both `/chat` and `/chat/stream` behave the same way.

### Abandoned streams

If you stop reading a stream part way through, the gateway drops the connection to
OpenAI, which stops generating. That saves money, but it also means OpenAI never sends
its final token count — it arrives at the very end or not at all.

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

Keys live in a Redis set called `api_keys`. There is no admin endpoint — add them
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
| `OPENAI_API_KEY` | Your OpenAI key (needed) | —                |
| `REDIS_ADDR`     | Where Redis is           | `localhost:6379` |
| `CACHE_TTL`      | How long answers remain reusable | `24h` |

## Trying it out

```bash
# ask something
curl -X POST http://localhost:8080/chat \
  -H "Content-Type: application/json" \
  -H "X-API-Key: some-secret-key" \
  -d '{"prompt": "What is the capital of France?"}'

# ask it a different way — this one should come back as a cache hit
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

If you are reading this to judge the code rather than to use it, these are the parts
worth your time.

Start with [internal/completion/stream.go](internal/completion/stream.go). It is the
hardest thing in the repo: it forwards bytes to the client while accumulating the answer
and the token counts, and it has to tell a finished stream apart from one that was cut
off.

Then [internal/ratelimit/redis.go](internal/ratelimit/redis.go), specifically the Lua
script and the comment explaining why the timestamps are milliseconds. That comment is
the fix for a bug that let roughly twelve times the configured rate through.

[docs/adr/](docs/adr/) has the decisions and what was rejected.
[docs/ENGINEERING-NOTES.md](docs/ENGINEERING-NOTES.md) writes up the two real bugs: how
they were found, why the tests missed them, and what pins them now. That is probably the
most useful thing here.

For the tests, [internal/handler/abandoned_test.go](internal/handler/abandoned_test.go)
is the one I would look at. It cancels a request mid stream against a real provider and
checks the caller still gets billed.

## Known limitations

Things that are not done, so you do not have to go looking for them.

**Never run against real OpenAI.** Every provider test uses a local fake. The request
and response shapes match OpenAI's documentation, but documentation is not the wire. The
end to end smoke test needs a real key and has not been run.

**The token estimate is unreconciled.** Abandoned streams are billed on a rough four
bytes per token. Nobody has checked that against an actual OpenAI invoice, and it reads
low for non-English text.

**Semantic similarity is corpus-dependent.** The 0.95 threshold remains a hypothesis
until the committed evaluator is run and its precision target is met. Isolation tests
prove the mechanics, not that every real prompt pair is safe to reuse.

**The routing keyword list is a guess.** It has never been checked against real traffic
to see how often it picks wrong.

**Configuration is deliberately small.** Rate limits, the cache similarity threshold
and model prices remain compile-time constants. Cache lifetime is configurable.

## How the code is laid out

| Package                  | What lives there                                   |
|--------------------------|----------------------------------------------------|
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

**Sliding window rate limiting.** A fixed window lets someone send a full window's
worth of requests at the end of one window and again at the start of the next. A
sliding window spreads it out.

**Cache on meaning, not exact text.** Exact-match caching misses anything reworded,
which is most of what people actually send.

**Keyword routing instead of a classifier.** Asking a model which model to use costs
a call and adds delay. Checking length and a word list is good enough and free.

**One place to add a model.** A model's id, description and price live together in
`internal/router`. Add one there and billing, routing and `/models` all pick it up.
