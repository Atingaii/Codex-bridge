# Native Context Compaction

## Goals

- Let users opt in to compacting native CLI context after each successful
  orchestration turn.
- Keep Codex and Claude Code behavior aligned at the lifecycle and observability
  level: Bridge attempts native maintenance after successful turns, and records
  explicit skip notes when a CLI surface lacks a safe control channel.
- Keep maintenance output out of cross-CLI handoffs so business turns stay
  readable and falsifiable.
- Persist the setting with the orchestration run so follow-up prompts keep the
  same behavior.

## Non-Goals

- Do not compact Hub's persisted event timeline.
- Do not make compaction mandatory for all runs.
- Do not treat compaction failure as a business turn failure.
- Do not add a background scheduler outside the active orchestration turn.

## Current State

- `internal/bridge/orchestration_codex.go:flushCodexInteractiveThread` calls
  `thread/unsubscribe` after successful Codex turns. That flushes/unloads the
  native Codex thread but does not ask Codex to compact its context.
- Claude Code stays alive as a stream-json process for a run in
  `internal/bridge/orchestration_claude.go:runClaudeInteractive`.
- Cross-CLI handoffs are built from `orchestrationTurn` records in
  `internal/bridge/orchestration.go:run`; maintenance output must not be added
  to that history.

## Design

Add a run-scoped setting named `nativeContextCompaction`:

| Value | Behavior |
| --- | --- |
| `off` | Existing behavior. No maintenance compaction turn is sent. |
| `after-turn` | After every successful business turn, Bridge asks that CLI to compact native context through a verified native maintenance channel before the CLI becomes idle. If a CLI surface does not expose such a channel, Bridge emits a skipped Bridge note instead of injecting `/compact` as a normal user message. |

The setting is passed from the frontend to Hub, persisted on
`orchestration_runs`, included in `OrchestrationStartPayload`, and echoed on
`run.start` data.

Maintenance turn lifecycle:

1. The normal CLI turn completes successfully and Bridge emits its usual
   `turn.delta` stream for the business answer.
2. Bridge emits the usual business `turn.end` event before starting visible
   maintenance, so browser timelines do not stop at a maintenance-in-progress
   note if compaction is slow or interrupted.
3. For non-final turns, Bridge sends a `turn.delta` bridge note with
   `bridgeNoteData.category=native-context-compaction`.
4. Bridge runs the native maintenance operation with a bounded timeout.
5. On success, Bridge emits an info bridge note. On failure or timeout, Bridge
   emits a warning bridge note and keeps the orchestration run successful.
6. Final-turn Codex maintenance runs after `run.end` as a silent best-effort
   operation. It does not append visible compaction notes after the terminal run
   event, because those notes can otherwise look like an interrupted final
   answer.
7. Codex still calls `thread/unsubscribe` after the maintenance attempt so the
   thread is flushed for native resume.

Bridge uses the same lifecycle for Codex and Claude Code, but only uses native
maintenance channels that are explicit for the current runner surface. Codex
uses the app-server `thread/compact/start` RPC and waits for the matching
compaction completion notification. Claude Code stream-json currently has no
verified native compact-control RPC, so Bridge skips automatic Claude
compaction and records an info Bridge note rather than sending `/compact` as a
model-visible user message.

Maintenance output is not appended to `history`, is not passed to the next CLI,
and does not contribute to `run.conclusion`.

## Data And Protocol Impact

- `internal/protocol/envelope.go:OrchestrationStartPayload` gains
  `nativeContextCompaction`.
- `internal/protocol/envelope.go:RunStartData` gains
  `nativeContextCompaction`.
- `internal/store/store.go:OrchestrationRun` and `orchestration_runs` gain
  `native_context_compaction`.
- `frontend/src/app/pages/OrchestrationWorkspace.tsx:OrchestrationWorkspace`
  sends the setting on create/continue and hydrates it when an existing run is
  selected.
- Existing event shape is reused with bridge notes; no new WebSocket frame kind
  is required.

## Implementation Steps

1. Add normalization for `off` / `after-turn`.
2. Add the SQLite column and store scanner/update plumbing.
3. Add the field to Hub create/continue requests and Bridge start payloads.
4. Add frontend controls and persist the selected value per run.
5. Add Codex native maintenance and Claude safe-skip helpers.
6. Run visible maintenance only after successful non-final turn-end events, and
   run final-turn maintenance silently after `run.end` before Codex thread
   unsubscribe.
7. Add tests for disabled, enabled, and failure-does-not-fail-run behavior.

## Exit Gates

- `/usr/local/go/bin/go test ./...`
- `npm run build` in `frontend/`
- `CGO_ENABLED=0 /usr/local/go/bin/go build -ldflags "-s -w" -o bin/codex-bridge .`
- `make doc-lint`

## Reviewer Q&A

**Why default to off?**  
Compaction consumes time and tokens, and slash-command behavior is controlled by
the native CLI. Users should opt in for long runs where native context growth is
worth the extra maintenance turn.

**Why use bridge notes instead of normal assistant messages?**  
The maintenance turn is operational. Showing it as a normal CLI answer would
pollute the handoff and make the next CLI read compaction output as task
evidence.

**What if `/compact` is unsupported by a CLI version?**  
Bridge either reports the native maintenance error as a warning or, when the
runner surface has no verified native control channel, emits an info skip note.
It does not inject `/compact` into a business conversation as normal user text.
