# Agent Scoped CLI Workspaces

## Goal

Chat sessions and orchestration runs should be isolated by CLI endpoint. When
the user switches the selected CLI endpoint in the browser, the visible chat
history, active session, thread id, run state, orchestration timeline, and
sidebar lists should switch to that endpoint's own space.

## Non-Goals

- Do not migrate SQLite data. `sessions.agent_id` and orchestration
  `agent_id` already exist.
- Do not change Hub-Bridge protocol frames.
- Do not delete or move sessions or orchestration runs across agents.

## Current State

- `internal/store/store.go:Session` already stores `AgentID`.
- `internal/hub/server.go:handleCreateSession` creates sessions for the chosen
  agent.
- `frontend/src/app/pages/Workspace.tsx:Workspace` loads all sessions and keeps
  one global `activeSessionId`, so switching the selected agent does not switch
  the visible chat space.
- `frontend/src/app/pages/OrchestrationWorkspace.tsx:OrchestrationWorkspace`
  loads all orchestration runs and previously kept one global active run id, so
  switching endpoints could leave another endpoint's run list and timeline
  visible.

## Design

Keep the server API unchanged and partition in the frontend:

- Compute `agentSessions` from all loaded sessions where
  `session.agentId === selectedAgent.id`.
- The sidebar groups and searches only `agentSessions`.
- Store the selected session per agent in local storage as
  `codexBridge.activeSessionByAgent`.
- Keep the CLI selector controlled by the persisted `selectedAgentId`. Online
  agents are only a first-load fallback, not a render-time fallback after the
  user chooses a different endpoint.
- Switching agents closes the current chat WebSocket, clears transient chat
  items/run/runner/thread state, then selects that agent's saved session when it
  still exists, otherwise that agent's latest session.
- Late message/run loads and WebSocket frames are ignored unless they still
  match the active session id, so a previous endpoint cannot repopulate the new
  endpoint's empty chat space.
- If the new agent has no sessions, the chat area becomes an empty draft state.
  Sending a prompt creates the first session for that selected agent.
- Deleting the active session selects the next remaining session for the same
  agent, not a session from another agent.
- Compute `agentRuns` from loaded orchestration runs where
  `run.agentId === selectedAgent.id`.
- The orchestration sidebar renders only `agentRuns`.
- Store the selected orchestration run per agent in local storage as
  `codexBridge.activeOrchestrationRunByAgent`.
- Switching agents closes the current orchestration WebSocket, clears transient
  timeline state, then selects that agent's saved run when it still exists,
  otherwise that agent's latest run.
- If the new agent has no orchestration runs, the timeline becomes an empty
  draft state that shows the selected CLI endpoint and "No orchestration runs".
- Starting a new orchestration run uses the currently selected endpoint and
  remembers that run only for that endpoint.

## Data And Protocol Impact

- No SQLite schema change.
- No Hub route change.
- No WebSocket frame change.
- Frontend local storage gains `codexBridge.activeSessionByAgent`.
- Frontend local storage gains `codexBridge.activeOrchestrationRunByAgent`.

## Implementation Steps

1. Add local-storage helpers for active session ids by agent.
2. Filter chat session lists by selected agent.
3. Update agent selection to switch active session state.
4. Update create/delete/refresh flows to preserve agent-local selection.
5. Add local-storage helpers for active orchestration run ids by agent.
6. Filter orchestration run lists by selected agent.
7. Update orchestration agent selection to switch active run and timeline state.
8. Build the frontend static assets and run the full verification set.

## Exit Gates

- Switching CLI endpoints changes the sidebar session list to that endpoint's
  sessions only.
- Switching to an endpoint with no sessions clears the active chat view without
  deleting other sessions.
- Sending a prompt in an empty endpoint space creates a session for that
  endpoint.
- Deleting an active session stays within the selected endpoint's remaining
  sessions.
- Existing WebSocket messages cannot bleed into the newly selected session.
- Switching CLI endpoints on the orchestration page changes the run list and
  timeline to that endpoint's saved run space.
- Switching to an endpoint with no orchestration runs clears the active timeline
  without deleting other endpoints' runs.
- Starting a run from an empty orchestration endpoint space creates the run for
  that selected endpoint.
- Full verification passes:
  `/usr/local/go/bin/go test ./...`, `cd frontend && npm run build`,
  `CGO_ENABLED=0 /usr/local/go/bin/go build -ldflags "-s -w" -o bin/codex-bridge .`,
  and `make doc-lint`.

## Reviewer Q&A

**Q1: Why filter in frontend instead of adding `GET /api/sessions?agentId=`?**

A: The existing API already returns `agentId` per session, and all session detail
endpoints enforce ownership by session id. Frontend partitioning achieves the
product behavior without changing the HTTP contract.

**Q2: What happens to old mixed sessions?**

A: They remain unchanged and appear under the CLI endpoint stored in their
existing `sessions.agent_id`.
