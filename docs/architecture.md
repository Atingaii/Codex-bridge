# Architecture

This is the single overview for runtime architecture. Detailed rationale lives
in ADRs; implementation paths live in [docs/code-map.md](code-map.md).

```text
Browser UI
  | WSS /ws/chat?sid=<session>
Hub (Go + embedded UI + SQLite)
  | reverse WSS /api/agents/connect?token=<enroll>
Bridge (Go)
  | spawn per prompt
codex exec --json
```

CLI endpoints created with the review-required profile use
`internal/bridge/appserver_runner.go` instead of `codex exec --json` for Codex
chat. That runner keeps a `codex app-server --listen stdio://` JSON-RPC session
open for the turn so Codex approval requests can be relayed to the browser.

The orchestration UI uses HTTP for create/continue/cancel plus a run-scoped
WebSocket for event streaming:

```text
Browser UI
  | POST /api/orchestrations
  | POST /api/orchestrations/<run>/prompts
  | WSS /ws/orchestrations?runId=<run>
Hub (SQLite orchestration_runs + orchestration_events)
  | reverse WSS orchestration_start / orchestration_event
Bridge
  | run-scoped native CLI sessions
Codex CLI / Claude CLI
```

The Bridge keeps orchestration deterministic while preserving native CLI
continuity. For each active orchestration run, Bridge keeps one long-lived
Codex app-server thread and one long-lived Claude Code stream-json session,
then sends later turns for the same run back into those native conversations.
Direct orchestration is a pass-through relay: the run's persisted `first_cli`
selection decides whether Claude or Codex receives the browser task first,
Bridge streams CLI deltas, typed command events, and terminal status to the
browser, and the next CLI receives the previous CLI's visible result plus
useful command context. Bridge persists the Codex thread id and stable Claude
session id so follow-up prompts can resume native history after a Bridge
restart where the CLI supports it. It does not add hidden proof strategy gates,
automatic verifier turns, or remediation turns. Formal-proof guidance is opt-in
through the persisted `profile=formal-proof` run setting selected in the
orchestration UI; the default profile does not activate proof guidance based on
prompt keywords. The native-session design is documented in
[docs/features/native-interactive-orchestration.md](features/native-interactive-orchestration.md),
and the relay contract is documented in
[docs/features/orchestration-pass-through-cli.md](features/orchestration-pass-through-cli.md).
Profile-specific prompt fragments, assessments, manual-build carry-over, and
command fingerprint policy live behind `internal/bridge/profiles/registry` and
`internal/bridge/profiles/formalproof/`; `internal/bridge/orchestration.go`
only calls the neutral registry boundary.

Orchestration events use a typed contract in
`internal/protocol/envelope.go:OrchestrationEventPayload`. `source`
distinguishes `cli`, `bridge`, and `user` events; `severity` carries
Bridge-internal log levels without overloading lifecycle `status`; command,
run-start, turn-start, run-end, Bridge-note, and final-conclusion details live
in typed sub-payloads. `turn.start.content` is a one-line status; the full
local prompt is kept in `TurnStartData.PromptText` for authenticated local
diagnostics and is stripped from public shares. Every terminal run emits one
structured `run.conclusion` event before `run.end`, `run.error`, or
`run.cancelled`.

Bridge long-command observation is controlled by
`bridge.long_command_observer`. Matching Claude commands can receive a tagged
stream-input note, and matching Codex commands emit a visible Bridge-note row
when no stdin side-channel exists. Both paths use
`BridgeNoteData.InjectedText` so the browser timeline records exactly what
Bridge said.

Review-required Claude orchestration uses Claude Code's
`--permission-prompt-tool` support. `internal/bridge/orchestration.go:runClaude`
and `internal/bridge/orchestration.go:runClaudeInteractive` write a temporary
MCP config, run `codex-bridge claude-approval-mcp` as a stdio MCP server, and
forward MCP permission prompts back to the parent Bridge over a Unix socket.
Hub then reuses existing `approval_request` and `approval_response` frames with
`payload.runId` for browser approval on the orchestration timeline.

Codex orchestration uses `codex app-server --listen stdio://` through
`internal/bridge/orchestration.go:runCodexInteractive`. App-server approval
callbacks are mapped to run-scoped `approval_request` frames with
`payload.runId`, and browser decisions return as `approval_response` frames to
the owning Bridge. The standalone `internal/bridge/appserver_runner.go:Prompt`
path remains the Codex app-server runner for chat and non-orchestration runner
uses.

CCB is not an active orchestration backend for new Hub-managed runs. Historical
CCB helper code and event rendering remain in place, but current orchestration
starts use the selected Bridge connection to run the direct Claude Code and
Codex CLI turn loop described above. See
[docs/features/manual-orchestration-rounds.md](features/manual-orchestration-rounds.md).

Bridge registration includes `protocol.RegisterPayload.Capabilities`. Hub keeps
the latest online capabilities in `internal/hub/pool.go` and returns them from
`GET /api/agents`, allowing the frontend to show whether Codex and Claude
orchestration execution and browser approval are available. Hub blocks
orchestration when the selected endpoint cannot execute both CLIs, and blocks
review-required orchestration when the endpoint cannot provide the required
approvals instead of falling back to `codex exec --json`.

Conversation share links are Hub-only public reads. Authenticated users create
share records for chat sessions or orchestration runs; anonymous viewers fetch
sanitized persisted transcripts through `GET /api/public/shares/<share>`. The
Bridge is not contacted for public reads, and the frontend `/share/<share>`
route renders before login bootstrap. Orchestration share sanitization in
`internal/hub/share.go:publicOrchestrationEvents` drops severity events,
internal Bridge notes, and `TurnStartData.PromptText`, while preserving public
run lifecycle and structured conclusion events.

## Decisions

| ID | Decision | Reason |
| --- | --- | --- |
| ADR-001 | Bridge reverse-connects to Hub | Works behind NAT without opening inbound ports |
| ADR-002 | Single Bridge WebSocket with `sid` envelopes | Multiple browser tabs share one long connection |
| ADR-003 | Short-lived Codex runner for v1 | Lower resident memory and simpler crash cleanup |
| ADR-004 | SQLite only on Hub | Single-user persistence without extra services |
| ADR-005 | Embedded native frontend | No Node build, smaller deployment surface |
| ADR-006 | Orchestration continue reuses `runID` | Follow-up tasks keep context through event compaction |
| ADR-007 | Public conversation share links | Anonymous readers can view sanitized transcripts without workspace access |

## Protocol

Every Hub-Bridge and browser-Hub frame uses:

```go
type Envelope struct {
    Type    string          `json:"type"`
    Sid     string          `json:"sid,omitempty"`
    Payload json.RawMessage `json:"payload,omitempty"`
}
```

Implemented frame types:

- `register`, `registered`, `heartbeat`
- `open_session`, `session_opened`, `close_session`
- `prompt`, `session_update`, `prompt_complete`
- `approval_request`, `approval_response`
- `cancel`, `error`, `status`
- `orchestration_start`, `orchestration_event`, `orchestration_cancel`
- `agent_shutdown`

Bridge-originated `heartbeat` payloads may include `workingDirs`. Hub treats
that as live endpoint metadata, updates `agents.working_dirs_json`, and still
accepts older heartbeat payloads that only carry a timestamp.

## Continuity

Chat continuity:

1. Hub loads `sessions.remote_thread_id`.
2. Hub sends it in `open_session`.
3. Bridge stores it in the live session.
4. The saved `remote_thread_id` is passed to the configured runner for
   follow-up prompts. Codex app-server runner paths use `thread/resume`; Codex
   exec runner paths use `codex exec resume <thread-id> -`.
5. Hub persists the latest returned thread id on `prompt_complete`.

Orchestration continuity:

1. New tasks create an `orchestration_runs` row.
2. Follow-up tasks call `/api/orchestrations/{runID}/prompts` and stay on the
   run's original `agentId`; switching CLI endpoint requires an explicit new
   run. The same persisted `first_cli` value is reused unless the request
   explicitly changes it.
3. Hub compacts prior `orchestration_events` into context.
4. Hub also restores native CLI state from `orchestration_runs`: the latest
   Codex thread id, whether Claude reached a successful turn, and the locked
   absolute run cwd reported by Bridge.
5. Bridge receives the same `runID` with `Resume=true`, reuses any live
   run-scoped native sessions, can resume Codex and Claude by persisted native
   ids after restart where supported, and materializes new uploads under the
   locked run cwd.
6. The frontend stores the last selected run id locally and restores it on
   `/orchestrate`.

Chat session isolation:

1. Each `sessions` row stores its owning CLI endpoint in `sessions.agent_id`.
2. The frontend filters the chat sidebar by the selected agent.
3. Switching agents closes the active chat WebSocket and restores that agent's
   remembered session from `codexBridge.activeSessionByAgent`.
4. Sending from an empty agent space creates a new session for that agent.

## Storage

SQLite tables:

- `users`
- `agents` (`deleted_at` soft-deletes CLI endpoints while preserving history)
- `sessions`
- `messages`
- `runs`
- `enroll_tokens`
- `orchestration_runs` (including persisted mode, `first_cli`, `profile`, cwd,
  max turns, status, native CLI continuity state, locked runtime cwd, and
  uploaded file metadata)
- `orchestration_events` (including `source`, `severity`, lifecycle status,
  and typed event payload JSON)
- `conversation_shares`

Hub stores browser auth and chat history. Bridge stores only its generated `machine_id` and reads Codex/OpenAI credentials from its local environment.
