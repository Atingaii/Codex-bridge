# Orchestration Pass-Through CLI

## Goals

- Treat Bridge orchestration as a relay between the browser task expression and
  the selected local CLI.
- Preserve uploaded files, working directory, compact context, CLI output, tool
  events, and terminal status in the browser-visible timeline.
- Avoid Bridge-owned proof strategies, proof acceptance gates, automatic proof
  remediation turns, or runtime command bans that constrain what the CLI may do.
- Keep one narrow safety boundary for Isabelle work: when Claude Code appears
  to be running a long Isabelle build, Bridge may append a user-style note to
  the same Claude stdin stream telling it to stop waiting, inspect the latest
  output/log, and continue if enough no-error evidence is available.

## Non-Goals

- Do not add new HTTP endpoints, WebSocket frames, protocol payload fields, or
  SQLite schema.
- Do not add a resident supervisor agent or a new runner boundary.
- Do not make Bridge decide whether a Coq/Isabelle proof is semantically valid.
  That judgement belongs to the CLI output and the user-visible result.
- Do not require a controlled background Isabelle build template. The CLI may
  choose foreground or background execution as long as it uses an explicit
  timeout for long Isabelle builds and reports timeouts clearly.

## Data And Protocol Impact

- No protocol changes.
- No frontend event shape changes.
- Existing `turn.start`, `turn.delta`, `command.start`, `command.end`,
  `turn.end`, `run.end`, `run.error`, and `run.cancelled` events continue to
  carry the visible result.
- Terminal `run.end` content becomes a relay summary of the CLI output and
  recorded commands instead of an independent proof-assessment checklist.

## Design

Bridge prompt construction keeps only generic orchestration context:

- latest user task, including uploaded-file materialization notes from
  `internal/bridge/orchestration.go:PrepareOrchestrationPromptFiles`;
- compact prior handoffs when the run has more than one scheduled turn;
- language and compact handoff formatting so browser output remains readable;
- a short Isabelle timeout boundary when the prompt looks like it may involve
  Isabelle build work.

Bridge no longer injects proof-assistant guardrails such as required
`Print Assumptions`, forbidden fuel wrappers, source scans, semantic weakening
checks, or fixed Isabelle build templates. It also no longer rejects foreground
Isabelle build commands after the CLI has run.

For Isabelle-looking Claude turns, Bridge starts Claude Code with
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
result rather than a Bridge proof strategy.

Automatic post-run remediation is disabled for pass-through orchestration. If a
CLI says it is finished, Bridge should not start a new hidden assessment or
repair turn just because a proof-specific checklist is missing.

The local CCB path follows the same rule: CCB status and final reply are relayed
without a Bridge-owned formal-proof acceptance gate.

## Implementation Steps

1. Mark the older strategy-optimization proof-gate design as deprecated.
2. Remove proof-specific prompt injection from normal, verifier, remediation,
   and CCB prompt composers.
3. Add a small Isabelle timeout-boundary prompt block.
4. Stop wrapping CLI results with foreground Isabelle build rejection.
5. Disable automatic final verifier and remediation decisions that interrupt
   pass-through completion.
6. Remove formal-proof and workspace-diff acceptance gates from final run
   resolution.
7. Add same-stdin Claude stream nudges for long Isabelle commands.
8. Update tests to assert the pass-through behavior and absence of proof
   guardrails.

## Exit Gates

- Prompts for the three-upload Coq task include the original task and uploaded
  files, but do not include proof-strategy guardrails such as `Print
  Assumptions`, `modify_lin_fuel`, `default_fuel`, or controlled-background
  Isabelle templates.
- Isabelle-looking prompts include only the explicit-timeout stop/report
  boundary and do not ban foreground builds.
- Long-running Isabelle command events in a Claude stream-input turn cause one
  same-process stdin nudge and a browser-visible `turn.delta` notice, without
  interrupting the CLI process.
- Idle Claude stream-input turns close stdin without interrupting the process,
  so Claude can finish naturally instead of waiting for more user input.
- Direct CLI execution does not reject foreground Isabelle build commands.
- A resolved CLI handoff for the three-upload task can complete without Bridge
  requiring proof-specific assessment dimensions.
- CCB completed replies are relayed without the old proof assessment gate.
- Full verification passes:
  `/usr/local/go/bin/go test ./...`,
  `CGO_ENABLED=0 /usr/local/go/bin/go build -ldflags "-s -w" -o bin/codex-bridge .`,
  and `make doc-lint`.

## Reviewer Q&A

**Q1: Why keep any instruction for Isabelle?**

A: The user explicitly allowed a timeout/stop boundary for long Isabelle
compilation. The boundary is operational: it prevents a CLI turn from hanging
indefinitely while leaving build strategy to the CLI.

**Q2: Why remove proof-specific acceptance gates?**

A: They made Bridge act as an extra proof verifier and constrained CLI strategy.
For this product path, the browser should show the CLI result and command
timeline, not an independent Bridge judgement about formal proof methodology.
