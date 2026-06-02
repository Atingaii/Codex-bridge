# Claude Native Resume Visibility

## Goals

- Make Claude Code orchestration sessions recoverable from the native CLI in the
  same way Codex orchestration threads are recoverable.
- Base resume metadata on Claude Code's real transcript files under
  `~/.claude/projects/<encoded-cwd>/<session-id>.jsonl`.
- Surface a truthful resume command and make the same session visible in the
  Claude Code `/resume` picker from the run cwd.
- Keep Codex and Claude native-resume reporting consistent in run-end data.

## Non-Goals

- Do not fabricate native CLI conversations that Claude Code did not write.
- Do not mutate unrelated projects in `~/.claude.json`.
- Do not depend on terminal UI scraping of the `/resume` picker.

## Current State

- Claude Code writes real transcript JSONL files under
  `~/.claude/projects/<encoded-cwd>/`.
- Older Bridge builds wrote `~/.claude/sessions/<session-id>.json` from the
  Claude orchestration path. That file is an auxiliary active-session style
  record and is not enough to guarantee `/resume` picker visibility.
- Codex orchestration already persists `codex_thread_id` and exposes it through
  `RunEndData`.

## Design

Bridge records native resume metadata for both CLIs:

| CLI | Native id | Resume command | Evidence |
| --- | --- | --- | --- |
| Codex | Codex thread id | `codex resume <thread-id>` | app-server thread id |
| Claude Code | Claude session id | `claude --resume <session-id>` | project transcript JSONL |

For Claude Code, Bridge computes the project transcript path using the same
encoding used by the existing ACP runner helper:
`~/.claude/projects/<encoded-cwd>/<session-id>.jsonl`.

After a successful Claude turn, Bridge verifies whether that JSONL exists,
contains lines, and belongs to the expected session id. It then updates only the
matching `projects[absCwd]` entry in `~/.claude.json` so the project points at
the Bridge session as its latest native session.

Claude Code 2.1.159 writes Bridge's headless stream-json turns with
`entrypoint:"sdk-cli"`, while the interactive `/resume` picker filters those
SDK/print sessions. Bridge therefore materializes the same Claude-written
project transcript into picker-visible form after successful turns:

- each JSONL record keeps the same session id, cwd, timestamps, messages, and
  attachments;
- records with `entrypoint:"sdk-cli"` are normalized to `entrypoint:"cli"`;
- Bridge appends a `~/.claude/history.jsonl` index row for the same project and
  session id when one is missing.

This does not create a separate conversation or overwrite unrelated Claude
projects. The transcript remains the native resume source, and the browser also
shows the direct `claude --resume <session-id>` command.

The old `~/.claude/sessions/<session-id>.json` compatibility file is kept only
as a best-effort hint and must not be the source of truth. It should not remain
misleadingly marked `busy` after the process is known to be usable as a history
session.

## Data And Protocol Impact

- `internal/protocol/envelope.go:RunEndData` gains native resume metadata for
  both CLIs.
- Orchestration event `data` gains matching resume fields for compatibility
  with older clients.
- `frontend/src/app/lib/types.ts:RunEndData` mirrors the typed metadata so
  browser/public-share reducers can preserve it.
- No new HTTP endpoint or WebSocket frame kind is required.
- No Hub SQLite schema change is required for resume metadata because the
  persisted ids already exist on `orchestration_runs`.

## Implementation Steps

1. Add a shared native resume metadata structure in `internal/protocol`.
2. Populate Codex metadata wherever `RunEndData` already includes a Codex thread
   id.
3. Add Claude transcript path calculation and JSONL verification.
4. Update the current cwd entry in `~/.claude.json` after successful Claude
   turns.
5. Normalize the Claude-written stream transcript into `/resume` picker-visible
   form and append the missing `history.jsonl` index row.
6. Replace the old registration call with transcript-based registration.
7. Add browser-visible metadata fields without changing event kinds.
8. Add tests for path encoding, `.claude.json` single-project update,
   transcript materialization, history indexing, metadata generation, and
   stale-session behavior.

## Exit Gates

- `/usr/local/go/bin/go test ./...`
- `npm run build` in `frontend/`
- `CGO_ENABLED=0 /usr/local/go/bin/go build -ldflags "-s -w" -o bin/codex-bridge .`
- `make doc-lint`
- Manual smoke from the target cwd:
  `claude --resume <bridge-claude-session-title-or-search-term>` shows the
  Bridge orchestration session in the picker.
- Manual smoke from the target cwd:
  `claude --resume <bridge-claude-session-id>`.

## Reviewer Q&A

**Why not only write `~/.claude/sessions/<id>.json`?**  
The transcript JSONL is the real recoverable history. A sidecar session JSON can
be stale or ignored by the picker.

**Can Bridge guarantee `/resume` picker visibility?**  
For the current supported Claude Code stream-json behavior, yes: Bridge
materializes the Claude-written transcript into the picker-visible `cli`
entrypoint shape and adds the missing history index. If a future Claude Code
version changes picker filtering, this manual smoke gate must catch it before
release.

**Why update `~/.claude.json` at all?**  
Claude Code uses that file for per-project recent session metadata. Updating
only the current cwd entry makes Bridge-created sessions easier to find without
touching unrelated projects.
