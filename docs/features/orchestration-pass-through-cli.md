# Orchestration Pass-Through CLI

## Goals

- Treat Bridge orchestration as a relay between the browser task expression and
  the selected local CLI.
- Preserve uploaded files, working directory, compact context, CLI output, tool
  events, and terminal status in the browser-visible timeline.
- Avoid Bridge-owned proof acceptance gates, automatic proof remediation turns,
  or runtime command bans that constrain what the CLI may do.
- For formal-proof-looking tasks, provide lightweight workflow and audit
  reminders in the initial CLI/CCB prompt so the browser-visible result records
  the original obligation, build/scan/audit evidence, and unresolved blockers.
- Keep one narrow safety boundary for explicit Isabelle work: when Claude Code
  appears to be running a long Isabelle build, Bridge may append a user-style
  note to the same Claude stdin stream telling it to stop waiting, inspect the
  latest output/log, and continue if enough no-error evidence is available.

## Non-Goals

- Do not add new HTTP endpoints, WebSocket frame kinds, runner boundaries, or
  hidden verifier/remediation channels.
- Do not add a resident supervisor agent or a new runner boundary.
- Do not make Bridge decide whether a Coq/Isabelle proof is semantically valid.
  That judgement belongs to the CLI output and the user-visible result.
- Do not reintroduce hidden final proof assessment or remediation turns merely
  because Bridge's proof-reminder checklist is incomplete.
- Do not require a controlled background Isabelle build template. The CLI may
  choose foreground or background execution as long as it uses an explicit
  timeout for long Isabelle builds and reports timeouts clearly.

## Data And Protocol Impact

- `internal/protocol.OrchestrationStartPayload` may carry optional `firstCli`
  when a run needs Codex rather than Claude to receive the first visible
  handoff; default/legacy behavior remains Claude-first.
- `internal/store.OrchestrationRun` persists `first_cli` so refreshed runs and
  follow-up prompts reuse the same first-turn selection.
- No frontend event shape changes.
- Existing `turn.start`, `turn.delta`, `command.start`, `command.end`,
  `turn.end`, `run.end`, `run.error`, and `run.cancelled` events continue to
  carry the visible result. When a CLI exits before returning final text,
  `turn.end` and `run.error` carry a sanitized process error so the browser
  timeline shows the failure reason.
- Terminal `run.end` content remains a relay summary of the CLI output and
  recorded commands instead of an independent proof-assessment checklist.

## Design

Bridge prompt construction keeps mostly generic orchestration context:

- latest user task, including uploaded-file materialization notes from
  `internal/bridge/orchestration.go:PrepareOrchestrationPromptFiles`;
- compact prior handoffs when the run has more than one scheduled turn;
- language and compact handoff formatting so browser output remains readable;
- a short Isabelle timeout boundary when the prompt looks like it asks for
  Isabelle build work. A Coq conversion task that happens to upload `.thy` and
  `ROOT` files does not receive this Isabelle runtime boundary.
- lightweight formal-proof relay guidance when the prompt looks like a Coq,
  Isabelle, Lean, or termination proof task. This guidance asks the CLI to work
  spec-first, avoid trust shortcuts, use explicit timeouts, run source scans and
  proof-assistant dependency/oracle audits when available, and record exact
  blockers in the visible handoff. For the Model.thy/Termination.thy/ROOT Coq
  benchmark, it specifically reminds the CLI to account for all uploads, create
  a new Coq project, run make/coqc, scan for shortcuts, run `Print Assumptions`
  on a named `modify_lin` target, and treat `modify_lin_fuel`/`default_fuel` as
  unresolved unless equivalence, decrease, and fuel sufficiency are proved.
  First-turn formal-proof prompts also declare the chosen orchestration
  strategy: collaboration uses an implementer/reviewer split, debate uses a
  proposer/critic split, and `firstCli=codex` starts with verifier/planner
  duties before broad proof search. The prompt tells the CLI to stop blind proof
  search after bounded failed attempts and to put a Chinese
  `最终测试结果` / `最终结论` section in the visible final answer so the browser
  timeline carries the test result directly instead of requiring the user to
  infer it from command logs.

The run may choose `firstCli=claude` or `firstCli=codex`. The selected value is
shown in the run start event, sent in the existing `orchestration_start`
payload, and only offsets the first handoff; collaboration and debate still
alternate CLIs turn by turn.

Bridge no longer injects fixed Isabelle build templates or rejects foreground
Isabelle build commands after the CLI has run. Formal proof reminders are prompt
guidance only: they are shown in `turn.start`, passed to the selected CLI, and
do not cause hidden verifier/remediation turns or a Bridge-owned proof verdict.

The formal-proof reminder content follows proof-assistant documentation rather
than local heuristics alone. Rocq/Coq documents `Print Assumptions` as the way
to display theorem dependencies, including the "Closed under the global
context" success case, and documents guard/positivity/universe bypass flags as
consistency risks that are reported through assumptions. Coq assumptions such
as `Axiom`, `Conjecture`, and `Parameter` are accepted postulates, not completed
proofs. Isabelle documents `sorry` as a fake proof that taints derivations via
an oracle, and documents `thm_oracles` as the command for inspecting oracle
dependencies. Those references justify the prompt's scan/audit checklist while
leaving the actual proof judgement to the CLI-visible result.

Reference inputs:

- <https://rocq-prover.org/doc/V8.19.2/refman/proof-engine/vernacular-commands.html#print-assumptions-reference>
- <https://rocq-prover.org/doc/V8.19.1/refman/language/core/assumptions.html#assumptions>
- <https://isabelle.in.tum.de/dist/library/Doc/Isar_Ref/Proof.html>
- <https://isabelle.in.tum.de/dist/library/Doc/Isar_Ref/Spec.html>

For explicit Isabelle-looking Claude turns, Bridge starts Claude Code with
`--input-format=stream-json` and sends the initial user task as a normal stream
JSON user message. If a command event for `isabelle build`, `isabelle process`,
or a related long Isabelle command remains active past
`internal/bridge/orchestration.go:claudeIsabelleLongCommandNudgeAfter`, Bridge
writes one additional stream JSON user message to the same stdin. That message
does not cancel the subprocess or start a hidden verifier; it asks Claude to use
the latest available output/log, skip further waiting when appropriate, and
continue the task. The browser receives a `turn.delta` event noting that the
nudge was sent. The stream input shape follows Claude Code's documented
headless SDK input mode:
`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"..."}]}}`.
When there are no active tool events and no further browser input, Bridge closes
the stream stdin after `internal/bridge/orchestration.go:claudeStreamInputIdleCloseAfter`.
That EOF tells Claude no more user messages are pending; it does not cancel or
kill the Claude process.

The runtime waits for the selected CLI to return and then relays the result.
Bridge may still report transport errors, context cancellation, repeated
identical blockers from CLI handoffs, or explicit CLI `blocked` / `needs_next`
handoffs as non-completed terminal states, because those are the CLI-visible
result rather than a Bridge proof verdict.

Automatic post-run remediation is disabled for pass-through orchestration. If a
CLI says it is finished, Bridge should not start a new hidden assessment or
repair turn just because a proof-specific checklist is missing.

The local CCB path follows the same rule: CCB receives the lightweight
formal-proof guidance when applicable, but CCB status and final reply are
relayed without a Bridge-owned formal-proof acceptance gate.

CLI turns run under managed process groups. Direct Codex, Claude, CCB, and
Codex app-server processes use the same cancellation boundary; on Linux their
direct child receives `Pdeathsig=SIGKILL` if Bridge exits unexpectedly. The
packaged Bridge service and generated user services set `OOMPolicy=continue` so
an OOM-killed child build does not by itself restart the Bridge parent.

## Implementation Steps

1. Mark the older strategy-optimization proof-gate design as deprecated.
2. Remove proof-specific hidden assessment/remediation behavior from normal,
   verifier, remediation, and CCB paths.
3. Add a small Isabelle timeout-boundary prompt block.
4. Stop wrapping CLI results with foreground Isabelle build rejection.
5. Disable automatic final verifier and remediation decisions that interrupt
   pass-through completion.
6. Remove formal-proof and workspace-diff acceptance gates from final run
   resolution.
7. Add same-stdin Claude stream nudges for long Isabelle commands.
8. Update tests to assert the pass-through behavior, the presence of lightweight
   formal-proof reminders, and the absence of hidden proof gates.
9. Persist optional first-turn CLI selection so smoke runs can start directly
   with Codex while keeping Claude-first as the default.

## Exit Gates

- Prompts for the three-upload Coq task include the original task, uploaded
  files, and lightweight proof workflow reminders for scans, `Print
  Assumptions`, named `modify_lin` obligations, and fuel-wrapper risks; they do
  not include controlled-background Isabelle templates or hidden Bridge proof
  gates. First-turn prompts also show the selected strategy and require a
  visible final test-result section.
- Isabelle-looking prompts include only the explicit-timeout stop/report
  boundary and do not ban foreground builds. Coq conversion prompts with
  uploaded Isabelle files do not enter the Isabelle stream-input/nudge path.
- A failed CLI subprocess with no final text is visible in the browser timeline
  as sanitized `turn.end` / `run.error` content instead of a generic status
  alone.
- Long-running Isabelle command events in a Claude stream-input turn cause one
  same-process stdin nudge and a browser-visible `turn.delta` notice, without
  interrupting the CLI process.
- Idle Claude stream-input turns close stdin without interrupting the process,
  so Claude can finish naturally instead of waiting for more user input.
- Direct CLI execution does not reject foreground Isabelle build commands.
- A resolved CLI handoff for the three-upload task can complete without Bridge
  requiring proof-specific assessment dimensions.
- CCB completed replies are relayed without the old proof assessment gate, while
  CCB prompts still carry the same lightweight proof reminders.
- Runs created with `firstCli: "codex"` show Codex as turn one in the browser
  timeline and include the selection in persisted run data.
- Full verification passes:
  `/usr/local/go/bin/go test ./...`,
  `CGO_ENABLED=0 /usr/local/go/bin/go build -ldflags "-s -w" -o bin/codex-bridge .`,
  and `make doc-lint`.

## Reviewer Q&A

**Q1: Why keep any instruction for Isabelle?**

A: The user explicitly allowed a timeout/stop boundary for long Isabelle
compilation. The boundary is operational: it prevents a CLI turn from hanging
indefinitely while leaving build strategy to the CLI.

**Q2: Why keep prompt guidance but remove proof-specific acceptance gates?**

A: Hidden gates made Bridge act as an extra proof verifier and constrained CLI
strategy. Prompt guidance is different: it is browser-visible, handed to the
CLI up front, and helps the CLI produce auditable evidence without letting
Bridge silently override the CLI's final result.
