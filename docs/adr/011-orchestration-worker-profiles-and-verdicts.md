# ADR-011: Orchestration Worker Profiles And Verifier Verdicts

## Status

Accepted

## Context

The existing remote CLI configuration switcher changes a machine-wide native
configuration. That is useful for normal interactive use, but two Codex
workers in one orchestration still inherit the same provider and model.
Additionally, a bounded orchestration keeps consuming its requested turns even
when the workspace already contains a completed, independently checked proof.

## Decision

An orchestration may bind a saved, Bridge-encrypted CLI preset to each worker
slot. Hub authorizes the binding and relays the encrypted preset only to the
selected Bridge. Bridge decrypts it in memory and creates a private,
mode-0700 runtime home for that run and slot. The CLI process receives that
home and provider values through its process environment; it never mutates the
operator's global Codex or Claude configuration. An absent binding preserves
the existing native configuration path.

The first release supports `codex-a`, `codex-b`, `claude`, `claude-a`, and
`claude-b` slots. The new-run browser workflow explicitly binds every active
slot to a matching saved preset and does not offer a machine-global default.
Bindings
are snapshot data on the run-start payload and in Hub's private
`orchestration_worker_profiles` table, so a follow-up retains the same provider
identity unless the user explicitly changes the binding. Native thread/session
continuity remains slot-specific and is only resumed with the same profile
fingerprint.

The binding also snapshots the selected reasoning effort. Bridge applies this
setting in the private slot runtime, and turn/usage events carry safe model,
preset-name, and effort fields from that same binding. A machine active preset
is only the default for ordinary single-CLI sessions; it is not used to report
or execute a bound orchestration worker.

Hub is the sole capability and context-policy authority. Each binding includes
the resolved native effort levels/default and, for Claude, the reviewed context
window flags. Bridge validates snapshot self-consistency, decrypts the secret,
and writes native files; it does not infer model capabilities or generate a
Codex model catalog.

After every successful relay turn, Bridge starts up to two bounded Agent
Verifier calls: one with worker role 1's bound model preset and one with worker
role 2's. The calls use fresh native sessions, so they reuse each role's
authorized provider, model, reasoning effort, and credential snapshot without
entering either worker's conversation history or seeing the other verifier's
answer. Legacy runs without bound presets still start both role slots with each
CLI's configured machine default; they do not degrade to a single judge. Each
verifier receives the task, compact turn evidence, and an explicit JSON verdict
contract; embedded worker text is evidence, not instructions.

Each Agent Verifier judges handoff completeness, evidence sufficiency, and
independence. Bridge validates both structured responses and retains local hard
gates for successful command evidence and recognized formal-proof checker
evidence. Every check from every invoked verifier must pass. A model error,
malformed response, disagreement, missing hard evidence, or any `continue`
check conservatively continues the scheduled run. Bridge emits the same
durable `verifier.verdict` event with per-agent slot/model decisions. A
unanimous passed verdict ends the remaining relay budget early with terminal
reason `verified-early`.

## Consequences

- Two Codex workers can use distinct providers/models in one workspace without
  touching each other's credentials or the machine-wide CLI configuration.
- Two Claude workers and mixed Claude/Codex workers receive the same isolation
  guarantee, with no cross-slot configuration fallback.
- Hub continues to store ciphertext only. Runtime homes and decrypted secrets
  stay on the private Bridge and are removed when the native session closes.
- The browser keeps its existing orchestration workflow. It gains compact slot
  profile selectors and a visible verifier outcome in the existing timeline,
  plan, and branch-map event data.
- The checker quorum is intentionally conservative. A successful command
  alone is insufficient, and the model evaluator may decline to terminate when
  evidence is incomplete. Each successful worker turn adds up to two bounded
  model calls until the run ends.
- Verifier execution requires an updated Bridge binary. Hub remains protocol
  compatible because the existing verdict event and terminal reason are reused.
- This does not add a general arbitrary-command verifier or an unbounded
  multi-agent scheduler. Those would require a separate security and capacity
  design.

`claude-claude`, slot-aware native resume, and the checker quorum are specified
by [ADR-012](012-dual-claude-and-quorum-verification.md).
