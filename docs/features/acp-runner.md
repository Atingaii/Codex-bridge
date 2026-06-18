# ACP Runner (long-lived interactive sessions over the Agent Client Protocol)

## Goal

Replace the per-turn, one-shot headless CLI spawn (`codex exec --json` /
`claude --print`) in browser chat with a real interactive long session backed by
an Agent Client Protocol (ACP) adapter, and make that same conversation
recoverable from the workspace with the native CLI's own `resume` command.

Two product targets, both required:

- **A — interactive long session.** A browser chat opens a session that maps to
  one resident ACP adapter process per chat session. Prompts are streamed into
  the live session and streamed back, without restarting the process between
  turns.
- **B — local takeover.** The user can copy a command from the browser and run
  it in the same workspace (`claude --resume <id>` / `codex resume <id>`) to
  continue the same conversation in the native TUI, with visible history.

This document covers PR-1: the Bridge-side ACP runner that can be verified for a
single CLI chat plus local resume. Orchestration reuse and frontend takeover UI
land in PR-2.

## Non-Goals

- No embedded TUI in the browser.
- No removal or behavior change of existing `echo`, `codex-exec`, or
  `codex-app-server` runners. They stay fully backward compatible.
- No change to the Hub<->Browser WebSocket frame *set*; existing frames are
  reused and only optional fields are added.
- PR-1 does not change orchestration; that is PR-2.
- We do not ship the ACP adapters themselves. The user installs them
  (`@zed-industries/claude-code-acp` via `npx`, `codex-acp` binary for Codex).

## Current State

- Chat: `internal/bridge/session.go:Prompt` calls
  `Runner.Prompt` once per browser turn. `CodexExecRunner` and
  `CodexAppServerRunner` each spawn a process per prompt (the app-server runner
  keeps the process only for the duration of one turn, then closes it).
- Continuity is carried by `sessions.remote_thread_id` round-tripped through
  `open_session` -> Bridge -> `prompt_complete`.
- These per-turn processes are not discoverable by the native CLI `resume`
  list, so target B is currently impossible.

## Design

### Transport topology (unchanged frames)

```text
Browser --WSS--> Hub --reverse WS (Envelope)--> Bridge --stdio (ACP JSON-RPC)--> ACP adapter --> Codex/Claude
```

The ACP runner is a new `Runner` implementation living beside the existing ones.
Selecting it is opt-in via `bridge.runner: acp`.

### Dual-ID model (the heart of target B)

| ID | Source | Used for |
| --- | --- | --- |
| `acpSessionId` | returned by the ACP adapter (`session/new`/`session/load`) | continuing the conversation from the browser (target A) |
| `nativeResumeId` | the underlying CLI's own session id | local `resume` from the workspace (target B) |

Probe finding: the Claude ACP adapter's ACP `sessionId` is the same UUID as its
native `.jsonl` session id under `~/.claude/projects/<cwd>/<uuid>.jsonl`. So for
Claude the two ids are naturally equal and B is nearly free. For Codex we prefer
the ACP-reported id and fall back to scanning `~/.codex/sessions/`.

`remote_thread_id` (already persisted by Hub) stores the **acpSessionId** so the
existing continuity plumbing keeps working unchanged. `nativeResumeId` is a new
optional field surfaced to the browser so PR-2 can render a takeover command.

### SessionRunner interface (extends Runner)

```go
type SessionRunner interface {
    Runner // Name(), Prompt(), Close()

    OpenSession(ctx, OpenSessionRequest) (SessionHandle, error)
    Resume(ctx, ResumeRequest) (SessionHandle, error)
    PromptSession(ctx, PromptSessionRequest, onUpdate) (RunnerResult, error)
    CloseSession(sid string)
}
```

Only `ACPRunner` implements `SessionRunner`. Callers use a type assertion: if a
runner is a `SessionRunner`, use the long-session path; otherwise fall back to
the existing one-shot `Prompt` path. This keeps old runners untouched.

`SessionHandle` carries both ids plus the resolved native resume command string
(or a reason it is unavailable, so the UI never fabricates a command).

### ACP JSON-RPC client

`internal/bridge/acp_client.go` is a generic bidirectional stdio JSON-RPC
channel modeled on `internal/bridge/appserver_runner.go:appServerClient`. It:

- spawns the adapter process and keeps it resident;
- performs the `initialize` handshake and records agent capabilities
  (`loadSession`, prompt capabilities, etc.);
- issues `session/new`, `session/load`, `session/prompt`, `session/cancel`;
- consumes agent->client `session/update` streaming notifications and maps them
  to `RunnerUpdate`/`RunnerToolEvent`;
- handles the agent->client reverse request `session/request_permission` by
  routing it through the existing `ApprovalRequester` browser approval channel.

### Native resume resolution

- Claude: `nativeResumeId = acpSessionId`; command is
  `claude --resume <id>` run from the absolute cwd.
- Codex: prefer an ACP-reported native/thread id; if absent, scan
  `~/.codex/sessions/` for the most recent rollout matching the cwd; command is
  `codex resume <id>`.
- Three honest degradations, never a fabricated command: adapter missing,
  native id not resolvable, cwd mismatch. Each is reported explicitly.

### cwd alignment

cwd is `filepath.Abs`-normalized and must match the cwd recorded at link time,
otherwise the native `resume` list will not show the session. The runner forces
alignment and reports a mismatch rather than emitting a wrong command.

## Implementation Steps (PR-1)

1. Protocol: add optional `NativeResumeID` / `NativeResumeCommand` to
   `SessionOpenedPayload` and `PromptCompletePayload`; add ACP availability +
   `loadSession`/resume support bits to `BridgeCapabilities`.
2. Config: add `acp` to allowed `bridge.runner`; add `bridge.acp.*` block
   (adapter commands/args, `prefer_native_resume`).
3. `acp_client.go`: generic ACP stdio JSON-RPC client.
4. `acp_runner.go`: `SessionRunner` interface in `runner.go` + `ACPRunner`
   implementation; `NewRunner` `case "acp"`.
5. link/connect: support `--runner acp`, preflight the selected adapter command
   with `command -v`, generate `--runner acp` in Settings command.
6. Tests + build + `make doc-lint` + doc sweep.

## Exit Gates

- [ ] `go test ./...` passes.
- [ ] `go build ./...` passes.
- [ ] `make doc-lint` passes.
- [ ] Unit tests cover: ACP `session/update` -> `RunnerUpdate` mapping, dual-id
      resolution, and old-runner fallback (type assertion) logic.
- [ ] Manual (user, off-sandbox): single-CLI ACP chat in the browser + local
      `resume` of the same conversation.

## Reviewer Q&A

**Q: Why not delete the old runners?** R5 (deprecate, do not delete) and the
runner-boundary invariant. ACP is additive and opt-in.

**Q: Why reuse `remote_thread_id` for the ACP session id?** It keeps the entire
existing continuity path (open_session -> prompt_complete -> store) working with
zero schema change. The native resume id is purely additive.

**Q: What if the sandbox cannot test end to end?** Correct — no `codex`/`claude`
CLIs or API keys exist in the sandbox. PR-1 guarantees logic correctness via
unit tests; the real browser->CLI->resume loop is verified by the user on their
machine. All three degradations surface honestly instead of faking a command.
