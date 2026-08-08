# Orchestration Uses The Selected Project Directory

## Goals

- Run every orchestration task in the project directory selected by the user.
- Keep native Codex and Claude resume metadata bound to that same directory.
- Retain durable task identity, retry lineage, and reviewer barriers without
  copying the user project into hidden task workspaces.
- Keep formal-proof notes as lightweight metadata under the selected project.

## Non-Goals

- Do not introduce a new browser setting or change existing run IDs, APIs, or
  worker-pair controls.
- Do not promise concurrent filesystem writes are safe in an arbitrary project.
- Do not migrate or delete historical isolated workspaces.

## Design

The durable graph remains a Hub-owned scheduling and recovery mechanism. Its
candidate, integrator, and reviewer nodes all retain the original run CWD.
Candidate B depends on candidate A, so writable CLI turns cannot overlap in the
same checkout. Integration and review remain serial after both candidates.

For `formal-proof`, source files and proof commands execute in the selected
project root. Bridge writes only one ledger at
`<cwd>/.codex-bridge/proof-notes/<run-id>.md`; uploaded proof files are placed
in the project root using the existing safe attachment materialization rules.
The ledger preserves requests and evidence but never changes the CLI CWD.

Codex uses the supported app-server `thread/start` API with a non-ephemeral
thread and the selected CWD. Current Codex CLI versions persist such threads as
interactive-source native sessions, so starting `codex` in that directory and
using `/resume` lists new Bridge conversations. Claude uses its existing
project transcript and history-index materialization path, so `claude` then
`/resume` lists the matching conversation. This relies on the user service and
the interactive CLI running as the same OS user with the same `HOME` and
`CODEX_HOME` / Claude home.

## Data And Protocol Impact

No HTTP, WebSocket, or SQLite shape changes. `RunStartData.CWD`, persisted
`run_cwd`, and native resume commands now consistently name the selected project
directory for newly started graph-backed runs.

## Implementation Steps

1. Remove Bridge task-workspace copying and retain the requested CWD.
2. Serialize candidate graph dependencies to prevent concurrent writes.
3. Make the formal-proof ledger project-local without a copied `project/` root.
4. Update focused tests and architecture documentation.

## Exit Gates

- Graph-backed CLI turns start in the user-selected CWD.
- `codex` then `/resume` and Claude `/resume` list new conversations from that
  same CWD when invoked by the same OS user.
- Candidate tasks do not overlap in a shared writable project.
- Formal-proof notes are retained without moving sources or proof commands.
- Focused task-graph and orchestration tests pass.

## Reviewer Q&A

### Why serialize candidates?

The previous parallelism depended on copied workspaces. Once a user explicitly
chooses one writable checkout, serial candidates preserve file integrity and
match the direct CLI workflow.

### What happens to old runs?

Their recorded CWDs and task evidence stay untouched. The change applies only
to new task attempts.
