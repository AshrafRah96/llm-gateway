# Threat model

This document makes the security claim deliberately narrow: the gateway demonstrates
important controls, but it is not ready for untrusted public traffic.

## Assets

- The provider API key, which can create real cost.
- Client API keys and their usage totals.
- Prompts and model responses, which may contain private data.
- Quota state and billing estimates.
- Service availability.

## Trust seams

```text
untrusted client → HTTP gateway → Redis
                         │
                         └──────→ OpenAI
```

The client is untrusted. Redis is assumed to be on a trusted private network. OpenAI is
an external processor receiving prompts. The gateway is responsible for validating the
client and limiting what one tenant can observe or spend.

## Implemented controls

### Cross-tenant cache disclosure

Vector similarity alone is not an authorization rule. Cache searches are therefore
filtered by a SHA-256 tenant fingerprint, routed model and schema version before Redis
ranks candidates. Entries also expire. Integration tests cover tenant and model
isolation, expiry, malformed entries and legacy-key exclusion.

The raw client key is not stored in semantic-cache keys or metadata. This is only cache
hardening: the authentication set and usage keys still need stronger key handling.

### Cost and availability

- Authentication runs before paid work.
- A per-key atomic sliding window rejects excess requests.
- Cancelling an SSE request cancels the provider request.
- Partial streams are billed with a clearly labelled estimate rather than written off.
- Only complete successful answers reach the cache.

### Verification

Redis-backed tests run against Redis Stack in CI. CI fails if those tests skip. The
semantic threshold has a separate labelled evaluation rather than being treated as a
security guarantee.

## Open risks

| Priority | Risk | Required production control |
|---|---|---|
| Critical | Unlimited body, prompt size, output, concurrency and spend | Request limits, token budgets, deadlines, concurrency caps and per-tenant spend ceilings |
| Critical | Client keys are plaintext Redis set members and appear in other Redis key names | Keyed fingerprints, constant-time verification, rotation, expiry and scopes |
| High | Compose exposes Redis and RedisInsight without ACL or TLS | Private networking, Redis ACL, TLS, secrets management and removal of public ports |
| High | Usage accounting is best effort and uses floating-point money | Durable idempotent events, integer minor units and reconciliation |
| High | Provider calls have no bounded retry policy or request-ID tracing | Deadline-aware retries with jitter, error taxonomy and correlation IDs |
| Medium | Similarity can return the wrong answer inside one tenant | Evaluated threshold, domain metadata, invalidation and monitoring |
| Medium | Hard-coded models and prices can become stale | Validated configuration and controlled catalogue updates |

Do not expose this repository's Compose setup directly to the internet.

## Source basis

- [OWASP API4: Unrestricted Resource Consumption](https://owasp.org/API-Security/editions/2023/en/0xa4-unrestricted-resource-consumption/)
  covers payload, execution-time and paid third-party resource limits.
- [OWASP Top 10 for LLM Applications 2025](https://owasp.org/www-project-top-10-for-large-language-model-applications/)
  includes sensitive-information disclosure and unbounded consumption.
- [Redis security](https://redis.io/docs/latest/operate/oss_and_stack/management/security/)
  states that Redis is designed for trusted environments and documents ACL and TLS
  controls.
- [Redis vector search](https://redis.io/docs/latest/develop/ai/search-and-query/vectors/)
  documents metadata filtering alongside vector queries.
