# Codex Bridge Plan

## Goal

Let a single user talk from any browser to Codex CLI running on a private machine.

## Resource Budget

- One public Hub process and one private Bridge process
- SQLite with a single open DB connection
- One reverse WebSocket between Bridge and Hub
- Native browser UI embedded with `go:embed`
- No React/Vite runtime, Redis, Postgres, queue, vector store, or file projection

## Phases

| Phase | Target | Status |
| --- | --- | --- |
| P0 | Echo runner over reverse WebSocket | implemented |
| P1 | `codex exec --json` runner | implemented |
| P2 | Multiple `sid` sessions over one Bridge connection | implemented |
| P3 | SQLite users, agents, sessions, messages | implemented |
| P4 | Cookie JWT login and Caddy/systemd deployment files | implemented |
| P5 | Heartbeat, reconnect, cancel, close-session cleanup | partial |
| P6 | Browser permission prompts over app-server | deferred |

## Follow-up

The current runner uses short-lived `codex exec` processes and resumes with the returned Codex thread id. A deeper integration should add a second runner backed by `codex app-server`, using `initialize`, `thread/start`, `turn/start`, streamed `item/agentMessage/delta`, and `turn/interrupt`.

