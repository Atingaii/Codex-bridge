# Conversation Share Links

## Goal

Every chat session and orchestration run can produce a share link. Opening the
link without logging in shows the conversation transcript in a read-only page.
Trying to use workspace features still requires normal login.

## Non-Goals

- Do not allow anonymous viewers to send prompts, approve commands, upload
  files, open WebSockets, manage agents, or inspect private settings.
- Do not expose Bridge credentials, enroll tokens, machine ids, remote Codex
  thread ids, or uploaded file bodies.
- Do not change chat continuity or orchestration follow-up semantics.
- Do not introduce a second persistence service.

## Current State

- Chat sessions and messages are stored in `internal/store/store.go:Session`
  and `internal/store/store.go:Message`.
- Orchestration runs and events are stored in
  `internal/store/store.go:OrchestrationRun` and
  `internal/store/store.go:OrchestrationEvent`.
- Existing transcript APIs are protected by `internal/hub/server.go:withAdmin`
  or `internal/hub/server.go:withAuth`.
- The frontend routes by `window.location.pathname` in
  `frontend/src/app/App.tsx:App`, and the static handler already falls back to
  the SPA for extensionless paths.

## Design

Add a Hub-owned `conversation_shares` table:

| Column | Purpose |
| --- | --- |
| `id` | Public bearer id generated with `store.NewToken("shr")` |
| `user_id` | User who created the share |
| `kind` | `chat` or `orchestration` |
| `target_id` | Session id or orchestration run id |
| `title` | Display title captured or refreshed when sharing |
| `created_at` | Creation timestamp |
| `updated_at` | Last share metadata update timestamp |
| `revoked_at` | Null/zero means public link is active |

Protected APIs:

- `POST /api/sessions/{sid}/share`
- `POST /api/orchestrations/{runID}/share`
- `DELETE /api/shares/{shareID}`

Public API:

- `GET /api/public/shares/{shareID}`

The public response is normalized:

```json
{
  "share": {
    "id": "shr_...",
    "kind": "chat",
    "title": "Conversation title",
    "createdAt": 1770000000,
    "updatedAt": 1770000010
  },
  "session": { "id": "ses_...", "title": "Conversation title", "createdAt": 1770000000, "updatedAt": 1770000010 },
  "messages": []
}
```

For orchestration shares, the payload uses `run` and `events` instead of
`session` and `messages`.

Public payloads strip owner-only fields:

- Chat session response omits `userId`, `agentId`, and `remoteThreadId`.
- Orchestration run response omits `userId` and `agentId`.
- Orchestration events preserve transcript fields and command metadata, but
  file data remains excluded because Hub only persists file metadata.

The frontend route `/share/<shareID>` loads the public endpoint before any
login check. It reuses the existing message/timeline renderers so collapsible
command blocks work like the normal UI. The page shows disabled product chrome
and a sign-in action; it does not open authenticated APIs or WebSockets.

## Implementation Steps

1. Add `conversation_shares` schema and share CRUD in `internal/store/store.go`.
2. Add store tests for create, reuse, owner checks, public lookup, and revoke.
3. Add protected and public share handlers in `internal/hub/share.go`.
4. Register routes in `internal/hub/server.go:NewServer`.
5. Add Hub tests that prove public reads work without auth and private APIs
   still require auth.
6. Add share buttons to the chat and orchestration UI in
   `frontend/src/app/App.tsx`.
7. Add `/share/<shareID>` read-only route that bypasses `/api/me`.
8. Build `frontend/` so `internal/web/static/` is refreshed.
9. Update architecture/code-map docs for the new table and API surface.

## Exit Gates

- Authenticated owners can create a share for a chat session or orchestration
  run they own.
- A user cannot share another user's session or run.
- Anonymous `GET /api/public/shares/{shareID}` succeeds for active shares.
- Anonymous private transcript APIs still return login errors.
- Revoked or unknown share ids return 404 from the public endpoint.
- Public responses do not include user ids, agent ids, machine ids, enroll
  tokens, remote thread ids, or uploaded file bodies.
- The public page renders without logging in.
- Command/detail blocks on the public page can be expanded.
- `npm run build`, Go tests, doc lint, and `git diff --check` pass.

## Reviewer Q&A

**Q1: Why use a bearer share id instead of making sessions public by id?**

A: Session and run ids are internal identifiers. A separate unguessable share id
allows revocation and avoids turning all internal ids into public capabilities.

**Q2: Why does public read use Hub storage instead of Bridge?**

A: The public link must work even when the private Bridge is offline, and it
must not expose private-machine access. Hub already persists the transcript.

**Q3: What exactly can anonymous viewers do?**

A: They can read the shared transcript and expand/collapse details. Prompting,
approval, file upload, settings, agent management, and navigation into the app
remain authenticated.

**Q4: Is the share frozen at creation time?**

A: No. The link reads current persisted transcript data. This keeps the first
implementation small; immutable snapshots can be added later with a stored blob.
