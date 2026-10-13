# Semantic-cache evaluation

CI proves cache mechanics and namespace isolation. Real embeddings measure language classification.
Neither proves that similarity `0.95` is universally safe.

## Corpus and targets

[`evaluation/cases-v1.json`](evaluation/cases-v1.json): 40 labelled English pairs.
Twenty should hit; twenty should miss. Both routing tiers appear.
Hard negatives differ by country, operation, or delivery guarantee.

Targets: precision ≥95%, recall ≥80%. Results describe this small corpus, not production traffic.
No measured evaluation or provider-smoke report is committed yet.

## Run

The evaluator makes paid OpenAI embedding calls and needs Redis Stack. Normal CI excludes it.
Each run creates a unique cache namespace. Use disposable Redis for published measurements.

```bash
docker run --rm -d --name llm-gateway-eval-redis \
  -p 6379:6379 redis/redis-stack-server:latest

export OPENAI_API_KEY=sk-your-key
export REDIS_ADDR=localhost:6379
export CACHE_TTL=24h
go run ./cmd/cache-eval \
  -dataset docs/evaluation/cases-v1.json \
  -json docs/evaluation/results-v1.json \
  -markdown docs/evaluation/results-v1.md \
  -environment 'local Redis Stack; describe host and region'

docker stop llm-gateway-eval-redis
```

Use a free port if another Redis already owns 6379. Optional Redis/cache settings default
to `localhost:6379` and `24h`. Output flags may point elsewhere, such as `tmp/results.json`
and `tmp/results.md`. The command generates its run ID; no `-run-id` flag exists.
Its runtime context has a 5-minute budget.

Expected outputs, created by a run:

- `docs/evaluation/results-v1.json`: provenance, cases, metrics.
- `docs/evaluation/results-v1.md`: summary, metadata, case table.

The command can overwrite existing output paths. Choose new paths to preserve prior runs.
Do not link to these reports or claim measured numbers before they exist.

## Metrics

| Metric | Meaning |
|---|---|
| Precision | Equivalent pairs among cache hits |
| Recall | Hits among equivalent pairs |
| Cache hit rate | Fraction avoiding simulated chat generation |
| p50/p95 lookup latency | Median/tail latency, including embedding request |

After a completed evaluation, reports are written before target failures return non-zero.
The fixed gates prevent retroactively redefining success.

## Publish

Keep corpus version/checksum, threshold, embedding model, run ID, source commit, dirty-state
flag, runtime, Redis/Search versions, deployment description, and raw results together.
Publish failures as well as passes. Keep labels fixed when tuning thresholds.

The evaluator exercises semantic-cache classification in its own namespace. It does not
measure full gateway RAG, prompt-injection resistance, or production answer quality.

## Real-provider smoke run

Start Redis Stack and the gateway with a real provider key and pepper. Provision a project key.
The command makes paid chat/embedding calls through the gateway.

```bash
export SMOKE_API_KEY=the-provisioned-project-key
go run ./cmd/provider-smoke \
  -gateway http://localhost:8080 \
  -json docs/smoke/provider-v1.json \
  -markdown docs/smoke/provider-v1.md
```

Checks: usage baseline, JSON completion, repeated cache hit, completed SSE, cancelled SSE,
and usage settlement. Reports omit API keys and response content.
`-prompt` overrides the JSON/cache question. Smoke run IDs are generated automatically.
The command writes the report before returning non-zero for a failed check.
Smoke output paths can overwrite earlier reports; choose distinct paths to preserve prior runs.

Use a fresh provisioned key and avoid concurrent traffic during the run. Protected status
endpoints also consume the 10-request/minute quota. The command has no bypass for rate limits.
