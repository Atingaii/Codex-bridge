# Sources And Licensing

## Fixture Provenance

The checked-in `.v` and `.thy` fixtures were newly written for this repository.
They do not copy proof scripts, theorem statements, or project files from
CoqGym, PISA, or AFP. The upstream projects determine the benchmark *shape*:
named proof obligations, real proof-assistant execution, theorem-dependency
audits, and deterministic pass/fail scoring. The fixtures therefore remain
subject to this repository's own licensing terms; this repository currently
does not contain a top-level license file.

## Upstream Method References

Metadata was last checked on 2026-07-22.

| Reference | Revision / release | License relevant to reference | How it influenced this suite |
| --- | --- | --- | --- |
| [CoqGym](https://github.com/princeton-vl/CoqGym) | `a739d99cdf5b0451dd8a362d3c541ca3b66112d3` | CoqGym framework is LGPL-2.1-or-later. Its README states that bundled Coq projects are independent projects, so none are vendored here. | Coq cases expose a named environment/goal and evaluate proof completion with the real prover. |
| [PISA](https://github.com/albertqjiang/Portal-to-ISAbelle) | `56def2c39f85d211e1f40cc5765581a567879106` | BSD-3-Clause | Isabelle cases are standalone session-buildable obligations suitable for an interactive proof workflow. |
| [Archive of Formal Proofs](https://www.isa-afp.org/) | Website checked 2026-07-22 | AFP files unmarked or marked BSD use the AFP BSD license; individually marked LGPL files use `LICENSE.LGPL`. No AFP source is vendored here. | Isabelle cases use named sessions, reusable definitions, invariants, and transitive dependency audits. |
| [miniF2F Isabelle split](https://github.com/openai/miniF2F/tree/main/isabelle) | `4e433ff5cadff23f9911a2bb5bbab2d351ce5554` | Apache-2.0 for `isabelle/` | The suite follows the familiar validation-vs-test discipline by keeping task holes separate from local reference solutions. No miniF2F statement is copied. |

## Why The Full Datasets Are Not Bundled

CoqGym's constituent projects have independent source repositories and
licenses. PISA's full AFP workflow requires a version-matched Isabelle/AFP
installation and large prebuilt heaps; its README describes hours of CPU time
and tens of gigabytes for parallel copies. Automatically downloading either
dataset would make ordinary tests network-dependent, slow, and difficult to
audit. This suite instead offers a compact product gate; full upstream
leaderboard evaluation should be maintained as a separate, explicitly pinned
research environment.

## Local Toolchain

The recorded run in `results/2026-07-22-local.md` uses Ubuntu's Coq 8.15.0 and
the official Isabelle2025-2 Linux distribution via
`/home/ubuntu/.local/bin/isabelle`. Proof-assistant binaries and standard
libraries are runtime prerequisites and are not redistributed by this
repository.
