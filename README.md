# Codex Bridge

Remote browser access to the **Codex** and **Claude Code** CLIs running on your
own machine — 1:1 chat with a single CLI, plus multi-CLI orchestration that
relays turns between a native Codex session and a native Claude Code session.

[简体中文](README.zh-CN.md) · [Deployment Guide](docs/deployment.md) · [Architecture](docs/architecture.md)

```text
Browser ──WSS──> Hub (public) <──reverse WS── Bridge (your machine) ──> Codex / Claude
```

## Features

- One Go binary, two modes: `hub` (public server) and `bridge` (your machine).
- Hub serves HTTPS/WSS behind a reverse proxy, embeds the static web UI, and
  stores history in SQLite — no Redis, Postgres, or external build pipeline.
- Bridge reverse-connects to the Hub and runs a selectable runner: `echo`,
  `codex exec --json`, the `codex app-server` runner, or the `acp` runner
  (resident Agent session + native local `/resume` takeover).
- Multi-CLI orchestration relays turns between native Codex and Claude Code
  sessions, with browser-side command/file approvals in review-required mode.
- Closing the browser tab does not stop an active run by default; reopen the
  same session to see persisted output.

## Requirements

| To do this | You need |
| --- | --- |
| Build from source | Go 1.25+, Node 20+ (for the web UI) |
| Run a Bridge | Codex CLI and/or Claude Code installed and authenticated |
| Run a production Hub | A TLS-terminating reverse proxy (Caddy config provided) |

> The web UI is compiled into the binary. When building from source, build the
> frontend first (`make frontend` / `make build-all`) so the embedded assets are
> current.

## Get the code

```bash
git clone https://github.com/Atingaii/Codex-bridge.git
cd Codex-bridge
```

## Quick Start (from source)

```bash
# 1. Config
cp configs/dev.yaml.example configs/dev.yaml
# Edit configs/dev.yaml: set auth.bootstrap_password and a strong auth.jwt_secret.

# 2. Create a login user and an enroll token
go run . user --username admin --password 'change-me'
go run . enroll
# Put the printed token into configs/dev.yaml under bridge.token

# 3. Run Hub and Bridge (two terminals)
make run-hub      # or: go run . hub
make run-bridge   # or: go run . bridge
```

Open <http://127.0.0.1:8088>.

For Codex instead of echo, set `bridge.runner` in `configs/dev.yaml`:

```yaml
bridge:
  runner: codex
  cwd: /path/to/workspace
  sandbox: danger-full-access
  approval_policy: never
```

For a resident-session chat backed by an Agent Client Protocol (ACP) adapter
(keeps one Agent process alive across turns and exposes a native local
`/resume` takeover), use the `acp` runner:

```yaml
bridge:
  runner: acp
  cwd: /path/to/workspace
  acp:
    cli: claude            # claude | codex
    claude_command: npx
    claude_args: ["-y", "@zed-industries/claude-code-acp"]
    codex_command: codex-acp
    prefer_native_resume: true
```

See [docs/features/acp-runner.md](docs/features/acp-runner.md) for the dual-ID
model and how local `claude --resume` / `codex resume` takeover works.

## Build & Install

```bash
make build-all                 # build the web UI, then the Go binary -> bin/codex-bridge
./bin/codex-bridge hub
sudo make install              # optional: install to /usr/local/bin/codex-bridge
make help                      # list all targets
```

## Deployment

| Method | When to use | Where |
| --- | --- | --- |
| From source | Local development, single host | [Quick Start](#quick-start-from-source) |
| `make` binary | Reproducible local/staging build | [Build & Install](#build--install) |
| Docker | Containerized Hub | [docs/deployment.md](docs/deployment.md#option-c--docker) |
| systemd + Caddy | Production with TLS | [docs/deployment.md](docs/deployment.md#option-d--production-systemd--caddy) |

The full multi-method guide — prerequisites, production config, verification,
and troubleshooting — is in **[docs/deployment.md](docs/deployment.md)**.

### Production (systemd + Caddy), at a glance

```bash
make build-all
sudo make install
sudo mkdir -p /opt/codex-bridge/configs /opt/codex-bridge/data
sudo cp configs/dev.yaml.example /opt/codex-bridge/configs/prod.yaml   # then edit for prod
sudo cp deploy/Caddyfile /etc/caddy/Caddyfile                          # edit the domain first
sudo cp deploy/systemd-hub.service /etc/systemd/system/codex-bridge-hub.service
sudo cp deploy/systemd-bridge.service /etc/systemd/system/codex-bridge-bridge.service
sudo systemctl daemon-reload && sudo systemctl enable --now codex-bridge-hub codex-bridge-bridge
sudo systemctl reload caddy
```

Production config keys (`/opt/codex-bridge/configs/prod.yaml`): `gateway.host:
127.0.0.1`, `gateway.port: 8088`, `hub.cookie_secure: true`, a fresh
`auth.jwt_secret`, `bridge.hub_url: https://<your-domain>`, and `bridge.token` /
`bridge.token_file`. See the [deployment guide](docs/deployment.md) for details.

## Commands

```bash
codex-bridge hub                  # public Hub server
codex-bridge user --username admin --password '...'   # create/update a login user
codex-bridge enroll --ttl 24h     # mint an enroll token for a new endpoint

codex-bridge link <token>         # endpoint: install + run as a background service (recommended)
codex-bridge connect <token>      # endpoint: run in the foreground (used internally by `link`)
codex-bridge bridge               # endpoint: run from configs/<env>.yaml (advanced/dev)
```

For a real endpoint, use the single command the Hub **Settings** page generates;
it runs `codex-bridge link`. `connect` and `bridge` are lower-level entry points.

Browser self-registration is disabled. Create or update approved users with
`codex-bridge user --username <name> --password <password>`.

After login, create a CLI token in Settings and copy the single install-and-connect
command from the target workspace, as the same OS user that runs Codex CLI and
Claude Code. The command writes logs under `~/.codex-bridge/logs/` and only
prints `codex-bridge connected` after the Bridge logs `[bridge] connected`;
otherwise it prints recent log lines for diagnosis. It preserves `HOME`,
`PATH`, `CODEX_HOME`, Claude config location variables, resolved Codex/Claude
CLI paths, common model credentials, and common proxy variables for the
background service so WSL/Linux shells that need a custom CLI, native history
home, or proxy path keep working after `systemd --user` starts the Bridge.
Generated user services keep the Bridge parent alive if a memory-heavy child
process is OOM-killed. If either CLI is missing from the shell running the
command, setup exits before registering an unusable endpoint.

The Settings flow offers two permission profiles. Review required uses the
Codex app-server runner so Codex command/file approval requests appear in the
browser for chat and orchestration. Auto execute keeps the previous
trusted-machine mode with `danger-full-access` and no prompts. Claude Code
orchestration also uses browser-side approval in review-required mode through
Claude Code's permission prompt MCP hook. Hub-managed orchestration uses the
selected Bridge connection for the whole run, alternates direct Claude Code and
Codex CLI turns, carries compact turn summaries forward, and shows each
endpoint's approval capabilities before a run starts. The orchestration page
also has a `default` / `formal-proof` profile selector; formal-proof guidance is
explicit opt-in and is persisted with the run. Orchestration timelines use typed
events with `source`, `severity`, command payloads, and one structured final
conclusion so Bridge notes and CLI output remain distinguishable.
Existing endpoints can be expanded under Settings -> Agents & Runtime to
generate a repair command. That command downloads the current Bridge binary and
reconnects the same endpoint with its existing machine id, name, and known
working directory. Deleting an online endpoint asks its local Bridge to stop the
matching generated user service and exit before the Hub hides the endpoint and
revokes consumed enroll tokens.

## Android APK

The Android wrapper uses Capacitor and points at `https://sparkapi.tech`.

```bash
cd frontend
npm run android:build

cd ../android
ANDROID_HOME=/usr/lib/android-sdk \
ANDROID_SDK_ROOT=/usr/lib/android-sdk \
JAVA_HOME=/usr/lib/jvm/java-21-openjdk-amd64 \
./gradlew assembleDebug
```

The debug APK is written to `android/app/build/outputs/apk/debug/app-debug.apk`.

## Configuration

Config loads from `configs/${APP_ENV:-dev}.yaml`, then selected environment variables override it. Set `CODEX_BRIDGE_CONFIG_DIR` to read config files from another directory.

Common overrides:

- `APP_HOST`, `APP_PORT`
- `HUB_DB_PATH`, `HUB_COOKIE_SECURE`
- `JWT_SECRET`, `HUB_USERNAME`, `HUB_PASSWORD`
- `BRIDGE_HUB_URL`, `BRIDGE_TOKEN`, `BRIDGE_TOKEN_FILE`
- `BRIDGE_NAME`, `BRIDGE_CWD`, `BRIDGE_RUNNER`, `BRIDGE_MODEL`
- `BRIDGE_SANDBOX`, `BRIDGE_APPROVAL_POLICY`
- `BRIDGE_LONG_COMMAND_OBSERVER_ENABLED`, `BRIDGE_LONG_COMMAND_OBSERVER_AFTER`
- `LOG_LEVEL`, `LOG_FORMAT`

Useful session behavior:

```yaml
hub:
  # false keeps Bridge/Codex work running after the browser tab is closed.
  # Set true only if closing the last browser tab should kill the backend session.
  browser_close_session: false
```

Optional orchestration long-command observation is configured under
`bridge.long_command_observer`; see `docs/dev-workflow.md` for the full env and
YAML reference.

## Notes

`codex exec --json` remains the automated trusted-machine runner. The
review-required profile uses the `codex app-server` JSON-RPC runner so approval
requests can round-trip through Hub and the browser for both chat and Codex
orchestration.
Run setup/repair commands from the same shell where Codex and Claude credentials
work; the generated background service preserves common model credential
variables such as `OPENAI_API_KEY` and `CLAUDE_CODE_OAUTH_TOKEN` in its private
0600 env file.

## Documentation

- [Deployment Guide](docs/deployment.md) — all deployment methods, production setup, troubleshooting
- [Architecture](docs/architecture.md) — components, data flow, protocol
- [Developer Workflow](docs/dev-workflow.md) — full env/YAML reference, local dev
- [Code Map](docs/code-map.md) — "change X → edit Y" guidance
- [Feature designs](docs/features/) — per-feature design docs

## Project Workflow

Engineering rules are documented in `AGENTS.md`. The short version:

- Non-trivial behavior changes need an ADR or `docs/features/` design first.
- Check `docs/change-impact.md` for coupled docs/tests before submitting.
- Commit messages use a final `Doc-Impact: ...` footer.
- Run `make doc-lint` when documentation, env vars, anchors, or rules change.
