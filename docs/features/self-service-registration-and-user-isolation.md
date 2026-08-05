# Self-Service Registration And User Isolation

## Goals

- Let an operator explicitly enable browser self-registration.
- Require Cloudflare Turnstile for every enabled registration and validate its
  single-use token on the Hub before creating a user.
- Sign a newly registered user in with the existing HttpOnly JWT cookie.
- Let users reveal or hide login, registration, and confirmation passwords
  independently without changing the submitted values.
- Let non-admin users use chat while keeping agents, sessions, messages, runs,
  orchestrations, and share-management operations scoped to their `users.id`.
- Keep registration disabled and fail closed when Turnstile is incomplete or
  unavailable.

## Non-Goals

- Email verification, password reset, invitations, account deletion, social
  login, billing, or organization/team membership.
- Sharing a private Bridge between unrelated users. Each user enrolls their own
  Bridge; the bootstrap administrator retains compatibility visibility of
  legacy unowned agents.
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
- `POST /api/register` accepts `username`, `password`, and `turnstileToken`.
  Enabled registration requires a 3-32 character ASCII username, a 10-256 byte
  password, a successful Turnstile Siteverify result, the configured action,
  and (when configured) the expected hostname. Success creates the user and
  returns the same `{user, expiresAt}` shape and cookie as login.
- Registration configuration lives under `auth.registration` and can be
  overridden by `REGISTRATION_ENABLED`, `TURNSTILE_SITE_KEY`,
  `TURNSTILE_SECRET`, and `TURNSTILE_HOSTNAME`. The secret remains server-only.
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
must use a managed widget restricted to the deployed hostname. At implementation
time the available Cloudflare MCP account credential returned authentication
error `10000`, so account-side widget creation is a documented deployment gate;
no credential or placeholder secret is committed.

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

- No user row is created unless the server validates Turnstile successfully.
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
configuration; an empty or broken Turnstile setup must not silently open access.

### Why not trust Cloudflare's browser callback?

The callback only supplies a token. Cloudflare documents Siteverify as mandatory;
tokens expire, are single-use, and can be invalid or replayed. Only the Hub can
keep the secret and make the authoritative decision.

### Can users see the administrator's Bridge?

No. Normal users see agents whose `agents.user_id` matches their JWT subject.
The bootstrap administrator's legacy visibility exists only for migration and
operations; it does not grant normal users access to unowned or other-user
agents.

### Does registration break conversation continuity?

No. Authentication changes who may reach a session, not its identity. Follow-up
chat prompts retain the same `sid` and `remote_thread_id`; orchestration prompts
retain the same `runID` and continuation endpoint.
