# Endpoint-Local Install Updates

## Goal

Installing the current Bridge binary while linking a workspace must not
interrupt active Bridge endpoints, native CLI sessions, or orchestration runs
in other working directories on the same private machine.

## Non-Goals

- Change the WebSocket protocol, enroll-token semantics, or endpoint identity.
- Automatically migrate every already-running endpoint to the newly downloaded
  binary.
- Alter task cancellation, native session resume, or orchestration continuity.

## Design

`internal/hub/server.go:handleInstallScript` downloads the binary to a sibling
temporary file and atomically renames it into `~/.local/bin/codex-bridge`. It
then exits. It does not inspect systemd units, nohup PID files, `/proc`, or
other endpoint metadata.

The generated setup and repair commands continue with `codex-bridge link`.
`link.go:prepareLinkOptions` derives a stable hash from the selected working
directory, and `link.go:startLinkedBridge` starts or restarts only the matching
systemd user service or nohup process. Existing endpoints therefore remain
running while a new directory is linked. A repair command intentionally
restarts its selected endpoint only.

This preserves the existing one-endpoint-per-working-directory machine-id file
layout. It also avoids dropping Bridge-resident native orchestration state
while a user adds another workspace from the same machine.

## Data And Protocol Impact

- No SQLite schema change.
- No HTTP or WebSocket contract change.
- No frontend change: generated setup and repair commands retain their existing
  shape.

## Implementation Steps

1. Remove global service and nohup restart logic from `/install.sh`.
2. Retain atomic binary replacement and download retry behavior.
3. Protect the installer with tests that prove it cannot call systemd or stop a
   managed nohup process.
4. Document the directory-scoped restart behavior for setup and repair.

## Exit Gates

- `/usr/local/go/bin/go test ./...`
- `CGO_ENABLED=0 /usr/local/go/bin/go build -ldflags "-s -w" -o bin/codex-bridge .`
- `make doc-lint`

## Reviewer Q&A

**Q: Does an existing endpoint receive the new binary immediately?**

No. It keeps its running process and active task intact. Run that endpoint's
repair command after its task is idle to restart just that endpoint on the new
binary.

**Q: Why not restart every endpoint after an install?**

On a shared machine, the user commonly links another project while an existing
project is executing. A global restart drops the active Bridge process and its
in-memory native CLI orchestration state, which violates task continuity.
