# Architecture assets

README embeds `architecture.svg`. The refreshed HTML and static SVG show the same 12 nodes and 13 relationships.
The SVG is a static companion extracted from finalized HTML geometry, using the existing Archify standalone
theme/font stylesheet. It contains no scripts or external images. This refresh did not use the viewer-menu exporter.
The browser connector's local-file restriction was not bypassed. Hosted GitHub rendering remains unchecked.

[Interactive HTML](architecture.html) · [Candidate](architecture.json)
· [Validation receipt](architecture.finalize-summary.json) · [SVG receipt](export-receipt.json)
· [Frozen source bundle](source.bundle) · [Source checksums](source-manifest.json)
· [Visual review](visual-review.json).

## Recheck

From repository root, with Archify installed:

```bash
export ARCHIFY=/path/to/archify/bin/archify.mjs
git clone docs/diagrams/source.bundle /tmp/llm-gateway-diagram-source
git -C /tmp/llm-gateway-diagram-source remote set-url origin https://github.com/AshrafRah96/llm-gateway.git
node "$ARCHIFY" finalize architecture \
  docs/diagrams/architecture.json docs/diagrams/architecture.html \
  --repo-root /tmp/llm-gateway-diagram-source \
  --out-dir /tmp/llm-gateway-diagram-recheck --quality showcase --json
```

Choose fresh temporary paths. This verifies the frozen local snapshot, not subsequent edits or a published GitHub revision.

## Update

Freeze current source, including untracked files. Update candidate facts, citations and revision.
Run `finalize`; inspect supported `visual-check` captures when visual review is needed.
Keep previous dated deliveries under ignored `.archify/` and current assets/receipts here.
The viewer offers **Export → SVG · Auto** when browser access permits it. If a static companion is
built outside that exporter, record its derivation honestly and inspect it separately.
