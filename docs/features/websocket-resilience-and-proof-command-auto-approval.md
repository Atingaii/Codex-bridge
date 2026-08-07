# WebSocket Resilience And Proof Command Auto-Approval

## Goals

- Keep browser chat and orchestration streams alive through ordinary timer
  throttling, short network stalls, and reverse-proxy idle handling.
- Reconnect chat automatically and reload persisted messages and run state
  without creating a new session.
- Reduce false Bridge offline transitions caused by a narrow heartbeat window.
- In review-required mode, automatically approve narrowly validated `cat`,
  `coqc`, and `coqtop` commands so formal-proof agents can inspect and compile
  workspace files without repeated browser interaction.

## Non-Goals

- This change does not alter the WebSocket envelope schema, HTTP API, session
  identity, orchestration run identity, or browser workflow.
- It does not auto-approve file edits, permission expansion, arbitrary shell
  commands, compound commands, pipes, redirections, substitutions, or commands
  whose file paths escape the active workspace.
- It does not infer a command from an ACP tool title when the adapter does not
  expose the complete command line.

## Data And Protocol Impact

There is no persistence or application-envelope change. Hub and Bridge add
standard WebSocket ping/pong control frames. Existing JSON heartbeat envelopes
remain supported for compatibility and working-directory refresh.

The Hub read timeout uses a resilience floor and remains configurable through
the existing `hub.bridge_read_timeout` setting. Browser chat reconnects with
the existing `sid`, then reloads `/messages` and `/runs` to recover persisted
state missed while disconnected. Pending chat approvals are retried with their
original request ID after a connection returns; repeated browser frames keep a
decision already made by the user visible.

## Command Validation And Threat Model

`internal/bridge/approval_allowlist.go:isProofCommandAutoApprovable` parses one
simple command without invoking a shell. Validation fails closed when the
command contains shell control syntax, expansion, redirection, a newline, an
unknown option, a missing workspace, or a path outside that workspace.

- `cat` accepts regular workspace files and read-only display options; stdin is
  rejected to avoid an indefinitely blocked turn.
- `coqc` accepts a conservative set of compile/diagnostic flags, workspace
  include/output paths, and workspace `.v` inputs.
- `coqtop` additionally requires batch execution and a workspace source file.

Auto-approval returns a one-request decision to Codex app-server. Manual
browser approval keeps its existing session-scoped behavior. Claude receives
the same one-request acceptance from its approval socket.

## Implementation Steps

1. Serialize WebSocket JSON and ping writes through the existing Hub sender.
2. Add Bridge ping/pong deadlines to its single reader/single writer loops;
   transient application-queue saturation skips a heartbeat instead of
   terminating the transport, and stable connections reset reconnect backoff.
3. Add bounded chat reconnect, state rehydration, and online/visibility wakeup.
4. Retry a pending chat approval on an attached Bridge connection without
   repeatedly buffering it while disconnected.
5. Add the conservative proof-command validator at Codex and Claude approval
   boundaries.
6. Add focused parser, timeout, approval, and frontend source checks.

## Exit Gates

- [x] Allowed proof commands receive one-request automatic acceptance.
- [x] Shell composition, substitution, redirection, unsafe options, and path
  traversal remain browser-reviewed.
- [x] Chat reconnect retains the selected `sid`, reloads messages/runs, and
  replays a pending approval without reopening a session.
- [x] Hub and Bridge ping/pong writes are serialized with JSON writes.
- [x] Frontend tests/build, Go tests/build, doc lint, and diff checks pass.

## Reviewer Q&A

**Why retain JSON heartbeats when ping/pong exists?**

Bridge heartbeat payloads also refresh discovered working directories, and old
clients remain compatible during rolling upgrades. Ping/pong handles transport
liveness independently.

**Why not auto-approve every command beginning with an allowed name?**

Prefix matching would approve constructs such as `cat file; dangerous-command`.
The validator accepts only one parsed command with a known executable, known
options, and workspace-contained paths.

**Does reconnect create a new Codex thread?**

No. `frontend/src/app/pages/Workspace.tsx:connectWS` reconnects the same session
ID, and `internal/hub/ws_browser.go:handleBrowserWS` reopens it with its stored
native thread ID.
