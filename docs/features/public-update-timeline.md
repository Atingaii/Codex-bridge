# Public Update Timeline

## Goal

Publish a concise, unauthenticated `/updates` page that groups recent product
changes by date and links naturally with the existing Help page.

## Non-Goals

- No automatic changelog generation or release API.
- No replacement for GitHub release notes or operator documentation.
- No change to Bridge, task, session, or persistence behavior.

## Current State

Recent user-visible changes are only discoverable through commit history and
long release summaries.

## Design

`frontend/src/app/pages/UpdatesPage.tsx:UpdatesPage` uses the existing Help page
header, theme controls, typography, and compact tool-oriented visual language.
It shows the current release, three high-signal highlights, and a dated timeline
with small category labels. `/updates` renders before authentication and links
to Help, the application, and the GitHub release.

## Implementation Steps

1. Add the public route and update page.
2. Add Help/header and workspace navigation links.
3. Add a source-level route and content regression check.
4. Rebuild the embedded UI and verify desktop/mobile rendering.

## Exit Gates

- `npm test` and `npm run build` pass.
- Go tests and documentation lint pass.
- `/updates` renders without authentication at desktop and mobile widths.
- Existing `/help`, `/hlep`, application, and orchestration routes are unchanged.

## Reviewer Q&A

**Why is the content checked in?** The page is a curated product summary, not a
raw commit feed; checked-in copy stays brief and auditable.

**Does it affect running work?** No. It adds static frontend routing and links
only.
