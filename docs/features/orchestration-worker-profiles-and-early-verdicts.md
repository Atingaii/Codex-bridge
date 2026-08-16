# Orchestration Worker Profiles And Early Verdicts

## Goals

Reasoning strength is treated as model metadata, not as a generic CLI-wide
enum. Settings tests connectivity through Bridge but authorizes the selected
model against the Hub's reviewed catalog,
persists its supported levels and default level with the preset, and the
orchestration worker selector reuses that exact snapshot. Unknown provider
aliases expose no fabricated levels and cannot receive an explicit effort
override.

- Let each orchestration worker slot select an existing saved Codex or Claude
  provider preset, including different presets for `codex-a` and `codex-b`.
- Require an explicit, matching saved preset for every worker before a new
  orchestration can start; do not expose machine-global endpoint defaults.
- Refresh the current machine's slot-preset candidates immediately after a
  preset is saved, edited, or deleted in Settings; no browser reload is needed.
- Keep the selected workspace shared while isolating credentials, provider
  settings, model selection, and CLI home state per run/slot.
- Snapshot the selected reasoning effort with each slot profile, apply it only
  to that slot's CLI process, and surface the same immutable model/effort on
  turn and usage events. Machine-level active presets remain a 1:1-chat
  default and are never an orchestration fallback when a slot is bound.
- Evaluate every completed turn with a bounded, external verifier and end a
  run immediately when completion is independently evidenced.
- Preserve browser workflow, task-graph continuity, native thread continuity,
  timeline events, and plan/branch-map data.

## Non-Goals

- Do not edit global `~/.codex` or Claude settings for an orchestration-bound
  profile.
- Do not infer a Claude context window from an unreviewed provider alias or
  copy context settings from the machine-wide Claude configuration.
- Do not expose plaintext credentials to Hub, browser, event logs, task graphs,
  public shares, generated prompts, or retained post-run runtime state.
- Do not add a default extra model turn, arbitrary shell hook, or concurrent
  verifier workload on a low-capacity Bridge.
- Do not change an existing run's profile bindings implicitly during a
  follow-up prompt.

## Open-Source Inputs

The design takes the useful constraints, not runtime dependencies, from
configuration switchers such as ccswitch: named profiles, isolated materialized
configuration, atomic lifecycle, and preservation of the user's primary CLI
state. It also applies harness ideas visible in DeepSeek Harness AgentTeams:
explicit role ownership, durable state, dependency-aware progress, and a
separate adjudication record rather than trusting a worker's prose claim.
The `claude-claude` pair and unanimous checker quorum extend this design in
[claude-claude-and-quorum-verification.md](claude-claude-and-quorum-verification.md).

## Data And Protocol Impact

- `protocol.OrchestrationStartPayload` gains slot-to-preset bindings. A binding
  contains preset identity, model, reasoning effort, and ciphertext, never
  plaintext credentials.
- `protocol.RunStartData` records safe slot label/model metadata. `RunEndData`
  records the terminal reason and verifier verdict.
- Hub creates, continues, validates, and snapshots bindings from its
  owner-scoped preset store. The private `orchestration_worker_profiles` table
  is the continuation source for every run; a task-graph start payload carries
  the same snapshot for dispatch recovery.
- Bridge emits `verifier.verdict` with a safe verdict, evidence summary, and
  early-stop marker. Existing event persistence and frontend reducers consume
  it as an ordinary orchestration event.
- Bridge registration advertises `isolatedWorkerProfiles`. Hub rejects a new
  or continued run with bound profiles when the selected endpoint has not
  advertised it, rather than allowing an older Bridge to interpret the payload
  through its retired `model_catalog_json` path.

## Implementation Steps

1. Add protocol models for profile bindings and verifier verdicts, rejecting
   unknown slots, mismatched CLI types, missing presets, and presets owned by
   another user/agent.
2. Add Hub request validation and payload construction. The new-run browser
   workflow requires a complete binding set. Follow-ups retain prior bindings
   if omitted and Hub rejects a partial or CLI-incompatible replacement
   binding.
3. On Bridge, materialize a private runtime home per `{runID, workerSlot}` and
   configure only the child process environment from the Hub snapshot. Codex
   receives `config.toml` plus `auth.json`; Claude receives `settings.json`.
   Bridge has no capability/context catalog and never creates a Codex model
   catalog. Unknown aliases use native defaults. Reuse the home for direct
   native resume and remove it after the session exits. Synchronize only native
   session/transcript records into ordinary `/resume` picker locations; never
   copy isolated configuration or credentials.
4. Apply local hard evidence gates after each successful turn. For completion
   candidates, run two fresh Agent Verifiers using role 1 and role 2's bound
   presets, validate their JSON decisions, emit the quorum before choosing the
   next turn, and stop only on a two-Agent and local unanimous pass.
5. Add compact required preset selectors to the existing orchestration form,
   refresh their candidates after Settings changes, and render
   verdict/terminal-reason data in the existing progress surfaces.
6. Rebuild embedded static UI and add focused protocol, Hub, Bridge, reducer,
   and integration coverage.

## Exit Gates

- `codex-codex` can bind two different saved Codex presets and each child
  process sees only its assigned runtime configuration, model, and reasoning
  effort. Events and usage report the bound snapshot rather than a machine
  active preset.
- A new run cannot start until every active Claude/Codex slot has a matching
  saved preset; no selector exposes a machine-global default.
- A saved/edited/deleted preset is available or removed from the current
  orchestration form without a browser refresh. Each bound Claude slot uses
  only its reviewed model context profile; an unknown model has no inherited
  context-window override.
- A profile secret is absent from Hub API responses, events, logs, prompts,
  task-graph public shares, and runtime artifacts after cleanup.
- A resolved turn with independent command evidence ends before unused turns
  only when both isolated Agent Verifiers and the local hard gates pass.
- Missing evidence, unresolved formal-proof obligations, verifier error, or a
  failed command never causes early termination.
- Existing plans, branch maps, follow-up continuity, Claude and Codex runner
  tests, and frontend reducers remain covered.

## Reviewer Q&A

**Why use both worker presets as Verifiers?**

The two configured roles may use different providers, models, and reasoning
strengths. Fresh, concurrent sessions give two independent completion judgments
without contaminating worker history. Any error, malformed JSON, disagreement,
or missing local evidence conservatively continues the scheduled run.

**Why snapshot ciphertext in private run storage and the start payload?**

The Bridge needs credential material but Hub must not decrypt it. Private
run-scoped storage preserves the binding for ordinary follow-ups, while the
start payload represents the dispatch snapshot for recovery. Storing only the
selected preset ID would force the Bridge to retrieve a secret through a new
protocol and make reconnect recovery ambiguous.

**Can a successful test incorrectly finish a task?**

Not by itself. Both Agent Verifiers must accept a structured final resolution
from the relevant worker with no outstanding risks/next work, and local gates
still require successful command evidence plus a recognized proof checker for
formal proofs.
