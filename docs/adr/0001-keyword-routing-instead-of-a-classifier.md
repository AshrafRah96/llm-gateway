# ADR-0001: Route with keywords instead of asking a model

Status: Accepted
Date: 2026-07-26

## Context

The gateway picks between a cheap model and an expensive one. Something has to decide
which prompt gets which.

The obvious approach is to ask a model. Send the prompt to a small classifier first, let
it judge the difficulty, then route accordingly. It reads well on a diagram.

It also means every request now makes two API calls. The classifier adds its own latency
before the real answer starts, and it costs money on every request including the ones
that were always going to be cheap. For a gateway whose whole point is reducing spend,
paying per request to decide how much to spend is a bad trade.

## Decision

Route on prompt length and a keyword list. Prompts over 500 characters go to the
expensive model, as do prompts containing words like "analyze", "compare", "debug" or
"step by step". Everything else goes to the cheap one.

See `internal/router/router.go`.

## Consequences

Routing costs nothing and adds no latency. It is also crude, and it will get things
wrong in both directions. A short prompt can be hard ("prove Fermat's last theorem") and
a long one can be trivial (a wall of text with "summarise this" on the end).

Misroutes are easy to spot because every request logs the selected model and prompt
length. If the logs show a pattern, the rule lives in one file with a focused test.

## What we rejected

A classifier model. Revisit this if measured routing mistakes cost more than an extra
classifier call on every request.

Letting callers pick the model themselves. Callers will pick the expensive one, which
defeats the purpose.
