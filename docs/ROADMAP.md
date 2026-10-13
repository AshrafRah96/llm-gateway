# Roadmap

Keep the gateway small. Separate shipped controls from unmeasured behavior.

## Shipped

- Bounded requests, deadlines, concurrency, model output and provider response reads.
- Compatible HMAC key migration, 90-day new-key expiry, revocation, scoped operator rotation.
- Atomic UTC-day token/USD reservations covering chat attempts, embeddings, imports and evaluations.
- Separate persistent integer ledger; identified events, retained uncertain holds, migration and reconciliation.
- Private authenticated TLS Compose, pinned images, separate volumes, resource caps and readiness.
- Backup and isolated RDB/AOF restore tooling with local synthetic proof.
- Project-filtered RAG and corpus/policy-versioned semantic cache.
- Fixed prompt policy, encoded references and separate question; shared JSON/SSE/fallback lifecycle.
- Grounded SSE fallback headers before flush; partial-answer and settlement-failure cache exclusion.
- Configured model catalogue, validated prices/caps and provider request-ID capture.
- Opt-in smoke/cache/adversarial tools; fixed gates; three JSON/SSE adversarial repetitions.
- CI race/integration gates plus unpaid TLS/ACL/recovery checks.
- Refreshed architecture/docs and persistent code/document knowledge graph.

[Local verification](verification/local-checks.json) records repository checks and recovery evidence.
Prepared evaluations have **no measured live-provider results**. No paid calls were authorized.

## Operator work

- Verify supported model IDs/prices/caps before legacy aliases shut down on 23 October 2026.
  [OpenAI deprecations](https://developers.openai.com/api/docs/deprecations).
- Issue production certificates; manage secrets and HTTPS ingress.
- Store encrypted off-host backups; set recovery time/loss targets; rehearse actual cutover.
- Run paid smoke/cache/adversarial measurements when authorized; publish failures as well as passes.
- Reconcile uncertain reservations and configured charges against provider usage/invoices.

## Measurement and observability

- Load-test concurrent admission, cancellation, quotas and graceful shutdown.
- Add metrics/traces and alerts for capacity, holds, storage, backups and provider errors.
- Measure routing quality/cost and cache precision/recall by domain and language.
- Compare estimates with real token counts; measure answer/cache poisoning before adding classifiers or screening.
- Measure configured failover and provider Retry-After behavior with real traffic.

## Scope

No external provisioning, admin portal, multi-provider control plane or full billing platform.
Graph isolation alone is not a defect. Add code only for a demonstrated requirement and reproducible check.
