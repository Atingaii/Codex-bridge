# AGENTS.md — Codex Bridge Session Brief

## Positioning

Single-user remote browser access to a Codex CLI running on a private machine.

## Architecture

- `hub`: public HTTP/WebSocket server, embedded UI, SQLite persistence
- `bridge`: private reverse WebSocket client, owns Codex credentials and workspace access
- Browser talks only to Hub; Hub never sees `OPENAI_API_KEY`
- All cross-process frames use `internal/protocol.Envelope`

## Resource Rules

- Keep Hub and Bridge as one Go module and one build artifact.
- Do not add Redis/Postgres/Node frontend build tooling unless a concrete feature requires it.
- Prefer native `net/http`, raw SQL, and embedded static files.
- Keep SQLite at one open connection unless real write contention appears.

## Main Code Map

| Area | Path |
| --- | --- |
| Config | `internal/config/` |
| Hub HTTP/API/WS | `internal/hub/` |
| Bridge reverse client | `internal/bridge/` |
| SQLite store | `internal/store/` |
| Auth/JWT | `internal/auth/` |
| Wire protocol | `internal/protocol/` |
| Embedded UI | `internal/web/static/` |

## Commands

```bash
/usr/local/go/bin/go test ./...
/usr/local/go/bin/go build -ldflags "-s -w" -o bin/codex-bridge .
```

Local smoke flow:

```bash
cp configs/dev.yaml.example configs/dev.yaml
/usr/local/go/bin/go run . user --username admin --password 'change-me'
/usr/local/go/bin/go run . enroll
/usr/local/go/bin/go run . hub
/usr/local/go/bin/go run . bridge
```

## Runner Notes

`bridge.runner=echo` is the deterministic P0 runner. `bridge.runner=codex` uses `codex exec --json` and resumes with the returned Codex thread id. A later `codex app-server` runner should stay behind `internal/bridge.Runner` and must not change Hub/browser protocol unless permission prompts are added.

