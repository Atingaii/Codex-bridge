# ACP Runner — PR-2: Orchestration Reuse + Frontend Takeover UI

> Builds on [acp-runner.md](acp-runner.md) (PR-1, backend core). PR-1 shipped the
> `ACPRunner`, the `SessionRunner` interface, the dual-ID model, and the
> `prompt_complete` / `session_opened` envelope fields `NativeResumeID` and
> `NativeResumeCommand`. PR-2 makes those surfaces visible and usable.

## Goal

1. **Frontend takeover UI (primary):** when the active chat session is backed by
   an ACP runner that resolved a native resume id, render — in the **current
   visual style** — a takeover hint that shows the exact local command the user
   can run from the workspace to continue the *same* conversation in the native
   CLI (`claude --resume <id>` / `codex resume <id>`).
2. **Capability badge:** surface ACP availability / native-resume support next
   to the existing runner + thread indicators, reusing existing chip/badge
   styling.
3. **Orchestration reuse (deferred):** reusing a resident ACP session in the
   orchestration path is intentionally deferred — see the "Orchestration reuse"
   section for why (it would touch the review-required approval pipeline).

## Non-Goals

- No new WebSocket frame types. `session_opened` and `prompt_complete` already
  carry `nativeResumeId` / `nativeResumeCommand`; we only consume them.
- No new visual language. Reuse the existing header chips, `ApprovalCard`-style
  cards, and `CommandBlock` for the copyable command.
- No behavior change to `echo` / `codex-exec` / `codex-app-server` runners.
- We do not auto-run the native CLI; the UI only *shows* the command.

## Honesty rule (carried from PR-1)

The takeover UI is shown **only** when the Bridge actually sent a non-empty
`nativeResumeCommand`. When PR-1 degraded (adapter missing / native id not
resolvable / cwd mismatch), the Bridge sends an empty command and the UI shows a
neutral "native resume unavailable" note instead of a fabricated command.

## Current State (entry points confirmed)

- Hub already forwards the full `session_opened` and `prompt_complete` envelopes
  to browsers (`internal/hub/ws_bridge.go:handleBridgeEnvelope`,
  `internal/hub/ws_bridge.go:handlePromptComplete`), so the new fields reach the
  browser with no Hub change required for display.
- Frontend `frontend/src/app/pages/Workspace.tsx:Workspace` handles
  `session_opened` (sets runner + thread) and `prompt_complete` (sets thread)
  via `payload` (typed `any`), so reading two more optional fields is additive.
- `frontend/src/app/lib/types.ts` defines `Session`, `BridgeCapabilities`, and
  `BridgeCLICapability`. We add optional `nativeResumeId` /
  `nativeResumeCommand` to `Session` and an optional `acp` capability shape.
- The reusable copy block already exists:
  `frontend/src/app/components/chat/CommandBlock.tsx`.

## Design

### Data flow (no new frames)

```text
Bridge ACPRunner --(session_opened / prompt_complete: nativeResumeId, nativeResumeCommand)--> Hub --forward--> Browser
Browser Workspace captures the two fields -> state -> TakeoverHint (CommandBlock) + capability badge
```

### Frontend changes

1. `types.ts`
   - `Session`: add optional `nativeResumeId?: string` (persisted for reopen).
   - Add `ACPCapability = { available?: boolean; loadSession?: boolean; nativeResume?: boolean }`
     and `BridgeCapabilities.acp?: ACPCapability` mirroring
     `internal/protocol/envelope.go:ACPCapability`.
2. `Workspace.tsx`
   - On `session_opened` and `prompt_complete`, read
     `payload.nativeResumeId` / `payload.nativeResumeCommand`; store in component
     state (`nativeResume`).
   - On reopen of a stored session, hydrate from `session.nativeResumeId` when
     present.
   - Render a `TakeoverHint` near the message composer / header when a non-empty
     command is present; render a neutral "unavailable" note when ACP is the
     runner but no command resolved.
3. New `frontend/src/app/components/chat/TakeoverHint.tsx`
   - Pure presentational, reuses existing chip + `CommandBlock` styling. Props:
     `{ command?: string; nativeId?: string; available: boolean }`.
4. Header badge
   - Extend the existing runner/thread chip row to show an "ACP" chip when the
     runner is `acp`, styled like the existing chips.
5. i18n (`lib/i18n.ts`)
   - Add `takeoverTitle`, `takeoverHint`, `takeoverUnavailable`, `acpBadge`
     strings in both `en` and `zh`.

### Persistence (minimal)

`nativeResumeId` is already round-tripped to the browser per turn, so display
needs no DB column. We persist it client-side with the session record (same
mechanism as `remoteThreadId`) so a takeover hint survives a session reopen
without waiting for the next `prompt_complete`. A Hub/store column is **not**
added in PR-2 to keep the change small; revisit only if cross-device persistence
is requested.

### Orchestration reuse (deferred — see note)

The orchestration path does not go through the generic `NewRunner`; it
constructs CLI-specific runners directly
(`internal/bridge/orchestration_codex.go` uses `NewCodexAppServerRunner` with a
bespoke app-server turn/resume + browser-approval pipeline, and the Claude path
uses an MCP approval bridge). Injecting ACP `SessionRunner` reuse there would
touch the review-required approval flow and is a materially larger, higher-risk
change than the chat dispatch added in PR-1.

To keep this PR focused and to honor the "tests must pass before commit" gate
without destabilizing the existing orchestration approval flow, **orchestration
resident-session reuse is deferred to a follow-up.** This PR ships the
user-visible value (interactive chat already reuses the resident ACP session via
PR-1, and PR-2 adds the takeover UI + badge). The deferral is recorded honestly
here rather than shipping a half-integrated change.

## Implementation Steps

1. Frontend types: `Session.nativeResumeId`, `ACPCapability`,
   `BridgeCapabilities.acp`.
2. Workspace: capture + persist + hydrate native resume; pass to UI.
3. `TakeoverHint.tsx` (reuse `CommandBlock`, existing chip styling) + ACP badge.
4. i18n strings (en/zh).
5. `npm run build` to regenerate embedded `internal/web/static` (R5 deployable
   output), `npm test` (visible-events-check), `go build ./...`,
   `go test ./...`, `make doc-lint` — all green before commit.
6. Doc sweep (architecture / code-map / change-impact / this doc / README) →
   commit on `genspark_ai_developer` → update PR.

## Exit Gates

- [ ] Frontend `npm run build` succeeds; embedded `internal/web/static`
      regenerated and committed.
- [ ] Frontend `npm test` (visible-events-check) passes.
- [ ] `go build ./...`, `go test ./...`, `make doc-lint` all green.
- [ ] Takeover hint shows the real command only when present; neutral note
      otherwise (honesty rule).
- [ ] UI uses existing components/styling only (no new visual language).
- [ ] End-to-end real CLI verification still deferred to the user's machine
      (sandbox has no codex/claude CLI or API key).

## Reviewer Q&A

- *Why no new frames?* PR-1 already added the optional payload fields; PR-2 only
  reads them, so the wire protocol frame set is unchanged.
- *Why client-side persistence?* `remoteThreadId` uses the same approach; adding
  a DB column is out of scope and would widen the change surface.
- *What if the runner is not ACP?* All new UI is gated on a non-empty command /
  `acp` runner, and orchestration reuse is gated on the `SessionRunner` type
  assertion, so non-ACP runners behave exactly as before.
