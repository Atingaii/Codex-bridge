# ProofBridge Portable Package

This directory is self-contained for a simple single-server bootstrap.

## Start

```bash
ADMIN_PASSWORD='change-me' APP_HOST=0.0.0.0 APP_PORT=8088 ./start.sh
```

If `ADMIN_PASSWORD` is omitted on first start, `start.sh` generates one and
writes it to `state/admin_password`.

Open the printed URL and log in with the printed admin credentials. The package
starts both Hub and a same-machine Bridge using the deterministic `echo` runner.

## Real CLI Runner

Install and authenticate Codex CLI / Claude Code on the server first, then start
with an appropriate runner:

```bash
PUBLIC_URL='https://bridge.example.com' \
BRIDGE_RUNNER=codex \
BRIDGE_CWD=/srv/workspace \
./start.sh
```

Common overrides:

- `ADMIN_USERNAME`, `ADMIN_PASSWORD`, `RESET_ADMIN_PASSWORD=1`
- `APP_HOST`, `APP_PORT`, `PUBLIC_URL`
- `BRIDGE_RUNNER`, `BRIDGE_CWD`, `BRIDGE_NAME`
- `BRIDGE_SANDBOX`, `BRIDGE_APPROVAL_POLICY`
- `LOG_LEVEL`, `LOG_FORMAT`

## Stop And Status

```bash
./status.sh
./stop.sh
```

Runtime files stay inside the package directory:

- `data/codex-bridge.db`
- `state/`
- `logs/`
- `run/`
- `work/`
