# ADR-0009: Separate gateway policy from untrusted input

Status: Accepted
Date: 2026-10-13

## Decision

Keep general-purpose chat, including legitimate security discussion.
Replace the combined RAG prompt with structured question/reference input.
At the provider boundary, construct fixed system policy, optional JSON-encoded reference
data in a user message, and the original question in a separate user message.
Reference identifiers/content never enter trusted policy. Input cannot choose API roles.
JSON, SSE, and fallback share `ProviderInput.Messages()`.

## Tradeoff

Use structural defenses without a paid classifier or phrase blocklist.
Application message boundaries are enforced; model resistance is not guaranteed.
Project isolation and the absence of tools/credential access constrain consequences.

Serving cache scope includes `prompt-policy-v1`. Bump `promptPolicyVersion` when policy
or framing changes. Earlier entries expire normally without replay.
Abandoned-stream estimates include policy, encoded references, and question; estimates remain approximate.

General chat, ordinary retrieval-error fallback, and immediate SSE delivery remain available.
No whole-answer screening is claimed. Payload tests cover role spoofing, forged delimiters,
benign security questions, JSON/SSE/fallback, cache isolation, and shared settlement.
Real-model attack-resistance claims require separate adversarial evaluation.

## Sources

Reviewed 2026-10-13:

- [OWASP Prompt Injection Prevention](https://cheatsheetseries.owasp.org/cheatsheets/LLM_Prompt_Injection_Prevention_Cheat_Sheet.html)
- [OWASP LLM01](https://genai.owasp.org/llmrisk/llm01-prompt-injection/)
- [OWASP RAG Security](https://cheatsheetseries.owasp.org/cheatsheets/RAG_Security_Cheat_Sheet.html)
- [OpenAI agent safety](https://developers.openai.com/api/docs/guides/agent-builder-safety)
- [OpenAI Chat Completions roles](https://platform.openai.com/docs/api-reference/chat/create)
