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
isolation, expiry and malformed entries.

Raw client keys are also absent from the authentication store, rate-limit keys and usage
keys: the gateway carries an HMAC fingerprint or project ID after authentication.

### Project RAG isolation

RAG chunks carry a project fingerprint, and every vector query filters on it before
ranking. The project comes from the HMAC-fingerprinted API key rather than a request
field. A corpus revision is part of the response-cache namespace, so a document change
cannot serve an answer built from earlier context. Retrieved documents remain untrusted
data: the gateway labels and delimits them, but cannot claim to eliminate prompt
injection. See [RAG](RAG.md).

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
| High | Spend ceilings and provider-response read limits remain incomplete | Per-tenant budgets and bounded provider readers |
| High | Project key rotation, expiry and administrative scopes are not yet implemented | Rotation metadata, expiry and separate scoped administrator credentials |
| High | Compose exposes Redis and RedisInsight without ACL or TLS | Private networking, Redis ACL, TLS, secrets management and removal of public ports |
| High | Usage accounting is best effort and uses floating-point money | Durable idempotent events, integer minor units and reconciliation |
| High | Failover has no provider request-ID tracing or observability pipeline | Correlation IDs, metrics and actionable alerts |
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
