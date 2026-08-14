# ADR-010: User Feature Rollouts

## Status

Accepted

## Context

New Hub and Bridge workflows need production validation without exposing an
unfinished capability to every account. Embedding `isAdmin` checks in each
feature couples rollout policy to product logic and makes later expansion or
removal error-prone.

## Decision

Use `internal/rollout` as the single evaluator for named user-facing features.
Hub configuration maps stable feature keys to one of these policies:

- `off`: no account;
- `admin`: only the configured Hub administrator;
- `all`: every authenticated account;
- `percent:N`: a deterministic percentage bucket based on feature key and user ID;
- `users:name1,name2`: an explicit username allowlist.

The Hub remains the authorization boundary. It rejects gated API operations
even if a browser manually submits them. Authentication responses include only
the enabled feature keys in `user.features`, allowing the frontend to hide
unavailable workflows without duplicating audience policy.

Each new gated feature registers a stable key in
`internal/rollout/rollout.go:RegisteredFeatures`, adds its default policy under
`hub.feature_rollouts`, and checks the evaluator at every state-changing
backend entry point. The frontend reads the same key from `user.features`; it
must not infer rollout membership from administrator status or duplicate a
rollout policy. The current stable defaults for `strict-workspace` and
`orchestration-plan-workspace` are `all`. The former remains a selectable
opt-in profile rather than changing existing endpoints automatically.

## Consequences

- Rollout policy can change without changing feature implementation.
- Percentage membership remains stable across requests and Hub restarts.
- Feature visibility is not a security boundary; server-side checks remain
  mandatory.
- This is account-level rollout, not per-request experimentation or analytics.
- Unknown features and malformed policies fail closed.
- Adding a rollout policy does not itself authorize an operation; the backend
  entry point must enforce the feature gate.
