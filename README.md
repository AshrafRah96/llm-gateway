# LLM Gateway

A small server that sits between your app and OpenAI.

Your app sends a prompt here instead of to OpenAI. The gateway checks the caller's
API key, makes sure they aren't sending too many requests, looks for a cached answer,
picks a model, calls OpenAI, and records what it cost.

## What it does

**Caches answers to similar questions.** "What is the capital of France?" and
"France's capital city?" are different strings but mean the same thing. The gateway
turns each prompt into a vector and looks for a stored answer that is close enough
(95% similar). A hit costs nothing and returns immediately.

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
│ similar      │──── yes ─▶ return the cached answer (X-Cache: HIT)
│ prompt seen  │
│ before?      │
└──────┬───────┘
       ▼
┌──────────────┐
│ pick a model │
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
- `X-Model` — which model answered (not set on a cache hit, because the stored answer
  may have come from a different prompt)
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

## Setup

You need Go 1.25+, an OpenAI API key, and Redis.

Use **RedisStack**, not plain Redis. The cache needs the vector search commands, which
only RedisStack has. Everything else works on plain Redis.

```bash
git clone https://github.com/ashrafrah96/llm-gateway
cd llm-gateway
export OPENAI_API_KEY=sk-your-key
export REDIS_ADDR=localhost:6379
go run main.go
```

The server listens on port 8080.

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

Most tests use fakes and need nothing running. The tests for the Redis parts skip
themselves if no Redis is reachable. To run those too:

```bash
REDIS_ADDR=localhost:6379 go test ./...
```

Plain Redis is enough for the tests. Only the semantic cache needs RedisStack, and it
has no tests yet for that reason.

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

**Sliding window rate limiting.** A fixed window lets someone send a full window's
worth of requests at the end of one window and again at the start of the next. A
sliding window spreads it out.

**Cache on meaning, not exact text.** Exact-match caching misses anything reworded,
which is most of what people actually send.

**Keyword routing instead of a classifier.** Asking a model which model to use costs
a call and adds delay. Checking length and a word list is good enough and free.

**One place to add a model.** A model's id, description and price live together in
`internal/router`. Add one there and billing, routing and `/models` all pick it up.
