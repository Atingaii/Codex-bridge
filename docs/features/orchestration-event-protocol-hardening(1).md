# Orchestration Event Protocol Hardening

> Status: partially implemented. This document collects findings from a
> 2026-05-29 review of how Bridge reports CLI processing back to the browser,
> how orchestration prompts are composed, how the final-conclusion contract is
> enforced, and how Isabelle-build timeouts are specially handled. It records
> what is unreasonable today, why it matters to the user, and the direction we
> should move in. Treat it as the parent design doc that subsequent feature
> docs and ADRs will refine.

## Goal

Make the orchestration event stream a stable, structured contract between
Bridge and the browser, so that:

- The user can tell at a glance whether a message came from the CLI, from the
  user, or from Bridge itself.
- The "final conclusion" of a run is a structured, machine-readable artifact,
  not a string the frontend has to grep for in Chinese keywords.
- Special task profiles such as Isabelle/Coq formal-proof handling live behind
  an explicit, opt-in profile boundary instead of being hard-coded into the
  generic orchestration core.
- Long-command observers (the Isabelle 2-minute nudge today) are configurable,
  visible to the user, and not limited to one CLI or one tool ecosystem.

## Non-Goals

- Replacing the reverse WebSocket transport or the
  `internal/protocol.Envelope` frame shape. The transport is fine; the payload
  contract above it is what needs hardening.
- Removing formal-proof support. Isabelle/Coq/Lean guidance stays available,
  but moves behind a profile flag instead of being inferred from prompt text.
- Changing the SQLite schema as the first step. Storage migrations are
  considered only after the new event fields stabilise.
- Touching CCB code paths. CCB has been deprecated as an active backend (see
  `docs/architecture.md`); residual frontend dispatch on `job_*` /
  `completion_*` kinds will be cleaned up only when this hardening lands.

## Current State

This section records what the code does today (commit-time anchored). All line
numbers are against the repository state at the time this doc was written; treat
them as starting points, not invariants.

### How Bridge reports CLI processing

Two sibling channels carry CLI processing back to the browser:

- Chat: `internal/protocol.SessionUpdatePayload` (`internal/protocol/envelope.go:130`).
  Carries `Delta`, `Content`, `Event` (`"delta" | "content" | "tool"`), and an
  optional `Tool` (`id/status/command/output/exitCode`). Routing lives in
  `internal/bridge/session.go:136` and `internal/hub/ws_bridge.go:appendAssistantDelta`.
- Orchestration: `internal/protocol.OrchestrationEventPayload`
  (`internal/protocol/envelope.go:190`). Carries `Kind`, `TurnID`, `Role`,
  `CLI`, `Content`, `Status`, `Error`, and a free-form
  `Data map[string]any`.

The orchestration `Kind` set in production today (grepped from
`internal/bridge/orchestration.go`):

- Lifecycle: `run.start`, `run.end`, `run.error`, `run.cancelled`,
  `run.canceling`.
- Turn: `turn.start`, `turn.end`, `turn.delta`.
- Command: `command.start`, `command.end`.
- User input: `user.message`.
- Historical / partly-dead: `ccb.terminal_prompt`, `claude.permission_prompt`.

`Data` is an undocumented but load-bearing schema. The frontend
(`frontend/src/app/App.tsx`) reads at least:
`cwd`, `mode`, `firstCli`, `maxTurns`, `promptSeq`, `relayOnly`,
`codexThreadId`, `claudeSessionId`, `cli`, `turn`, `id`, `command`, `input`,
`output`, `name`, `status`, `exitCode`, `startedAt`, `completedAt`,
`durationMs`, `contentKind`, `eventId`, `replyId`, `jobId`, `pid`, `pgid`,
`afterSeconds`. None of these are declared in `internal/protocol`.

### How orchestration prompts are composed

`composeRelayPromptWithFirstCLI` (`internal/bridge/orchestration.go` near
line 5361) concatenates, per turn:

1. A static framing header.
2. `orchestrationLanguageRule` (force Chinese for user-visible prose; keep
   machine-readable `Msg:` / `Handoff:` field names in English).
3. `isabelleTimeoutBoundary` if `looksLikeIsabelleRuntimeTask(userPrompt)`.
4. `formalProofRelayGuidance` if `looksLikeFormalProofTask(userPrompt)`,
   which pulls in `coqProofRelayGuidance` and `isabelleProofRelayGuidance`.
5. `initialFormalProofOrchestrationStrategy` for first-turn formal-proof runs.
6. Mode/role-specific blocks (`implementer`/`reviewer` or
   `proposer`/`critic`).
7. Compacted prior history.

Each fragment is gated by a `looksLikeXxxTask` string-keyword detector
(`internal/bridge/orchestration.go:5988+`). The full assembled prompt is then
echoed back to the frontend verbatim as the `Content` of the `turn.start`
event:

```go
m.emit(payload.RunID, protocol.OrchestrationEventPayload{
    Kind:    "turn.start",
    Content: "Prompt sent to " + cli + ":\n" + prompt,
    Data:    map[string]any{..., "relayOnly": true},
})
```

`maxTurns` is silently clamped to 12 (`internal/bridge/orchestration.go:300`). The frontend
receives no signal that its requested value was capped.

### How the final conclusion is produced

There are three independent layers of "final conclusion" today:

1. Bridge's `relayTerminalContent` (`internal/bridge/orchestration.go:408`) takes the last
   turn's raw content and ships it as `run.end.Content`.
2. The prompt itself contains a "Browser-visible result rule" instructing the
   CLI to emit a Chinese `最终测试结果/最终结论` section. This is enforcement by
   prompt only; nothing structural validates it.
3. The frontend `finalOrchestrationConclusionFallback` (`frontend/src/app/App.tsx:1138`)
   detects whether the visible content is "a readable final conclusion" by
   keyword search (`isReadableFinalConclusion`, `frontend/src/app/App.tsx:1296` —
   `最终结论 / 最终总结 / 最终测试结果 / final conclusion / final summary` plus
   loose `结论 + 完成|通过|验证 / 剩余风险` heuristics). If absent, the frontend
   synthesises its own bilingual fallback summary.

The fallback only fires when `last.kind === 'run.end' && status === 'completed'`.
`run.error` and `run.cancelled` get no fallback summary at all.

### How Isabelle-build timeouts are specially handled

At least 17 distinct sites in `internal/bridge/orchestration.go` are dedicated
to Isabelle handling:

- `isabelleTimeoutBoundary` (5279): prompt block telling the CLI to use an
  explicit timeout for long Isabelle builds.
- claudeIsabelleLongCommandNudgeAfter = 2 * time.Minute (74):
  hard-coded nudge timer.
- `scheduleClaudeIsabelleNudge` (3993): when a Claude tool event for an
  `isabelle build / process / scala / jedit` command stays running past the
  timer, Bridge writes a synthetic JSON `user`-role message into Claude's
  stdin via `writeClaudeStreamUserMessage` (4037).
- `shouldUseClaudeStreamInput` (4067): only Isabelle tasks get the
  stream-input mode where Bridge can poke Claude mid-turn at all.
- `closeStreamInput` idle-window logic (3826).
- `isabelleManualBuildSignal` (570) and friends: scrape the history for
  phrases like `manual build`, `do not rerun`, `exit 124`, `超时`, `build.pid`,
  `build.log`, etc., to decide whether the next CLI turn should skip rerunning
  the same build.
- `emitIsabelleManualBuildCarryOver` (596): inject a `turn.delta` reminder
  into the next turn.
- `looksLikeIsabelleUploadProofBenchmark` (6011): triggers
  benchmark-specific guidance, including a hard-coded reference to the
  function name `modify_lin` (5345).
- An "Isabelle oracle 审计" boolean dimension in the proof assessment matrix
  (4761).

The frontend has its own Isabelle-aware string match
(`frontend/src/app/App.tsx:799`):

```ts
return content.startsWith('Bridge closed Claude stream input after an idle window')
    || content.startsWith('Bridge sent Claude an Isabelle timeout nudge');
```

so it can render those Bridge-originated `turn.delta` events without
double-counting them as CLI output.

## Findings

This is the user-visible review. Each finding lists the problem, the user
impact, and the change direction. Severity is the user impact, not the code
risk.

### F1 — `Status` field is overloaded across three meanings (P0)

The same `Status` slot in `OrchestrationEventPayload` carries:

- run lifecycle states (`running` / `completed` / `failed` / `canceled`);
- command execution states (`in_progress` / `completed` / `failed` /
  `interrupted` / `canceled`);
- Bridge-internal log levels (`info`, `warning` for nudge / idle-close
  notices).

`commandEventFailed` (`frontend/src/app/App.tsx:1313`) treats any event with `error` or a
`failed/error` status as a real failure. A Bridge-internal warning ("could not
write to Claude stdin") therefore inflates the failed-command count shown in
the run summary card.

**Direction:** split into `Status` (lifecycle, enumerated) and an optional
`Severity` (`info` / `warning` / `error`). Bridge-internal notes always have
`Severity != ""`, never set business `Status`.

### F2 — Bridge-originated events are detected by string prefix (P0)

The frontend identifies "Bridge said this, not the CLI" by string matching on
hard-coded English prefixes (`frontend/src/app/App.tsx:799`). A wording change on either side
silently breaks the contract; CLIs can also accidentally echo a string starting
with `"Bridge "` and be misclassified.

`emitIsabelleManualBuildCarryOver` is even worse: it emits a `turn.delta`
without `relayOnly: true`, so it is indistinguishable from a real CLI delta on
the wire.

**Direction:** add a first-class `Source` field (`"cli" | "bridge" | "user"`)
to `OrchestrationEventPayload`. Every Bridge-originated event sets
`Source: "bridge"`. Frontend renders Bridge events in a distinct row style and
counts them out of CLI metrics. Drop the `relayOnly` flag and the prefix
match.

### F3 — `Data map[string]any` is the real schema, but undocumented (P0)

Frontend behaviour depends on at least 25 keys in `Data`. None are declared in
`internal/protocol/envelope.go`. Tooling cannot detect drift; tests do not
fail when a key is renamed.

**Direction:** promote the load-bearing keys into typed sub-payloads:

- `CommandData` (`id, command, input, output, name, status, exitCode,
  startedAt, completedAt, durationMs, pid, pgid`).
- `RunStartData` (`cwd, mode, firstCli, maxTurnsRequested, maxTurnsApplied,
  promptSeq`).
- `TurnStartData` (`cli, turn, maxTurns, promptText, profile`).
- `RunEndData` (`codexThreadId, claudeSessionId`).
- `BridgeNoteData` (`category, command, afterSeconds`).

Keep a small `Extra map[string]any` escape hatch, but move the documented
fields out of it.

### F4 — Tool timing is computed twice and only "happens" to agree (P1)

Bridge stamps `startedAt / completedAt / durationMs` in `stampToolTiming`
(`internal/bridge/orchestration.go:4091`) and stores them into `Data`. The frontend
recomputes the same fields in `mergeOrchestrationToolEvents` and
`mergeOrchestrationToolData` (`frontend/src/app/App.tsx:860, 904`). Both sides must agree on
field names; nothing enforces it.

**Direction:** Bridge always emits a single `command.end` carrying final
`startedAt + durationMs`. Frontend trusts those values and stops merging.

### F5 — Claude `Read` tool starts are silently deferred (P1)

`emitClaudeTool` (`internal/bridge/orchestration.go:3878+`) caches `in_progress` events for
Claude `Read ...` commands and only emits them once `command.end` arrives, to
hide paginated-read failures. From the frontend's perspective, work happens
without any visible event.

**Direction:** still emit `command.start` immediately, but include a
`willSuppressOnFailure: true` hint. If the command ends with the empty-pages
failure pattern, emit a `command.cancelled` (or similar terminal kind) so the
event timeline never has unexplained gaps.

### F6 — Full prompt text is echoed into `turn.start.Content` (P0)

`turn.start` ships `"Prompt sent to <cli>:\n<prompt>"` as its `Content`
(`internal/bridge/orchestration.go:340`). The prompt is the full assembled relay prompt:
language rule, formal-proof guidance, Isabelle boundary, role block, history
compaction, plus the user's text — often hundreds to thousands of lines.

This:

- pollutes the visible event stream with internal scaffolding;
- leaks Bridge's prompt construction onto the public share page (`/share/...`);
- pushes the frontend's `mergeOrchestrationDeltaEvents` /
  `isReadableFinalConclusion` heuristics to operate on text they were not
  designed for.

**Direction:** `turn.start.Content` becomes a one-line summary
("Sent prompt to claude (turn 3/8, role=reviewer)"). The full prompt moves to
`Data.TurnStartData.PromptText`. Frontend renders it behind an opt-in
"View prompt" disclosure. Public share strips `PromptText` entirely.

### F7 — `maxTurns` is silently clamped to 12 (P1)

`internal/bridge/orchestration.go:300` clamps `maxTurns` to 12 with no event signalling the
clamp. Users who chose 20 see 12 with no explanation.

**Direction:** emit both `maxTurnsRequested` and `maxTurnsApplied` in
`RunStartData`. Frontend shows a small "limited to 12" hint when they differ.

### F8 — Final conclusion is enforced by prompt + frontend keyword search (P0)

Three layers cooperate fragilely (see Current State, "How the final conclusion
is produced"). Failure modes observed in code paths:

- Model produces a Chinese `最终结论` and an English `Final summary` in the same
  turn → both render, plus possibly a third `run.end.Content` copy.
- Model says only "任务搞定" → `isReadableFinalConclusion` returns false →
  fallback summary fires even though the model did conclude.
- Model says "我先**完成**第一步,然后看**结论**" mid-turn →
  `isReadableFinalConclusion` returns true → fallback is suppressed and the
  run looks summary-less.
- `run.error` / `run.cancelled` paths get no fallback summary at all, even
  though those are the cases users most need a summary for.

**Direction:** introduce a structured final-conclusion artifact, carried in a
new event kind `run.conclusion` with a typed payload:

```go
type RunConclusion struct {
    Outcome              string   // "satisfied" | "unsatisfied" | "blocked" |
                                  // "canceled" | "errored"
    Summary              string   // user-facing prose
    BuildOrAuditCommands []string
    UnmetObligations     []string
    EvidenceRefs         []string // file paths, log refs
}
```

Bridge requires the last CLI turn to call a tool / emit a structured block
that fills this in, or Bridge synthesises a deterministic best-effort
`RunConclusion` itself (with `Outcome: "errored" | "canceled" | "blocked"`)
for failure / cancellation. Frontend renders one final-conclusion card,
period. `isReadableFinalConclusion` and the bilingual fallback prose
generator are deleted.

### F9 — Isabelle / Coq / formal-proof handling is fused into the generic core (P0)

`internal/bridge/orchestration.go` is 7634 lines; a sizable share is formal
proof. The function name `modify_lin` from a specific benchmark is hard-coded
in user-facing prompt text (`internal/bridge/orchestration.go:5345`). This means:

- A user pasting a `.thy` file but asking Codex to convert it to Coq still
  gets the full Isabelle audit guidance.
- A user with no formal-proof intent who writes "也许参考 Isabelle 的方式"
  ends up in stream-input mode.
- A user actually doing Isabelle work in Chinese (`帮我跑这个证明`) without
  the literal word `isabelle` triggers nothing — the most relevant case
  is the most likely to silently miss the special path.
- The repo signals an undisclosed real-world driver task hard-coded into a
  "general" tool.

**Direction:** carve out `internal/bridge/profiles/` with `default` and
`formal-proof` profiles. The profile is set explicitly when the run is
created — UI selector on the Orchestrate page, persisted in
`orchestration_runs.profile`. The profile owns:

- which prompt fragments to compose,
- which long-command observers run,
- which assessment dimensions appear,
- whether stream-input mode is enabled.

The generic core only orchestrates turns; it knows nothing about Isabelle,
Coq, `modify_lin`, or `Print Assumptions`. Keyword-based detectors stay
only as a one-time **suggestion** ("This run looks formal-proof. Switch
profile?"), never as silent activation.

### F10 — Isabelle nudge is a silent prompt injection (P0)

`scheduleClaudeIsabelleNudge` writes a `{"type":"user", ...}` JSON message
into Claude's stdin (`writeClaudeStreamUserMessage`, `internal/bridge/orchestration.go:4037`)
when an Isabelle build runs longer than 2 minutes. The browser sees a
small `Status: "info"` `turn.delta` saying Bridge sent a nudge, but the
nudge text itself is not aligned with Claude's subsequent reply in the
visible timeline. Claude cannot tell the message came from Bridge instead
of the user.

This is silent prompt injection, even if benign in intent. It is also
Claude-only: Codex running the same Isabelle command gets no observer.
The 2-minute window is hard-coded with no per-task or per-machine knob.

**Direction:**

- Make the long-command observer generic
  (`bridge.long_command_observer.{enabled, after, command_pattern}`),
  available to both Claude and Codex, and not gated on Isabelle keyword
  matching.
- Render every Bridge-originated stdin injection as an explicit "Bridge
  note" row in the timeline, **next to** the CLI's own response, so the
  user can see exactly what Bridge said and when.
- Tag the injected stdin payload with a Bridge-prefixed sentinel so a
  later audit (or a model trained on the transcript) can distinguish it
  from genuine user input.
- Surface a per-command card in the UI: "Bridge will nudge in N seconds.
  [Cancel nudge] [Cancel command]."

### F11 — `isabelleManualBuildSignal` derives state from log scraping (P1)

`isabelleManualBuildSignal` (`internal/bridge/orchestration.go:570`) decides whether a
build has already timed out by scanning history text for English and
Chinese phrases such as `manual build`, `exit 124`, `status=124`, `超时`,
`build.pid`, `build.log`, plus a `cancelled + running` co-occurrence
heuristic. An Isabelle CLI version bump, a wording change, or a different
locale flips the answer and sends the next turn back into the same
multi-hour build.

**Direction:** Bridge already owns the ground truth. Track an in-memory
`commandFingerprint -> {timedOut, ranAtLeast, lastExitCode}` map keyed by
`(cwd, normalisedCommand)`. The "do not rerun" decision uses that map
directly, not text scraping. Keep the text scrape only as a fallback for
events that originated before fingerprint tracking landed.

### F12 — `looksLikeIsabelleRuntimeTask` is an alias of `looksLikeIsabelleProofTask` (P2)

`looksLikeIsabelleRuntimeTask` is defined as `return looksLikeIsabelleProofTask(text)`
(`internal/bridge/orchestration.go:5988`). Six call sites use the two names interchangeably,
which suggests a vanished distinction. New contributors cannot tell which
predicate to call.

**Direction:** delete one. If "runtime" vs "proof" is meaningful (e.g.
runtime allows builds, proof requires audit), give them different
implementations and document the distinction. Otherwise collapse to one
name.

### F13 — `case 'job_accepted' | 'job_queued' | 'completion_item' | ...` is dead-or-half-dead frontend code (P2)

`frontend/src/app/App.tsx:1271–1289` dispatches on CCB-style event kinds (`job_accepted`,
`job_started`, `completion_item`, `completion_terminal`, `job_completed`,
`job_failed`, `job_cancelled`, `agent_console`, `callback_*`) even though
`docs/architecture.md` says CCB is no longer an active backend. Same for
the residual `ccb.terminal_prompt` / `ccb.*` constants in Bridge.

**Direction:** treat this hardening as the trigger to delete the dead CCB
event dispatch (frontend) and the unused `ccb.*` event constants
(backend). Keep the persisted CCB-rendered events readable on the share
page only if there are existing share links pointing at CCB-era runs;
otherwise drop them.

### F14 — Public share leaks Bridge-internal scaffolding (P1)

Because `turn.start.Content` carries the full assembled prompt (F6) and
Bridge-originated notes are not labelled (F2), the public `/share/<id>`
page renders both. Anyone with a share link can read:

- the full set of formal-proof prompt blocks composed for that task,
- Bridge's Isabelle nudge text,
- Bridge's idle-close notice text.

**Direction:** the public share sanitiser strips, in order:
`Source: "bridge"` events (rendered as a generic "Bridge sent N internal
notes" line at most), `Data.TurnStartData.PromptText`, and any event with
`Severity` set. This works automatically once F2, F3, and F6 are in.

## Direction (summary table)

| # | Change | Carrier |
| --- | --- | --- |
| 1 | Add `Source` to `OrchestrationEventPayload` | `internal/protocol/envelope.go`, Hub passthrough, frontend renderer |
| 2 | Split `Status` from `Severity` | same |
| 3 | Promote load-bearing `Data` keys to typed sub-payloads | same + `internal/store/orchestration_events` if persisted |
| 4 | Single source of truth for tool timing | `internal/bridge/orchestration.go:stampToolTiming`, frontend `mergeOrchestrationToolData` deletion |
| 5 | One-line `turn.start.Content` + `Data.PromptText` | same |
| 6 | Surface clamped `maxTurns` | `RunStartData.MaxTurnsRequested/Applied`, frontend banner |
| 7 | Structured `run.conclusion` event | new event kind, deletes `isReadableFinalConclusion` and the bilingual fallback prose generator |
| 8 | Carve out `internal/bridge/profiles/` | new package, default vs formal-proof; `orchestration_runs.profile` column; UI selector |
| 9 | Generic long-command observer | config block under `bridge.long_command_observer`, applies to any CLI |
| 10 | Visible Bridge-note rows | frontend renderer + sentinel tagging in stdin injections |
| 11 | Fingerprinted command history | in-memory map in Bridge orchestration session state |
| 12 | Collapse `looksLikeIsabelleRuntimeTask` / `looksLikeIsabelleProofTask` | follow-up cleanup |
| 13 | Delete dead CCB dispatch | frontend + Bridge constants |
| 14 | Sanitiser hardening for public share | falls out of 1, 2, 3, 5 |

## Implementation Steps

The work is sequenced so each step can ship and be reviewed independently.
Each step is its own commit (or PR) with its own `Doc-Impact` footer per R6.

1. **Protocol skeleton.** Add `Source`, `Severity`, and the typed sub-payloads
   (`CommandData`, `RunStartData`, `TurnStartData`, `RunEndData`,
   `BridgeNoteData`) to `internal/protocol/envelope.go`. Keep `Data` as a
   passthrough escape hatch for one release. No behavior change yet.
2. **Bridge emission migration.** Update every `m.emit(...)` call in
   `internal/bridge/orchestration.go` to set `Source` correctly and to fill
   the typed sub-payloads in addition to (not instead of) `Data`. Keep the
   old keys for one release to avoid breaking any in-flight client.
3. **Frontend dual-read.** Update `App.tsx` to prefer the typed sub-payloads
   when present, fall back to `Data` otherwise. Replace
   `isBridgeRelayNotice` with `event.source === 'bridge'`. Keep the keyword
   check as a one-release fallback for events from older Bridges.
4. **`turn.start.Content` slimming + `maxTurns` exposure.** Move the prompt
   text to `Data.TurnStartData.PromptText`. Surface
   `maxTurnsRequested` / `maxTurnsApplied` in `RunStartData`. Frontend
   collapses the prompt under a disclosure and shows the clamp banner.
5. **Tool-timing collapse.** Bridge becomes the sole source of
   `startedAt/completedAt/durationMs`. Delete frontend
   `mergeOrchestrationToolData` timing logic.
6. **`run.conclusion` event.** Define `RunConclusion`. Bridge always emits
   one `run.conclusion` per run (synthesising a deterministic one for
   error / canceled / blocked outcomes). Frontend renders one conclusion
   card per run. Delete `finalOrchestrationConclusionFallback` and
   `isReadableFinalConclusion`.
7. **Profile carve-out.** Create `internal/bridge/profiles/`. Move
   `formalProofRelayGuidance`, `coqProofRelayGuidance`,
   `isabelleProofRelayGuidance`, `isabelleTimeoutBoundary`,
   `initialFormalProofOrchestrationStrategy`,
   `looksLikeIsabelleUploadProofBenchmark`, the `modify_lin` text,
   the Isabelle assessment dimension, and the keyword detectors into
   `profiles/formalproof/`. Add `orchestration_runs.profile` (default
   `"default"`). The Orchestrate UI gains a profile selector. Keyword
   detectors become a one-time UI suggestion only.
8. **Generic long-command observer.** Add `bridge.long_command_observer`
   config block (`enabled`, `after`, `command_patterns`, `applies_to`).
   Generalise `scheduleClaudeIsabelleNudge` to a
   `scheduleLongCommandObserver` that runs against any CLI whose runner
   exposes a side-channel input. Tag injected payloads with a sentinel
   marker. Frontend shows a per-command card with cancel-nudge and
   cancel-command actions.
9. **Command fingerprinting for "do not rerun".** Replace
   `isabelleManualBuildSignal` with an in-memory fingerprint map carried
   by `orchestrationSessionState`. Keep the textual scrape as a strict
   fallback only.
10. **Public share sanitiser.** Now that Bridge-originated events,
    Bridge-internal severity events, and `PromptText` are all explicit,
    rewrite `internal/hub/share.go` sanitiser to drop them by structure
    rather than by content.
11. **Cleanup pass.** Delete CCB frontend dispatch and unused `ccb.*`
    backend constants. Collapse
    `looksLikeIsabelleRuntimeTask`/`looksLikeIsabelleProofTask`. Remove
    the one-release `Data`-key compat shims added in steps 1–4.

Each step ships behind a feature flag in config where it changes user-visible
behavior (steps 4, 6, 7, 8). Flags get removed in step 11.

## Exit Gates

- [x] `OrchestrationEventPayload.Source` is the single mechanism the frontend
      uses to distinguish Bridge messages from CLI output. No code path
      reads `event.content.startsWith("Bridge ")`.
- [x] No frontend code path inspects free-form `Data` for any of the keys
      listed in F3 — they all read typed sub-payloads.
- [x] `turn.start.Content` is at most one line in 100% of orchestration
      runs in the test suite. The full prompt is reachable through
      `Data.TurnStartData.PromptText` only.
- [x] `isReadableFinalConclusion`, `finalOrchestrationConclusionFallback`,
      and `hasFreshFinalConclusion` are deleted from `App.tsx`. Every run
      (`completed`, `errored`, `canceled`, `blocked`) ends with exactly
      one `run.conclusion` event and one rendered conclusion card.
- [x] `internal/bridge/orchestration.go` contains zero literal occurrences
      of `isabelle`, `coq`, `lean`, `proof`, `oracle`, `modify_lin`,
      `Print Assumptions`. All formal-proof code lives under
      `internal/bridge/profiles/formalproof/`.
- [x] `bridge.long_command_observer` is configurable, applies to both
      Claude and Codex, and is exercised by an integration test that
      does not mention Isabelle.
- [x] Every Bridge-originated stdin injection is also visible in the
      browser timeline as an explicitly-labelled Bridge-note row, with
      the full injected text.
- [x] A regression test demonstrates that an Isabelle build whose log
      wording changes (e.g. localised differently) still does not get
      rerun, because the fingerprint map remembers the timeout.
- [x] `docs/architecture.md`, `docs/code-map.md`, and
      `docs/change-impact.md` reflect the new event fields, profile
      package, and observer config block. `make doc-lint` passes.
- [x] CCB dispatch is removed from `App.tsx`; existing share links from
      CCB-era runs either still render correctly via persisted snapshot
      data or are explicitly listed as no-longer-supported.

## Reviewer Q&A

**Q1: Why one big design doc instead of per-finding feature docs?**
The findings are not independent. F2 enables F14, F3 enables F4 and F6, F8
depends on F2. Splitting them up front would force premature decisions about
event shape; once steps 1–3 land, follow-up feature docs (per profile, per
observer) can refine details without re-litigating the protocol contract.

**Q2: Doesn't carving Isabelle out of the core hurt formal-proof users?**
No, because today's keyword-based activation already misses any
formal-proof user who does not literally type the word. Making the
profile explicit means a `.thy`-uploading user gets a one-click "switch to
formal-proof profile" suggestion and known-on behavior, instead of
silent partial activation.

**Q3: Won't a structured `RunConclusion` lose information that free-form
prose conveys?**
The structure has a `Summary` string field for prose. The structure adds
a machine-checkable outcome and explicit unmet-obligation list. Failure
modes today (model writing only "搞定" or only English while UI is
Chinese, or no summary at all on `run.error`) become impossible.

**Q4: What about backward compatibility for old Bridges talking to a new
Hub frontend?**
Steps 1–4 ship the new fields alongside the old ones. The frontend's
"prefer typed, fall back to `Data`" stage covers one full release. After
that, the cleanup pass in step 11 removes the compat shims. The Hub
itself is Source-of-truth-agnostic (it forwards envelopes), so no Hub
upgrade flag-day is required.

**Q5: Is `bridge.long_command_observer` a security risk if it can inject
into Codex stdin?**
The observer only injects when the runner explicitly exposes a
side-channel (Claude Code's stream-input today). A Codex runner would
need an analogous explicit hook before any injection happens; absence of
hook means observer just emits a Bridge-note row and times out the turn
locally. The audit trail in F10 makes injections visible to the user.

**Q6: Why keep keyword detectors at all?**
Only as a one-time suggestion at run-creation time ("This task looks
formal-proof — switch profile?"). They never silently rewrite the
prompt. Users can dismiss the suggestion. This preserves the
discoverability benefit of detection while removing its current opacity.

## Implementation Notes

### 2026-05-29 protocol-hardening pass

Implemented:

- `internal/protocol/envelope.go:OrchestrationEventPayload` now carries
  `Source`, `Severity`, typed command/run/turn/end/Bridge-note payloads, and
  `RunConclusion`.
- `internal/store/store.go:AddOrchestrationEvent` persists `source`,
  `severity`, and typed sub-payloads in event JSON while keeping legacy `Data`
  compatibility for existing rows.
- `internal/hub/orchestration.go:handleOrchestrationEvent` persists typed
  events and updates run continuity from typed run-start/run-end payloads.
- `internal/bridge/orchestration.go:emit` normalizes source/severity and emits
  one `run.conclusion` before terminal run events.
- `internal/bridge/orchestration.go:composeRelayPromptWithFirstCLI` gates
  formal-proof prompt guidance behind explicit `profile=formal-proof`; Hub,
  Store, and the UI persist/pass the profile.
- `frontend/src/app/App.tsx:visibleOrchestrationEvents` uses `source`,
  `severity`, `commandData`, and `runConclusion`; deleted the keyword final
  conclusion fallback helpers and CCB event-state dispatch.
- `internal/hub/share.go:publicOrchestrationEvents` strips private
  `TurnStartData.PromptText`, severity events, and internal Bridge notes from
  public shares.
- `bridge.long_command_observer` config was added under `internal/config/`.
  Claude stream-input notes include the `[Codex Bridge observer note]`
  sentinel and are mirrored as Bridge-note events; Codex commands without a
  stdin side-channel emit visible Bridge-note rows.
- Claude `Read` tool starts are emitted immediately with
  `CommandData.WillSuppressOnFailure`; the known empty-pages failure is
  terminally cancelled instead of silently creating a gap.
- Formal-proof prompt, assessment, manual-build, and benchmark-specific logic
  now lives under `internal/bridge/profiles/formalproof/`. The generic
  orchestration core calls neutral registry functions and
  `internal/bridge/orchestration.go` no longer contains the formal-proof
  literal strings listed in the exit gate.
- `orchestrationSessionState.CommandFingerprints` records long build command
  fingerprints. Manual-build carry-over checks prefer that memory and keep text
  scraping only as a fallback for older events; a regression test covers
  localized output that does not contain legacy timeout/manual-build wording.

Compatibility notes:

- The observer does not inject into Codex stdin because the current Codex JSONL
  runner exposes no side-channel; it records an explicit Bridge-note event
  instead. This satisfies the injection audit gate for actual Bridge-originated
  stdin injections while keeping Codex observations visible.
- `Data` remains for persisted-row compatibility and for `user.message`
  uploaded file metadata. Frontend command/run/final-conclusion behavior no
  longer reads the F3 command/run keys from free-form `Data`; the remaining
  `event.data?.files` access is outside the F3 schema.
