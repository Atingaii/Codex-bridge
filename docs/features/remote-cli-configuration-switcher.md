# Remote CLI Configuration Switcher

## Goals

- Manage remembered Codex and Claude Code presets from Hub using a name, model,
  base URL, and API key.
- Edit saved presets without exposing their encrypted API keys to the browser;
  a blank key keeps the saved credential and a newly entered key replaces it.
- Test from the target Bridge, normalize common `/v1` and terminal endpoint
  suffixes, and return a selectable model list when available.
- Apply native configuration or restore official-login-compatible defaults
  without changing MCP, skills, sessions, workspaces, or unrelated settings.

## Non-Goals

- No task-level provider pinning or different providers for Codex A/Codex B.
- No official login flow, arbitrary environment variables, or Redis.

## Data And Protocol Impact

- `internal/store.Store.Migrate` adds machine-scoped encrypted presets.
- `internal/protocol.Envelope` adds configuration test/apply/reset controls and
  results plus a capability/public-key advertisement.
- `internal/hub/cli_config.go`, `internal/bridge/cli_config.go`, and
  `frontend/src/app/components/CLIConfigSwitcher.tsx` implement the flow.

## Implementation

Bridge derives a small candidate set from the submitted URL, strips known
`/models`, `/chat/completions`, and `/responses` suffixes, and probes candidates
serially with short timeouts. A successful models endpoint returns sorted,
deduplicated IDs; unsupported model listing can fall back to a minimal request
when a model was supplied.

Writes are locked, backed up, written to a temporary user-only file, and
atomically renamed. Codex updates its model/provider section and native
`~/.codex/auth.json` credential file. Claude Code parses JSON and updates only the
managed model and connection environment fields. Official reset removes only
those managed fields. Active processes are left alone.

Preset updates are ownership-scoped by user, agent, and preset ID. The Hub can
reuse the existing Bridge-encrypted secret for connection testing and saving,
but never serializes that secret to the browser. Editing does not implicitly
apply a preset. Changing an active preset's URL, model, or credential clears its
active marker until the user applies it again; renaming alone preserves it.

## Exit Gates

- [x] Hub never receives plaintext API keys.
- [x] Saved presets can be edited while a blank API Key retains the encrypted
  credential without returning it to the browser.
- [x] URL normalization and model discovery are covered by tests.
- [x] Apply/reset preserves unrelated TOML/JSON configuration.
- [x] Legacy Bridges retain existing chat and orchestration behavior.
- [x] Codex + Codex UI states that both workers share one configuration.
- [x] Embedded frontend and Go/frontend test suites pass.

## Reviewer Q&A

**Why native files?** This is intentionally a remote equivalent of a local AI
switcher and keeps direct local CLI use consistent with Hub.

**What happens to running work?** Nothing is restarted. If a resident process
later restarts and reads the new native configuration, that is accepted.
