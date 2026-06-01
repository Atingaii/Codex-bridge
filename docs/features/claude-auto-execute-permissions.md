# Claude Auto-Execute Permissions

## Goals

- Make Claude Code orchestration honor the existing `auto-execute` profile
  without asking each user to tune local Claude settings.
- Keep auto-execute semantics aligned with Codex: `approval_policy=never` and
  `sandbox=danger-full-access` means trusted-machine execution with no browser
  approval prompts.
- Preserve the shell environment captured by the Bridge service when launching
  Claude so common binaries, credentials, proxy variables, and native CLI homes
  remain available.
- Keep review-required mode unchanged: Claude Code approval requests continue to
  route through the browser via the temporary MCP permission tool.

## Non-Goals

- Do not change Hub protocol frames or orchestration event persistence.
- Do not modify user-level `~/.claude/settings.json`.
- Do not add new permission profiles.
- Do not weaken review-required approval behavior.

## Current State

- Permission profiles are selected when the Bridge connects. The settings flow
  documents them in `docs/dev-workflow.md`.
- Codex orchestration maps `approval_policy=never` plus
  `sandbox=danger-full-access` to the Codex bypass flag in
  `internal/bridge/orchestration_codex.go:codexOrchestrationArgs`.
- Claude orchestration builds native CLI arguments in
  `internal/bridge/orchestration_claude.go:claudeArgsWithSession` and
  `internal/bridge/orchestration_claude.go:claudeArgsWithStreamInput`.
- Claude approval routing is enabled by
  `internal/bridge/orchestration_claude.go:prepareClaudeApprovalServer` when
  `internal/bridge/orchestration_claude.go:shouldBridgeClaudeApproval` returns
  true.
- Managed CLI child processes share process-group cancellation through
  `internal/bridge/command_process.go:configureManagedCommand`.

## Design

Bridge owns the runner-facing translation from the user-selected permission
profile to each native CLI's flags. Users should not need to edit Claude Code
global configuration for Bridge-created orchestration turns.

In trusted auto-execute mode, Claude orchestration always passes
`--permission-mode bypassPermissions`. When the Bridge itself is running as
root, the launched Claude child process also receives `IS_SANDBOX=1`. This
keeps the workaround scoped to Bridge-managed Claude processes and avoids
changing global Claude settings.

In review-required mode, Bridge keeps the temporary Claude approval MCP enabled
and passes `--permission-mode default` through the existing helper. Bash/file
approval requests are surfaced to the orchestration timeline for browser
approval instead of being auto-accepted.

Managed child process environment construction must inherit the Bridge
process's environment before applying runner-specific additions. This preserves
the `PATH`, `HOME`, native CLI config locations, credentials, and proxy
settings captured by install/repair commands while still allowing Bridge to add
variables such as `IS_SANDBOX=1`.

## Data And Protocol Impact

- No HTTP endpoint changes.
- No WebSocket frame changes.
- No SQLite schema changes.
- No orchestration event shape changes.
- Runtime behavior changes only inside Bridge-launched Claude Code processes.

## Implementation Steps

1. Add a shared command environment helper in
   `internal/bridge/command_process.go`.
2. Use the helper when Claude orchestration needs to append `IS_SANDBOX=1`.
3. Change Claude auto-execute argument construction to use
   `--permission-mode bypassPermissions` for all users.
4. Keep `shouldBridgeClaudeApproval` disabled only for trusted auto-execute
   mode, so review-required mode continues using browser approval.
5. Add focused tests for Claude auto-execute arguments and environment
   inheritance.
6. Run Go tests, build the binary, and run document lint.

## Exit Gates

- `/usr/local/go/bin/go test ./...`
- `CGO_ENABLED=0 /usr/local/go/bin/go build -ldflags "-s -w" -o bin/codex-bridge .`
- `make doc-lint`

## Reviewer Q&A

**Why not ask users to set Claude Code's default mode?**  
Bridge already exposes a product-level permission profile. Native CLI flags
must be derived by Bridge so all endpoints behave consistently.

**Why keep `IS_SANDBOX=1`?**  
Claude Code may require this variable before accepting bypass mode from a root
process. Setting it only on the child process keeps the workaround local to
Bridge-managed execution.

**What happens in review-required mode?**  
Nothing changes. Bridge still attaches the Claude MCP permission prompt tool and
the browser remains the approval surface.
