# LLM Gateway

A production-grade API gateway for LLM providers. Handles rate limiting, semantic caching, intelligent routing, and usage tracking in a single service.

## Features

- **Rate limiting** — Sliding window algorithm backed by Redis, per API key
- **Semantic caching** — Vector similarity search returns cached responses for similar prompts
- **Intelligent routing** — Routes simple prompts to cheaper models, complex ones to more capable models
- **Usage tracking** — Tracks tokens, costs, and request counts per API key
- **Streaming support** — Server-sent events for real-time responses

## Architecture

```
Client Request
      │
      ▼
┌─────────────────┐
│   HTTP Server   │  ← Port 8080
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  Auth Middleware │  ← Validates X-API-Key
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│   Rate Limiter  │  ← Sliding window (Redis)
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  Semantic Cache │  ← Vector similarity lookup
└────────┬────────┘
         │ (cache miss)
         ▼
┌─────────────────┐
│    LLM Router   │  ← Model selection
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│   LLM Provider  │  ← OpenAI
└─────────────────┘
```

## API

### Endpoints

| Method | Path           | Description                          |
|--------|----------------|--------------------------------------|
| POST   | `/chat`        | Send a prompt, receive a response    |
| POST   | `/chat/stream` | Stream a response via SSE            |
| GET    | `/usage`       | Get token and cost usage for API key |
| GET    | `/limits`      | Get current rate limit status        |
| GET    | `/models`      | List available models                |
| GET    | `/health`      | Health check                         |

### Example Requests

**Chat:**
```bash
curl -X POST http://localhost:8080/chat \
  -H "Content-Type: application/json" \
  -H "X-API-Key: your-api-key" \
  -d '{"prompt": "What is the capital of France?"}'
```

**Streaming:**
```bash
curl -X POST http://localhost:8080/chat/stream \
  -H "Content-Type: application/json" \
  -H "X-API-Key: your-api-key" \
  -d '{"prompt": "Explain quantum computing"}'
```

**Usage:**
```bash
curl http://localhost:8080/usage \
  -H "X-API-Key: your-api-key"
```

### Response Headers

- `X-Cache: HIT|MISS` — Whether the response came from cache
- `X-Model` — Which model handled the request

## Setup

### Prerequisites

- Go 1.21+
- Redis 7+ with vector search module (RedisStack)
- OpenAI API key

### Running Locally

```bash
git clone https://github.com/ashrafrah96/llm-gateway
cd llm-gateway
export OPENAI_API_KEY=your-key
export REDIS_ADDR=localhost:6379
go run main.go
```

### Docker

```bash
docker build -t llm-gateway .
docker run -p 8080:8080 \
  -e OPENAI_API_KEY=your-key \
  -e REDIS_ADDR=host.docker.internal:6379 \
  llm-gateway
```

## Configuration

| Variable         | Description                      | Default          |
|------------------|----------------------------------|------------------|
| `OPENAI_API_KEY` | OpenAI API key (required)        | —                |
| `REDIS_ADDR`     | Redis connection address         | `localhost:6379` |

## Design Decisions

**Sliding window rate limiting** — Fixed windows create burst problems at window boundaries. A sliding window spreads the limit evenly, preventing traffic spikes.

**Semantic caching** — Exact-match caching misses rephrased questions. Vector embeddings catch "What's the capital of France?" and "France's capital city?" as the same query.

**Heuristic routing** — Using an LLM to classify prompts adds latency and cost. Simple heuristics (prompt length, keyword detection) route effectively without the overhead.
