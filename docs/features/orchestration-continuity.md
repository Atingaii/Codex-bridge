# Orchestration Continuity

## Goal

Follow-up tasks in the orchestration UI must continue the selected run instead
of silently creating a new run. A user should be able to send an initial task,
wait for completion, type another task, and have the Bridge receive prior run
context in the same `runID`.

## Non-Goals

- Do not merge orchestration runs with chat sessions.
- Do not introduce Redis, a queue, or a second database.
- Do not add new `internal/protocol.Envelope` frame kinds unless a future
  runner requires it.
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
- Follow-up prompts also preserve the run's persisted `firstCli` value unless a
  request explicitly changes it, so the visible relay schedule after refresh
  matches the originally selected first-turn CLI.
- Follow-up prompts preserve the run's persisted orchestration `profile`.
  `default` is the generic relay profile; `formal-proof` is explicit opt-in for
  proof-assistant guidance. The default profile does not silently enable formal
  proof instructions based on keywords in the user prompt.
- Follow-up prompts restore native CLI continuity where possible. Hub persists
  the Codex thread id reported by Bridge because it is non-deterministic, stores
  whether Claude reached a successful `turn.end`, and records the absolute run
  cwd reported by `run.start`. Resumed `orchestration_start` payloads send those
  values back so Bridge can reuse live run-scoped native sessions while it is
  running, resume the saved Codex app-server thread after restart where
  possible, choose Claude `--resume` only for an actually started session, and
  keep all later turns in the locked absolute working directory.
- Native CLI resume is best-effort. Bridge always keeps Hub's compacted
  context in the prompt as the required continuity fallback, exposes the chosen
  resume path through `event.data.resumeMode`, retries Codex fresh when resume
  clearly misses the local thread data, and retries Claude once with
  `--session-id` if `--resume` reports a missing session.
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
- `command.*` events carry typed `CommandData`; frontend command cards use that
  typed payload as the source of truth for command text, output, status,
  exit-code, and timing. Bridge-originated notes use `source=bridge` and
  optional `severity`, so they do not count as CLI command failures.
- Bridge emits one structured `run.conclusion` event before every terminal run
  event. The frontend renders that event as the final conclusion card instead
  of guessing from keywords in `run.end.content`.
- Formal-proof profile runs can receive prompt-level proof-assistant guidance.
  Generic default-profile runs do not get that guidance based only on prompt
  wording.
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
  without Bridge adding a hidden proof verdict. Formal-proof-looking prompts
  receive lightweight, browser-visible proof workflow reminders up front so the
  CLI records target obligations, build/scan/audit evidence, and blockers in
  its normal result.
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

`turn.start` is rendered as a lightweight status item and carries diagnostic
metadata such as `resumeMode` in typed `TurnStartData`; it must not echo the
full internal relay prompt as visible content. User message event status is not
shown as an authoritative processing state because the persisted `queued`
marker only records submission, not whether later turns are already running.

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
Long orchestration transcripts are grouped by `turnId`, role, and CLI in
`frontend/src/app/lib/utils.ts:orchestrationTimelineGroups` and rendered through
`frontend/src/app/components/OrchestrationComponents.tsx:OrchestrationTimelineGroupItem`.
Each turn group can be collapsed from the browser timeline and from public
orchestration shares. Historical completed turns may default to collapsed in
long transcripts, while the latest turn, active commands, failed turns, and
terminal turns missing a `turn.end` event stay expanded. If a terminal run has
visible turn output but no `turn.end`, the group header shows an explicit
missing-end state so native-context compaction or process interruption is not
mistaken for a final CLI summary.
Bridge emits successful business `turn.end` events before visible native
context-compaction notes, and final-turn native maintenance runs silently after
`run.end`. This keeps long runs from visually ending on an in-progress
maintenance note such as native context compaction when the CLI or Bridge is
interrupted.

User message cards render attached file metadata from `event.data.files`.
The right-side file panel also shows `OrchestrationRun.files` for the selected
run when no draft files are pending, so historical uploads remain discoverable
after the draft upload list is cleared.
Follow-up prompts preserve previously uploaded run file metadata even when no
new files are attached. New follow-up uploads are merged into the run-level
metadata while each `user.message` event still records only that prompt's
attachments.
Bridge materializes new follow-up uploads under the locked absolute run cwd
when Hub has one, so attached file paths match the CLI execution directory
across follow-up prompts and Bridge reconnects.
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
   `PromptSeq`, plus the persisted `FirstCLI` selection when present.
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
12. Include typed command metadata in Bridge-emitted command events and show
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
20. Preserve first-turn CLI selection across create, refresh, and continue so a
    Codex-first smoke remains Codex-first.
21. Persist Codex thread id, Claude-started state, and absolute run cwd from
    Bridge events, then include them in resumed `orchestration_start` payloads.
22. Materialize uploaded files under the locked absolute run cwd when a run is
    resumed.
23. Preserve the orchestration profile across create, refresh, and continue.
24. Emit and render structured `run.conclusion` events for completed, failed,
    and canceled runs.

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
- Command cards read command details from typed `CommandData`; the frontend
  does not inspect free-form `Data` for command fields.
- Bridge-originated notes are distinguished by `source=bridge`, not content
  string prefixes, and do not inflate command failure counts.
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
- Continuing a Codex-first run sends `FirstCLI=codex` in the resumed
  `orchestration_start` payload unless the user intentionally changes it.
- Continuing a formal-proof run sends `Profile=formal-proof` in the resumed
  `orchestration_start` payload unless the user intentionally changes it.
- A resumed run sends the saved Codex thread id, Claude-started state, and
  locked absolute run cwd to Bridge.
- The first Codex turn in a resumed run uses Codex app-server `thread/resume`
  when Hub has a saved thread id; the first Claude turn uses `--resume` only
  after a prior Claude turn reached `turn.end`.
- If Codex returns a different thread id while Bridge expected a resume target,
  the browser timeline shows a warning and Hub stores the returned thread id for
  later follow-ups.
- If Codex resume clearly misses the local thread data or Claude `--resume`
  reports a missing session, Bridge emits a warning, keeps the compacted context
  fallback, and continues with a fresh native CLI session/thread.
- `turn.start.content` does not contain the full relay prompt or compacted
  context; the full prompt is carried only in typed `TurnStartData.PromptText`
  for authenticated local diagnostics and is stripped from public shares.
- Every terminal run has one `run.conclusion` event and one rendered final
  conclusion card.

## Reviewer Q&A

**Q1 (trade-off): Why reuse the same orchestration run instead of creating a new
run per prompt?**

A: The run is the user-visible work container and the only event stream the UI
can replay. Reusing it keeps context, status, and history in one place. A new
run is still available through the explicit New Run action.

**Q2 (trade-off): Why compact context if native CLI sessions are also
preserved?**

A: Codex and Claude keep separate native histories, and either native process
can be lost on Bridge restart. Compacting events gives both CLIs a shared
handoff format and remains the continuity fallback when native resume is not
available.

**Q3 (boundary): What happens if the selected run is still active?**

A: The frontend disables submit and the Hub rejects continue with `RUN_ACTIVE`.
Only one active orchestration prompt per run is allowed.

**Q4 (scenario): A user reports that a follow-up opened a new context. Where do
you look first?**

A: Check whether the UI cleared `codexBridge.activeOrchestrationRunId`, whether
`startRun` used `/api/orchestrations` instead of the run-specific prompts
endpoint, and whether Hub logged `Resume=true` in the Bridge start payload.
