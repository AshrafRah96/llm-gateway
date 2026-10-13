# Decision records

Decisions, tradeoffs, and rejected alternatives. Dates preserve history; later notes identify current scope.

| ADR | Decision | Tradeoff |
|---|---|---|
| [0001](0001-keyword-routing-instead-of-a-classifier.md) | Keyword routing | No paid classifier; crude difficulty estimate |
| [0002](0002-cache-on-meaning-not-exact-text.md) | Semantic cache | Rewording reuse; embedding cost and false-hit risk |
| [0003](0003-the-store-decides-the-rate-limit.md) | Store-owned quota decision | Atomic admission; more logic in Redis |
| [0004](0004-millisecond-scores-in-the-lua-script.md) | Millisecond scores, random members | Safe float64 range; coarser time resolution |
| [0005](0005-one-module-for-a-chat-request.md) | Shared completion lifecycle | One JSON/SSE policy; transport adapters stay narrow |
| [0006](0006-estimate-and-bill-abandoned-streams.md) | Estimated abandoned-stream usage | Visible spend; approximate counts |
| [0007](0007-isolate-semantic-cache-by-tenant-and-model.md) | Scoped cache | Confidentiality over cross-project hit rate |
| [0008](0008-portfolio-evidence-gates.md) | Portfolio evidence gates | Measured claims; opt-in provider cost |
| [0009](0009-separate-gateway-policy-from-untrusted-input.md) | Prompt trust separation | General chat and live SSE; residual injection risk |

[Engineering notes](../ENGINEERING-NOTES.md) explain the quota and streaming regressions behind ADRs 0003, 0004, and 0006.
