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

`codex exec --json` is the first practical Codex runner because it is already available in the installed CLI and keeps the bridge simple. The runner boundary is intentionally small so a later `codex app-server` JSON-RPC adapter can replace it without changing Hub, storage, or the browser protocol.
