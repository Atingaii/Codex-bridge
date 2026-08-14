# Strict Workspace Auto Execute

## Goal

Add an opt-in CLI endpoint permission profile that runs Codex and Claude Code
without browser approvals while preventing their process trees from reading or
writing user projects outside the directory bound by `codex-bridge link`.

The initial production validation used the shared feature rollout evaluator
with an `admin` policy. The stable default is now `all`, so every account can
explicitly select this profile when creating or repairing an endpoint. The Hub
continues to enforce the rollout policy server-side if an operator overrides
it. Existing endpoints keep their selected profile; this release does not
silently migrate them. See
[ADR-010](../adr/010-user-feature-rollouts.md).

The existing `review-required` and `auto-execute` profiles keep their current
arguments and behavior.

The profile follows an environment-capability rule instead of trying to infer
whether arbitrary directories contain compilers. A linked endpoint records the
executable search path from both the invoking shell and the user's default
login shell. Paths deliberately exported there are treated as shared runtime
components; unrelated ordinary home directories remain private.

## Non-Goals

- Do not migrate or change existing endpoints automatically.
- Do not treat Codex `workspace-write` as a filesystem confidentiality
  boundary; it primarily controls writes and is only defense in depth here.
- Do not block model-provider network traffic.
- Do not sandbox the long-lived Bridge process itself. Only managed CLI child
  processes and their descendants enter the filesystem restriction.
- Do not promise support on non-Linux hosts or Linux kernels without Landlock.

## Security Boundary

`strict-workspace` combines:

- Codex's native command sandbox disabled for the managed child, because the
  Bridge applies the stronger outer filesystem boundary before Codex starts;
- `approval_policy=never` and Claude Code bypass permission mode;
- a Bridge child-process wrapper that applies Linux Landlock before executing
  Codex, Claude Code, or an ACP adapter.

The Landlock allowlist grants:

- read/write access to the Bridge-bound workspace and a Bridge-owned private
  runtime directory used through `TMPDIR`;
- read/write access to the native CLI state directories required for model
  configuration, skills, MCP configuration, and native session continuity;
- read/execute access to standard operating-system and runtime trees, including
  `/proc`, `/sys`, `/dev`, `/run`, `/etc`, `/usr`, `/opt`, and `/var`;
- read/execute access to filesystem branches outside the endpoint owner's home
  container, excluding shared `/tmp`;
- read/execute access to existing top-level hidden entries in the real user
  home so provider switchers, CLI launchers, hooks, and language/tool managers
  can keep working without granting writes outside the bound workspace;
- read/execute access to absolute `PATH` entries and their component roots when
  a `bin` or `sbin` directory was explicitly exported by the endpoint's saved
  shell environment; an exported symlink to an otherwise private directory
  exposes only its resolved executable file unless that target also belongs to
  an explicitly exported `bin` or `sbin` component;
- no filesystem access to other unrecognized ordinary directories in the user
  home.

Landlock restrictions are inherited by every command spawned by the CLI. The
wrapper leaves networking unrestricted so model APIs and dependency downloads
continue to work. A requested run directory must resolve inside the configured
workspace root. Symlinks do not expand the allowlist because Landlock checks
the resolved filesystem object.

The Bridge deliberately uses one filesystem boundary in this profile. Codex is
started with its `dangerFullAccess`/bypass sandbox setting only *inside* the
already restricted Landlock process tree. This prevents Codex from nesting a
Bubblewrap mount sandbox whose replacement `/proc` objects conflict with the
inherited Landlock object allowlist. It does not grant host-wide access: every
Codex command remains a descendant of the Bridge wrapper and cannot escape or
relax Landlock. Existing `review-required` and `auto-execute` endpoints retain
their native Codex sandbox arguments unchanged.

This profile is user-directory isolation, not a minimal operating-system
sandbox. Standard system trees and non-home mount branches are deliberately
broad and read-only so nested command sandboxes, compilers, package managers,
Coq, Isabelle, and other proof tools can initialize normally. Shared `/tmp`
remains unavailable; CLI children receive a private writable `TMPDIR` instead.

Linux has no reliable metadata that says whether an ordinary home directory is
a private user project or a public component. The Bridge therefore does not
scan the home directory, recognize product names, consult previous runs, or
guess from ownership. At link time it merges the current process `PATH` with a
clean login shell `PATH` resolved from the real home directory. At run time it
allows those absolute search directories read-only. If such a directory is a
`bin` or `sbin` directory directly below a component, the component is also
read-only so executables can load adjacent libraries and data. Administrators
can add uncommon component roots with `BRIDGE_STRICT_WORKSPACE_READ_ONLY`.
Other ordinary home directories are denied by default.

This rule covers a new machine on its first run without history: anything that
is available in the user's normal current or login-shell executable environment
is available to the endpoint. A tool that exists only inside another private
project's local environment is intentionally unavailable. Automatically using
that tool would require exposing the other project and would contradict the
profile's confidentiality goal; install or activate it as a shared tool, or
place the project-local environment inside the bound workspace.

The first strict-mode run for a workspace seeds private Codex and Claude state
with the existing configuration, credentials, skills, and session state. Later
turns reuse that workspace-scoped state. The child keeps the real `HOME` and
XDG environment for normal shell and tool-manager path resolution, while
`CODEX_HOME`, `CLAUDE_CONFIG_DIR`, and temporary paths point to Bridge-owned
state. Landlock still makes top-level hidden home entries read-only and denies
unrecognized ordinary home directories. Large skill/plugin trees can add
one-time initialization work, but are never linked back to the real CLI state.
`CODEX_BRIDGE_RUNTIME_DIR` can place private state on a machine-appropriate
local filesystem.

The hidden-entry compatibility rule is deliberately read-only and only covers
entries that already exist directly below the real home when a CLI child is
started. It does not make the real home writable, and it does not expose normal
project directories merely because another hidden tool references them. Hidden
entries can contain credentials such as `.ssh`, `.aws`, or `.kube`; selecting
this compatibility-oriented profile therefore protects those entries from
mutation but does not promise their confidentiality.

The bound workspace remains writable as one Landlock subtree, including its
own `.git` metadata. Landlock path-beneath rules are additive and cannot remove
`.git` from an already allowed workspace subtree or pause an `open(2)` call for
a browser decision. Consequently this profile does not claim per-file `.git`
approval. A future implementation would require a separately supervised Git
filesystem/tool proxy; browser approval at the CLI command layer alone cannot
make this kernel boundary both dynamically deniable and dynamically grantable.

Codex configurations generated by provider switchers may contain absolute
`model_instructions_file` paths, while Hub-managed Codex configuration uses an
absolute `model_catalog_json` path. Before entering Landlock, the Bridge copies
each explicitly referenced regular file into the workspace-private Codex home
and rewrites only the private `config.toml` to that copy. Symlink, missing, or
non-regular references fail closed with the field name in the error. The source
configuration is never rewritten. The private copies are refreshed before
every strict-mode CLI start, so later Hub or local switcher changes apply
without relinking the endpoint. The broader hidden-entry compatibility rule
also lets Claude Code execute hidden provider-switcher, hook, and status-line
dependencies read-only without requiring product-specific directory names.

The profile is fail-closed: if Landlock is unavailable or a restriction cannot
be installed, the CLI turn fails with an actionable error instead of running
without isolation.

## Alternatives Considered

- Bubblewrap is a policy construction tool rather than a complete policy. It
  requires user and mount namespaces and creates a replacement filesystem
  view. Nesting it with Codex's own sandbox caused the observed `/proc` access
  failures and varies across distributions.
- NsJail provides broader namespace, cgroup, and seccomp isolation, but adds a
  separately installed privileged runtime and solves a larger containerization
  problem than this profile needs.
- Firejail also depends on a separately installed SUID or namespace sandbox and
  distribution-maintained profiles. Its host setup and version become part of
  every endpoint's compatibility surface.
- AppArmor and SELinux can express host policies, but require administrator
  provisioning and path-specific policy installation, which is unsuitable for
  a user-installed Bridge on arbitrary machines.

Landlock is the smallest mature kernel boundary that an unprivileged Bridge can
apply directly to itself and all descendants without changing the filesystem
layout. The remaining ambiguity is semantic rather than technical: Linux
cannot infer whether an ordinary home directory is a private project or a
shared tool installation. The saved shell `PATH` is therefore the explicit,
first-run capability declaration for user-installed tools.

## Data And Protocol Impact

- No SQLite or WebSocket shape changes.
- `bridge.strict_workspace` and `BRIDGE_STRICT_WORKSPACE` select the child
  process wrapper.
- Bridge capability metadata reports `approvalMode=strict-workspace`.
- Bridge-token APIs include a third `permissionProfiles` entry. Existing
  response fields remain compatible. With the default `all` rollout, every
  account receives that entry.

## Implementation Steps

1. Add the `strict-workspace` link/Hub/UI permission profile.
2. Add Bridge configuration and capability reporting for strict isolation.
3. Re-exec managed CLI commands through an internal sandbox entry point.
4. Apply Landlock read/write and read-only path rules before the target exec.
5. Cover profile command generation, path containment, and kernel enforcement.
6. Rebuild the embedded frontend and update setup documentation.
7. Localize external Codex instruction and model-catalog files referenced by
   the copied configuration without widening the filesystem allowlist.
8. Add read-only compatibility for system/runtime trees, existing top-level
   hidden home entries, and PATH-declared tool components.
9. Avoid nested Codex Bubblewrap under the inherited Landlock boundary while
   retaining the same outer restriction for the complete Codex process tree.
10. Save current and login-shell executable paths when linking an endpoint,
    and preserve real-home path resolution while isolating CLI state.

## Exit Gates

- `npm test`
- `npm run build`
- `/usr/local/go/bin/go test ./...`
- `CGO_ENABLED=0 /usr/local/go/bin/go build -ldflags "-s -w" -o bin/codex-bridge .`
- `make doc-lint`
- A subprocess smoke test can read/write the workspace but cannot read an
  outside file.
- A Codex configuration can read localized `model_instructions_file` and
  `model_catalog_json` copies while the source switcher remains read-only.
- Codex and Claude children can read an existing hidden tool directory but
  cannot write it or read a normal sibling project outside the workspace.
- Nested command sandboxes can read Linux runtime metadata such as
  `/proc/sys/kernel/overflowuid`, while unrecognized ordinary home directories
  remain inaccessible.
- Codex exec, resume, and app-server paths do not create a nested Bubblewrap
  sandbox in strict mode; non-strict profiles keep their existing arguments.
- A first-time endpoint can execute a tool exported by either the invoking or
  login shell without previous Bridge runs, while an unrelated ordinary sibling
  directory remains unreadable.

## Reviewer Q&A

**Why is a private temporary directory allowed?**

Compilers and CLIs require temporary files. `TMPDIR` points at a Bridge-owned
directory unique to the bound workspace, while the host `/tmp` is not exposed.

**Can a user avoid all command approvals in this profile?**

Yes. Commands execute automatically inside the kernel-enforced filesystem
boundary. Out-of-bound access fails with `permission denied`; it does not open
an approval prompt.

**Does this affect an already connected endpoint?**

No. The endpoint must be explicitly linked or repaired with the new profile,
and it requires the updated Bridge binary.

**Must a provider preset be recreated after this compatibility fix?**

No. Applying the preset updates the real CLI configuration as before. The next
strict-mode turn refreshes its private configuration and referenced files.

**Can strict mode automatically borrow a local switch from another project?**

No. A project-local switch belongs inside that other ordinary directory. The
kernel cannot expose its executable and support files while proving that no
other project file can be read. Strict mode uses the current workspace or the
user's explicitly exported shared executable environment instead.

**Why does `.git` not open a browser approval from a filesystem denial?**

Landlock denies filesystem syscalls inside the already-running CLI process and
cannot suspend them for Hub interaction or loosen that process after approval.
The workspace rule also necessarily covers its descendants. Command approval
and filesystem mediation are therefore different boundaries; presenting an
approval that cannot alter the latter would be misleading.
