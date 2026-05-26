> **DEPRECATED - replaced by share links**
>
> Current design:
> [conversation-share-links.md](conversation-share-links.md). Historical only;
> do not implement from this doc.

# Conversation Snapshot Page

## Goal

Add a static route that presents the specified orchestration conversation as a
read-only snapshot using the same visual language as the existing browser chat
UI.

## Non-Goals

- Do not add a new HTTP endpoint, WebSocket frame, SQLite table, or persistence
  model.
- Do not change chat session continuity or orchestration follow-up behavior.
- Do not export or archive live user conversations automatically.

## Current State

The frontend in `frontend/src/app/App.tsx` switches between the main chat
workspace and the orchestration workspace using `window.location.pathname`.
There is no standalone display route for a read-only conversation artifact.

## Design

Add `/conversation-snapshot` under the existing app router. It renders a static
snapshot of the termination-framework orchestration result. The page should:

- Use the existing theme tokens, typography, sidebar/header density, message
  cards, and command blocks so it visually matches the product UI.
- Present the final `Msg`/`Handoff` result and the attached command output as
  read-only chat/timeline content.
- Keep command/output blocks clickable with native collapsible details.
- Disable non-conversation controls in the snapshot chrome, including sidebar,
  header, composer, attachment, send, copy, and navigation buttons.
- Avoid any network calls beyond the existing `/api/me` boot check.

The route is intentionally static. It is a presentation artifact, not a new
source of truth for sessions or orchestration events.

## Implementation Steps

1. Add the route branch in `frontend/src/app/App.tsx`.
2. Add a read-only React component for the snapshot page and static snapshot
   data in the same file.
3. Refresh `internal/web/static/` by running the frontend build.

## Exit Gates

- `npm run build` from `frontend/` succeeds and refreshes embedded static
  assets.
- Go tests continue to pass.
- `make doc-lint` succeeds because a feature document was added.

## Reviewer Q&A

Q: Why is this static instead of pulling from the session APIs?

A: The request is for a display page and route. A static page avoids changing
the persistence/API contract and keeps continuity behavior untouched.

Q: Does this affect follow-up prompts?

A: No. Chat still keeps the same `sid` and orchestration follow-ups still use
`POST /api/orchestrations/{runID}/prompts`.
