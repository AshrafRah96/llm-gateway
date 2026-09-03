# Project-scoped RAG

RAG is deliberately a small, operator-controlled capability in this repository. It
demonstrates the security boundary around retrieval without becoming a document SaaS.

## Provisioning and ingestion

Each caller key belongs to one project. Provision it with a keyed fingerprint, never a
plaintext Redis value:

```bash
export API_KEY_PEPPER='a-long-random-secret'
go run ./cmd/project-key -project support-app
```

The command prints the generated key once. Keep it in the calling application's secret
store. It stores only an HMAC-SHA-256 fingerprint and its project binding in Redis.

Import only reviewed UTF-8 Markdown or text:

```bash
export OPENAI_API_KEY=sk-your-key
go run ./cmd/rag-import -project support-app -file docs/support-policy.md
```

An import validates size and encoding, chunks on word boundaries, embeds every chunk,
then activates the complete replacement in Redis and advances that project's corpus
revision. A failed embedding leaves the previously active document untouched.

## Runtime behaviour

Every authenticated `/chat` and `/chat/stream` request uses its project's corpus. The
retriever filters Redis Search by a project fingerprint before vector ranking; chunks
from another project cannot satisfy the query. A corpus revision is part of the response
cache namespace, so importing or replacing material cannot serve an answer cached under
the prior corpus.

When the retrieval threshold has no result, the gateway still calls the model but marks
the JSON response with `gateway.grounded: false`. Grounded responses include bounded
source IDs and excerpts. Streams emit an initial `sources` event before content deltas.

Retrieved text is enclosed as **untrusted reference data**. The gateway does not give
the model tools or downstream actions, and it tells the model not to follow instructions
inside references. This reduces blast radius but does not make prompt injection solved:
project administrators must review documents before import.

## Limits

The gateway defaults to a 64 KiB HTTP body, 32 KiB prompt, 256 KiB document, 60 second
request deadline, 16 concurrent gateway requests, and four concurrent requests per
project. `REQUEST_TIMEOUT`, `MAX_REQUEST_BODY_BYTES`,
`MAX_CONCURRENT_REQUESTS`, and `MAX_CONCURRENT_PER_PROJECT` override the runtime
defaults. Each catalogue model supplies its own output-token cap.

## Research basis

OWASP identifies permission-aware vector stores, source validation, and monitoring as
key mitigations for vector and embedding weaknesses, especially in multi-tenant RAG.
It also notes that RAG does not remove prompt-injection risk.

- [OWASP LLM08: Vector and Embedding Weaknesses](https://genai.owasp.org/llmrisk/llm082025-vector-and-embedding-weaknesses/)
- [OWASP LLM01: Prompt Injection](https://genai.owasp.org/llmrisk/llm01-prompt-injection/)
- [NIST AI RMF Generative AI Profile](https://www.nist.gov/publications/artificial-intelligence-risk-management-framework-generative-artificial-intelligence)
