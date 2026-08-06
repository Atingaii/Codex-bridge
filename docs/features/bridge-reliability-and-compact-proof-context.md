# Bridge Reliability And Compact Proof Context

## Goals

- Let an enrolled Bridge reconnect after the bootstrap token expires, while
  keeping that credential permanently bound to its original machine id.
- Expose online Bridge version and connection time through the existing
  authenticated agent list so users can distinguish a healthy Hub from a
  usable CLI endpoint.
- Reduce orchestration prompt size by avoiding redundant replay into the same
  native CLI session and by preferring the latest cross-worker handoff.
- Keep `proof-notes.md` as the only formal-proof ledger while bounding repeated
  follow-up growth.

## Non-Goals

- No new HTTP route, WebSocket frame, SQLite table, or column.
- No relaxation for unused expired enroll tokens or tokens presented by a
  different machine id.
- No change to user ownership filters, orchestration roles, turn counts,
  runner selection, or native session continuity.
- No return to generated check scripts or multi-file Proof Harness metadata.

## Current State

`internal/store/store.go:ConsumeEnrollTokenInfo` checks expiry before checking
whether a token was already bound to the presenting machine. Bridge keeps the
same token for reconnects, so every endpoint eventually enters an endless
`invalid enroll token` retry loop after the bootstrap TTL.

`internal/protocol.RegisterPayload` already carries the Bridge version, but
Hub discards it after registration. `/api/agents` therefore reports only a
boolean online state and capabilities.

Relay prompts cap local history, but still replay earlier output to a worker
whose native session already contains it. Formal-proof follow-ups append to one
document without a request-count or size bound.

## Design

### Machine-Bound Reconnect

Token validation first loads the token and its binding. The result is:

| Token state | Presenting machine | Result |
| --- | --- | --- |
| Unused and unexpired | Any valid id | Bind and accept |
| Unused and expired | Any | Reject expired |
| Bound | Same id | Accept, even after bootstrap expiry |
| Bound | Different id | Reject consumed |
| Revoked by endpoint deletion | Any | Reject consumed |

Expiry therefore limits initial enrollment, while the existing
`used_by_machine` field acts as a durable reconnect binding. User ownership is
still loaded from the same token row, and endpoint deletion continues to revoke
the binding.

### Online Metadata

`internal/hub/pool.go:BridgeConn` retains the registration version and a Hub
connection timestamp for the lifetime of that socket. The authenticated
`GET /api/agents` response adds optional `version` and `connectedAt` fields only
for online connections. No private token, path, or cross-user record is added.
The existing agent selector shows these values for the selected endpoint and
warns when it is offline.

### Compact Relay

When composing a prompt for a worker with an existing native session, Bridge
does not replay that same worker's older visible result. It forwards the latest
turn from another worker, plus bounded command evidence, and falls back to the
latest available turn only when no cross-worker handoff exists. Resumed Hub
context remains capped and the latest user task remains authoritative.

### Bounded Proof Notes

`proof-notes.md` keeps its stable sections. Follow-up requests are maintained
inside one `后续请求` section with a bounded recent window, a per-request size
limit, and a compact count of older requests. The original task and all
worker-maintained proof evidence sections are preserved; Bridge only replaces
its own delimited follow-up block.

## Data And Protocol Impact

- No wire shape changes: `RegisterPayload.Version` already exists.
- `/api/agents` adds optional response-only `version` and `connectedAt` fields.
- No SQLite migration. Existing `enroll_tokens.used_by_machine` is reused.
- Existing formal-proof run cwd and `proof-notes.md` paths are unchanged.

## Implementation Steps

1. Correct machine-bound reconnect validation and add store tests.
2. Retain online Bridge metadata in Pool and expose it in the agent response.
3. Render endpoint version/connection state in the existing selector.
4. Filter redundant same-native-worker relay history.
5. Rewrite only a delimited, bounded follow-up block in `proof-notes.md`.
6. Rebuild embedded frontend and run focused plus full tests.

## Exit Gates

- A bound token reconnects from the same machine after expiry.
- Unused expired, cross-machine, and revoked tokens remain rejected.
- Authenticated agent responses expose version and connected time only while
  the endpoint is online and visible to that user.
- A returning native worker does not receive redundant copies of its own prior
  output; it still receives the latest cross-worker handoff and command evidence.
- Repeated formal-proof follow-ups keep one bounded notes document and preserve
  worker-maintained evidence.
- Frontend tests/build, `/usr/local/go/bin/go test ./...`, `make doc-lint`, and
  `git diff --check` pass.

## Reviewer Q&A

### Why may an expired token reconnect?

The token expiry remains the deadline for first use. After first use, the
credential is no longer a bearer enrollment invitation: it is bound to one
machine id and behaves as that endpoint's reconnect credential. A different
machine cannot use it, and deleting the endpoint revokes it.

### Why not persist Bridge version in SQLite?

Version and connection time describe the active socket, not historical agent
identity. Keeping them in the connection pool avoids a schema migration and
prevents stale offline versions from looking current.

### Does bounded notes maintenance lose proof evidence?

No. Bridge rewrites only its own delimited follow-up block. The original task,
obligation, validation, blocker, and decision sections remain worker-owned and
unchanged.
