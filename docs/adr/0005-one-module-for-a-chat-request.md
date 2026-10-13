# ADR-0005: One module for a chat request

Status: Accepted
Date: 2026-07-26

## Failure

Duplicated HTTP pipelines drifted. `/chat/stream` decoded, routed, and forwarded bytes,
but omitted cache lookup/storage, usage, costs, and logs. Streamed spend disappeared.
Concrete collaborators prevented substitution; tests supplied nil cache/usage,
forcing nil guards and leaving cache hits untested.

## Decision

Move chat policy into `internal/completion`:

```go
func (c *Completion) Complete(ctx, Request) (Response, error)
func (c *Completion) Stream(ctx, Request) (*Stream, error)
```

`Provider`, `Cache`, and `Recorder` interfaces live with their consumer.
`New` takes all three collaborators; callers must supply valid values.
`NewWithRouting` also supplies routing and optional retrieval.
HTTP handlers share decoding, call the lifecycle, and encode responses.

Provider decodes OpenAI SSE into neutral content/usage/done events.
Completion accumulates and applies route, retrieval, prompt, cache, and billing policy.
Normal exhaustion settles; `Close` is the idempotent abandonment fallback.
HTTP encodes outbound SSE. Modules test their own interfaces.

## Tradeoff

One lifecycle reduces JSON/SSE policy drift. Streaming complexity remains explicit.
Fakes exercise lifecycle and cache-hit paths without network dependencies.

Rejected: placing transport-independent policy in `handler`, because ownership stays misleading;
a shared HTTP-shaped helper, because callers can skip it and the seam stays tied to HTTP.
