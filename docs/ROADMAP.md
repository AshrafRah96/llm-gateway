# Roadmap

Prioritize remaining risks. Keep the gateway small.

## Shipped

- Body/prompt limits, request deadlines, concurrency caps, model output caps.
- HMAC project keys; non-secret rate-limit and usage identity.
- Project-filtered RAG; corpus-aware semantic-cache namespaces.
- Fixed prompt policy, encoded references, separate question, policy-versioned cache.
- One configured pre-output fallback at the same provider.
- Shared JSON/SSE lifecycle; labelled partial-stream estimates; bounded settlement.
- Opt-in real-provider smoke command and semantic-cache evaluator.
- CI with Redis Stack and rejection of skipped integration tests.

Shipped commands do not imply measured real-provider results. No evaluation or smoke report is committed yet.

## Next: bound spend and work

- Bound provider response reads.
- Add project token/spend budgets.
- Load-test cancellation, quotas, and graceful shutdown.
- Keep individual request bounds; measure behavior under concurrent traffic.

## Make identity and money auditable

- Add key rotation, expiry, and scoped administrator credentials.
- Use integer monetary units instead of `float64`.
- Persist idempotent usage events; reconcile aggregates and provider charges.
- Separate disposable cache data from billing records.
- Include or separately report embedding spend.

## Harden dependencies

- Redis ACL, authentication, TLS, private ports.
- Pinned container versions, persistence, backups, resource limits.
- Redis Search/provider readiness checks.
- Metrics, traces, request correlation, alerts.

## Validate provider policy

- Configure model prices/output caps with IDs; keep `/models` aligned.
- Measure one-shot failover and `Retry-After` behavior against the provider.
- Capture provider request IDs; define stable error categories.
- Fix fallback-header timing on grounded SSE responses.
- Publish the existing smoke command's measured report.

## Measure AI quality

- Publish reproducible cache evaluation on the 40-case corpus.
- Grow privacy-reviewed cases; report precision/recall by domain and language.
- Evaluate token estimates against provider usage/invoices.
- Measure keyword routing quality and cost.
- Run real-model adversarial tests for direct and retrieved prompt injection.
- Measure answer/cache poisoning risk before adding detection or output screening.

## Scope

No full multi-provider control plane, admin portal, or billing platform.
Add work when it demonstrates a specific engineering decision with reproducible evidence.
