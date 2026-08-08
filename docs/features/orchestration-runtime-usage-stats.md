# Orchestration Runtime And Usage Statistics

> **DEPRECATED - character-count token fallback removed**
>
> Current design: [Gateway Native Usage Accounting](gateway-native-usage-accounting.md). Historical details below are retained for context only.

## Goals

- Allow users to explicitly select a one-turn orchestration for simple tasks.
- Persist per-turn CLI usage metadata and expose aggregate runtime, token, and
  estimated cost statistics for the owning user.
- Show a dedicated, readable statistics page without changing the existing
  orchestration workflow.
- Make the terminal Agent conclusion visually prominent after a run completes.

## Non-Goals

- No billing, quota enforcement, or provider-side invoice reconciliation.
- No fabricated exact usage when a CLI does not report token counts; estimated
  values are labeled as estimates.
- No cross-user statistics or administrative aggregation.

## Data And Protocol Impact

CLI workers emit a persisted `turn.usage` orchestration event. Its `data` object
contains the CLI, model, input/output/cache token counts, native/provenance
flags, and cost availability. Missing provider counts are explicitly marked
unavailable; the gateway no longer fabricates token counts from characters.
The run record's existing `created_at` and `finished_at` fields are used for
wall-clock runtime; running runs use the current time for a live duration.

## Implementation Steps

1. Emit a compact `turn.usage` event for every final relay turn. Native provider
   accounting can be added without changing the event shape; the current
   implementation uses a labelled text-length estimate for CLI versions that
   do not expose stable usage fields.
2. Add an owner-scoped stats endpoint that aggregates events and runtime and
   returns a stable JSON shape for the UI.
3. Permit `maxTurns=1` while retaining the current default and upper bound.
4. Add a `/orchestrate/stats` page and navigation entry.
5. Highlight the final `runConclusion` block when the run reaches a terminal
   state.

## Exit Gates

- Go unit tests cover usage normalization, cost calculation, stats ownership,
  and one-turn validation.
- Frontend builds successfully and renders the stats route.
- Existing orchestration and integration tests remain green.

## Reviewer Q&A

**Why events instead of new tables?** Existing event replay already survives
 reconnects and keeps the accounting close to the CLI evidence.

**Are costs authoritative?** No. They are transparent estimates based on the
 configured model family and are intended for operational visibility.
