# LLM Gateway

Terms for gateway trust, measurements, and release evidence.

## Prompt safety

**Prompt injection**:
An attempt through a user question or retrieved material to override gateway policy.
Quoting or discussing an attack is not itself an attack.

**Gateway policy**:
Governing instructions set by the gateway owner.
User questions and reference material have no authority to replace them.

**Reference material**:
Project content used to answer a question, without authority to change policy or authorize actions.

## Evaluation

**Evaluation run**:
A bounded measurement of semantic-cache classification against the committed labelled corpus.

**Real-provider smoke run**:
An opt-in check through a running gateway for provider, cache, streaming, cancellation, and usage behavior.

**Portfolio release**:
A versioned repository snapshot backed by reproducible tests, measured results, and passing validation.
