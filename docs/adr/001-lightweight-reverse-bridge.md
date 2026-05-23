# ADR-001: Lightweight Reverse Bridge

## Background

The target machine may sit behind NAT, have limited CPU/memory, and should keep Codex credentials local. The user still needs browser access from arbitrary devices.

## Decision

Use a public Hub and a private Bridge connected by one reverse WebSocket. Hub stores users, agents, sessions, and messages in SQLite. Bridge multiplexes sessions by `sid` and delegates prompts to a runner.

For v1, the default runner is `echo`; the Codex runner uses short-lived `codex exec --json` processes and resumes sessions with Codex's returned thread id.

## Trade-offs

Short-lived `codex exec` costs process startup per prompt, but keeps resident memory low and makes crash cleanup simple. It also works with the currently installed Codex CLI without depending on a second protocol adapter.

`codex app-server` is the better long-term deep integration surface because it streams JSON-RPC events and supports approvals. That runner is deferred until v2 so P0-P3 can stay small and deployable.

The frontend is plain HTML/CSS/JS embedded with `go:embed`. This avoids Node/Vite build memory and artifact churn, at the cost of less component abstraction.

## Code Anchors

- `internal/protocol/envelope.go`: shared frame format
- `internal/hub/ws_bridge.go`: Bridge registration and Hub-bound frames
- `internal/hub/ws_browser.go`: Browser chat WebSocket
- `internal/bridge/client.go`: reverse dial and reconnect loop
- `internal/bridge/runner.go`: echo and Codex runner boundary
- `internal/store/store.go`: SQLite schema and CRUD

## Revisit When

- Permission prompts are required.
- Multiple concurrent Codex turns per session are required.
- The CLI JSONL event format becomes too lossy for browser UI needs.
- The machine can afford a persistent `codex app-server` process per Bridge.

