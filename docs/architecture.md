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
  | spawn per turn
Codex CLI / Claude CLI
```

The Bridge keeps orchestration deterministic and low overhead: it still spawns
one CLI per turn in auto-execute mode, and uses `codex app-server` for Codex
turns when browser approval is required. Prompts use compact `Msg:` and
`Handoff:` lines between turns, and the Bridge stores parsed handoff fields as
turn state so the next CLI receives structured context instead of the full
transcript. In collaboration mode Claude acts as builder and Codex as reviewer;
in debate mode Claude proposes and Codex criticizes with evidence. The handoff
contract is documented in
[docs/features/orchestration-strategy-optimization.md](features/orchestration-strategy-optimization.md).

Review-required Claude orchestration uses Claude Code's
`--permission-prompt-tool` support. `internal/bridge/orchestration.go:runClaude`
writes a temporary MCP config, runs `codex-bridge claude-approval-mcp` as a
stdio MCP server, and forwards MCP permission prompts back to the parent Bridge
over a Unix socket. Hub then reuses existing `approval_request` and
`approval_response` frames with `payload.runId` for browser approval on the
orchestration timeline.

Review-required Codex orchestration reuses
`internal/bridge/appserver_runner.go:Prompt`. App-server
approval callbacks are mapped to run-scoped `approval_request` frames with
`payload.runId`, and browser decisions return as `approval_response` frames to
the owning Bridge.

Bridge registration includes `protocol.RegisterPayload.Capabilities`. Hub keeps
the latest online capabilities in `internal/hub/pool.go` and returns them from
`GET /api/agents`, allowing the frontend to show whether Codex and Claude
orchestration browser approval are available. Hub blocks review-required
orchestration when the selected endpoint cannot provide the required approvals
instead of falling back to `codex exec --json`.

## Decisions

| ID | Decision | Reason |
| --- | --- | --- |
| ADR-001 | Bridge reverse-connects to Hub | Works behind NAT without opening inbound ports |
| ADR-002 | Single Bridge WebSocket with `sid` envelopes | Multiple browser tabs share one long connection |
| ADR-003 | Short-lived Codex runner for v1 | Lower resident memory and simpler crash cleanup |
| ADR-004 | SQLite only on Hub | Single-user persistence without extra services |
| ADR-005 | Embedded native frontend | No Node build, smaller deployment surface |
| ADR-006 | Orchestration continue reuses `runID` | Follow-up tasks keep context through event compaction |

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

## Continuity

Chat continuity:

1. Hub loads `sessions.remote_thread_id`.
2. Hub sends it in `open_session`.
3. Bridge stores it in the live session.
4. `codex exec resume <thread-id> -` is used for follow-up prompts.
5. Hub persists the latest returned thread id on `prompt_complete`.

Orchestration continuity:

1. New tasks create an `orchestration_runs` row.
2. Follow-up tasks call `/api/orchestrations/{runID}/prompts`.
3. Hub compacts prior `orchestration_events` into context.
4. Bridge receives the same `runID` with `Resume=true`.
5. The frontend stores the last selected run id locally and restores it on
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
- `orchestration_runs`
- `orchestration_events`

Hub stores browser auth and chat history. Bridge stores only its generated `machine_id` and reads Codex/OpenAI credentials from its local environment.
