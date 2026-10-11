# LLM Gateway

This glossary defines the terms used to describe the gateway's measured behavior and
portfolio release evidence.

## Evaluation

**Evaluation run**:
A bounded measurement of semantic-cache classification against the committed labelled
corpus.

**Real-provider smoke run**:
An opt-in end-to-end check that sends a small set of requests through a running gateway
to verify provider, cache, streaming, cancellation, and usage behavior.

**Portfolio release**:
A versioned repository snapshot whose documented claims are backed by reproducible tests,
measured evaluation results, and a passing validation pipeline.
