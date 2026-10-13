# Architecture assets

`architecture.svg` is the canonical Archify **SVG · Auto** export. README and architecture docs embed it.
It has no scripts or external images and adapts to host color preference.

[Interactive HTML](architecture.html)
· [Candidate](architecture.json)
· [Validation receipt](architecture.finalize-summary.json)
· [Export receipt](export-receipt.json)
· [Frozen source bundle](source.bundle)
· [Source checksums](source-manifest.json)

## Recheck this snapshot

From repository root, with Archify installed:

```bash
export ARCHIFY=/path/to/archify/bin/archify.mjs
git clone docs/diagrams/source.bundle /tmp/llm-gateway-diagram-source
node "$ARCHIFY" finalize architecture \
  docs/diagrams/architecture.json \
  docs/diagrams/architecture.html \
  --repo-root /tmp/llm-gateway-diagram-source \
  --out-dir /tmp/llm-gateway-diagram-recheck \
  --quality showcase --json
```

Use new temporary paths if those directories exist. This verifies the frozen source,
not later working-tree edits. Snapshot identity is local; it is not a published GitHub revision.

## Update after code changes

Freeze the new source, update candidate facts/citations and revision, then run Archify `finalize`.
Open the passing HTML. Choose **Export**, then **SVG · Auto**; save to `docs/diagrams/architecture.svg`.
Update README/viewer links and the export receipt together. Keep prior dated diagrams locally under ignored `.archify/`. Publish current viewer/source/evidence under `docs/diagrams/`.
The source snapshot must include new untracked source files as well as tracked edits.
