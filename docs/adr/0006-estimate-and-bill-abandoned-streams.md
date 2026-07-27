# ADR-0006: Estimate and bill abandoned streams

Status: Accepted
Date: 2026-07-26

## Context

The gateway asks OpenAI for `stream_options.include_usage`, which adds one final chunk
carrying the token totals. Every chunk before it reports usage as null, so that last
chunk is the only place the real numbers appear.

When a client stops reading a stream, its request context is cancelled. That context
reaches the upstream HTTP request through `http.NewRequestWithContext`, so cancelling
kills our read of OpenAI's response. The read fails with `context canceled` partway
through, and the usage chunk never arrives.

Tokens were then recorded as zero. Not "unknown", not "estimated". Zero, indistinguishable
in `/usage` from a request that never happened.

This is not an unusual path. It is what happens every time somebody hits stop on a
streaming response, which for a chat interface is routine. Every one of them was free.

The tokens were real. OpenAI charges the full prompt whether or not you read the answer,
plus whatever it generated before the connection dropped.

## Decision

When no usage chunk arrives, estimate from the exact prompt and the partial answer
received before cancellation. `estimateTokens` uses roughly four bytes per token.

Record the charge, and mark it:

- `usage.Entry.Estimated` flags it
- `Stats.EstimatedRequests` counts it, so `/usage` reports how many of a key's charges
  were inferred
- the request log carries `"estimated": true`

Normal provider exhaustion settles immediately. The HTTP handler also defers `Close`;
if the client abandons the stream first, that call crosses the same idempotent
settlement path with a bounded context detached from the cancelled request.

## Consequences

Abandoned streams appear in billing instead of vanishing.

Cancelling still drops the upstream connection, so OpenAI stops generating as before.
The change affects the usage record, not provider spend.

The number is approximate. Four bytes per token is a rule of thumb for English and reads
low for CJK. It has never been reconciled against a real OpenAI invoice, which is the
main open risk with this decision.

Every estimate must remain visible. A customer disputing a charge needs to know whether
the provider reported the number or the gateway inferred it.

Truncated streams are still not cached. An estimate is good enough to bill, not good
enough to serve to somebody else.

## What we rejected

Recording zero and logging a warning. Honest about the gap, but it writes off real cost
by default and buries the evidence in logs that rotate.

Detaching the upstream read from the client's context so the stream always runs to
completion and reports real usage. Gives exact numbers, and pays OpenAI to generate an
answer nobody will read. Spending more money to measure money is the wrong direction.

Adding a real tokenizer such as tiktoken. More accurate, and a runtime dependency with
vocabulary files to ship, for a fallback path. Worth doing if reconciliation shows the
heuristic drifting enough to matter.
