# Semantic-cache evaluation

Semantic similarity is probabilistic. A unit test can prove tenant isolation and Redis
mechanics, but it cannot prove that `0.95` is a safe threshold for real language.

This project therefore separates two questions:

1. **Does the cache enforce its rules?** Deterministic integration tests answer this in
   CI.
2. **Does the embedding threshold classify prompt pairs well?** The opt-in evaluator
   measures this with real embeddings.

## Corpus

[`evaluation/cases-v1.json`](evaluation/cases-v1.json) contains 40 labelled pairs:

- 20 equivalent questions that should hit;
- 20 related but meaningfully different questions that should miss;
- examples routed through both the cheap and powerful model tiers.

Hard negatives differ by details such as country, operation or delivery guarantee. They
are deliberately more useful than random unrelated prompts.

The corpus is small and English-only. Results describe this corpus, not all production
traffic.

## Run it

The evaluator makes paid calls to the OpenAI embeddings endpoint and requires Redis
Stack. It is intentionally excluded from normal CI. Each run uses a unique cache
namespace; use a disposable Redis instance when publishing a result.

```bash
docker run --rm -d --name llm-gateway-eval-redis \
  -p 6379:6379 redis/redis-stack-server:latest

export OPENAI_API_KEY=sk-your-key
go run ./cmd/cache-eval \
  -json docs/evaluation/results-v1.json \
  -markdown docs/evaluation/results-v1.md
```

Optional environment:

```bash
export REDIS_ADDR=localhost:6379
export CACHE_TTL=24h
```

The default outputs are committed portfolio evidence:

- `docs/evaluation/results-v1.json` contains provenance, every case, and all metrics.
- `docs/evaluation/results-v1.md` contains the human-readable summary and case table.

Paths and corpus can be overridden:

```bash
go run ./cmd/cache-eval \
  -dataset docs/evaluation/cases-v1.json \
  -json tmp/results.json \
  -markdown tmp/results.md \
  -run-id 20261011T120000Z
```

## Metrics

- **Precision:** of all cache hits, how many were genuinely equivalent?
- **Recall:** of all equivalent pairs, how many were found?
- **Cache hit rate:** how often the lookup avoided a completion in this simulation.
- **p50/p95 latency:** median and tail lookup time, including the embedding request.

The command exits non-zero when precision is below 95% or recall is below 80%, after
writing the result files. Both gates are fixed before running the corpus so a threshold
cannot be tuned after seeing the result.

## Publishing results

No benchmark number is committed yet because the evaluation has not been run in a
controlled environment. When publishing:

1. keep the corpus version, checksum, embedding model, threshold, source commit and raw JSON result together;
2. record runtime and Redis setup;
3. publish failed targets as well as successful ones;
4. tune the threshold without changing labels to flatter the result;
5. do not describe this small corpus as production traffic.

This makes a missing result honest and a future result reproducible.

## Real-provider smoke run

The smoke command is opt-in and sends a small, bounded set of requests through a running
gateway. Start Redis Stack and the gateway with a real `OPENAI_API_KEY`, provision a
project key, then run:

```bash
export SMOKE_API_KEY=the-provisioned-project-key
go run ./cmd/provider-smoke \
  -json docs/smoke/provider-v1.json \
  -markdown docs/smoke/provider-v1.md
```

It checks a JSON completion, a repeated cache hit, a completed stream, a cancelled
stream, and the resulting usage settlement. Reports contain no API key or response
content. The command exits non-zero when any check fails and writes its report first.
