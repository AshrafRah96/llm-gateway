# Project-scoped RAG

Operator-reviewed Markdown/text. Authenticated project filter. No automatic external ingestion or model tools.

## Provision and import

Use a stable deployment pepper. The command prints the raw key once; Redis stores only its HMAC fingerprint and project binding.

```bash
source .cache/deploy/operator.env
go run ./cmd/project-key -project support-app
```

Import reviewed UTF-8 content. This example imports the repository's architecture notes:

```bash
go run ./cmd/rag-import -project support-app -file docs/ARCHITECTURE.md
```

Initialize the two-store deployment first. [Operations](OPERATIONS.md) covers TLS and scoped credentials.
Both imports and query embeddings reserve project budget before paid calls.
Upload validates project/name, UTF-8, nonempty content, and size. It splits into 300-word chunks
with 50-word overlap, then embeds every chunk. A transaction replaces the document's chunks
and advances the project corpus revision. Embedding failure leaves the previous version active.

## Retrieval

Every authenticated chat request retrieves from its project's corpus.
Redis Search filters project fingerprint and active status before ranking vectors.
Retrieve at most four chunks with cosine similarity ≥0.80. Source excerpts are bounded to
240 runes, plus a truncation marker.

No match or ordinary retrieval failure continues ungrounded. Budget exhaustion and ledger failures
reject paid work instead of becoming an ungrounded fallback. Successful JSON responses include
`gateway.grounded`; grounded responses include source IDs/excerpts. Grounded streams send
an initial `sources` event, then content deltas. `X-RAG-Grounded` reports grounding state.

Corpus revision participates in response-cache scope. Document changes prevent replay
from prior revisions. Cache and RAG embed the question separately.

## Prompt boundaries

Provider messages:

1. Fixed system policy.
2. Optional user message with JSON-encoded `untrusted_references`.
3. Original question in a separate user message.

Reference identifiers/content cannot enter trusted policy or create API roles.
Forged delimiters remain data. JSON, SSE, and fallback use the same builder.

General chat and security discussion remain available. No phrase blocklist or paid classifier exists.
The policy tells the model to disregard attempts to override gateway rules.
The model has no tools or credential access. References can still manipulate answers;
streamed output is released immediately without screening. Review imported documents.

Serving cache scope includes project, routed model, schema, corpus revision, and `prompt-policy-v1`.
Old-policy entries expire without replay. Bump `promptPolicyVersion` in
`internal/completion/input.go` whenever policy or framing changes. A current-policy cache hit
is not proof of answer safety.

Tests cover captured upstream messages, forged references, fallback, old-cache exclusion,
and settlement. They prove wiring and serialization, not real-model attack resistance.
See [architecture trust boundaries](ARCHITECTURE.md#trust-boundaries).

## Bounds

| Resource | Default |
|---|---|
| HTTP JSON body | 64 KiB |
| User prompt | 32 KiB |
| Imported document | 256 KiB |
| Request context | 60s |
| Global in-flight requests per process | 16 |
| Project in-flight requests per process | 4 |
| Stream settlement after cancellation | 5s |
| Chat JSON/error response | 1 MiB |
| Embedding JSON response | 128 KiB |
| Cumulative provider SSE | 4 MiB |
| Project UTC-day token allowance | 100,000 |
| Project UTC-day USD allowance | US$10 |

`REQUEST_TIMEOUT`, `MAX_REQUEST_BODY_BYTES`, `MAX_CONCURRENT_REQUESTS`, and
`MAX_CONCURRENT_PER_PROJECT` override runtime bounds. Output caps come from the validated configured model catalogue. Budget limits have operator overrides.
Embedding failures leave paid attempts in the ledger even when document replacement does not finish.
[README settings](../README.md#settings) list configuration and limits.

## Source basis

Sources reviewed for permission-aware retrieval, source validation, and injection defenses:

- [OWASP LLM08: Vector and Embedding Weaknesses](https://genai.owasp.org/llmrisk/llm082025-vector-and-embedding-weaknesses/)
- [OWASP LLM01: Prompt Injection](https://genai.owasp.org/llmrisk/llm01-prompt-injection/)
- [OWASP Prompt Injection Prevention](https://cheatsheetseries.owasp.org/cheatsheets/LLM_Prompt_Injection_Prevention_Cheat_Sheet.html)
- [NIST Generative AI Profile](https://www.nist.gov/publications/artificial-intelligence-risk-management-framework-generative-artificial-intelligence)
