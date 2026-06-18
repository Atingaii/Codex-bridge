# Codex + Codex Orchestration

## Goals

- Add a browser-selectable orchestration worker pair for two Codex participants.
- Keep the existing Claude Code + Codex behavior as the default path.
- Let Codex + Codex runs use two independent native Codex threads so each
  participant keeps its own local context across turns and follow-up prompts.
- Preserve existing orchestration modes, profiles, file uploads, browser
  approval, native context compaction, run sharing, and continuation semantics.

## Non-Goals

- Do not add arbitrary N-agent orchestration or custom participant editing.
- Do not add a second Bridge endpoint requirement. Both Codex participants run
  on the selected endpoint.
- Do not change chat session behavior.
- Do not change explicit New Run behavior; a new run still creates new native
  contexts.

## Data And Protocol Impact

- `internal/protocol.OrchestrationStartPayload` gains optional `workerPair` and
  `codexThreadIds`.
- `internal/protocol.RunStartData` and `internal/protocol.RunEndData` mirror
  `workerPair`; `RunEndData` also carries `codexThreadIds`.
- `internal/store.OrchestrationRun` persists `worker_pair` and
  `codex_thread_ids_json`.
- HTTP create/continue orchestration requests accept optional `workerPair`.
  Empty or unknown values normalize to `claude-codex` for compatibility.
- No new HTTP endpoint or WebSocket frame kind is required.

## Design

The worker pair is intentionally narrow:

- `claude-codex`: existing behavior. Turns alternate between Claude Code and
  Codex, ordered by `firstCli`.
- `codex-codex`: two Codex participants. Collaboration alternates
  implementer/reviewer; debate alternates proposer/critic. `firstCli` is
  normalized to `codex` because there is no Claude participant.

Bridge keeps Codex native state by participant slot. The existing single Codex
thread remains the `codex` slot for Claude + Codex runs. Codex + Codex uses
`codex-a` and `codex-b`, with each slot backed by its own app-server thread and
persisted thread id. This prevents the two Codex participants from reading and
writing the same private native context.

Hub validates capabilities against the selected worker pair. Claude + Codex
still requires both CLIs and both browser-approval adapters in review-required
mode. Codex + Codex only requires Codex orchestration capability and Codex
browser approval.

Follow-up prompts keep using `POST /api/orchestrations/{runID}/prompts`.
Unless the browser sends a different pair, Hub preserves the run's persisted
`workerPair`. Persisted Codex thread ids are sent back to Bridge so the same
participant slots can resume native context.

## Implementation Steps

1. Add worker-pair and Codex-thread-id-map protocol fields.
2. Persist worker pair and Codex thread id map in SQLite and store structs.
3. Normalize and validate worker pair in Hub create/continue flows.
4. Make Bridge relay scheduling derive turns from worker pair and first CLI.
5. Key Codex interactive sessions by participant slot.
6. Add a frontend worker-pair segmented control in the orchestration toolbar
   and settings panel, and send `workerPair` in create/continue payloads.
7. Rebuild embedded static assets.

## Exit Gates

- A request without `workerPair` still starts Claude + Codex and can choose
  `firstCli` as before.
- A request with `workerPair: "codex-codex"` can run when only Codex
  orchestration capability is available.
- Codex + Codex emits Codex turns for both participant roles and uses distinct
  Codex thread ids when app-server threads are available.
- Follow-up prompts preserve the persisted worker pair and resume persisted
  Codex thread ids.
- Verification passes:
  `/usr/local/go/bin/go test ./...`,
  `CGO_ENABLED=0 /usr/local/go/bin/go build -ldflags "-s -w" -o bin/codex-bridge .`,
  and `npm run build` when frontend source changes.

## Reviewer Q&A

**Q1: Why not represent this as `firstCli=codex`?**

A: `firstCli` only chooses which existing participant moves first. It cannot
distinguish Claude + Codex from two Codex participants or persist separate
native contexts.

**Q2: Why persist a Codex thread id map instead of one extra column?**

A: The map keeps the old single `codex_thread_id` compatibility field while
making the participant slots explicit. It avoids a future migration if the two
fixed Codex slots need to be renamed or one slot is absent.
