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
- Event `data.resumeMode` gains interactive modes such as
  `codex-interactive-thread`, `codex-interactive-resume`,
  `claude-interactive-session`, and `claude-interactive-resume`.
- Hub's existing persisted `codex_thread_id`, `claude_started`, and `run_cwd`
  remain the restart/follow-up continuity data.

## Design

Bridge keeps a run-scoped native session object keyed by `runID`.

Codex uses a long-lived `codex app-server --listen stdio://` process. Bridge
starts or resumes one Codex thread for the run, names it with the Bridge run id,
and sends every Codex turn through `turn/start` on that same thread. This is the
closest non-TUI Codex surface to the interactive application: it creates native
Codex threads, streams structured deltas/tool events, supports approvals, and
can be resumed by native Codex tooling.

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
2. Keep Codex app-server clients alive per run and reuse one thread for all
   Codex turns.
3. Keep Claude stream-json processes alive per run and reuse one stable session
   for all Claude turns.
4. Update relay prompt wording for same-native-session turns.
5. Keep existing event shapes and expose interactive resume modes in event data.
6. Close native sessions on Bridge shutdown and explicit cancellation.

## Exit Gates

- A multi-turn run starts at most one Codex app-server process and one Claude
  stream-json process for that run.
- Later Codex turns use the same Codex thread id as the first Codex turn.
- Later Claude turns use the same Claude session id as the first Claude turn.
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

A: Yes, subject to the CLI's own picker behavior and the service user's home
directory. Bridge runs under the configured Bridge service user, so native
history is written under that user's `CODEX_HOME` / Claude home.
