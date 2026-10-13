# Threat model

Implemented controls bound access, spend, reads, and state. Local proof does not certify a production deployment.

## Assets and trust

Assets: provider/client keys, prompts, responses, quotas, reservations, usage events, money, and availability.
Clients and retrieved documents are untrusted. OpenAI receives questions and selected reference content.
The gateway controls identity, access and resource bounds. Private Redis connections use authenticated TLS.
Operator credentials and imported content require trusted human administration.

## Controls and limits

| Risk | Control | Remaining limit |
|---|---|---|
| Cross-project disclosure | Authenticated project filter before RAG/cache ranking; scoped versioned cache | Similarity does not validate answers |
| Key exposure or reassignment | HMAC fingerprints; expiry, revocation, rotation; operator ACL; compatible migration | Pepper rotation needs separate migration |
| Stale reference/policy reuse | Corpus revision and `prompt-policy-v1` in serving cache scope | Developers must bump policy after framing changes |
| Role/delimiter forgery | Fixed system message; encoded references; separate user question; shared delivery builder | Models may still follow hostile instructions |
| Excess paid work | Atomic daily budget reservations before every chat/embedding attempt; fail closed on ledger errors | Correct configured prices/provider output caps are required |
| Excess memory/output | Request/deadline/concurrency bounds; bounded JSON, embedding and cumulative SSE readers | Concurrency caps remain per process |
| Lost uncertain spend | Persistent identified events, integer money, retained holds, evidence-based reconciliation | Estimates can undercount; invoice agreement is unmeasured |
| Partial-answer replay | Cache only complete successful responses; preserve close/settlement errors | A complete answer can still be wrong or poisoned |
| Exposed/corrupt state | Separate TLS/ACL ledger, private network, pinned images, no eviction, AOF, isolated restore drill | Storage failure/off-host recovery remains operator work |
| Inconsistent model billing | Validate model ID/prices/caps; advertise configured catalogue | No account availability or invoice verification |

Raw caller keys never enter completion, quota, cache, or usage records. Model instructions cannot grant
state access, credentials, or cross-project retrieval. The model has no tools. System policy is not a secret.

No classifier, phrase blocklist, or output screening exists. SSE releases generated content immediately.
Hostile user/reference content may manipulate answers and poison a same-project cache. Reviewed imports
reduce exposure without proving safety. Ordinary retrieval errors fail open; budgets and access fail closed.

## Verification

Local race tests cover financial admission, duplicate/conflicting events, cancellation, fallback charges,
malformed usage, key lifecycle/migration, response caps, flushed headers, and prompt serialization.
TLS tests verify unauthenticated/wrong-host rejection and runtime denial of key/budget administration.
A synthetic RDB restore preserves auth, events, holds, totals and daily state across AOF restart.
CI uses pinned Redis Stack, rejects integration skips, and runs the unpaid deployment/recovery checks.

Captured payloads prove application boundaries. The [prepared adversarial suite](EVALUATION.md)
checks narrow marker/control observables across three repetitions per JSON/SSE case.
**No paid smoke, cache-quality, real-model adversarial or invoice measurement occurred.**
See [local evidence](verification/local-checks.json) and [recovery limits](LEDGER-RECOVERY.md).

## Remaining work

- Issue production certificates; distribute secrets; provide HTTPS ingress and encrypted off-host backups.
- Monitor capacity, pending holds, errors, backup age and provider availability; define actionable alerts.
- Run authorized real-provider smoke/cache/adversarial evaluations and reconcile invoices.
- Load-test quotas, cancellation and shutdown across multiple gateway processes.
- Measure domain/language routing, semantic quality, and answer/cache poisoning risk.
- Replace default legacy model aliases before their scheduled 23 October 2026 shutdown.
  [OpenAI deprecations](https://developers.openai.com/api/docs/deprecations).

[OWASP API4](https://owasp.org/API-Security/editions/2023/en/0xa4-unrestricted-resource-consumption/)
· [OWASP prompt injection](https://cheatsheetseries.owasp.org/cheatsheets/LLM_Prompt_Injection_Prevention_Cheat_Sheet.html)
· [Redis TLS](https://redis.io/docs/latest/operate/oss_and_stack/management/security/encryption/)
· [Redis ACL](https://redis.io/docs/latest/operate/oss_and_stack/management/security/acl/)
· [RAG boundaries](RAG.md).
