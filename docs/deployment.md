# Deployment Guide

Codex Bridge is a single Go binary with two modes:

- **Hub** — the public server. Serves HTTPS/WSS (behind a reverse proxy),
  embeds the web UI, and stores history in SQLite.
- **Bridge** — runs on the machine that has Codex CLI / Claude Code installed.
  It reverse-connects to the Hub over WebSocket and runs the selected runner.

```text
Browser ──WSS──> Hub (public) <──reverse WS── Bridge (your workstation) ──> Codex / Claude
```

You always run **one Hub** and **one or more Bridges**. This guide covers the
common ways to deploy both.

## Prerequisites

| Component | Requirement |
| --- | --- |
| Build the binary | Go 1.25+ |
| Build the web UI | Node 20+ (only needed when building from source; releases ship the UI embedded) |
| Run a Bridge | Codex CLI and/or Claude Code installed and authenticated for the OS user |
| Production Hub | A reverse proxy that terminates TLS (Caddy config is provided) |

The web UI is compiled into the binary (`internal/web/static`). When you build
from source you must build the frontend first (`make frontend` or `make
build-all`); otherwise the embedded assets are stale.

## Option A — From source (development / single host)

```bash
git clone https://github.com/Atingaii/Codex-bridge.git
cd Codex-bridge

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

## Option B — Build a binary with make

```bash
make build-all        # builds the frontend, then the Go binary -> bin/codex-bridge
./bin/codex-bridge hub
./bin/codex-bridge bridge
```

Useful targets (`make help` lists them all):

| Target | Purpose |
| --- | --- |
| `make frontend` | Build the web UI into `internal/web/static` |
| `make build` | Build the Go binary (assumes the UI is already built) |
| `make build-all` | `frontend` + `build` |
| `make portable-package` | Build a copy-to-server tarball under `dist/` |
| `make install` | Copy the binary to `$(PREFIX)/bin` (default `/usr/local/bin`) |
| `make test` | Run the Go test suite |
| `make doc-lint` | Validate docs |

## Option C — Portable package

Use this when a fresh Linux server should only need an archive upload, extract,
and one start command. The package includes the static binary, package-local
config, and scripts that initialize admin credentials, SQLite state, a same-host
Bridge token, logs, and pid files under the extracted directory.

Build the archive on the source machine:

```bash
make portable-package
# -> dist/codex-bridge-<version>-linux-amd64.tar.gz
```

Copy the archive to the target server and run:

```bash
tar -xzf codex-bridge-<version>-linux-amd64.tar.gz
cd codex-bridge-<version>-linux-amd64
ADMIN_USERNAME=admin ADMIN_PASSWORD='change-me' APP_HOST=0.0.0.0 APP_PORT=8088 ./start.sh
```

Open `http://<server-ip>:8088` and log in with the admin credentials. If
`ADMIN_PASSWORD` is omitted on first start, `start.sh` generates one and writes
it to `state/admin_password`.

The portable package defaults the same-host Bridge to `BRIDGE_RUNNER=echo` so a
new server can be verified without Codex or Claude installed. For real CLI use,
install and authenticate the target CLI as the same OS user, then start with
overrides such as:

```bash
PUBLIC_URL='https://bridge.example.com' \
BRIDGE_RUNNER=codex \
BRIDGE_CWD=/srv/workspace \
./start.sh
```

Operational commands:

```bash
./status.sh
./stop.sh
```

Runtime files remain inside the extracted directory: `data/`, `state/`,
`logs/`, `run/`, and `work/`. Use the systemd + Caddy path below when you need
host-level service management and TLS automation.

## Option D — Docker

A multi-stage `Dockerfile` builds the UI, compiles the binary, and ships a
minimal distroless image.

```bash
# Build (also available as: make docker)
docker build -t codex-bridge:local .

# Run the Hub (mount your config and a data volume)
docker run --rm -p 8088:8088 \
  -e APP_ENV=prod \
  -v "$PWD/configs:/opt/codex-bridge/configs:ro" \
  -v codex-bridge-data:/opt/codex-bridge/data \
  codex-bridge:local hub
```

The image `ENTRYPOINT` is the binary and the default `CMD` is `hub`; pass
`bridge` (or any subcommand) to change the mode. Provide config via a mounted
`configs/prod.yaml` or via the environment variables listed below.

Notes:

- A Bridge usually runs on your own workstation next to Codex/Claude, so the
  container path mainly suits the Hub. To run a Bridge in a container you must
  also make the Codex/Claude CLIs and their credentials available inside it.
- The runtime image is non-root (`distroless ... :nonroot`); ensure mounted
  volumes are writable by that user.

## Option E — Production (systemd + Caddy)

This mirrors the `sparkapi.tech` setup using the assets in `deploy/`.

```bash
# 1. Install the binary
make build-all
sudo make install                       # -> /usr/local/bin/codex-bridge

# 2. Lay out config + data
sudo mkdir -p /opt/codex-bridge/configs /opt/codex-bridge/data
sudo cp configs/dev.yaml.example /opt/codex-bridge/configs/prod.yaml

# 3. Reverse proxy (edit the domain in deploy/Caddyfile first)
sudo cp deploy/Caddyfile /etc/caddy/Caddyfile
sudo systemctl reload caddy

# 4. systemd units (edit BRIDGE_HUB_URL / domain as needed)
sudo cp deploy/systemd-hub.service /etc/systemd/system/codex-bridge-hub.service
sudo cp deploy/systemd-bridge.service /etc/systemd/system/codex-bridge-bridge.service
sudo systemctl daemon-reload
sudo systemctl enable --now codex-bridge-hub
sudo systemctl enable --now codex-bridge-bridge
```

Set these in `/opt/codex-bridge/configs/prod.yaml` for production:

- `gateway.host: 127.0.0.1`
- `gateway.port: 8088`
- `hub.cookie_secure: true`
- `auth.jwt_secret`: a fresh 32+ byte secret
- `auth.registration.enabled: false` unless a production Turnstile Managed
  widget has been created and all registration fields are configured
- `bridge.hub_url: https://<your-domain>`
- `bridge.token` or `bridge.token_file`: the enroll token

Caddy terminates HTTPS and proxies the Hub on `127.0.0.1:8088`. If a CDN
(e.g. Cloudflare) already points the domain at the host, Caddy still issues and
serves the certificate to the origin.

### Recommended endpoint flow (Settings page)

For a real endpoint you usually do **not** hand-edit the Bridge config. After
logging into the Hub, open **Settings**, create a CLI token, and copy the single
install-and-connect command. Run it from the workspace as the same OS user that
runs Codex/Claude. It installs and starts a `systemd --user` service, preserves
`HOME`, `PATH`, `CODEX_HOME`, Claude config vars, resolved CLI paths, model
credentials, and proxy variables, and only reports success after the Bridge logs
`[bridge] connected`. See the README "Commands" section for `link` / `connect` /
`bridge` details.

## Configuration reference

Config loads from `configs/${APP_ENV:-dev}.yaml`, then selected environment
variables override it. Set `CODEX_BRIDGE_CONFIG_DIR` to read config files from
another directory. Common overrides:

- `APP_HOST`, `APP_PORT`, `APP_ENV`
- `HUB_DB_PATH`, `HUB_COOKIE_SECURE`, `HUB_BROWSER_LEASE_TTL`
- `JWT_SECRET`, `HUB_USERNAME`, `HUB_PASSWORD`
- `REGISTRATION_ENABLED`, `TURNSTILE_SITE_KEY`, `TURNSTILE_SECRET`,
  `TURNSTILE_HOSTNAME`
- `BRIDGE_HUB_URL`, `BRIDGE_TOKEN`, `BRIDGE_TOKEN_FILE`
- `BRIDGE_NAME`, `BRIDGE_CWD`, `BRIDGE_RUNNER`, `BRIDGE_MODEL`
- `BRIDGE_SANDBOX`, `BRIDGE_APPROVAL_POLICY`
- `LOG_LEVEL`, `LOG_FORMAT`

The full env and YAML reference (including browser session lease TTL, the ACP
runner, and the long-command observer) lives in
[dev-workflow.md](dev-workflow.md).

## Verifying a deployment

1. Hub health: `curl -fsS http://127.0.0.1:8088/health` returns `200`.
2. Bridge connection: the Bridge logs `[bridge] connected`; the Hub logs
   `bridge connected` and the endpoint appears online under Settings.
3. Open a chat session and send a prompt; confirm streamed output.
4. When registration is enabled, open the register tab, complete the Turnstile
   challenge, and confirm the new account sees no other user's endpoints,
   sessions, runs, or shares.

## Troubleshooting

- **Endpoint never shows online** — the CLI is missing from the Bridge shell, or
  the enroll token is wrong/expired. Check `~/.codex-bridge/logs/`.
- **Stale UI after a code change** — rebuild the frontend (`make frontend`) so
  `internal/web/static` is regenerated, then rebuild the binary.
- **Cookies rejected over HTTPS** — set `hub.cookie_secure: true` only when
  serving over TLS; keep it `false` for plain-HTTP local dev.
- **Registration tab is hidden** — registration fails closed unless enabled and
  both Turnstile keys are non-empty. Check the widget hostname and set
  `TURNSTILE_HOSTNAME` to the hostname Siteverify returns in production.
