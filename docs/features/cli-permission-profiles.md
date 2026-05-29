# CLI Permission Profiles

## Goal

Users adding a CLI endpoint from the settings UI should choose between two clear
runtime profiles:

- **Review required**: Codex chat uses the app-server runner so approval
  requests can be reviewed in the browser. Codex orchestration uses the same
  app-server turn path, and Claude Code orchestration routes permission prompts
  to the browser through a short-lived MCP permission tool.
- **Auto execute**: the existing fully automated mode that bypasses local
  permission prompts for trusted machines.

The UI must make the tradeoff visible before the user copies the setup command.

## Non-Goals

- Do not add a separate persistence layer for approval prompts; approval cards
  stay transient WebSocket state.
- Do not remove the current short-lived `codex exec --json` runner; it remains
  the auto-execute path.
- Do not change orchestration follow-up continuity or create a new run/session
  for approval responses.

## Current State

`internal/hub/server.go:handleCreateBridgeToken` used to return a single setup
command. That command started `codex-bridge connect` without explicit permission
flags, so runtime behavior came from `internal/config/config.go:Default` and
local config/env values. User-installed endpoints now need an explicit,
understandable choice.

The existing automated Bridge runner uses short-lived non-interactive CLI calls:

- `internal/bridge/runner.go:execArgs` calls `codex exec --json`.
- `internal/bridge/orchestration.go:claudeArgs` calls `claude --print
  --output-format=stream-json`.

`codex exec --json` does not expose an approval request/response channel through
the Bridge protocol. `codex app-server` does, so the review-required profile uses
a separate app-server runner for Codex chat and Codex orchestration. Claude Code `--print` supports
`--permission-prompt-tool`, so orchestration can bridge Claude permission
prompts through a local MCP tool.

## Design

`POST /api/bridge-tokens` accepts an optional `permissionProfile`:

- `review-required`
- `auto-execute`

If the field is missing, the Hub returns both commands and uses
`review-required` as the default setup command.

`internal/hub/server.go:bridgeConnectCommand` adds explicit connect flags for
each profile:

- Review required:
  - `--runner codex-app-server`
  - `--sandbox workspace-write`
  - `--approval-policy untrusted`
  - Codex approval requests are forwarded as `approval_request` frames and the
    browser replies with `approval_response`.
  - Codex orchestration turns use `codex app-server`, not `codex exec --json`,
    so approval callbacks are run-scoped and appear on the orchestration
    timeline.
  - Claude Code orchestration runs with `--permission-mode default`,
    `--mcp-config <temp>`, and
    `--permission-prompt-tool mcp__codex_bridge__browser_approval`.
- Auto execute:
  - `--runner codex`
  - `--sandbox danger-full-access`
  - `--approval-policy never`
  - Codex uses the existing dangerous bypass flag.
  - Claude Code orchestration uses bypass permissions when supported by the
    current Bridge runtime.

`handleCreateBridgeToken` returns:

- `permissionProfile`: selected/default profile.
- `permissionProfiles`: profile metadata and setup commands for the UI.
- `setupCommand`, `installCommand`, `connectCommand`, and `commands`: retained
  for compatibility, based on the selected/default profile.

The settings UI presents two selectable profile rows before generating the
token. After generation it shows the chosen command first and keeps the alternate
command available.

## Browser Approval Scope

Browser approval is implemented for Codex chat by the `codex-app-server` runner.
The Bridge sends `internal/protocol.TypeApprovalRequest` to Hub, Hub broadcasts
it to the browser session, and the browser returns
`internal/protocol.TypeApprovalResponse` to the same Bridge session.

Claude Code orchestration uses `internal/bridge/orchestration.go:runClaude` to
write a temporary MCP config and launch `claude --permission-prompt-tool`. The
MCP subprocess runs `codex-bridge claude-approval-mcp --socket <path>`, forwards
the permission prompt to the parent Bridge process over a Unix socket, and the
parent reuses the same `approval_request` / `approval_response` frames with
`payload.runId` and `payload.turnId`.

Codex orchestration uses `internal/bridge/orchestration.go:runCodexAppServer`
when the selected endpoint is not auto-execute. The app-server runner emits
`approval_request` with `payload.runId` and the orchestration turn id in
`payload.promptId`, and the browser response is routed back to the same Bridge
run. It also normalizes turn input to valid UTF-8 before `turn/start`, matching
the `codex exec` runner's prompt handling so relay handoffs cannot fail before a
visible CLI response because of invalid bytes. Hub rejects review-required
orchestration if the online Bridge does not advertise browser approval support
for both Claude and Codex.

## Implementation Steps

1. Add profile constants and command option generation in
   `internal/hub/server.go`.
2. Extend `POST /api/bridge-tokens` request/response handling.
3. Add integration coverage for both generated commands.
4. Update `frontend/src/app/App.tsx` settings UI to show both profiles.
5. Add `internal/bridge/appserver_runner.go` and approval request/response
   frames for Codex chat.
6. Add run-scoped orchestration approval routing and the Claude MCP permission
   bridge.
7. Reuse the app-server runner for review-required Codex orchestration turns.
8. Add Bridge capability reporting, frontend capability matrix, and Hub policy
   guards to prevent silent approval downgrades.
9. Rebuild `internal/web/static/`.
10. Update README and workflow docs.

## Exit Gates

- `/usr/local/go/bin/go test ./...`
- `npm run build`
- `make doc-lint`

## Reviewer Q&A

**Q: Does review-required mean users approve in the browser?**

Yes. Codex chat approvals are session-scoped. Codex and Claude Code
orchestration approvals are run-scoped and are shown on the orchestration
timeline.

**Q: Why keep auto execute?**

The product is currently positioned for trusted single-user/private-machine
access. Auto execute gives the intended browser-first remote workflow, while the
conservative profile gives users an explicit safer option.
