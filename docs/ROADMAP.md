# Roadmap

The project is intentionally small. This list prioritises risks rather than feature
count.

## Next: bound cost and work

- Limit HTTP body and prompt size.
- Add endpoint-aware request deadlines and bounded upstream response reads.
- Cap concurrent requests and output tokens.
- Add per-tenant token and spend budgets.
- Load-test cancellation, quotas and graceful shutdown.

This is the next slice because one accepted request can currently consume unbounded time
and provider spend.

## Then: make identity and money auditable

- Replace plaintext client keys with keyed fingerprints and rotation metadata.
- Stop placing raw client keys in rate-limit and usage key names.
- Record money as integer minor units rather than `float64`.
- Write idempotent usage events to durable storage and reconcile aggregates.
- Separate disposable semantic-cache data from billing records.

## Then: harden dependencies

- Configure Redis ACL, authentication and TLS.
- Remove public Redis and RedisInsight ports from production deployment examples.
- Pin container versions and add persistence, backups and resource limits.
- Add readiness checks for Redis Search and the provider path.
- Add metrics, traces, correlation IDs and actionable alerts.

## Then: modernise provider policy

- Move model IDs, prices, routing rules and output limits to validated configuration.
- Add bounded retries only for documented transient failures, respecting deadlines and
  `Retry-After`.
- Capture provider request IDs and use a stable error taxonomy.
- Add an opt-in real-provider contract smoke test.

## AI quality work

- Grow the semantic corpus from observed, privacy-reviewed prompt patterns.
- Report precision and recall by domain and language.
- Evaluate the abandoned-stream token estimate against provider invoices.
- Measure routing quality and cost instead of relying on keywords indefinitely.

## Explicitly out of scope

This portfolio project does not aim to become a full multi-provider product, admin
portal or billing platform. New features should be added only when they demonstrate a
specific engineering decision and can be verified.
