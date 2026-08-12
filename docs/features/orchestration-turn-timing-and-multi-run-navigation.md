# Orchestration Turn Timing And Multi-Run Navigation

## Goal

Show elapsed time for every orchestration turn, including turns added by a
follow-up prompt, and make the orchestration workspace navigate between runs
without changing or canceling any other run.

## Non-Goals

- Change orchestration execution, native CLI sessions, or run isolation.
- Open a high-frequency WebSocket for every run in the browser.
- Recalculate historical durations from token usage or provider billing data.

## Current State

Runs and events are already persisted and scoped by `runID`. Follow-up prompts
reuse the same run and native session. The browser currently streams only the
selected run, but its selection state can fall back to remembered agent runs.

## Design

Bridge emits `turnEndData` with start, completion, and elapsed milliseconds for
each `turn.end`. The start timestamp is captured immediately before the CLI
turn begins; completion is captured when the turn returns, including errors and
cancellation. The browser groups this metadata by `turnId`, displays it in the
turn header, and ticks the active turn locally until a terminal event arrives.

The URL-selected run is authoritative. Creating a new run enters a draft state
without clearing other runs from the list. Selecting a run changes only the
detail stream and URL; all run state remains isolated by ID. Background list
refresh remains lightweight and does not replace an explicit URL selection.

## Implementation Steps

1. Add protocol/store/frontend types for turn-end timing.
2. Capture and emit timing from the Bridge relay, and preserve it through Hub
   event persistence and public-share sanitization.
3. Render per-turn and live elapsed time in the orchestration timeline.
4. Tighten workspace navigation state and add focused tests.

## Exit Gates

- `go test ./...`
- `npm test` and `npm run build`
- `make doc-lint`
- Static build and deployment smoke check without interrupting active runs.

## Reviewer Q&A

**Does follow-up create another task?** No. It keeps the same run ID and appends
new timed turns to that run.

**Can several runs execute concurrently?** Yes. Each run has independent Hub
events and Bridge execution handles; the UI only limits the selected live
stream for browser load.
