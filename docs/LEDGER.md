# Ledger and migration

Separate Redis owns project keys, immutable usage events, lifetime totals, budget policy,
UTC-day spend, and pending reservations. Cache/quota/RAG Redis is disposable. Do not merge them.

## Budget rules

Default: 100,000 tokens and US$10 per project per UTC day. Operator overrides may set zero.
Admission atomically checks committed spend plus pending holds before each paid operation.
Chat attempts, fallback attempts, cache/RAG embeddings, imports, and cache evaluation all count.
Cache hits avoid chat charges but still pay for embeddings.

The Redis clock selects the UTC window. Settlement charges the reservation's original day.
An old unresolved hold remains recoverable without blocking the next day's allowance.
Reservations never expire automatically. A reset cannot erase unresolved provider work.

Chat input reservation uses serialized UTF-8 bytes plus 256 bytes per message; output uses the
configured provider cap. Embedding reservations use the smaller of UTF-8 byte length and the provider’s 8192-token input cap.
This favors rejecting some affordable work over admitting unbounded spend. Model rates must be correct.
Unknown outcomes retain full holds; displayed estimates are not release authority.

Amounts are integer nanodollars: 1 USD = 1,000,000,000 units. Financial arithmetic is bounded
by Redis Lua's exact-integer range, 2^53−1. Wire USD floats are derived display values.
Events store operation/client IDs and available provider request IDs, never prompt/response content.

## Upgrade without losing history

Stop old gateway/import/evaluation writers. Back up both stores and the stable pepper.
Point `REDIS_*` to the old source and `LEDGER_REDIS_*` to the new ledger using operator credentials.
Do not start the new gateway until migration checks pass.

```bash
go run ./cmd/usage-admin -action migrate-keys -project legacy-cutover
go run ./cmd/usage-admin -action migrate -project existing-project
go run ./cmd/usage-admin -action audit -project existing-project
go run ./cmd/usage-admin -action status -project existing-project
```

Key migration scans all HMAC records, preserving project binding, revocation, and existing expiry.
Old records without expiry stay valid. Migration receipts permit later legitimate rotation/revocation.
Conflicting destination records fail; source hashes remain unchanged. Do not rotate the pepper during cutover.
The older raw `api_keys` test-only set is not a production project-key migration format.

Run usage migration once for every historical `usage:<project>` hash. It records raw legacy values,
source checksum, and a baseline event. Decimal costs round upward by at most one nanodollar.
Historical usage conservatively consumes the opening day's allowance; review and set limits accordingly.
Identical replay does nothing; changed source data fails. Startup detects unmigrated usage/key records.

Keep source backups. For rollback, stop new writers, retain the new ledger, and reconcile changes before
resuming an old binary. The old binary cannot represent new embedding events or rotated key state.
Do not claim a rollback by replacing the ledger with the earlier snapshot.

## Reconcile and repair

```bash
go run ./cmd/usage-admin -action pending -project demo
go run ./cmd/usage-admin -action reconcile -project demo -event EVENT_ID \
  -input-tokens 123 -output-tokens 45 -cost-usd 0.00123 -evidence provider-receipt-ID
go run ./cmd/usage-admin -action audit -project demo
go run ./cmd/usage-admin -action audit -repair -project demo
```

Use provider usage or invoice evidence; never guess a hold away. If a crash left no observation,
provide `-kind` (`chat`, `cache_lookup`, `rag_lookup`, `rag_import`, or `cache_evaluation`).
Reconciliation appends a resolution; it does not edit the observation. Evidence references are bounded
and must contain no credentials. Amounts beyond reserved bounds fail for investigation.

Audit validates event identities and resolution references, then recomputes lifetime aggregates.
Explicit repair uses an atomic snapshot comparison. It cannot repair unknown provider outcomes,
daily holds, corrupt event history, or invoice disagreement. Back up before administrative repair.

[Backup and restore](LEDGER-RECOVERY.md) · [Operations](OPERATIONS.md).
