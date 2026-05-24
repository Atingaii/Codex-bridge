# Delete CLI Endpoint

## Goal

Users can remove a CLI endpoint from the settings modal after adding it. Deleted
agents disappear from agent selectors, and an online Bridge connection for that
agent is asked to shut down before its Hub connection is closed.

## Non-Goals

- Delete historical chat sessions, messages, orchestration runs, or events.
- Kill arbitrary local OS processes unrelated to the selected Bridge endpoint.
- Remove all user shell artifacts under `~/.codex-bridge`.

## Data And Protocol Impact

- `internal/store.Store.Migrate` adds `agents.deleted_at` for soft deletion.
- `internal/store.Store.DeleteAgent` marks an agent deleted instead of removing
  the row, preserving foreign-key references from sessions and orchestration
  history.
- `DELETE /api/agents/{agentID}` deletes only agents visible to the current
  user. Admin users may delete any agent visible through the admin listing.
- Hub sends a best-effort `protocol.TypeAgentShutdown` frame through
  `internal/hub/pool.go:ShutdownAgent` before closing the in-memory connection.
- `internal/bridge/client.go:requestShutdown` stops active work, tries to
  disable the generated `systemd --user` service for the endpoint, and exits.
  For `nohup` fallback endpoints, exiting the process is the cleanup action.
- The endpoint revokes enroll tokens consumed by the deleted machine id so a
  background process cannot immediately reconnect with the same token.

## Implementation Steps

1. Add the Hub route in `internal/hub/server.go:NewServer`.
2. Add store soft-delete and token-revocation helpers.
3. Filter deleted agents from list and lookup methods.
4. Add a delete icon button in `frontend/src/app/App.tsx:SettingsModal`.
5. Add `protocol.TypeAgentShutdown` and handle it in
   `internal/bridge/client.go:handleEnvelope`.
6. Rebuild embedded static assets.
7. Add store and Hub tests.

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

If the endpoint is online and was started by the generated connect command, the
Bridge receives `agent_shutdown`, disables its matching `systemd --user`
service, and exits. If the endpoint was started by the `nohup` fallback, the
Bridge exits and is not restarted by Codex Bridge. If the endpoint is offline,
Hub still soft-deletes it and revokes consumed enroll tokens.
