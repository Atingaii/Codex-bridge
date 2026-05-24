# Agent Repair Command

## Goal

Users should be able to open any existing CLI endpoint in the settings UI and
copy a repair command that updates/restarts that endpoint with the current
Bridge binary and selected permission profile. This is especially important for
endpoints created with older setup commands that do not advertise
`RegisterPayload.Capabilities`.

## Non-Goals

- Do not persist Bridge capabilities in SQLite; they remain live connection
  state owned by `internal/hub/pool.go:AgentCapabilities`.
- Do not remotely execute repair commands. The user still runs the command on
  the private machine that owns the CLI endpoint.
- Do not change orchestration continuity or create a new run/session.

## Design

`POST /api/agents/{agentID}/repair-token` validates that the caller can see the
agent, creates a short-lived enroll token for the same user/label, and returns
the same command shape as `internal/hub/server.go:handleCreateBridgeToken`.
The generated command is specialized for the selected agent:

- `--name` uses the existing agent name.
- `--cwd` defaults to the first known `workingDirs` entry when available.
- `--machine-id` writes the existing agent machine id into the generated
  per-directory machine id file before reconnecting.
- The default permission profile is `review-required`, with `auto-execute` as
  the alternate profile.

`codex-bridge connect` accepts optional `--machine-id`. When supplied, it writes
that value to `--machine-id-file` before loading the machine id. This lets a
repair command bind back to the existing agent instead of accidentally creating
a new endpoint if the command is run from a different directory.

The settings UI expands each row under `Agent & Runtime` into a detail section.
The detail section shows live capability status when available and a button to
generate repair commands. The repair token is only created when the user asks
for it, so simply opening settings does not mint unused credentials.

## Data And Protocol Impact

- No SQLite schema change.
- No WebSocket frame change.
- HTTP API adds `POST /api/agents/{agentID}/repair-token`.

## Implementation Steps

1. Add `--machine-id` to `main.go:runConnect`.
2. Extract reusable command response construction from
   `internal/hub/server.go:handleCreateBridgeToken`.
3. Add `internal/hub/server.go:handleCreateAgentRepairToken`.
4. Update `frontend/src/app/App.tsx:SettingsModal` with per-agent details and
   repair command generation.
5. Add integration coverage for repair command authorization, syntax, and
   machine-id pinning.
6. Rebuild `internal/web/static/`.

## Exit Gates

- `cd frontend && npm run build`
- `/usr/local/go/bin/go test ./...`
- `CGO_ENABLED=0 /usr/local/go/bin/go build -ldflags "-s -w" -o bin/codex-bridge .`
- `make doc-lint`

## Reviewer Q&A

**Q: Why require the user to run the command on the target machine?**

The Hub is intentionally only a transport. The private machine owns workspace
access and local process management.

**Q: Why include the machine id in the repair command?**

Without pinning the machine id, running a repair command from another directory
could create a new agent because the background service stores machine ids by
workspace hash.
