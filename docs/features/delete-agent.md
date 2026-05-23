# Delete CLI Endpoint

## Goal

Users can remove a CLI endpoint from the settings modal after adding it. Deleted
agents disappear from agent selectors, and an online Bridge connection for that
agent is closed.

## Non-Goals

- Delete historical chat sessions, messages, orchestration runs, or events.
- Kill arbitrary local OS processes on the private Bridge machine.
- Change enroll token creation or the reverse WebSocket frame shape.

## Data And Protocol Impact

- `internal/store.Store.Migrate` adds `agents.deleted_at` for soft deletion.
- `internal/store.Store.DeleteAgent` marks an agent deleted instead of removing
  the row, preserving foreign-key references from sessions and orchestration
  history.
- `DELETE /api/agents/{agentID}` deletes only agents visible to the current
  user. Admin users may delete any agent visible through the admin listing.
- The endpoint closes the in-memory Bridge WebSocket through
  `internal/hub/pool.go:DisconnectAgent`.
- The endpoint revokes enroll tokens consumed by the deleted machine id so a
  background process cannot immediately reconnect with the same token.

## Implementation Steps

1. Add the Hub route in `internal/hub/server.go:NewServer`.
2. Add store soft-delete and token-revocation helpers.
3. Filter deleted agents from list and lookup methods.
4. Add a delete icon button in `frontend/src/app/App.tsx:SettingsModal`.
5. Rebuild embedded static assets.
6. Add store and Hub tests.

## Exit Gates

- `cd frontend && npm run build`
- `/usr/local/go/bin/go test ./...`
- `make doc-lint`
- `make build`

## Reviewer Q&A

**Q: Why soft-delete instead of hard-delete?**

Sessions and orchestration runs reference agents by foreign key. Soft deletion
keeps historical records intact while removing the endpoint from active UI.

**Q: Does this stop the user's local background process?**

The Hub closes the active WebSocket and revokes the consumed token. It cannot
kill arbitrary local OS processes, so users may still stop the background
process locally if they do not want it retrying.
