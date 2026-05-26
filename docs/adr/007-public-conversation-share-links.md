# ADR-007: Public Conversation Share Links

## Background

Users need ChatGPT/Claude-style share links for every conversation. A recipient
without an account should be able to open the link and read the conversation,
while all interactive product features remain behind Hub authentication.

The Hub is already the only persistence layer and owns browser auth. The Bridge
must not receive any new public inbound path and must not be asked for data when
an anonymous viewer opens a share link.

## Decision

Store share records in Hub SQLite. Each record has an unguessable public id,
the owner user id, a target kind (`chat` or `orchestration`), a target id, title
metadata, and revoke timestamps.

Authenticated users create or revoke shares through normal protected Hub API
routes. Anonymous viewers can only call a dedicated public read endpoint by
share id. The public endpoint loads persisted Hub transcript data and returns a
sanitized, read-only snapshot payload.

The browser route `/share/<id>` is an SPA route served by the existing embedded
static handler. The frontend renders that route before the login bootstrap, so
public viewers do not need `/api/me`. All non-transcript controls on the share
page are disabled or point to sign-in rather than performing workspace actions.

## Trade-offs

Share links are bearer capabilities: anyone with the id can read the shared
snapshot until it is revoked. This keeps the user experience simple and matches
common chat sharing behavior. Revocation is retained through `revoked_at`.

Public payloads are generated from current persisted rows rather than a frozen
copy at share creation time. This avoids duplicate transcript storage and means
new messages/events can appear on an existing share. A future immutable export
can add a snapshot blob without changing the public route shape.

No Bridge call is made for public reads. This avoids exposing private-machine
state and keeps anonymous traffic on Hub-only storage.

## Code Anchors

- `internal/store/store.go`: `conversation_shares` schema and CRUD
- `internal/hub/share.go`: protected share creation/revocation and public read
- `internal/hub/server.go:NewServer`: share API route registration
- `frontend/src/app/App.tsx`: share buttons and `/share/<id>` read-only route
- `docs/features/conversation-share-links.md`: user workflow and API contract

## Revisit When

- Share links need password protection, expiry, or per-recipient access logs.
- Public pages must show live streaming updates.
- Exports need immutable point-in-time snapshots.
