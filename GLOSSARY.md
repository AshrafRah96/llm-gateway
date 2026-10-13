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

## Access and usage

**Project budget**:
A project's token and spend ceilings for one UTC calendar day, covering chat and embedding work.

**Usage reservation**:
Capacity held within a project budget before paid work begins.
An unresolved provider outcome keeps that capacity held until reconciliation.

**Usage event**:
An identified record of one provider operation's measured or estimated tokens and charge.
Recording the same event again does not add another charge.

**Key rotation**:
Replacement of a project's access key, with a bounded period when both keys may remain valid.
Rotation preserves project identity and does not change the key-fingerprinting secret.

## Evaluation

**Evaluation run**:
A bounded measurement of semantic-cache classification against the committed labelled corpus.

**Real-provider smoke run**:
An opt-in check through a running gateway for provider, cache, streaming, cancellation, and usage behavior.

**Adversarial evaluation run**:
A bounded test of model responses to a fixed set of direct and retrieved prompt-injection attempts and benign controls.
Its results describe the tested cases and models, not immunity to prompt injection.

**Portfolio release**:
A versioned repository snapshot backed by reproducible tests, measured results, and passing validation.
