# ProofBridge Brand Rename

## Goal

Rename the user-facing product and repository references to ProofBridge while
preserving compatibility with existing installations.

## Non-Goals

- Do not rename the `codex-bridge` executable, Go module, service units, config
  paths, environment variables, storage keys, database files, or API routes.
- Do not migrate sessions, agents, orchestration runs, or Bridge enrollment.
- Do not change `sparkon.cn`.
- Do not reactivate or require maintenance of the archived Android wrapper.

## Data And Protocol Impact

None. This is a brand and repository-reference update. Existing identifiers and
wire behavior remain unchanged.

## Implementation Steps

1. Update visible product names across Hub UI, PWA, CLI messages, and
   documentation.
2. Update repository links and release branding to `Atingaii/ProofBridge`.
3. Rebuild the embedded UI.
4. Rename the GitHub repository, publish the next release, and deploy the Hub.

## Exit Gates

- No tracked references to the retired spaced or mixed-case brand variants
  remain.
- Lowercase compatibility identifiers remain unchanged.
- Frontend tests/build, Go tests/build, and docs lint pass. Android is archived
  and explicitly excluded from release validation.
- Production health and desktop/mobile browser smoke checks pass.

## Reviewer Q&A

**Why retain the old executable name?** Existing Bridge services, install
commands, update scripts, and filesystem state depend on `codex-bridge`.

**Does the rename interrupt tasks?** The code change does not. Deploying the Hub
requires a brief service restart, so active runs must be checked before restart.
