# Browser Session Lease

## Goals

- Keep a chat session's Bridge-side runner alive for a bounded lease after the
  last browser WebSocket disconnects.
- Let a browser reconnect with the same `sid` during the lease and reattach to
  the existing Bridge session instead of forcing a fresh CLI process.
- Expire the lease after a configurable TTL and then send `close_session` to the
  selected Bridge.
- Document the user-facing continuity model together with the ACP runner,
  native resume, and one-to-one `sid` binding.

## Non-Goals

- No new WebSocket frame types.
- No persistence schema change; leases are Hub memory only.
- No attempt to keep sessions alive across Hub restart. Native CLI `resume`
  remains the recovery path after process restart or TTL expiry.
- No change to explicit session deletion; delete still sends `close_session`
  immediately.

## Data And Protocol Impact

- Hub config gains `hub.browser_lease_ttl`, with environment override
  `HUB_BROWSER_LEASE_TTL`.
- Existing `hub.browser_close_session: true` keeps its legacy behavior: closing
  the last browser WebSocket sends `close_session` after
  `hub.browser_close_grace` instead of using the lease TTL.
- Browser and Bridge continue to use the existing
  `open_session`/`session_opened`/`prompt`/`close_session` frames in
  `internal/protocol/envelope.go`.
- Hub records an in-memory `leaseIdleLeased` state per disconnected `sid`.
  `internal/hub/ws_browser.go:handleBrowserWS` starts that lease when the last
  browser socket leaves.
- Reconnecting with the same `sid` calls `tryReattach` before sending the normal
  `open_session` frame. The Bridge already reuses the same live session in
  `internal/bridge/session.go:Open`, so no Bridge protocol
  change is needed.

## Implementation Steps

1. Add `BrowserLeaseTTL` to Hub config defaults, YAML examples, env loading, and
   environment documentation.
2. Add Hub-side lease state helpers in `internal/hub/`.
3. Update browser WebSocket connect/disconnect handling to start leases,
   reattach during the TTL, and expire by sending `close_session`.
4. Add integration tests for TTL expiry and same-`sid` reattach.
5. Add the concise Chinese ACP/bridge/resume/lease explanation to the Chinese
   README and link the behavior from architecture/code-map docs.

## Exit Gates

- [x] `go test ./...` passes.
- [x] `CGO_ENABLED=0 go build -ldflags "-s -w" -o bin/codex-bridge .` passes.
- [x] `make doc-lint` passes.
- [x] Browser close lease test proves `close_session` is delayed until TTL.
- [x] Browser reattach test proves same-`sid` reconnect cancels the close timer.

## Reviewer Q&A

**Q: Why keep sending `open_session` on reconnect?**

Because `open_session` is the existing reattach signal for the Bridge session
layer. When the `sid` already exists, `internal/bridge/session.go:Open`
updates the browser output channel and returns the existing `remote_thread_id`
and native resume metadata without spawning a new runner process.

**Q: What happens after the TTL expires?**

Hub sends `close_session`; Bridge releases the resident runner process for that
`sid`. After that, reopening the browser still uses the persisted Hub session
row, but the Bridge has to open a fresh live runner session. For ACP sessions,
native CLI `resume` remains available when the underlying CLI wrote local
history.

**Q: Why is the lease in memory only?**

The lease protects accidental browser tab closes and short network drops. It is
not a durable recovery mechanism; durable history belongs to Hub transcripts and
native CLI session storage.
