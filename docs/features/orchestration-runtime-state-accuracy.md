# Orchestration Runtime State Accuracy

## Goals

- Persist a user-readable orchestration failure reason when the verifier
  explains why acceptance failed.
- Keep terminal `run.end` / `run.error` content visible as an explicit summary
  in the browser timeline when it differs from the last turn.
- Prevent terminal orchestration runs from showing stale command cards as still
  running.
- Keep an online Bridge endpoint's `workingDirs` list fresh while the reverse
  WebSocket stays connected, including removal of paths that no longer exist.

## Non-Goals

- Do not change the orchestration continuity model or create new runs for
  follow-up prompts.
- Do not add a new WebSocket frame type.
- Do not change SQLite schema.
- Do not mark a verifier-rejected proof task as complete merely because its
  project builds.

## Data And Protocol Impact

- `protocol.TypeHeartbeat` keeps the same frame type.
- Bridge-originated heartbeat payloads may include
  `protocol.HeartbeatPayload.WorkingDirs`.
- Hub updates `agents.working_dirs_json` from heartbeat payloads via
  `internal/hub/ws_bridge.go:handleBridgeEnvelope`.
- Older Bridges that send heartbeat payloads without `workingDirs` continue to
  only refresh `agents.last_seen_at`.
- Orchestration event shape is unchanged. The frontend derives non-running
  display state for unclosed command events when the selected run is terminal
  and renders terminal run content as a visible summary message when useful.

## Implementation Steps

1. Teach `internal/bridge/client.go` to rediscover working directories for each
   heartbeat.
2. Add a store helper that touches an agent and optionally replaces
   `working_dirs_json`.
3. Decode Bridge heartbeat payloads in `internal/hub/ws_bridge.go` and refresh
   working directories when present.
4. Prefer verifier prose over raw acceptance-check command text in
   `internal/bridge/profiles/formalproof:AcceptanceFailure`.
5. In `frontend/src/app/lib/utils.ts:finalizeTerminalCommandEvent`, fold
   terminal run status into command event rendering so unpaired `command.start`
   events do not remain active forever.
6. Poll `/api/agents` quietly while the browser is visible so refreshed working
   directories reach the selector without a reconnect.
7. Render terminal `run.end` / `run.error` content as a timeline summary unless
   it duplicates an already visible turn conclusion.
8. Filter discovered working directories through `os.Stat` so deleted paths do
   not remain advertised after the next Bridge heartbeat.

## Exit Gates

- A verifier message such as "acceptance cannot be marked complete" becomes the
  stored `run.error` reason instead of the raw shell command used to check it.
- A failed, completed, or canceled run does not render any command as still
  running when no matching `command.end` was recorded.
- The final CLI/run result is visible in the orchestration timeline when
  `run.end` or `run.error` carries a user-readable conclusion.
- Creating and deleting a first-level workspace directory under the Bridge CWD
  is reflected in `GET /api/agents` after a heartbeat.
- `cd frontend && npm run build`
- `/usr/local/go/bin/go test ./...`
- `CGO_ENABLED=0 /usr/local/go/bin/go build -ldflags "-s -w" -o bin/codex-bridge .`
- `make doc-lint`

## Reviewer Q&A

**Q: Why reuse heartbeat instead of adding an agent-update frame?**

A: Working directories are liveness-adjacent metadata and do not need a separate
delivery guarantee. Reusing heartbeat avoids a new frame type while keeping old
Bridge clients compatible.

**Q: Why mark unpaired command starts only in the frontend?**

A: Historical persisted event logs should remain immutable. The terminal run
status is already authoritative, so the UI can render a derived stopped state
without inventing stored `command.end` events.

**Q: Why not treat a compiling proof project as success?**

A: For proof tasks, build success is necessary but not sufficient when the
verifier identifies a semantic gap such as replacing a termination proof with a
fuel wrapper. That explanation must remain visible as the failure reason.
