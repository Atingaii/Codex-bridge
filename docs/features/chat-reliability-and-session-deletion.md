# Chat Reliability And Session Deletion

## Goals

- Prevent Codex app-server protocol noise from terminating the active turn as
  an empty reply.
- Recover final assistant text from terminal app-server payloads when delta
  notifications are absent or incomplete.
- Never persist or present a successful chat run with no assistant content.
- Make session deletion discoverable and keyboard-accessible in both desktop
  and mobile session lists.
- Delete the selected Hub transcript and release the matching live Bridge
  session without opening a replacement Codex context implicitly.

## Non-Goals

- Automatically retry a prompt after app-server failure. A retry could execute
  tools twice and is unsafe without an idempotency contract.
- Delete Codex CLI's native thread files from the private machine.
- Add bulk deletion, trash recovery, or retention policies.
- Change chat `sid` or `remote_thread_id` continuity behavior.

## Data And Protocol Impact

- No SQLite schema or protocol payload changes.
- `DELETE /api/sessions/{sid}` remains the authenticated deletion endpoint. It
  deletes the owned Hub session (with SQLite cascades), clears buffered output,
  sends the existing `close_session` frame to the owning Bridge when online,
  and notifies connected browsers with `SESSION_DELETED`.
- `internal/bridge/appserver_runner.go:Prompt` treats only
  terminal events attributable to the active turn as authoritative. Empty,
  unscoped app-server error notifications are ignored while the turn remains
  active; non-empty errors remain failures.
- `internal/hub/ws_bridge.go:handlePromptComplete` rejects an empty completion
  if neither the payload nor Hub's streamed assistant buffer contains text.
- Hub validates Bridge chat-frame ownership once per live session and caches
  the successful `sid` to agent mapping for the streaming hot path. Session
  deletion invalidates that cache before late Bridge frames can be accepted.

## Implementation Steps

1. Harden app-server event parsing, terminal-text recovery, and diagnostics.
2. Retry app-server preparation once with a short backoff, but never replay
   `turn/start` or an in-flight user prompt.
3. Add Bridge and Hub regression tests for empty/error completion paths.
4. Preserve the existing delete endpoint and verify its cascade/close behavior.
5. Render accessible rename and delete actions on every session row.
6. Rebuild the embedded frontend and run focused plus full validation.

## Exit Gates

- App-server tests cover unscoped empty errors followed by a valid completion,
  terminal-only assistant text, real error propagation, and truly empty turns.
- Hub tests prove empty completions fail instead of marking runs successful.
- Session deletion tests prove ownership checks and database cascades.
- Frontend tests/build pass and embedded assets are refreshed.
- `/usr/local/go/bin/go test ./...`, the production Go build, and
  `make doc-lint` pass.

## Reviewer Q&A

### Why not retry an empty response automatically?

The Bridge cannot know whether the first turn already changed files or ran an
external command. Replaying it could duplicate side effects. This design
recovers protocol-delivered text and reports a deterministic failure when no
text exists, leaving retry control with the user.

### Why ignore only empty unscoped errors?

An error carrying a message is actionable and remains fatal. An error tied to
the active thread or turn is also authoritative. The observed empty app-server
notification has neither content nor ownership metadata, so allowing the
active turn's terminal event to decide the result avoids a false failure.

### What happens when the active session is deleted?

The Hub removes its persisted transcript, sends `close_session` so Bridge
cancels work and closes resident runner state, then the frontend selects the
most recent remaining session for that endpoint or shows the empty state. It
never creates a new session automatically.
