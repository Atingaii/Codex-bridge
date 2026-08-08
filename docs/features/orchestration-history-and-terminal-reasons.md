# Orchestration History And Terminal Reasons

## Goals

- Make "Load earlier events" deterministic for long orchestration timelines.
- Present the terminal conclusion after the complete timeline as a prominent,
  concise result, with outstanding obligations when relevant.
- Persist the concrete Bridge transport close reason when a reconnect grace
  period expires and an active orchestration must be failed.

## Non-Goals

- Do not alter running CLI processes, continuation semantics, or user task
  ownership.
- Do not add a database migration or a WebSocket frame type.
- Do not expose private command contents or credentials in a transport reason.

## Data And Protocol Impact

- `GET /api/orchestrations/{runID}/events` adds `hasMoreBefore`. Existing
  `events` consumers remain compatible.
- The Hub passes a bounded WebSocket read-close error into the existing
  orchestration failure event when reconnect grace expires.
- The existing event `error` and `runConclusion` fields continue to carry the
  terminal reason; no schema change is required.

## Implementation Steps

1. Fetch one extra event record in the Hub event handler and return an
   authoritative earlier-history indicator.
2. Use the indicator in `OrchestrationWorkspace` instead of inferring more
   history from page length.
3. Capture the reverse Bridge WebSocket read failure and include it in the
   delayed offline failure reason.
4. Render the conclusion after timeline groups, including a compact summary,
   status, commands, and unmet obligations.

## Exit Gates

- Paginated event requests correctly report whether an earlier page exists.
- A terminal transport failure says why the Bridge connection ended.
- The conclusion remains visible and follows the task history.
- Focused Hub tests, frontend build, complete Go tests, and doc lint pass.

## Reviewer Q&A

**Why return a boolean instead of inferring it in the browser?**

Only the Hub sees the authoritative ordered event set. Page length can be
ambiguous after filtering, reconnect event merges, or a changed page size.

**Why retain a raw WebSocket error at all?**

It distinguishes a real transport timeout, peer close, and process restart.
The value is bounded before it reaches persisted user-visible data.
