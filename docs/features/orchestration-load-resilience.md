# Orchestration load resilience

## Goals

- Keep the orchestration workspace usable when a detail request is interrupted
  or returns a malformed success payload.
- Preserve the last valid run state while the existing WebSocket reconnect and
  polling paths recover automatically.
- Replace an otherwise blank application with a small recovery view if an
  unrelated render error escapes page-level handling.

## Non-goals

- Do not change orchestration lifecycle, continuity, protocol frames, or Bridge
  execution behavior.
- Do not retry mutations or create a new run/session during recovery.
- Do not treat malformed payloads as valid empty runs.

## Data and protocol impact

There is no API or protocol shape change. The browser validates the existing
`GET /api/orchestrations/{runID}` response before updating React state. Invalid
run objects are ignored by state merging and surfaced through the existing
page error UI. The active-run polling and WebSocket reconnect paths continue to
retry the same run ID.

## Implementation

1. Validate orchestration run objects at the API-to-state boundary.
2. Make the run upsert helper discard invalid current and incoming entries.
3. Retry an interrupted initial load for the same run every three seconds and
   immediately after the browser returns online or becomes visible.
4. Add an application error boundary that offers a reload only for unexpected
   render failures outside the normal request recovery path.
5. Add a frontend regression check and rebuild the embedded static UI.

## Exit gates

- A missing `run` field cannot reach `upsertOrchestrationRun` as valid state.
- Run merging tolerates missing entries without reading `undefined.id`.
- Initial transport failure recovers without creating a new run or requiring a
  manual page reload.
- The application root has a visible last-resort recovery view.
- Frontend checks, production build, Go tests, doc lint, and diff checks pass.

## Reviewer Q&A

### Does a transient network failure stop the task?

No. The task remains owned by the Hub and Bridge. The browser retains its last
valid state, and the existing reconnect/polling paths request updates for the
same run ID after connectivity returns.

### Why keep a reload button in the error boundary?

The boundary is only the final fallback for an unexpected render exception.
Known transport and payload failures stay inside the orchestration page and
recover automatically without reaching it.
