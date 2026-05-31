# Manual Orchestration Rounds

> **UPDATE 2026-05-30** — The CCB backend and its helper code/tests were fully
> removed (this doc's original non-goal of preserving them no longer holds). Hub
> still rejects endpoints that cannot run both Codex and Claude. Current
> orchestration design: [orchestration-pass-through-cli.md](orchestration-pass-through-cli.md).

## Goals

- Restore browser orchestration to the Bridge-managed manual Claude Code and
  Codex CLI round-robin workflow.
- Make the round count visible and configurable for every new or continued
  orchestration run.
- Keep each continued run on the same selected Bridge/CLI endpoint so compact
  context is not lost by switching machines mid-conversation.
- Keep follow-up prompts on the existing run through
  `internal/hub/orchestration.go:handleContinueOrchestration`.

## Non-Goals

- Do not remove the historical CCB helper code or terminal approval tests in
  this change.
- Do not add new HTTP endpoints, WebSocket frame types, or SQLite columns.
- Do not change chat session continuity.

## Data And Protocol Impact

- `internal/protocol.OrchestrationStartPayload.MaxTurns` remains the wire field
  for the configured round budget.
- `internal/store.OrchestrationRun.MaxTurns` remains the persisted value.
- Bridge capability advertisement no longer treats `ccb` as an orchestration
  backend. Hub capability validation requires the direct `claude` and `codex`
  orchestration capabilities.
- `internal/hub/orchestration.go:handleContinueOrchestration` rejects attempts
  to change `agentId` on follow-up prompts. Switching endpoint requires an
  explicit new run.
- Existing CCB-specific approval frame handling stays present for historical
  runs and tests, but new orchestration starts do not select the CCB backend.

## Implementation Steps

1. Remove the `ccb` shortcut from
   `internal/bridge/orchestration.go:run` so all starts enter the manual turn
   loop.
2. Stop advertising `orchestrationRunner=ccb` and `orchestration.ccb` as a
   usable orchestration capability from `internal/bridge/client.go:BridgeCapabilities`.
3. Remove the CCB capability bypass from
   `internal/hub/orchestration.go:validateOrchestrationCapabilities`.
4. Reject follow-up prompts that try to change the run's selected Bridge agent.
5. Update the orchestration UI in
   `frontend/src/app/pages/OrchestrationWorkspace.tsx:OrchestrationWorkspace`
   so the mode switch, capability matrix, and turns input are always shown for
   manual Claude/Codex orchestration.
6. Update tests and docs that described CCB as an active orchestration backend.

## Exit Gates

- Creating or continuing an orchestration sends the UI-selected `maxTurns`.
- An endpoint with only `ccb` capability is rejected for orchestration.
- Continuing a run with a different `agentId` is rejected.
- Capability UI lists Claude Code and Codex CLI, not CCB, for orchestration
  readiness.
- `cd frontend && npm run build`
- `/usr/local/go/bin/go test ./...`
- `CGO_ENABLED=0 /usr/local/go/bin/go build -ldflags "-s -w" -o bin/codex-bridge .`
- `make doc-lint`

## Reviewer Q&A

**Q: Why keep CCB helper code if the runner is disabled?**

A: This narrows the behavior change. CCB parsing and terminal approval helpers
remain available for historical event rendering and existing tests, while new
runs return to the manual Bridge-owned round loop.

**Q: Why not keep accepting CCB-only endpoints as a fallback?**

A: The user-visible workflow is manual Claude/Codex orchestration with a round
budget. Accepting a CCB-only endpoint would silently route around that control
surface and ignore the selected round count.
