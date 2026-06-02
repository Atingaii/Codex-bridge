# Native Interactive Orchestration

## Goals

- Keep one native Codex conversation and one native Claude Code conversation per
  orchestration run.
- Reuse those native conversations for every scheduled turn and follow-up prompt
  for the same run, unless the user explicitly starts a new run.
- Make native resume/audit practical: Codex turns should live in a Codex native
  thread, and Claude turns should live in a Claude Code session with a stable
  session id and display name.
- Preserve the browser timeline's structured `turn.delta` and `command.*`
  events.

## Non-Goals

- Do not drive full-screen TUI panes as the primary automation surface. TUI
  scraping is not a reliable completion or tool-event boundary.
- Do not merge the two CLIs into one native conversation. Codex and Claude Code
  keep separate native histories for the same Bridge run.
- Do not preserve OS processes across Bridge service restarts. After restart,
  Bridge resumes native history by thread/session id where the CLI supports it.
- Do not change explicit New Run behavior; a new run gets new native
  conversations.

## Data And Protocol Impact

- No new HTTP endpoint or WebSocket frame kind is required.
- Existing `TurnStartData.ResumeMode`, `RunEndData.CodexThreadID`, and
  `RunEndData.ClaudeSessionID` continue to expose native ids.
- `RunStartData.NativeContextCompaction` and
  `OrchestrationRun.native_context_compaction` carry the optional post-turn
  maintenance setting.
- `RunEndData.NativeResume`, `RunEndData.CodexNativeResume`, and
  `RunEndData.ClaudeNativeResume` expose direct native resume commands and
  visibility status for both CLIs.
- Event `data.resumeMode` gains interactive modes such as
  `codex-interactive-thread`, `codex-interactive-resume`,
  `claude-interactive-session`, and `claude-interactive-resume`.
- Hub's existing persisted `codex_thread_id`, `claude_started`, and `run_cwd`
  remain the restart/follow-up continuity data.

## Design

Bridge keeps a run-scoped native session object keyed by `runID`.

Codex uses a long-lived `codex app-server --listen stdio://` process. Bridge
starts or resumes one persisted, non-ephemeral Codex thread for the run, names it
with the Bridge run id, and sends every Codex turn through `turn/start` on that
same thread. This is the closest non-TUI Codex surface to the interactive
application: it creates native Codex threads, streams structured deltas/tool
events, supports approvals, and can be resumed by native Codex tooling.

After a Codex turn completes, Bridge calls `thread/unsubscribe` for that thread
but keeps the run-scoped app-server process alive. This asks Codex to flush and
unload the thread so the rollout jsonl is available to native `codex resume`
immediately after the browser turn, while preserving one native Codex process
for the browser conversation. Bridge keeps the same thread id for the run; the
next Codex turn uses `thread/resume` on that same process and thread id before
sending the next `turn/start`. Bridge only closes the app-server process on
explicit run cancellation, replacement, Bridge shutdown, or if the process dies.

When `nativeContextCompaction=after-turn`, Bridge calls the Codex app-server
`thread/compact/start` RPC on the same thread after the successful business turn
and before `thread/unsubscribe`. Bridge waits for the matching compaction
completion notification. Maintenance output is tagged with
`BridgeNoteData.Category=native-context-compaction` and is not appended to
cross-CLI handoff history.

Claude Code uses a long-lived headless stream process:

```bash
claude --print --input-format=stream-json --output-format=stream-json \
  --session-id <stable-run-derived-uuid>
```

For a resumed run whose Claude session has already completed at least one turn,
Bridge uses `--resume <session-id>` instead. Every Claude turn is written as a
stream-json user message to the same stdin. Bridge reads stdout until that turn's
`result` event, then leaves the process alive for later turns and follow-up
prompts.

Claude native resume metadata is based on the real transcript file under
`~/.claude/projects/<encoded-cwd>/<session-id>.jsonl`. After a successful
Claude turn, Bridge updates only the matching `projects[absCwd]` entry in
`~/.claude.json` and exposes `claude --resume <session-id>` in run-end metadata.
Because Claude Code's interactive `/resume` picker filters stream-json
`sdk-cli` transcripts, Bridge also materializes the same Claude-written
transcript into picker-visible `entrypoint:"cli"` form and appends the missing
`~/.claude/history.jsonl` index row.

Claude Code stream-json does not currently expose a verified native compaction
control RPC. When `nativeContextCompaction=after-turn`, Bridge emits an info
skip note for Claude rather than writing `/compact` to stdin, because that stdin
path is model-visible user input. This keeps Claude's business conversation
clean while preserving the same post-turn lifecycle and observability.

The relay prompt changes slightly for same-CLI turns. If a CLI has already seen
earlier turns in its own native session, Bridge tells it this is another message
in the same native conversation and keeps the cross-CLI handoff compact. The
other CLI's visible result is still included because Codex cannot see Claude's
private native history and vice versa.

When Bridge exits or a native process dies, OS-process continuity is lost. The
next follow-up still carries compacted Hub context and native ids; Codex resumes
the saved thread where possible, and Claude Code resumes the saved session where
possible.

## Implementation Steps

1. Add a run-scoped native session map to `internal/bridge/orchestration.go`.
2. Reuse one Codex thread id for all Codex turns in a run.
3. Keep Claude stream-json processes alive per run and reuse one stable session
   for all Claude turns.
4. Flush completed Codex turns with `thread/unsubscribe` while keeping the
   run-scoped app-server process alive, then resume the same thread id before
   later Codex turns.
5. Update relay prompt wording for same-native-session turns.
6. Keep existing event shapes and expose interactive resume modes in event data.
7. Close native sessions on Bridge shutdown and explicit cancellation.
8. If `nativeContextCompaction=after-turn`, run Codex compaction through
   `thread/compact/start` and skip CLI surfaces that have no verified native
   compact-control channel, keeping maintenance output out of handoff history.
9. Emit native resume metadata for Codex and Claude in run-end data.

## Exit Gates

- A multi-turn run uses one persisted Codex thread id, one Codex app-server
  process, and one Claude stream-json process for that run.
- Later Codex turns use the same Codex thread id as the first Codex turn.
- The Codex thread is persisted under the OS user that ran the generated
  `codex-bridge link` command. Its `HOME` / `CODEX_HOME` and cwd are preserved
  by the generated user service, so it is visible to `codex resume` / `/resume`
  for the same cwd after each completed Codex turn. That user must own and be
  able to write `CODEX_HOME/sessions`, including the current date directory, or
  Codex can create state DB rows without writing the rollout jsonl.
- Later Claude turns use the same Claude session id as the first Claude turn.
- Run-end metadata includes `codex resume <thread-id>` and
  `claude --resume <session-id>` when the native ids are available.
- Claude resume visibility is checked against the project transcript JSONL and
  the current cwd entry in `~/.claude.json` is updated without changing other
  projects. The same Bridge session is visible from the run cwd through
  Claude Code `/resume`.
- When native context compaction is enabled, Codex receives
  `thread/compact/start` after successful business turns; Claude stream-json is
  skipped with an info Bridge note until a verified native control channel is
  available. Compaction failures emit warning Bridge notes and do not fail the
  run.
- Follow-up prompts for the same run reuse the same live native sessions while
  Bridge remains running.
- After Bridge restart, follow-up prompts resume native history by persisted
  thread/session id where possible and keep compacted Hub context as fallback.
- Browser timeline event ordering remains chronological.
- Verification passes:
  `/usr/local/go/bin/go test ./...`,
  `CGO_ENABLED=0 /usr/local/go/bin/go build -ldflags "-s -w" -o bin/codex-bridge .`,
  and frontend build when `frontend/` changes are included.

## Reviewer Q&A

**Q1: Why not drive `/resume` inside the TUI directly?**

A: TUI control is screen scraping. It cannot reliably delimit turn completion,
extract command events, handle approval callbacks, or survive terminal redraws.
`codex app-server` and Claude stream-json keep native histories while preserving
machine-readable events.

**Q2: Can the user see the sessions with native resume?**

A: Yes, under the same-user invariant. The generated browser setup command must
be run by the workspace OS user, from the target workspace directory. Bridge
then writes native history under that user's `HOME` / `CODEX_HOME` / Claude
home. Codex remains visible through Codex's native thread metadata, and Bridge
materializes Claude stream-json transcripts so they appear in Claude Code's
interactive `/resume` picker from the run cwd. The user can inspect Codex
history from the same identity and cwd:

```bash
cd /home/alice/project
codex resume --include-non-interactive
```

Use `codex resume --all --include-non-interactive` only when intentionally
disabling Codex's cwd filter. `sudo -u <service-user>` is a legacy deployment
diagnostic for endpoints that were incorrectly started by a central service
user; it is not the normal user workflow.
