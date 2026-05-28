# Codex Bridge

Remote browser access for a Codex CLI running on your own machine.

## Shape

- One Go binary with two modes: `hub` and `bridge`
- Hub serves HTTPS/WSS behind Caddy, embeds the static web UI, and stores history in SQLite
- Bridge reverse-connects to Hub, multiplexes chat sessions, and runs either `echo` or `codex exec --json`
- No Redis, Postgres, React build pipeline, or file projection
- Closing the browser tab does not stop an active Codex run by default; reopen the same session to see persisted output

## Local Quick Start

```bash
cp configs/dev.yaml.example configs/dev.yaml
# edit configs/dev.yaml: set auth.bootstrap_password and a strong auth.jwt_secret

/usr/local/go/bin/go run . user --username admin --password 'change-me'
/usr/local/go/bin/go run . enroll
```

Put the printed enroll token into `configs/dev.yaml` under `bridge.token`, then run:

```bash
/usr/local/go/bin/go run . hub
/usr/local/go/bin/go run . bridge
```

Open `http://127.0.0.1:8088`.

For Codex instead of echo:

```yaml
bridge:
  runner: codex
  cwd: /path/to/workspace
  sandbox: danger-full-access
  approval_policy: never
```

## Commands

```bash
codex-bridge hub
codex-bridge bridge
codex-bridge user --username admin --password '...'
codex-bridge enroll --ttl 24h
```

Browser self-registration is disabled. Create or update approved users with
`codex-bridge user --username <name> --password <password>`.

After login, create a CLI token in Settings and copy the single install-and-connect
command. The command writes logs under `~/.codex-bridge/logs/` and only prints
`codex-bridge connected` after the Bridge logs `[bridge] connected`; otherwise
it prints recent log lines for diagnosis. It preserves `PATH`, resolved
Codex/Claude CLI paths, and common proxy variables for the background service
so WSL/Linux shells that need a custom CLI or proxy path keep working after
`systemd --user` starts the Bridge. Generated user services keep the Bridge
parent alive if a memory-heavy child process is OOM-killed. If either CLI is
missing from the shell running the command, setup exits before registering an
unusable endpoint.

The Settings flow offers two permission profiles. Review required uses the
Codex app-server runner so Codex command/file approval requests appear in the
browser for chat and orchestration. Auto execute keeps the previous
trusted-machine mode with `danger-full-access` and no prompts. Claude Code
orchestration also uses browser-side approval in review-required mode through
Claude Code's permission prompt MCP hook. Hub-managed orchestration uses the
selected Bridge connection for the whole run, alternates direct Claude Code and
Codex CLI turns, carries compact turn summaries forward, and shows each
endpoint's approval capabilities before a run starts.
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
- `LOG_LEVEL`, `LOG_FORMAT`

Useful session behavior:

```yaml
hub:
  # false keeps Bridge/Codex work running after the browser tab is closed.
  # Set true only if closing the last browser tab should kill the backend session.
  browser_close_session: false
```

## sparkapi.tech Deployment

The included Caddy config is already bound to `sparkapi.tech`:

```bash
sudo cp bin/codex-bridge /usr/local/bin/codex-bridge
sudo mkdir -p /opt/codex-bridge/configs /opt/codex-bridge/data
sudo cp configs/dev.yaml.example /opt/codex-bridge/configs/prod.yaml
sudo cp deploy/Caddyfile /etc/caddy/Caddyfile
```

For production set these in `/opt/codex-bridge/configs/prod.yaml`:

- `gateway.host: 127.0.0.1`
- `gateway.port: 8088`
- `hub.cookie_secure: true`
- `auth.jwt_secret`: a fresh 32+ byte secret
- `bridge.hub_url: https://sparkapi.tech`
- `bridge.token` or `bridge.token_file`: the enroll token

Then install the systemd units from `deploy/` and reload Caddy. With Cloudflare already pointing `sparkapi.tech` at the VPS, Caddy terminates HTTPS and proxies Hub on `127.0.0.1:8088`.

## Notes

`codex exec --json` remains the automated trusted-machine runner. The
review-required profile uses the `codex app-server` JSON-RPC runner so approval
requests can round-trip through Hub and the browser for both chat and Codex
orchestration.

## Project Workflow

Engineering rules are documented in `AGENTS.md`. The short version:

- Non-trivial behavior changes need an ADR or `docs/features/` design first.
- Check `docs/change-impact.md` for coupled docs/tests before submitting.
- Commit messages use a final `Doc-Impact: ...` footer.
- Run `make doc-lint` when documentation, env vars, anchors, or rules change.
