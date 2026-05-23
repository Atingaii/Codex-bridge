# Development Workflow

## Requirements

- Go 1.22+
- Node.js 20+ only when rebuilding the frontend or Android wrapper
- Android SDK + JDK 21 only when building APKs
- Codex CLI on the private Bridge machine when `bridge.runner=codex`

## Environment Variables

This table is the single detailed source for environment variables. README files
should list names and point here for detail.

| Variable | Purpose | Default/source |
| --- | --- | --- |
| `APP_ENV` | Selects `configs/<env>.yaml` | `dev` |
| `CODEX_BRIDGE_CONFIG_DIR` | Alternate config directory | `configs` |
| `APP_HOST` | Hub listen host | config `gateway.host` |
| `APP_PORT` | Hub listen port | config `gateway.port` |
| `HUB_DB_PATH` | SQLite database path | config `hub.db_path` |
| `HUB_COOKIE_SECURE` | Force secure auth cookie | config `hub.cookie_secure` |
| `HUB_BRIDGE_DOWNLOAD_URL` | Optional external CLI binary URL for `/install.sh`; empty uses the current Hub binary | config `hub.bridge_download_url` |
| `JWT_SECRET` | JWT signing secret, 32+ bytes | config `auth.jwt_secret` |
| `HUB_USERNAME` | Bootstrap/default username | config `auth.bootstrap_username` |
| `HUB_PASSWORD` | Bootstrap/default password | config `auth.bootstrap_password` |
| `BRIDGE_HUB_URL` | Hub URL used by Bridge | config `bridge.hub_url` |
| `BRIDGE_TOKEN` | Bridge enroll token | config `bridge.token` |
| `BRIDGE_TOKEN_FILE` | File containing Bridge token | config `bridge.token_file` |
| `BRIDGE_NAME` | Agent display name | config `bridge.name` |
| `BRIDGE_CWD` | Default workspace path for runner | config `bridge.cwd` |
| `BRIDGE_RUNNER` | `echo` or `codex` | config `bridge.runner` |
| `BRIDGE_MODEL` | Model argument for Codex runner | config `bridge.model` |
| `BRIDGE_SANDBOX` | Codex sandbox policy | config `bridge.sandbox` |
| `BRIDGE_APPROVAL_POLICY` | Codex approval policy | config `bridge.approval_policy` |
| `LOG_LEVEL` | `debug`, `info`, `warn`, `error` | config `observability.log_level` |
| `LOG_FORMAT` | `console` or `json` | config `observability.log_format` |

## Local Smoke Flow

```bash
cp configs/dev.yaml.example configs/dev.yaml
# edit configs/dev.yaml: auth.jwt_secret must be 32+ bytes

/usr/local/go/bin/go run . user --username admin --password 'change-me'
/usr/local/go/bin/go run . enroll
# copy the printed token to bridge.token in configs/dev.yaml

/usr/local/go/bin/go run . hub
/usr/local/go/bin/go run . bridge
```

Open `http://127.0.0.1:8088`.

For deterministic tests use `bridge.runner=echo`. For real Codex:

```yaml
bridge:
  runner: codex
  cwd: /path/to/workspace
  sandbox: danger-full-access
  approval_policy: never
```

## Frontend Build

The Go binary serves `internal/web/static/`. If you edit `frontend/`, rebuild
that output before final verification:

```bash
cd frontend
npm run build
cd ..
```

Then run Go verification:

```bash
/usr/local/go/bin/go test ./...
/usr/local/go/bin/go build -ldflags "-s -w" -o bin/codex-bridge .
```

## Documentation Check

```bash
make doc-lint
```

The doc linter checks required docs, basic code anchors, and environment
variable references. It is intentionally lightweight and should not replace
review.

## Commit Messages

```text
<type>(<scope>): <short summary>

Change summary:
- <path>: <what changed>

Exit gate:
- [x] <test or manual verification>

Doc-Impact: none
```

Allowed `type`: `feat`, `fix`, `docs`, `refactor`, `perf`, `test`, `chore`.

Use a path list instead of `none` when docs changed or should change:

```text
Doc-Impact: docs/dev-workflow.md, AGENTS.md
```

## Security Notes

- Do not commit `configs/*.yaml`; only `configs/*.yaml.example` belongs in git.
- Do not commit real enroll tokens, JWT secrets, OpenAI keys, or private host
  names.
- Put private ticket/security scan notes in `docs/private/`; that directory is
  ignored by git.
- Do not log passwords, API keys, enroll tokens, or browser cookies.
