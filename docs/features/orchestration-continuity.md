# Orchestration Continuity

## Goal

Follow-up tasks in the orchestration UI must continue the selected run instead
of silently creating a new run. A user should be able to send an initial task,
wait for completion, type another task, and have the Bridge receive prior run
context in the same `runID`.

## Non-Goals

- Do not merge orchestration runs with chat sessions.
- Do not introduce Redis, a queue, or a second database.
- Do not change `internal/protocol.Envelope` unless a future runner requires it.
- Do not add permission prompts; those belong to the deferred app-server runner.

## Current State

- New orchestration runs are created by
  `internal/hub/orchestration.go:handleCreateOrchestration`.
- Follow-up prompts are handled by
  `internal/hub/orchestration.go:handleContinueOrchestration`.
- The continue handler loads prior events, compacts them with
  `internal/hub/orchestration.go:compactOrchestrationContext`, and sends the
  same `runID` to the Bridge with `Resume=true`.
- The frontend must preserve the selected run and call
  `/api/orchestrations/{runID}/prompts` for follow-ups.
- While a follow-up is active, the frontend must surface `turn.start`,
  `command.start`, and run status events instead of leaving the user message as
  the only visible item.
- The orchestration WebSocket is the live path. If it disconnects while the
  selected run is active, the frontend reconnects and reloads persisted events
  so progress that arrived during the gap is rendered.
- The final turn must leave a user-readable conclusion. If the CLI only emits a
  process note and command events, the Bridge appends a concise fallback summary
  from prior conclusions and successful verification commands.

## Design

There are two explicit states in the UI:

| UI state | Submit behavior |
| --- | --- |
| No active/selected run, after explicit New Run | `POST /api/orchestrations` |
| Existing selected run that is not running | `POST /api/orchestrations/{runID}/prompts` |

The selected orchestration run id is stored in browser local storage so a page
refresh or returning to `/orchestrate` restores the same run when it still
exists. The New Run button clears that selection intentionally.

`turn.start` is rendered as a lightweight status item. User message event status
is not shown as an authoritative processing state because the persisted
`queued` marker only records submission, not whether later turns are already
running.

Event cards display their precise event time. The orchestration sidebar shows
the run date beside status so history can be scanned without repeating full
timestamps inside the run list.

## Implementation Steps

1. Keep Hub continue semantics in `handleContinueOrchestration`.
2. Keep Bridge start payloads carrying `RunID`, `Context`, `Resume`, and
   `PromptSeq`.
3. Make the frontend restore the last selected run from local storage.
4. Make the frontend update mode/cwd/max-turn controls from the selected run.
5. Preserve the explicit New Run action as the only way to clear run selection.
6. Show turn/run progress events and reconnect orchestration WebSockets while a
   selected run remains active.
7. Add a Bridge-side final-summary fallback when the final turn does not provide
   a clear conclusion.
8. Render event times in the main timeline and run dates in the sidebar.

## Exit Gates

- Continuing a completed run returns HTTP 200 and the same run id.
- The Bridge receives an `orchestration_start` payload with `Resume=true`.
- The compacted context contains prior user messages and recent agent output.
- Refreshing `/orchestrate` reselects the last selected run when it exists.
- Explicit New Run still creates a new run.
- If a live WebSocket drops during an active run, the page reconnects and
  reloads events.
- A newly started follow-up shows the active turn before the CLI returns final
  prose.
- A final turn that only emits process text still produces a clear final
  conclusion in the timeline.
- Timeline events include specific times, and sidebar runs include dates.

## Reviewer Q&A

**Q1 (trade-off): Why reuse the same orchestration run instead of creating a new
run per prompt?**

A: The run is the user-visible work container and the only event stream the UI
can replay. Reusing it keeps context, status, and history in one place. A new
run is still available through the explicit New Run action.

**Q2 (trade-off): Why compact context instead of persisting a native Codex thread
for orchestration?**

A: Orchestration alternates CLIs and roles, so a single Codex thread is not the
whole conversation. Compacting events gives both Codex and Claude a shared
handoff format without changing Hub/Bridge protocol.

**Q3 (boundary): What happens if the selected run is still active?**

A: The frontend disables submit and the Hub rejects continue with `RUN_ACTIVE`.
Only one active orchestration prompt per run is allowed.

**Q4 (scenario): A user reports that a follow-up opened a new context. Where do
you look first?**

A: Check whether the UI cleared `codexBridge.activeOrchestrationRunId`, whether
`startRun` used `/api/orchestrations` instead of the run-specific prompts
endpoint, and whether Hub logged `Resume=true` in the Bridge start payload.
