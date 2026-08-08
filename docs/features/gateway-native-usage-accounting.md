# Gateway Native Usage Accounting

## Goal

Record the provider's per-turn token usage at the Bridge gateway and calculate
costs only from native usage or an explicitly configured price catalog.

## Non-Goals

- No provider invoice or subscription reconciliation.
- No character-count token fabrication presented as actual usage.
- No change to orchestration controls or user-facing prompt flow.

## Current State

Orchestration statistics currently estimate tokens from prompt and response
characters even when a CLI returns native usage. This can double-count context,
miss cache fields, and apply the wrong model price.

## Design

Codex app-server `thread/tokenUsage/updated` notifications and Claude
stream-json `result` usage fields are captured for each Bridge turn. The
gateway normalizes snake_case and camelCase provider fields into the existing
`turn.usage` event. A turn is marked `native` only when the provider supplied
the counts. Cost uses provider-reported cost when present; otherwise it uses a
known model price catalog and is labeled catalog-calculated. If neither is
available, cost is null/zero and the UI reports that pricing is unavailable.

Retries and continuations are separate provider calls and are each recorded;
the aggregate is their sum, with no character-based fallback.

## Implementation Steps

1. Capture native usage in Codex and Claude scanners.
2. Thread normalized usage through orchestration turn records.
3. Add source and pricing provenance to usage events and stats.
4. Update the statistics UI, docs, and focused tests.

## Exit Gates

- Native Codex and Claude usage is persisted per provider call.
- Missing usage never creates fabricated token counts or cost.
- Aggregate stats preserve retry/continuation calls without double counting.
- Focused and full Go/frontend checks pass.

## Reviewer Q&A

**Can the gateway guarantee a provider invoice?** No. It can guarantee that
reported provider usage is preserved exactly; catalog-derived prices remain
operational calculations and are labeled as such.
