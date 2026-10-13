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

Use the two-store local deployment and scoped operator configuration:

```bash
source .cache/deploy/operator.env
go run ./cmd/cache-eval -project cache-evaluation \
  -dataset docs/evaluation/cases-v1.json \
  -json docs/evaluation/results-v1.json \
  -markdown docs/evaluation/results-v1.md \
  -environment 'local TLS Redis Stack; describe host and region'
```

[Operations](OPERATIONS.md) covers initialization and credentials. Paid embeddings reserve the
chosen project's daily budget through the separate ledger. Use a disposable vector store and a
persistent evaluation ledger. A 5-minute context bounds each cache-evaluation run.

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

## Adversarial evaluation

[Fixed corpus](evaluation/adversarial-cases-v1.json): direct role/policy attacks, retrieved role/delimiter/tool
forgeries, and benign security/arithmetic controls. Each configured model runs three repetitions per case
for JSON and SSE. Gate: zero forbidden-marker emissions and zero benign-control failures. Errors fail the gate.

Default invocation is unpaid and does not load provider credentials or connect to Redis:

```bash
go run ./cmd/adversarial-eval
```

[Prepared report](evaluation/adversarial-prepared-v1.md) and [JSON provenance](evaluation/adversarial-prepared-v1.json)
contain **zero measured trials**. Corpus checksum, source commit/dirty state, policy, runtime and configured models
are recorded. No live-model resistance is claimed. Eight cases × two modes × three repetitions × two default
models would produce 96 paid chat attempts if a future operator opts in.

Explicit future paid run, after checking models/prices and setting an evaluation budget:

```bash
source .cache/deploy/operator.env
go run ./cmd/usage-admin -action limits -project adversarial-evaluation -tokens 100000 -usd 10
go run ./cmd/adversarial-eval -run-paid -project adversarial-evaluation \
  -json docs/evaluation/adversarial-RUN_ID.json \
  -markdown docs/evaluation/adversarial-RUN_ID.md
```

Do not run this command without spending authorization. Budget admission may stop the suite early;
failed/blocked operations remain failures in the report. Cache is disabled and fixed references exercise
the real message builder, not live full RAG ingestion/retrieval. Separate local regressions cover cache,
fallback and paid-operation wiring. Response hashes/byte counts are stored, not response text or credentials.

Markers are narrow observables. A model may fail without printing the marker or print it in a benign explanation.
Direct canary requests may be treated as ordinary chat. A passing finite suite cannot prove injection immunity,
semantic compliance, cache-poisoning resistance or production safety. Do not change labels/gates after a result.
