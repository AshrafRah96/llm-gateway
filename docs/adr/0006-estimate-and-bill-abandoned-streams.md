# ADR-0006: Estimate and bill abandoned streams

Status: Accepted
Date: 2026-07-26

## Decision

Request `stream_options.include_usage`. Provider counts arrive at the end; cancellation
can prevent them from arriving. Recording zero hides real consumed work.

When usage is absent, estimate roughly four bytes per token. Current input includes system
policy, encoded references, and question; output includes received content.
Label the charge through `usage.Entry.Estimated`, `Stats.EstimatedRequests`, and log `"estimated": true`.
`/usage` reports project totals.

Normal exhaustion settles immediately. Deferred `Close` handles abandonment through the
same idempotent path. Settlement detaches from cancellation but has a 5s timeout.
Partial streams are never cached. Provider usage overrides estimates when present.

## Tradeoff

Cancellation still stops upstream generation. Estimated accounting records the cost without
buying an unwanted complete answer. Counts omit exact chat-framing overhead, can undercount
CJK/non-English text, and have not been reconciled against provider invoices.
The estimate flag distinguishes inference from measurement during billing review.

## Rejected

- Zero usage plus warning: writes off spend and hides evidence in rotating logs.
- Drain upstream after cancellation: exact counts require paid output nobody reads.
- A tokenizer with vocabulary assets: extra dependency for a fallback path. Revisit if reconciliation shows material drift.
