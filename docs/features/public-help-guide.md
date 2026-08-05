# Public Help Guide

## Goals

- Provide a public, Chinese-first guide at `/help` that can be read before a
  user signs in.
- Cover registration, login, CLI endpoint enrollment, one-to-one chat,
  collaboration/debate orchestration, formal-proof runs, approvals, sharing,
  settings, and common troubleshooting.
- Use screenshots captured from the real checked-in frontend with deterministic
  demo API data so the guide stays inspectable without exposing production
  users, tokens, paths, or conversations.
- Keep the guide responsive, keyboard accessible, and visually consistent with
  the existing application.
- Accept `/hlep` as a compatibility alias for the common typo in the requested
  URL.

## Non-Goals

- No new Hub API, WebSocket frame, SQLite table, authentication rule, or Bridge
  behavior.
- No live interactive demo that can execute commands or create production
  records.
- No production credentials, enrollment tokens, private machine identifiers,
  or real user content in screenshots.
- No replacement for deployment and operator documentation in
  [docs/deployment.md](../deployment.md) and
  [docs/dev-workflow.md](../dev-workflow.md).

## Data And Protocol Impact

- `frontend/src/app/App.tsx:App` recognizes `/help` and `/hlep` before the
  anonymous `/api/me` bootstrap and renders the public guide.
- `frontend/src/app/pages/HelpPage.tsx:HelpPage` contains the guide structure,
  local navigation, demo prompts, image captions, and links back to the app.
- Screenshot assets live under `frontend/public/help/` and are copied by Vite
  into `internal/web/static/help/`. They are generated from the application UI
  with mocked demo responses and contain no production data.
- The existing SPA fallback in `internal/hub/server.go:staticHandler` serves the
  same embedded entry point for `/help` and `/hlep`; no server route changes are
  required.

## Implementation Steps

1. Add the public route branch before authentication bootstrap.
2. Build a restrained documentation layout with a sticky table of contents,
   sequential workflows, callouts, screenshots, demo prompts, and diagnostics.
3. Add discoverable help links to the login screen and authenticated
   navigation without changing existing task flows.
4. Capture deterministic desktop and mobile screenshots from the real frontend
   and add descriptive alternative text and captions.
5. Add static regression checks, rebuild `internal/web/static/`, and verify the
   guide in desktop/mobile Chromium.

## Exit Gates

- Anonymous visitors can open `/help` and `/hlep` without an `/api/me` request
  controlling access.
- Every major workflow has a real application screenshot, ordered steps, and a
  concrete demo example.
- Images do not contain production usernames, tokens, machine IDs, private
  paths, or conversation content.
- Desktop and 360px mobile layouts have no horizontal overflow, overlapping
  navigation, clipped headings, or broken images.
- Help links are available from login and authenticated navigation while all
  existing login, chat, and orchestration behavior remains unchanged.
- `npm test`, `npm run build`, `/usr/local/go/bin/go test ./...`,
  `make doc-lint`, and browser smoke checks pass.

## Reviewer Q&A

### Why is the guide public?

New users need registration, Turnstile, and endpoint-enrollment instructions
before they have a usable authenticated workspace. The page is static and does
not disclose account or Hub state.

### Why use mocked screenshots instead of production screenshots?

The frontend is still the real application build, but deterministic mock data
keeps screenshots reproducible and prevents accidental disclosure of users,
tokens, hostnames, paths, conversations, or proof files.

### Does `/help` weaken authentication?

No. Only the static guide bypasses the `/api/me` bootstrap. Every existing API,
chat, orchestration, settings, and share operation keeps its current server-side
authorization.

### How are screenshots kept current?

The checked-in generator drives the actual application and asserts the target
controls before saving images. Frontend changes that alter those workflows can
regenerate the assets and rerun the browser guide check.

Run `npm run help:screenshots` from `frontend/`. Playwright is pinned as a
development dependency; its Chromium browser and ImageMagick's `convert`
command must be installed on the capture machine.
