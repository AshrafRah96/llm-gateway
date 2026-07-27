# ADR-0005: One module for a chat request

Status: Accepted
Date: 2026-07-26

## Context

The gateway has two entry points that do nearly the same work: `POST /chat` and
`POST /chat/stream`. Originally each was a plain `http.HandlerFunc` containing its own
copy of the sequence: decode the body, validate it, look in the cache, pick a model,
call the provider, count tokens, record usage, log, store the answer.

Except the streaming one did not contain most of that. It decoded, validated, routed,
called the provider, and forwarded bytes. No cache lookup, no cache write, no usage
recording, no cost, no logging.

That is what happens when the knowledge of "what a chat request does" lives in the
handlers instead of in one place. The second handler was written by copying part of the
first, and the missing parts were invisible because nothing named them.

The cost was not theoretical. Streamed traffic was absent from `/usage` entirely, so a
customer who streamed every request was billed nothing.

Testing had the same shape of problem. The handler held concrete types
(`*provider.OpenAIClient`, `*cache.SemanticCache`, `*usage.Tracker`), so tests could not
substitute them. They built a handler with `cache` and `usage` set to nil, which is why
production code carried `if h.cache != nil` guards. Those branches existed to keep the
tests compiling. The cache-hit path had never run in a test.

## Decision

Put a chat request in its own module, `internal/completion`, with two entry points of
its own:

```go
func (c *Completion) Complete(ctx, Request) (Response, error)
func (c *Completion) Stream(ctx, Request) (*Stream, error)
```

It depends on three interfaces declared where they are used: `Provider`, `Cache`,
`Recorder`. `New` requires all three, so there is no way to build a half-configured one
and no reason for a nil check.

The HTTP handlers decode, call, and encode. Nothing else.

## Consequences

Streaming and non-streaming cannot drift apart, because there is only one description of
what a chat request does. Streams are now cached, metered, costed and logged.

Both handlers share one `decode`, so they cannot disagree about what a valid request
looks like.

The nil guards are gone.

Tests construct a real `Completion` over fake collaborators, so they exercise everything
except the network. Handler tests no longer hijack a TCP connection to simulate an
upstream failure.

The streaming path still has real complexity, but its ownership is now explicit. The
OpenAI adapter decodes SSE into provider-neutral content, usage and completion events.
`Stream` accumulates those events and applies cache and billing policy, while the HTTP
handler owns outbound SSE encoding. Each module is tested through its own interface.

## What we rejected

Keeping the logic in `internal/handler` as a non-HTTP type. Fewer files, but the module
would sit in the package named after the transport it is meant to be independent of.

A shared helper function called by both handlers. Removes the duplication without
removing the problem: nothing stops a third caller skipping the helper, and the helper's
inputs and outputs would still be HTTP-shaped.
