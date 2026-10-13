# ADR-0002: Cache on meaning, not exact text

Status: Superseded in part by ADR-0007; extended by ADR-0009
Date: 2026-07-26

## Decision

Embed prompts with `text-embedding-3-small`. Search Redis vectors and reuse the nearest
answer when cosine similarity is at least 0.95. See `internal/cache/semantic.go`.
Exact hashes miss rewordings such as "What is the capital of France?" and "France's capital city?".

## Tradeoff

Each cache attempt makes a paid embedding call. It retains that vector for storage,
so a cacheable miss embeds once for the cache. RAG embeds separately.
A hit avoids chat generation; it is not free. Mostly unique traffic reduces cache value.
Redis Stack/Search is required; plain Redis lacks the needed vector commands.

The threshold has not been measured on production traffic. Lower thresholds risk wrong
answers; higher thresholds lose rewording hits. A 40-case evaluator exists; no measured report is committed yet.

## Alternatives and current scope

Exact-match caching was rejected because it misses semantic reuse.
The original shared cache was replaced by scoped caching in ADR-0007.
Current serving scope includes authenticated project, primary routed model, schema,
corpus revision, and prompt-policy version. Confidentiality takes priority over cross-project hit rate.
