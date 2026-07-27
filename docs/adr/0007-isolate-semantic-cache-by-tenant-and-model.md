# ADR-0007: Isolate semantic cache entries by tenant and model

Status: Accepted
Date: 2026-07-27

## Context

ADR-0002 originally chose a shared semantic cache to maximise hit rate. That permits a
prompt from one API key to receive another key's stored response. A similarity score is
not an authorization check, and prompts may contain private data.

A model or cache-format change can also make an otherwise similar answer stale.

## Decision

Route before lookup. Filter every vector query by:

- a SHA-256 fingerprint of the caller's API key;
- routed model ID;
- cache schema version.

Store the same metadata on every entry and expire it after `CACHE_TTL`, which defaults
to 24 hours. Use a new `cache:v2:` prefix and index, and never read old unscoped keys.

## Consequences

Tenants cannot benefit from one another's cached answers, so the hit rate will be lower.
That cost is accepted because confidentiality is not a performance tradeoff.

Raw API keys and prompts do not appear in semantic-cache key names. The fingerprint is
stable, so weak client keys could still be guessed offline; stronger authentication-key
storage is separate roadmap work.

Model and schema changes cause misses instead of returning an answer from a different
behavioral contract. TTL bounds staleness and storage growth.

## What we rejected

Keeping a global cache for public-looking prompts. Correctly classifying a prompt as
public would itself require a reliable security policy.

Migrating old entries. They have no tenant metadata, so there is no safe tenant to
assign them to.
