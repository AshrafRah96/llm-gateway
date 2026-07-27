# Semantic-cache evaluation

Semantic similarity is probabilistic. A unit test can prove tenant isolation and Redis
mechanics, but it cannot prove that `0.95` is a safe threshold for real language.

The tests and evaluator answer different questions:

1. **Does the cache enforce its rules?** Deterministic integration tests answer this in
   CI.
2. **Does the embedding threshold classify prompt pairs well?** The opt-in evaluator
   measures this with real embeddings.

## Corpus

[`evaluation/cases-v1.json`](evaluation/cases-v1.json) contains 40 labelled pairs:

- 20 equivalent questions that should hit;
- 20 related but meaningfully different questions that should miss;
- examples routed through both the cheap and powerful model tiers.

Hard negatives differ by details such as country, operation or delivery guarantee.
Random unrelated prompts would make the threshold look safer than it is.

The corpus is small and English-only. Results describe this corpus, not all production
traffic.

## Run it

The evaluator makes paid calls to the OpenAI embeddings endpoint and requires Redis
Stack. It is intentionally excluded from normal CI.

```bash
docker run --rm -d --name llm-gateway-eval-redis \
  -p 6379:6379 redis/redis-stack-server:latest

export OPENAI_API_KEY=sk-your-key
go run ./cmd/cache-eval
```

Optional environment:

```bash
export REDIS_ADDR=localhost:6379
export CACHE_TTL=24h
```

Default outputs are ignored working files:

- `tmp/cache-eval-results.json` contains environment metadata and every case.
- `tmp/cache-eval-results.md` contains the short results table.

Paths and corpus can be overridden:

```bash
go run ./cmd/cache-eval \
  -dataset docs/evaluation/cases-v1.json \
  -json tmp/results.json \
  -markdown tmp/results.md
```

## Metrics

| Metric | Meaning |
|---|---|
| Precision | Of all cache hits, how many were genuinely equivalent? |
| Recall | Of all equivalent pairs, how many were found? |
| Cache hit rate | How often did the lookup avoid a completion in this simulation? |
| p50/p95 latency | Median and tail lookup time, including the embedding request |

The command exits non-zero when precision is below 95%, after writing the result files.
Recall is reported rather than used as a hard gate: a false miss costs performance, but
a false hit gives the user the wrong answer.

## Publishing results

No benchmark number is committed because the evaluation has not run in a controlled
environment. Before publishing a result:

1. keep the corpus version, embedding model and raw JSON result together;
2. record machine, region and Redis setup;
3. publish failed targets as well as successful ones;
4. keep the labels fixed, and use a separate validation set if tuning the threshold;
5. describe the result as a measurement of this corpus, not production traffic.

Until those conditions are met, leaving the result unpublished is more accurate than
quoting an unrepeatable score.
