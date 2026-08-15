# Self-Service Registration And User Isolation

## Goals

- Let an operator explicitly enable browser self-registration.
- Require Cloudflare Turnstile by default for every enabled registration and
  validate its single-use token on the Hub before creating a user.
- Permit a deliberately isolated, trusted trial deployment to enable
  registration without Turnstile, without weakening the default deployment.
- Sign a newly registered user in with the existing HttpOnly JWT cookie.
- Let users reveal or hide login, registration, and confirmation passwords
  independently without changing the submitted values.
- Let every user, including the bootstrap administrator, use chat while keeping agents, sessions, messages, runs,
  orchestrations, and share-management operations scoped to their `users.id`.
- Keep normal registration disabled and fail closed when Turnstile is required
  but incomplete or unavailable.

## Non-Goals

- Email verification, password reset, invitations, account deletion, social
  login, billing, or organization/team membership.
- Sharing a private Bridge between unrelated users. Each user, including the
  bootstrap administrator, sees only agents explicitly owned by that user.
  Legacy unowned agents require an explicit ownership migration before use.
- Applying Turnstile to login or authenticated prompts. Login keeps its existing
  IP-and-username rate limit; registration gets a separate stricter limit.
- Storing Cloudflare secrets or Turnstile tokens in SQLite, logs, API responses,
  frontend bundles, or checked-in configuration.

## Data And Protocol Impact

- No SQLite migration is required. `users`, `agents`, `sessions`,
  `orchestration_runs`, and `conversation_shares` already carry user ownership;
  child messages, chat runs, and orchestration events inherit access through
  their owned parent.
- `GET /api/auth/config` is an anonymous, no-store endpoint. It returns only
  `registrationEnabled` and the public `turnstileSiteKey`. It never returns the
  Turnstile secret or expected hostname.
- `POST /api/register` accepts `username`, `password`, and optionally
  `turnstileToken`. Enabled registration requires a 3-32 character ASCII
  username and a 10-256 byte password. When `require_turnstile` is true, it
  also requires successful Turnstile Siteverify, the configured action, and
  (when configured) the expected hostname. Success creates the user and returns
  the same `{user, expiresAt}` shape and cookie as login.
- Registration configuration lives under `auth.registration`; Turnstile is
  required by default and can be overridden by `REGISTRATION_ENABLED`,
  `REGISTRATION_REQUIRE_TURNSTILE`, `TURNSTILE_SITE_KEY`, `TURNSTILE_SECRET`,
  and `TURNSTILE_HOSTNAME`. The secret remains server-only.
- Chat HTTP endpoints and `/ws/chat` move from administrator-only middleware to
  authenticated middleware. Their existing store calls continue to require the
  JWT `uid`, and session creation first verifies that the selected agent is
  visible to that user.
- Hub-to-Bridge `internal/protocol.Envelope` frames do not change. Conversation
  continuity remains keyed by the same owned `sessions.id` and persisted
  `remote_thread_id`.

## Turnstile Verification

`internal/hub/registration.go:handleRegister` calls Cloudflare's canonical
`https://challenges.cloudflare.com/turnstile/v0/siteverify` endpoint through a
bounded HTTP client before `internal/store/store.go:CreateUser`. The request
contains the server-only secret, browser token, and client IP. The Hub accepts
only `success=true`, `action=register`, and the configured hostname when one is
set. Network errors, malformed responses, empty tokens, expired/replayed tokens,
and mismatches reject registration without creating a user.

Cloudflare test keys may be used only in local/test configuration. Production
must use a managed widget restricted to the deployed hostname. A separately
isolated trusted trial may set `require_turnstile: false`; it retains the same
input validation, registration rate limit, ownership isolation, and session
creation but intentionally makes no Siteverify request. This exception must not
be used by the primary deployment.

## Implementation Steps

1. Add registration and Turnstile config, environment overrides, examples, and
   anonymous public-config response.
2. Implement input validation, registration rate limiting, Siteverify client,
   duplicate-user handling, cookie issuance, and secret-safe errors.
3. Change chat routes to authenticated access and audit every data load for
   ownership checks.
4. Add the login/register switch, independent password-visibility controls,
   confirmation password, explicit Turnstile widget lifecycle, bilingual text,
   loading, expiry, and error states.
5. Rebuild `internal/web/static/` and update architecture, code map, workflow,
   roadmap, deployment guidance, and change-impact records where applicable.
6. Add backend tests for disabled/misconfigured registration, validation,
   Siteverify success/failure/hostname/action, rate limiting, duplicates,
   automatic login, and cross-user HTTP/WebSocket access denial.

## Exit Gates

- In normal deployments, no user row is created unless the server validates
  Turnstile successfully. The isolated trusted-trial opt-out is explicitly
  tested and requires configuration.
- A registered user can sign in, enroll only their own Bridge, and use chat and
  orchestration against that Bridge.
- User A cannot list, load, mutate, delete, stream, continue, cancel, revoke, or
  share user B's private records when an identifier is guessed.
- Turnstile secrets never appear in public config, frontend output, logs, or
  checked-in examples.
- Frontend production build refreshes embedded output and passes responsive
  browser checks for login and registration.
- Login and registration password controls remain keyboard accessible, expose
  localized show/hide labels, and do not overlap typed values on narrow screens.
- `/usr/local/go/bin/go test ./...`, the production Go build, and
  `make doc-lint` pass.

## Reviewer Q&A

### Why is registration disabled by default?

This service grants remote execution on enrolled private machines. An operator
must deliberately enable account creation and provide a working anti-abuse
configuration; an empty or broken required Turnstile setup must not silently
open access.

### Why not trust Cloudflare's browser callback?

The callback only supplies a token. Cloudflare documents Siteverify as mandatory;
tokens expire, are single-use, and can be invalid or replayed. Only the Hub can
keep the secret and make the authoritative decision.

### Can users see another user's Bridge?

No. Users see agents whose `agents.user_id` matches their JWT subject. The
bootstrap administrator follows the same ownership rule: administrator status
does not reveal or authorize legacy unowned agents or another user's agents.

### Does registration break conversation continuity?

No. Authentication changes who may reach a session, not its identity. Follow-up
chat prompts retain the same `sid` and `remote_thread_id`; orchestration prompts
retain the same `runID` and continuation endpoint.
