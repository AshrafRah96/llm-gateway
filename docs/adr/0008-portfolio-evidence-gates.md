# ADR-0008: Portfolio evidence gates

Status: Accepted  
Date: 2026-10-11

The portfolio release requires the semantic-cache evaluator to achieve at least 95%
precision and 80% recall on the committed corpus. Reports must identify the corpus,
threshold, embedding model, source commit, runtime, Redis setup, and run ID. A separate
opt-in real-provider smoke run covers a JSON completion, a repeated cache hit, a completed
stream, an abandoned stream, and usage settlement. These gates keep the portfolio's
claims measurable without pretending that the small corpus or smoke run represents
production traffic.
