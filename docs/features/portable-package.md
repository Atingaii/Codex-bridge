# Portable Package Deployment

## Goals

- Produce a compressed Linux amd64 package that can be copied to a fresh server,
  extracted, and started with one script.
- Include the embedded-UI `codex-bridge` binary, a package-local config file,
  and runtime scripts for start/stop/status.
- On first start, initialize package-local state: SQLite database, JWT secret,
  admin login user, Bridge enroll token, machine id, logs, and pid files.
- Run Hub and a same-machine Bridge by default so a fresh install can be
  verified immediately with the deterministic `echo` runner.
- Allow operators to provide admin credentials and common runtime settings with
  environment variables without editing source files.

## Non-Goals

- No new wire protocol, HTTP API, WebSocket frame, or SQLite schema.
- No replacement for the production systemd + Caddy deployment. The portable
  package is a simple single-directory bootstrap path.
- No bundled Codex CLI, Claude Code CLI, Node.js, or model credentials. Real CLI
  execution still depends on tools installed on the target server.
- No automatic TLS or reverse proxy setup.

## Data And Protocol Impact

- Uses existing config keys through `configs/portable.yaml` and environment
  overrides already handled by `internal/config/load.go`.
- Uses existing CLI commands:
  - `codex-bridge user --username ... --password ...`
  - `codex-bridge enroll --token ... --ttl ...`
  - `codex-bridge hub`
  - `codex-bridge bridge`
- Runtime state is local to the extracted package directory:
  - `data/codex-bridge.db`
  - `state/jwt_secret`
  - `state/bridge.token`
  - `state/bridge_machine_id`
  - `run/*.pid`
  - `logs/*.log`

## Implementation Steps

1. Add `deploy/portable/` templates for package config, start, stop, status,
   and package-local README.
2. Add `scripts/build-portable-package.sh` to build the static binary and copy
   the portable templates into a versioned directory under `dist/`, then create
   a `.tar.gz` archive.
3. Add a `make portable-package` target.
4. Document the package path in `docs/deployment.md`, README files, and developer
   workflow notes.

## Exit Gates

- `scripts/build-portable-package.sh` creates `dist/codex-bridge-<version>-linux-amd64.tar.gz`.
- Extracting that archive into a clean temp directory and running
  `ADMIN_USERNAME=... ADMIN_PASSWORD=... APP_PORT=... ./start.sh` starts both
  Hub and Bridge.
- `curl /health` returns OK from the extracted package.
- `codex-bridge` logs show the Bridge reached `[bridge] connected`.
- `./status.sh` reports both processes running, and `./stop.sh` stops them.
- Go tests still pass.

## Reviewer Q&A

**Why default to the `echo` runner?**

It makes a fresh server package verifiable without requiring Codex or Claude
installation. Operators can set `BRIDGE_RUNNER=codex` or another supported
runner once the target server has the required CLI and credentials.

**Why not use systemd in the package script?**

The requested flow is unzip-and-run. Keeping pid files and logs in the package
directory avoids root privileges and works on minimal servers. Systemd remains
the production path in `docs/deployment.md`.

**How are secrets handled?**

`start.sh` writes generated secrets under `state/` with `0600` file permissions.
The package builder does not embed real admin passwords, enroll tokens, model
keys, or hostnames.
