# Orchestration usage overview

## Goals

- Present usage with four explicit measures: input, output, cache, and total tokens.
- Show a compact per-round trend for every orchestration run.
- Switch run usage between the whole conversation and each user task while
  keeping configured internal rounds inside their parent task.
- Provide a user-scoped overview grouped by machine, orchestration task, and CLI/model.
- Keep the all-conversation overview separate from a single run, with selectable
  time windows and token/cost trend dimensions.
- Reuse the existing Hub SQLite ledger and Bridge usage sync; add no service, port, or user command.

## Non-goals

- This is not an invoice or provider billing replacement.
- Do not change native CLI scanning, pricing catalog rules, orchestration continuity, or run lifecycle behavior.
- Do not expose internal proof notes, relay prompts, or transport details in the product view.

## Data and API impact

`GET /api/orchestrations/{runID}/stats` keeps all existing fields and adds
`cacheTokens`, `totalTokens`, `rounds`, and `tasks`. Each round contains input,
output, cache, total tokens, calls, and cost. Each task repeats the same stats
shape plus `taskNumber`, `promptSeq`, and `prompt`. Task boundaries come from
durable graph payloads: graph generations with the same `promptSeq` are internal
rounds, while a follow-up prompt starts the next task. The top-level fields
remain the exact all-run aggregate for compatibility.

`GET /api/usage/overview` returns only the authenticated user's runs, grouped by
machine and task, with totals, CLI/model breakdowns, and a daily trend. The
optional `days` query accepts `7`, `30`, `90`, or `0` (all history). The optional
`timezoneOffset` query uses the browser's `Date.getTimezoneOffset()` convention
so day boundaries match the user's local calendar. Range filtering is applied
to persisted usage-event timestamps, not just run creation timestamps.

## Implementation

1. Aggregate native ledger events in Hub, deriving round boundaries from persisted orchestration round-advance events.
2. Add a user-scoped overview query path that reuses the run stats builder and joins each run to its Agent metadata.
3. Add a responsive run chart and an independent overview page using the
   existing React, Tailwind, lucide, and Recharts dependencies. Compact chart
   axes use `K`, `M`, `B`, and `T`; tooltips retain exact values.
4. Let the overview switch between total, input, output, cache, and known-cost
   daily trends, and group its task list into explicit machine sections.
5. Keep legacy turn-snapshot fallback behavior and old API fields intact.
6. Return all task projections in the initial stats response so task switching
   is browser-local and adds no polling, worker, or Redis load.

## Exit gates

- Go tests cover total-token arithmetic, round aggregation, time-range filtering,
  task boundary aggregation, daily trends, and user-scoped overview behavior.
- `npm run build` refreshes `internal/web/static/`.
- `go test ./...`, `git diff --check`, and a Hub health check pass.

## Reviewer Q&A

### Why combine cache read and cache write?

The requested product metric is total cache consumption. The existing read/write fields remain available for detailed accounting, while `cacheTokens` is their sum.

### Are historical conversations included?

Yes. The overview scans the existing persisted runs and uses the native ledger when available, falling back to legacy turn snapshots for older runs.

### Does this require another Bridge update?

No for the overview and chart changes themselves. They only change the Hub API
and embedded UI and reuse the already-shipped local usage ledger. A Bridge only
needs updating if it predates local usage-ledger support.

### Why not query each task separately?

The Hub already loads the run ledger once. Projecting its persisted events into
task time windows during that request avoids extra browser round trips and keeps
task switching instant without increasing steady-state server load.

Legacy usage records without a parseable timestamp are assigned to task 1 so
the sum of task totals remains equal to the compatible all-run total.
