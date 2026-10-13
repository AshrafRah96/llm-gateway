# ADR-0001: Route with keywords instead of asking a model

Status: Accepted
Date: 2026-07-26

## Decision

Route the original question locally. Over 500 **bytes**, or a case-insensitive complexity
keyword, selects the powerful tier. Otherwise select the cheap tier.
Examples: `analyze`, `compare`, `debug`, `step by step`.
See `internal/router/router.go`.

## Tradeoff

A classifier adds a paid call and latency before every answer, including simple questions.
Local routing avoids that call but misclassifies short hard questions and long trivial ones.
Request logs include selected model and original prompt length; routing quality is not measured yet.
Revisit a classifier when measured routing mistakes cost more than classification.

Caller-selected models were rejected because they bypass gateway cost policy.
Runtime model IDs and same-provider fallback IDs are configurable; prices/output caps remain tier constants.
