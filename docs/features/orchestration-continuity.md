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
- Do not redefine permission prompts; review-required approval behavior is
  covered by
  `docs/features/orchestration-deep-collaboration-and-approval.md`.

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
- Follow-up prompts stay on the run's original `agentId`; switching CLI
  endpoint requires an explicit new run so the compacted context is not handed
  to a different machine unexpectedly.
- While a follow-up is active, the frontend must surface `turn.start`,
  `command.start`, and run status events instead of leaving the user message as
  the only visible item.
- The orchestration WebSocket is the live path. If it disconnects while the
  selected run is active, the frontend reconnects and reloads persisted events
  so progress that arrived during the gap is rendered.
- The final turn should leave a user-readable conclusion, and successful
  `turn.end` / `run.end` events carry the CLI's visible content. If the CLI
  returns no text, Bridge reports that absence rather than adding an independent
  proof assessment.
- Command events include timing metadata so long-running checks such as
  Isabelle, Coq, and Lean builds show when the command started and how long it
  has been running or took to finish.
- Isabelle-looking proof runs receive only a prompt-level timeout boundary:
  when a CLI chooses to run a full Isabelle build or long Isabelle compilation,
  it should choose an explicit timeout and report the command, elapsed time,
  timeout value, and latest output/log if the timeout is exceeded. Bridge does
  not require a controlled background build template and does not reject the
  CLI's foreground build choice after the fact.
- If a Bridge disconnects or restarts while an orchestration run is active, Hub
  marks that run failed and appends a `run.error` event instead of leaving the
  browser stuck in `running`.
- Bridge-launched Codex and Claude CLI turns run in managed process groups.
  Canceling a run cancels the direct CLI process and its child process tree so
  Hub can receive `run.cancelled` instead of leaving the page in `canceling`.
- Direct Codex JSONL orchestration waits for the CLI process and scanner to
  finish so the browser-visible timeline reflects the actual CLI output and
  terminal status.
- The frontend must render persisted `turn.delta` and `command.*` events as
  visible timeline entries. Detailed content that reaches `/events` must not be
  hidden behind only `turn.start` status cards.
- Turn-to-turn strategy now uses the pass-through relay documented in
  [orchestration-pass-through-cli.md](orchestration-pass-through-cli.md);
  the next CLI receives the previous visible result plus useful command context
  without Bridge adding a proof strategy.
- Uploaded orchestration file contents are sent to the Bridge with the current
  prompt, while `user.message` events persist only file metadata in
  `event.data.files` so the timeline can show what was attached without
  storing duplicate file bodies.

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

Event cards display their precise clock time down to seconds. The orchestration
sidebar shows the run calendar date beside status so history can be scanned
without repeating full timestamps inside the run list.

Assistant deltas are merged by `runId`, `turnId`, role, and CLI before timeline
rendering. This keeps token-level app-server deltas from becoming one card per
word while still preserving the final streamed text. Command events are rendered
in the timeline with expandable command details and runtime labels so users can
confirm which command/output payload arrived from Hub and whether a long proof
build is still running.
When the user scrolls away from the latest orchestration event, the timeline
shows a floating jump-to-bottom control; if the user is already at the bottom,
new events continue to follow automatically.

User message cards render attached file metadata from `event.data.files`.
The right-side file panel also shows `OrchestrationRun.files` for the selected
run when no draft files are pending, so historical uploads remain discoverable
after the draft upload list is cleared.
Follow-up prompts preserve previously uploaded run file metadata even when no
new files are attached. New follow-up uploads are merged into the run-level
metadata while each `user.message` event still records only that prompt's
attachments.
Successful turn-end events carry the final turn content so the UI can show the
CLI answer after command events instead of visually ending on the last
`command.end` card. Bridge does not append proof-specific acceptance summaries
or final verifier conclusions; if a CLI response is sparse, the browser still
shows the recorded command events and relay terminal message.
The browser event stream is only kept open for active runs. Completed, failed,
or canceled runs are read from persisted Hub events and show the stream as idle,
so the stream indicator cannot be confused with the selected worker's online
state.

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
9. Render detailed `turn.delta` and `command.*` events directly in the timeline
   instead of relying on `turn.start` cards or nested command-only sections.
   Merge same-turn `turn.delta` events before display and context compaction.
10. Preserve a bottom-following timeline by default, and show a jump-to-bottom
    button when the user has scrolled up.
11. Persist uploaded file metadata on `user.message` orchestration events and
    render that metadata in the timeline and selected-run side panel.
12. Include command timing metadata in Bridge-emitted command events and show
    active runtime or completed duration in the frontend timeline.
13. Mark active orchestration runs failed with a visible `run.error` event when
    their Bridge connection disconnects or restarts.
14. Preserve run-level file metadata across follow-up prompts and merge new
    follow-up uploads without duplicating existing file entries.
15. Emit successful `turn.end` content and render contentful turn-end events as
    final answer cards after command events.
16. Preserve CLI-provided turn-end and run-end content without adding hidden
    proof assessment, verifier, or remediation conclusions.
17. Only open `/ws/orchestrations` for active runs; terminal runs should use
    persisted events and show an idle event stream.
18. Manage CLI subprocess groups and detect idle direct-Codex JSONL turns after
    command completion so cancellation and stalled final responses become
    terminal, visible events.
19. For Isabelle-looking tasks, keep the prompt-level timeout boundary visible
    and leave execution strategy to the CLI.

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
- A final turn that only emits process text still produces a terminal
  browser-visible run status and preserves recorded command events.
- Timeline events include specific times, and sidebar runs include calendar
  dates.
- Persisted assistant deltas and command outputs returned from `/events` are
  visible in the timeline, with token-sized deltas merged into one turn entry.
- Running command cards show elapsed time, and completed command cards show
  total duration.
- Scrolling up in the orchestration timeline exposes a jump-to-bottom button,
  and clicking it returns to the latest event.
- Uploaded files appear beside the user message that submitted them, and prior
  selected-run files remain visible in the side panel after send.
- Active orchestration runs do not remain permanently `running` after their
  Bridge disconnects or restarts.
- Canceling an active orchestration kills the CLI process tree and eventually
  persists `run.cancelled`.
- A direct Codex turn that has completed its command events but stops emitting
  JSONL produces a visible turn error and terminal run status instead of
  remaining `running`.
- An Isabelle-looking prompt includes the explicit timeout stop/report boundary
  and later turns continue from the previous visible CLI result.
- Continuing a run without new uploads keeps the original uploaded files visible
  in the selected-run side panel.
- A final response carried on `turn.end.content` is visible in the timeline
  after command cards.
- A turn that ends with only command output still leaves those command events
  visible and the run reaches a terminal browser-visible state.
- Selecting a completed run does not show the browser event stream as connected.

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
