# Frontend Route Loading

## Goals

- Reduce the JavaScript downloaded and parsed before a user can open the
  ProofBridge login or workspace shell.
- Serve content-addressed frontend bundles with a long immutable cache lifetime.

## Non-goals

- Do not change Hub APIs, authentication, Bridge behavior, run ownership, or
  orchestration/chat continuity.
- Do not cache HTML, service-worker, or runtime recovery files.

## Data and protocol impact

None. This is a browser bundle and HTTP cache-header change only.

## Implementation

1. Load route pages lazily from `frontend/src/app/App.tsx` while preserving the
   existing route selection and login bootstrap behavior.
2. Keep a small in-app loading indicator while a selected page chunk arrives.
3. Mark hashed `/assets/` JavaScript and CSS as immutable; the Hub continues to
   serve HTML and runtime recovery files with `no-store`.

## Exit gates

- Initial route code excludes non-selected application pages.
- Navigating to each route keeps the same path and behavior after its chunk loads.
- Hashed assets have immutable cache headers; HTML and service worker do not.
- Frontend build and relevant Go tests pass.

## Reviewer Q&A

### Can this interrupt a running task?

No. A running task remains on the Hub and Bridge. This only changes how browser
code is fetched and rendered.
