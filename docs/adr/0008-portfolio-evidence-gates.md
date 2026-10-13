# ADR-0008: Portfolio evidence gates

Status: Accepted
Date: 2026-10-11

Semantic-cache release targets: precision ≥95%, recall ≥80% on the committed corpus.
Reports identify corpus/checksum, threshold, embedding model, source commit/dirty state,
runtime, Redis/Search setup, and run ID.

The opt-in real-provider smoke command checks JSON, repeated cache hit, completed SSE,
abandoned SSE, and usage settlement. Provider runs incur cost and remain outside normal CI.

Small-corpus and smoke results do not represent production traffic.
The commands exist; no measured evaluation or smoke report is committed yet.
