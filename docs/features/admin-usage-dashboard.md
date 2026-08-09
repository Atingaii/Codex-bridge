# Admin Usage Dashboard

## Goals

- Give the configured bootstrap administrator one read-only view of user
  activity, connected Bridge endpoints, chat and orchestration workload, Token
  usage, and official-catalog cost estimates.
- Keep time-range filtering based on the timestamp of each usage event and the
  browser's local timezone.
- Preserve the existing per-user isolation of all non-admin APIs.
- Make unknown pricing explicit: partial pricing is never presented as a known
  zero-dollar total.
- Provide a dense desktop table and an equivalent compact mobile layout.
- Let the administrator open one user's read-only detail page to identify that
  user's chat sessions and orchestration tasks by title, endpoint, status,
  activity time, and per-conversation usage.
- Let the administrator inspect one selected conversation's message bodies or
  orchestration prompt on demand, with an explicit ownership check and without
  loading content into aggregate/list responses.

## Non-goals

- User creation, deletion, impersonation, password reset, quota enforcement, or
  billing settlement.
- Exposing workspace paths, native CLI session identifiers, credentials,
  attachments, or private Bridge execution metadata. Message bodies and the
  original orchestration prompt are the only conversation content exposed.
- Opening another user's writable workspace, impersonating that user, or
  reusing the existing user-scoped conversation APIs.
- Changing Bridge protocol frames, local CLI collection, or requiring a Bridge
  upgrade.
- Replacing `/api/usage/overview`, whose results remain scoped to the current
  user.

## Data And API Impact

`GET /api/admin/usage?days=7|30|90|0&timezoneOffset=<minutes>` is protected by
`internal/hub/server.go:withAdmin`. It returns summary totals, a daily trend,
and one aggregate row per user. Non-admin callers receive `403 ADMIN_ONLY`.

`GET /api/admin/users/{userID}/usage?days=7|30|90|0&timezoneOffset=<minutes>`
uses the same administrator middleware. It returns the selected user's account
summary plus read-only chat and orchestration rows. Each row contains only its
Hub identifier, title, kind, endpoint label, status, mode/turn limit where
applicable, timestamps, activity count, and normalized usage totals. Unknown
users return `404 NOT_FOUND`; non-admin callers receive `403 ADMIN_ONLY` before
the target user is resolved.

`GET /api/admin/users/{userID}/conversations/{kind}/{conversationID}` uses the
same administrator middleware and performs a server-side ownership check before
returning content. For `chat`, the response contains ordered message roles,
bodies, and timestamps. For `orchestration`, it contains the original prompt
and ordered user-visible event text. It never returns workspace paths, native
CLI identifiers, credentials, files, or writable conversation handles. A
conversation that does not belong to the path's user returns `404 NOT_FOUND`.

`internal/store/admin_analytics.go:AdminUsageSnapshot` performs read-only
aggregation over existing Hub tables. Online state is added by
`internal/hub/admin.go:handleAdminUsage` from `internal/hub/pool.go:AgentOnline`.
No schema migration is required.

Activity is the newest known timestamp across the user record, active agents,
chat sessions/messages/runs, orchestration runs, and orchestration events.
Statuses are:

- `online`: at least one connected endpoint.
- `active`: activity during the previous 24 hours.
- `idle`: activity during the previous seven days.
- `inactive`: older activity.

Chat usage comes from completed run `usage_json`. Orchestration usage prefers
the native `orchestration_usage_events` ledger; a run with ledger events does
not also include legacy `turn.usage` snapshots. Usage event timestamps, not run
creation timestamps, determine whether Tokens fall inside the selected range.

## Implementation Steps

1. Add the read-only Store snapshot and focused aggregation tests.
2. Add the administrator-only HTTP handler, online-state enrichment, pricing,
   range validation, and authorization tests.
3. Add `/admin/usage`, administrator-only navigation, trend controls, user
   search/sort, desktop table, and mobile user summaries.
4. Make user rows open `/admin/usage/users/{userID}` and add a responsive,
   read-only conversation list.
5. Add an administrator-only, ownership-checked content endpoint and load one
   selected conversation into a read-only detail panel on demand.
6. Keep the current and immediately previous hashed UI assets embedded so a
   cached HTML document cannot white-screen during a rolling deployment.
7. Rebuild `internal/web/static/` and run repository exit gates.

## Exit Gates

- [x] A normal user receives `403 ADMIN_ONLY`; the administrator can load all
  user aggregates.
- [x] The same 401/403 boundary protects user detail, unknown targets return
  404, and an administrator can load only the documented metadata projection.
- [x] Lists contain no message or prompt content; the content endpoint returns
  only message bodies, orchestration prompt/event text, roles, and timestamps.
- [x] Wrong-user conversation IDs return 404, while workspace paths and native
  session fields remain absent from every administrator response.
- [x] Daily buckets honor range and browser timezone.
- [x] Native ledger and legacy orchestration usage are not double counted.
- [x] Unknown catalog pricing produces `costKnown=false`.
- [x] Online endpoint counts come from the current Hub connection pool.
- [x] Desktop and 390px layouts have no horizontal page overflow.
- [x] `go test ./...`, frontend tests/build, `make doc-lint`, and
  `git diff --check` pass.

## Reviewer Q&A

**Why is this a separate endpoint instead of extending the existing overview?**

The existing endpoint is deliberately user-scoped. A separate route with the
admin middleware keeps the cross-user authorization boundary visible and
testable.

**Is the dollar amount an invoice?**

No. It is an official API list-price estimate. Rows with unmatched models are
marked as incomplete instead of silently treating unknown usage as free.

**Does a remote Bridge need to be updated?**

No. The feature reads Hub persistence and the Hub's live connection pool only.

**Why is conversation content loaded separately?**

It keeps overview and user-list queries bounded, makes the sensitive read
operation visible in routing and authorization tests, and avoids transferring
every conversation body when the administrator needs only one.
