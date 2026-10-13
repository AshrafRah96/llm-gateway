# Threat model

Implemented controls reduce risk. This reference project is not ready for untrusted public traffic.

## Assets and trust

Assets: provider/client keys, prompts, responses, quota state, usage estimates, money, and availability.
Clients and retrieved documents are untrusted. Redis assumes a trusted private network.
OpenAI is an external processor receiving questions and any selected reference content.
The gateway controls project identity, access, and resource bounds.

## Implemented controls

| Risk | Control | Limit |
|---|---|---|
| Cross-project cache disclosure | Filter SHA-256 project fingerprint, routed model, schema/corpus/policy before ranking; expire entries | Similarity is not authorization or answer validation |
| Raw key exposure in state | HMAC auth lookup; non-secret key/project identity downstream | Rotation, expiry, and admin scopes remain absent |
| Cross-project retrieval | Authenticated project binding; filter project/active state before vector ranking | Administrators must review imports |
| Stale context/policy | Corpus revision and `prompt-policy-v1` in serving cache scope | Bump policy version after policy/framing changes |
| Prompt-structure forgery | Fixed system message; JSON-encoded references; separate user question; shared JSON/SSE/fallback builder | Models can still follow attacks |
| Excess work | Auth before paid work; atomic key quota; body/prompt/deadline/output/concurrency bounds | No project token/spend budget; concurrency caps are per process |
| Abandoned SSE cost | Cancel upstream; record labelled estimate; bounded idempotent settlement | Estimates are unreconciled; writes are best effort |
| Partial-answer replay | Cache only complete successful responses | Current-policy answers can still be wrong or poisoned |

Raw caller keys never enter production completion, quota, cache, or usage state.
Model instructions cannot grant Redis access, credentials, or cross-project retrieval.
The model has no tools. System policy is neither a secret nor an authorization boundary.

No phrase blocklist, classifier, or output screening exists. SSE releases generated content immediately.
Retrieved material or user questions may still manipulate an answer, which may then enter the scoped cache.
Ordinary retrieval failures continue ungrounded; cache failures become misses. Access/quota store failures reject requests.

## Verification

Redis-backed tests run with Redis Stack in CI. CI rejects skipped integration tests,
including completion-policy cache isolation. Captured-payload tests cover forged delimiters,
role spoofing, benign security questions, JSON/SSE/fallback, and old-policy cache exclusion.
These tests establish application behavior, not real-model injection resistance.

The [cache evaluator](EVALUATION.md) has separate language-quality gates. No measured
cache report, provider-smoke report, or real-model adversarial result is committed yet.

## Open risks

| Priority | Risk | Production work |
|---|---|---|
| High | Incomplete spend ceilings and provider read bounds | Project budgets; bounded response readers |
| High | No key rotation/expiry/admin scopes | Rotation metadata, expiry, scoped administrator credentials |
| High | Compose exposes Redis/RedisInsight without ACL/TLS or declared volume | Private networking, ACL, TLS, secrets management, persistence/backups |
| High | Best-effort floating-point accounting; embedding spend omitted | Durable idempotent events, integer money, reconciliation, embedding accounting |
| High | No provider request-ID tracing or alert pipeline | Correlation, metrics, traces, actionable alerts |
| Medium | Prompt injection and same-project answer/cache poisoning | Real-model adversarial evaluation; measured guardrail decisions |
| Medium | Similarity can return a wrong answer within one project | Domain/language evaluation, metadata, invalidation, monitoring |
| Medium | Model-ID overrides inherit compiled tier prices/caps; `/models` stays compiled | Validated model catalogue/configuration |
| Medium | Grounded SSE can flush before fallback header is set | Set all response headers before source-event flush |

Do not expose this Compose deployment directly to the internet.

## Sources

- [OWASP API4: Unrestricted Resource Consumption](https://owasp.org/API-Security/editions/2023/en/0xa4-unrestricted-resource-consumption/)
- [OWASP LLM applications risks](https://owasp.org/www-project-top-10-for-large-language-model-applications/)
- [OWASP prompt injection prevention](https://cheatsheetseries.owasp.org/cheatsheets/LLM_Prompt_Injection_Prevention_Cheat_Sheet.html)
- [Redis security](https://redis.io/docs/latest/operate/oss_and_stack/management/security/)
- [Redis vector search](https://redis.io/docs/latest/develop/ai/search-and-query/vectors/)
- [RAG boundaries](RAG.md)
- [Prompt-boundary decision](adr/0009-separate-gateway-policy-from-untrusted-input.md)
