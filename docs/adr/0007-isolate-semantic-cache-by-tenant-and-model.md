# ADR-0007: Isolate semantic cache entries by tenant and model

Status: Accepted
Date: 2026-07-27

## Decision

Route before lookup. Apply tenant, primary routed model, and schema filters before vector ranking.
Use `cache:v2:` keys and `prompt_cache_v2`; never read old unscoped entries.
Store the same metadata and expire entries after `CACHE_TTL`, default 24h.
Similarity is not authorization.

## Current scope

The original decision fingerprinted caller keys. Project authentication now scopes the
cache to SHA-256 of authenticated `ProjectID`. Keys in one project share cache and usage;
key quotas still use HMAC `KeyID`.
RAG adds corpus revision. ADR-0009 adds `prompt-policy-v1`.
Fallback answers retain the original primary-route namespace.

Raw keys and prompts never appear in semantic-cache key names. HMAC auth fingerprints
replace plaintext caller-key records; key rotation/expiry remain open work.
Model, schema, corpus, or policy scope changes cause misses. TTL bounds reuse and storage lifetime.

## Tradeoff and rejected options

Project isolation lowers hit rate; confidentiality wins.
A global cache for "public" prompts requires reliable public/private classification and was rejected.
Old unscoped entries have no safe tenant assignment, so migration was rejected.
