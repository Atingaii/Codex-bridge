# Orchestration Explicit-Timeout Convergence

## Goals

- Prevent an orchestration turn from remaining `running` forever after a CLI
  starts a shell command with an explicit GNU `timeout` boundary but never
  reports the command or turn as terminal.
- Preserve normal long-running commands, transient Bridge-to-Hub disconnect
  recovery, native-session continuity, and the existing task-graph failure
  boundary.
- Keep one command lifecycle start and one terminal event while representing
  Codex app-server output deltas as progress updates.

## Non-Goals

- Do not impose a global turn or orchestration duration limit.
- Do not infer failure from silence, sparse output, Bridge heartbeats, or a
  command merely taking a long time.
- Do not replay a prompt or shell command whose filesystem side effects are
  uncertain.
- Do not add a configuration field, database column, HTTP endpoint, or
  WebSocket payload field.

## Runtime Contract

1. Bridge recognizes only an executable `timeout DURATION ...` command at the
   start of a command string or immediately after a supported shell boundary.
   Text that merely mentions `timeout` is not treated as an execution limit.
2. The watchdog starts when the native CLI reports the command start. It waits
   for the declared timeout, any declared `--kill-after` duration, and a small
   Bridge scheduling grace period.
3. A matching native terminal command event cancels the watchdog. Commands
   without a parseable explicit timeout never receive this watchdog.
4. If the boundary is exceeded, Bridge emits a visible operational note and
   interrupts the current native CLI turn/process group. The existing turn and
   task-graph error path records a terminal outcome; Bridge does not resubmit
   the original prompt.
5. A resident native CLI process that must be stopped may be recreated on the
   next distinct orchestration turn using its persisted native thread/session
   metadata. This is continuity for later work, not replay of the uncertain
   turn.
6. Formal-proof guidance requires `timeout --kill-after=10s ...` for bounded
   proof-assistant checks so the shell normally terminates its own descendants;
   the Bridge watchdog remains a last-resort convergence guard.
7. Codex app-server `outputDelta` notifications become `command.update`
   events. They retain the command id and streamed output, but do not create
   repeated `command.start` lifecycle records.

## Data And Protocol Impact

- No payload field or persistence schema changes.
- The existing orchestration event envelope may carry `kind=command.update`.
  It uses the same `CommandData` payload and command id as `command.start` and
  `command.end`; existing visible-event merging continues to coalesce it.
- Existing stored runs are not rewritten. New events are normalized at the
  Bridge emitter.

## Implementation Steps

1. Parse supported GNU `timeout` durations conservatively and schedule one
   bounded watchdog per active command id.
2. Wire watchdog interruption to direct Codex, Codex app-server, and Claude
   one-shot/resident turn ownership without changing Hub transport recovery.
3. Require forced-kill grace in formal-proof relay guidance.
4. Mark native output deltas and running item updates as command progress.
5. Add focused parser, lifecycle, interruption, and non-interference tests.

## Exit Gates

- A command with `timeout 120s` that never emits a terminal event eventually
  interrupts its current native turn and cannot leave the run permanently
  `running`.
- A command without explicit `timeout` remains unrestricted, including when it
  is silent for longer than the observer interval.
- A terminal event cancels its watchdog and does not interrupt later work.
- A phrase such as `echo timeout 1s` does not arm a watchdog.
- Codex output deltas no longer persist as repeated `command.start` events.
- Existing transport-reconnect and orchestration tests remain green.

## Reviewer Q&A

### Why not set a maximum duration for every turn?

Valid builds, proof checks, and model calls can be intentionally long or quiet.
The command author supplied the only reliable duration contract available here,
so Bridge enforces that contract rather than inventing another one.

### Why interrupt the native turn instead of running the command again?

The command may already have changed files before it became stuck. Interrupting
and recording failure is bounded; replaying it could duplicate or corrupt those
side effects.

### Why keep both `--kill-after` guidance and a Bridge watchdog?

`--kill-after` lets GNU timeout clean up its child process locally. The Bridge
watchdog covers malformed process trees, missing native terminal notifications,
or a CLI relay that remains blocked after the shell boundary should have ended.
