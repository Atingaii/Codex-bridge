# Orchestration Turn Timing And Multi-Run Navigation

## Goal

Show elapsed time for every orchestration turn, including turns added by a
follow-up prompt, and make the orchestration workspace navigate between
machines and runs strictly from explicit user actions without changing or
canceling any other run. Each turn also provides a shortcut to its first
visible text message. Runs can be removed from their machine-scoped history,
while active runs and their endpoint are visually marked in green. Removing an
active run first drives it through durable cancellation before deletion.

## Non-Goals

- Change orchestration execution, native CLI sessions, or run isolation.
- Open a high-frequency WebSocket for every run in the browser.
- Recalculate historical durations from token usage or provider billing data.
- Change Bridge-side cancellation semantics for runs that remain visible.

## Current State

Runs and events are already persisted and scoped by `runID`. Follow-up prompts
reuse the same run and native session. The browser streams only the selected
run. Previously, changing machines could implicitly restore a remembered run
and override the page the user had selected.

## Design

Bridge emits `turnEndData` with start, completion, and elapsed milliseconds for
each `turn.end`. The start timestamp is captured immediately before the CLI
turn begins; completion is captured when the turn returns, including errors and
cancellation. The browser groups this metadata by `turnId`, displays it in the
turn header, and ticks the active turn locally until a terminal event arrives.

The URL-selected run is authoritative. Creating a new run enters a draft state
without clearing other runs from the list. Selecting a run changes only the
detail stream and URL; all run state remains isolated by ID. Selecting a
machine leaves the previous run URL and shows that machine's run list without
automatically opening any remembered, recent, or running task. A run opens only
after an explicit run click, new-run creation, follow-up action, browser
back/forward navigation, or direct URL visit. Background refresh never changes
the selected page.

Each turn header exposes a shortcut when that turn contains a visible text
message. The shortcut expands the turn when necessary and scrolls smoothly to
its first text message. It only changes the viewport and collapsed UI state.

Visibility and online-recovery refreshes are data-only operations. They update
endpoint metadata, run summaries, and the selected run's events, but never
clear a run route or choose another endpoint. A `/orchestrate/runs/{runID}` URL
remains authoritative until the user explicitly chooses a machine, another
run, or New Run.

The machine-scoped run list exposes deletion for every state. The Hub rechecks
ownership and records a durable deletion intent. Terminal runs are deleted
immediately. Active runs transition from `queued` or `running` to `canceling`,
dispatch one cancellation request, and remain hidden while either the Bridge
acknowledges cancellation or the bounded timeout settles them as `canceled`.
Only a terminal row is physically deleted, cascading its events, usage, and
task-graph rows. Late start events and successor task claims cannot revive a
run after deletion begins. A Hub restart settles and removes any recorded
deletion intent. Active states receive a green task marker; any endpoint owning
at least one active run receives the same marker in the machine selector.

## Implementation Steps

1. Add protocol/store/frontend types for turn-end timing.
2. Capture and emit timing from the Bridge relay, and preserve it through Hub
   event persistence and public-share sanitization.
3. Render per-turn and live elapsed time in the orchestration timeline.
4. Make machine selection clear the detail route without auto-selecting a run.
5. Add a first-message shortcut to each eligible turn header.
6. Add focused navigation and timeline tests.
7. Add cancel-then-delete state transitions and active task/endpoint markers.
8. Make focus and visibility recovery refresh data without navigation.

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

**Does changing machines stop or select a task?** No. It only changes the
machine-scoped list and leaves the detail page. Running tasks continue in the
background.

**Does the first-message shortcut load or mutate events?** No. It only expands
an already loaded turn and moves the current viewport.

**Can an active run be deleted?** Yes. It disappears from the list immediately,
but the Hub first cancels and settles execution before cascading its rows.
