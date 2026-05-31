# Registration Disabled

## Goals

- Disable public browser/API self-registration for new users.
- Keep login, cookies, JWT issuance, and existing user accounts working.
- Keep controlled account creation through the CLI-backed store path.

## Non-Goals

- Do not delete existing users, sessions, agents, messages, runs, or tokens.
- Do not add a browser admin panel for user management.
- Do not change the Bridge WebSocket `register` frame; that frame is agent
  enrollment, not browser account signup.

## Data And Protocol Impact

- `POST /api/register` remains routed for compatibility, but always returns
  HTTP 403 with `REGISTRATION_DISABLED`.
- `POST /api/login` is unchanged, so users already present in `users` can keep
  signing in.
- The SQLite schema is unchanged.
- Controlled account creation remains available through
  `internal/store.Store.UpsertUser` as used by the `codex-bridge user` command.
- The frontend login screen no longer calls `/api/register` or renders a
  registration mode.

## Implementation Steps

1. Make `internal/hub/server.go:handleRegister` reject every request before
   parsing credentials.
2. Remove the public registration switch from `internal/config`.
3. Remove registration controls from
   `frontend/src/app/pages/LoginScreen.tsx:LoginScreen`.
4. Update auth and integration tests to pre-create non-admin users through the
   store.
5. Rebuild embedded static assets under `internal/web/static/`.
6. Update user-facing setup docs that previously described self-registration.

## Exit Gates

- `cd frontend && npm run build`
- `/usr/local/go/bin/go test ./...`
- `CGO_ENABLED=0 /usr/local/go/bin/go build -ldflags "-s -w" -o bin/codex-bridge .`
- `make doc-lint`

## Reviewer Q&A

**Q: Can existing registered users still use the site?**

A: Yes. Login still authenticates against the existing `users` table and issues
the same auth cookie. Only new public account creation is blocked.

**Q: How are new approved users created now?**

A: Use the existing CLI path:
`codex-bridge user --username <name> --password <password>`. That path writes
to the same SQLite users table without exposing browser self-registration.

**Q: Why keep `/api/register` instead of removing the route?**

A: Keeping the route gives old clients a deterministic 403 instead of a generic
404 and lets tests assert the policy directly.
