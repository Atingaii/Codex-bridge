# Orchestration First CLI Selection

## Goals

- Let a browser orchestration run choose whether Claude or Codex handles the
  first relay turn.
- Preserve the existing collaboration/debate turn alternation after the chosen
  first CLI.
- Keep the selected first CLI visible and durable on the run record so refreshes
  and follow-up prompts reuse the same routing.
- Support proof-smoke runs on endpoints where the user explicitly wants Codex to
  start and report command evidence in the browser timeline.

## Non-Goals

- Do not add a new runner boundary or a new HTTP endpoint.
- Do not change approval policy behavior.
- Do not change the event shape for command output or proof assessment.
- Do not make Bridge judge Coq or Isabelle proof validity.

## Data And Protocol Impact

- `internal/protocol.OrchestrationStartPayload` gains optional `firstCli`.
- `internal/hub/orchestration.go:handleCreateOrchestration` and
  `internal/hub/orchestration.go:handleContinueOrchestration` accept
  `firstCli` in the existing JSON body.
- `internal/store.OrchestrationRun` stores `first_cli` as `FirstCLI` with
  values `claude` or `codex`; empty legacy rows behave as `claude`.
- `frontend/src/app/pages/OrchestrationWorkspace.tsx:OrchestrationWorkspace`
  sends and displays the first-turn CLI selector.

## Implementation Steps

1. Add `first_cli` migration, store field, scanner, create, and update wiring.
2. Add request normalization and protocol payload forwarding.
3. Change relay turn routing to offset the existing role/CLI schedule by the
   selected first CLI.
4. Add focused Hub, Store, and Bridge tests.
5. Add a compact frontend control and include `firstCli` in create/continue
   requests.

## Exit Gates

- Creating a run with `firstCli: "codex"` persists the value and sends a start
  payload with `FirstCLI == "codex"`.
- Bridge turn one emits Codex for `firstCli: "codex"`, then alternates to
  Claude on turn two.
- Existing requests without `firstCli` still start with Claude.
- Full verification passes:
  `/usr/local/go/bin/go test ./...`,
  `CGO_ENABLED=0 /usr/local/go/bin/go build -ldflags "-s -w" -o bin/codex-bridge .`,
  and `make doc-lint`.

## Reviewer Q&A

**Q1: Why store this instead of treating it as one request option?**

A: Users inspect and continue orchestration runs after refreshes. Persisting the
choice makes the visible run configuration match the actual relay routing and
keeps follow-up prompts on the same schedule.

**Q2: Why not add a Codex-only mode?**

A: The product is still a relay orchestration between available CLIs. The needed
control is which CLI receives the first handoff; alternation remains unchanged.
