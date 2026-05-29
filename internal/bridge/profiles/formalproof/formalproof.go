package formalproof

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tencent/codex-bridge/internal/bridge/profiles"
)

func TimeoutBoundary(userPrompt string) string {
	if !LooksLikeSystemBRuntimeTask(userPrompt) {
		return ""
	}
	return "Isabelle timeout boundary: if you run a full Isabelle build or long Isabelle compilation, choose and use an explicit timeout appropriate to the task. If that timeout is exceeded, stop the build and report the command, elapsed time, timeout value, and latest output or log. Bridge otherwise does not constrain how you run the CLI task.\n"
}

func RelayGuidance(userPrompt, mode, role string) string {
	if !LooksLikeFormalTask(userPrompt) {
		return ""
	}
	var b strings.Builder
	b.WriteString("Formal proof relay guidance:\n")
	b.WriteString("- Start spec-first: identify the exact theorem/function obligation, name the target fact, map uploaded source constructs to the proof-assistant definitions, and treat build success as a smoke check rather than proof completion.\n")
	b.WriteString("- Use explicit timeouts for proof-assistant commands. Run proof probes serially; do not leave detached or stale Coq/Isabelle/Lean jobs unless you report their PID/log/exit status in the visible answer.\n")
	b.WriteString("- Do not weaken theorem statements, change the target definition's semantics, move the obligation to a helper-only statement, or rely on trust shortcuts such as Axiom, Parameter, Conjecture, Admitted, admit, Abort, sorry, quick_and_dirty, Guard Checking changes, bypass_check, TODO, or placeholders.\n")
	b.WriteString("- For recursive or termination obligations, state the measure/relation or semantic-equivalence obligation before coding. If you use bounded/fuel wrappers, fixed default fuel, or structurally recursive helper functions, also prove equivalence to the original semantics plus decrease/well-foundedness and fuel sufficiency; otherwise report needs_next or blocked.\n")
	b.WriteString("- Include audit evidence when available: source-only shortcut scans, the project build command, Coq/Rocq Print Assumptions or equivalent dependency audit, Lean #print axioms, Isabelle thm_oracles/oracle-free audit, and the named target theorem/fact.\n")
	b.WriteString("- Keep handoffs falsifiable: target theorem/fact, uploaded-source mapping, branch/decrease obligation, semantic constraints, attempted proof path, exact blocker, commands run, and remaining risks.\n")
	b.WriteString("- Browser-visible result rule: the final answer must include a concise Chinese \"最终测试结果/最终结论\" section that says whether the original user requirement is satisfied, lists the exact build/audit commands, and names any unmet proof obligation. Do not leave the user to infer the result only from command logs.\n")
	if guidance := systemARelayGuidance(userPrompt); guidance != "" {
		b.WriteString(guidance)
	}
	if guidance := systemBRelayGuidance(userPrompt); guidance != "" {
		b.WriteString(guidance)
	}
	if mode == "debate" {
		if role == "critic" {
			b.WriteString("- Debate critic focus: first try to falsify the proof by checking weakened statements, hidden assumptions, fuel/default_fuel shortcuts, missing equivalence lemmas, and obligations that were moved rather than discharged.\n")
		} else {
			b.WriteString("- Debate proposer focus: leave a falsifiable proof claim with named audit commands and explain why it preserves the original obligation without fuel shortcuts or added assumptions.\n")
		}
	}
	return b.String()
}

func InitialStrategy(mode, firstCLI, userPrompt string) string {
	if !LooksLikeFormalTask(userPrompt) {
		return ""
	}
	firstCLI = normalizeFirstCLI(firstCLI)
	var b strings.Builder
	b.WriteString("Initial orchestration strategy for this formal-proof task:\n")
	if mode == "debate" {
		b.WriteString("- Use proposer/critic flow. The proposer makes one falsifiable proof claim or patch with named checks; the critic first tries to refute it by inspecting statements, definitions, trust assumptions, and build/audit output.\n")
	} else {
		b.WriteString("- Use implementer/reviewer flow. The implementer builds the smallest faithful project and proof-obligation ledger; the reviewer independently falsifies completion claims before any resolved handoff.\n")
	}
	if firstCLI == "codex" {
		b.WriteString("- Because Codex starts first, use it as the verifier/planner first: identify the original obligation, expected target theorem/fact, forbidden shortcuts, and exact evidence the next turn must produce before broad proof search.\n")
	} else {
		b.WriteString("- Because Claude starts first, use it as the builder first: create the visible project and ledger before attempting long proof search, then hand the audit checklist to the next CLI.\n")
	}
	b.WriteString("- Stop blind proof search after three failed strategies or one long proof-assistant attempt. Hand off a ledger instead of repeating similar measure guesses.\n")
	b.WriteString("- Acceptance is not compile-only: the visible final result must say whether the original theorem/termination obligation is discharged, not merely whether a generated project builds.\n")
	return b.String()
}

func TaskGuidance(userPrompt, mode, role string) string {
	if !LooksLikeFormalTask(userPrompt) {
		return ""
	}
	var b strings.Builder
	b.WriteString("Formal proof task guardrails:\n")
	b.WriteString("- Work spec-first: identify the exact theorem/function obligation before changing code, name the target fact, and keep a short mapping from the uploaded source constructs to the proof-assistant definitions you create.\n")
	b.WriteString("- Treat build success as a smoke check only. The acceptance criterion is the requested proof obligation, not merely compiling.\n")
	b.WriteString("- Run proof-assistant commands serially and wait for each result before starting the next one. Do not launch parallel or detached tool calls for Coq/Isabelle builds, version checks, scans, or proof probes; stale in-progress commands make the browser smoke result unverifiable. The only exception is the controlled Isabelle long-build log workflow below, where later commands may only poll/tail that same build and must not start other proof probes or a second build.\n")
	b.WriteString("- Use explicit timeouts for every proof-assistant/toolchain command. Quick probes such as coqc --version, rocq --version, isabelle version, command -v, and rg scans should use timeout 10s to 60s; full builds may use longer documented timeouts. If a required tool is missing or a probe times out, stop and report a visible needs_next/blocked ledger instead of hanging the run.\n")
	b.WriteString("- Do not weaken theorem statements, change the target definition's semantics, move the obligation elsewhere, or add trust assumptions such as Axiom, Parameter, Conjecture, Admitted, admit, Abort, sorry, quick_and_dirty, Guard Checking changes, bypass_check, TODO, or placeholders.\n")
	b.WriteString("- If you introduce a bounded/fuel wrapper or default fuel for a recursive function, you must also prove equivalence to the original recursive semantics, the required termination/decrease measure, and that the default fuel is sufficient for every intended input. Otherwise report status=needs_next or blocked.\n")
	b.WriteString("- Include a proof audit when relevant: placeholder scans with rg, Coq Print Assumptions <target> showing Closed under the global context, Lean #print axioms <target>, Isabelle thm_oracles <target>, and the project build command.\n")
	b.WriteString("- Reviewer falsification checklist: print or inspect the final target definition/fact, compare it to the original uploaded obligation, reject helper-only rewrites without equivalence, and verify that scans/audits ran on the deliverable project instead of hidden staging or scratch files.\n")
	b.WriteString("- Keep a proof-obligation ledger in the handoff: target theorem/definition, uploaded-source mapping, branch/decrease obligation, semantic constraints, attempted proof path, exact blocker, and verification command.\n")
	b.WriteString("- Exploration budget: after at most three failed proof strategies or one long proof-assistant build/proof attempt, stop blind search and hand off a compact obligation ledger with the failed goals, attempted measures/relations, and the next most promising lemma. Do not spend the entire turn repeating similar measure guesses.\n")
	if guidance := systemATaskGuidance(userPrompt); guidance != "" {
		b.WriteString(guidance)
	}
	if guidance := systemBTaskGuidance(userPrompt); guidance != "" {
		b.WriteString(guidance)
	}
	if mode == "debate" {
		b.WriteString("- Debate proof workflow: the proposer must leave a falsifiable proof claim or patch, and the critic must decide whether the original obligation is actually discharged; unresolved falsification blocks status=resolved.\n")
		if role == "critic" {
			b.WriteString("- Debate critic strategy: first try to falsify the proof by checking for weakened statements, fuel/default_fuel shortcuts, hidden axioms/admissions, missing equivalence lemmas, or obligations that were made unprovable but hidden by wrappers.\n")
		} else {
			b.WriteString("- Debate proposer strategy: present the strongest proof plan or patch, name the exact lemmas/audit commands that would validate it, and explicitly state why it preserves the original statement without fuel shortcuts or added assumptions.\n")
		}
	} else if role == "reviewer" || role == "verifier" {
		b.WriteString("- Reviewer strategy: inspect the diff and proof script for semantic weakening before accepting any successful build; reject compile-only evidence when the proof obligation remains open.\n")
	} else {
		b.WriteString("- Implementer strategy: prefer proving the original obligation directly or proving a well-founded decrease/equivalence lemma before changing definitions. For hard formal tasks, first establish a minimal reproducible obligation, then switch to lemma planning/reviewer handoff instead of continuing unbounded trial-and-error.\n")
	}
	return b.String()
}

func VerifierGuidance(userPrompt, mode string) string {
	if !LooksLikeFormalTask(userPrompt) {
		return ""
	}
	lines := []string{
		"Formal proof final verifier guardrails:",
		"- Verify the original proof obligation, not just that Coq/Isabelle/Lean accepts the project.",
		"- Reject status=resolved if any target theorem/definition was weakened, any proof obligation was replaced by a bounded/fuel wrapper without equivalence and fuel-sufficiency proofs, or any Axiom/Parameter/Conjecture/Admitted/admit/Abort/sorry/oops/sketch/quick_and_dirty/Guard Checking/bypass_check/TODO/placeholder remains.",
		"- Prefer proof-assistant dependency checks when available: Coq Print Assumptions <target> with Closed under the global context, Lean #print axioms <target>, Isabelle thm_oracles <target>, plus placeholder scans and the project build command.",
		"- For termination tasks, require evidence of the actual decrease/well-founded measure or a proof that the encoded recursion is equivalent to the original semantics for all intended inputs.",
		"- End with a multi-dimensional result assessment that is visible to the browser: uploaded inputs accounted for, new project/workspace path, build result, placeholder scan, proof-assistant assumption/oracle check, original obligation/equivalence or termination audit, and remaining risks.",
	}
	if LooksLikeSystemAUploadBenchmark(userPrompt) {
		lines = append(lines,
			"- This task matches the Coq upload benchmark with Model.thy, Termination.thy, and ROOT. Your visible final conclusion must explicitly assess: Model.thy/Termination.thy/ROOT were used, a new Coq project folder was written under the requested cwd, make/coqc passed, source-only placeholder scan found no forbidden tokens, Coq Print Assumptions showed Closed under the global context for the named target theorem, Print/inspection of modify_lin did not reveal helper-only semantic weakening, and a named theorem states the original termination modify_lin obligation with branch-decrease or equivalence evidence. Reject compile-only projects, tautological proofs, or modify_lin_fuel/default_fuel unless equivalence, decrease, and fuel sufficiency are proved.",
		)
	}
	if LooksLikeSystemBUploadBenchmark(userPrompt) {
		lines = append(lines,
			"- This task matches the Isabelle upload benchmark with Model.thy, Termination.thy, and ROOT. Your visible final conclusion must explicitly assess: Model.thy/Termination.thy/ROOT were used, a new Isabelle project folder was written under the requested cwd, the ROOT layout was preserved or deliberately remapped, timeout-aware isabelle build passed with no detached/background build left unresolved, source-only scan found no sorry/quick_and_dirty/oops/sketch/admit/placeholders and no diagnostic leftovers such as Repro.thy or *_original.thy, Isabelle thm_oracles or oracle-free audit was run for the target facts, and termination modify_lin was actually proved with branch-decrease evidence rather than replaced by a compile-only framework. If an Isabelle build was too long and handed off for manual execution, do not rerun that same build automatically; report the manual command, build log path, current tail/exit status, and that final acceptance is pending the user's manual build result.",
		)
	}
	if mode == "debate" {
		lines = append(lines, "- Debate verifier strategy: synthesize the adversarial result; concrete critic falsification of weakened semantics, fuel/default_fuel shortcuts, missing equivalence, or hidden assumptions overrides proposer confidence.")
	}
	return strings.Join(lines, "\n")
}

func systemARelayGuidance(userPrompt string) string {
	if !LooksLikeSystemATask(userPrompt) {
		return ""
	}
	var b strings.Builder
	b.WriteString("- Coq/Rocq workflow: create a self-contained _CoqProject/Makefile style project when converting uploaded files, run make or coqc from the project root, and keep a traceable mapping from Isabelle names such as modify_lin to Coq definitions/theorems.\n")
	b.WriteString("- Coq/Rocq audit: scan deliverable .v files for proof shortcuts, run Print Assumptions on the named target theorem, and require Closed under the global context before claiming a completed proof.\n")
	if LooksLikeSystemAUploadBenchmark(userPrompt) {
		b.WriteString("- Coq upload benchmark: for Model.thy, Termination.thy, and ROOT, the visible result should account for all three uploads, write a new Coq project under the requested cwd, pass make/coqc, run a source-only shortcut scan, run Print Assumptions on a named modify_lin target, and audit the original termination modify_lin obligation. modify_lin_fuel/default_fuel or fixed-fuel translations remain unresolved unless equivalence, decrease, and fuel sufficiency are proved.\n")
		b.WriteString("- Coq upload benchmark final checklist: explicitly report uploaded files used, new project path, target theorem name, make/coqc result, shortcut scan result, Print Assumptions result, final modify_lin definition inspection, original-obligation/decrease/equivalence evidence, and remaining risks.\n")
	}
	return b.String()
}

func systemBRelayGuidance(userPrompt string) string {
	if !LooksLikeSystemBTask(userPrompt) {
		return ""
	}
	var b strings.Builder
	b.WriteString("- Isabelle workflow: preserve or deliberately remap the uploaded ROOT/session layout, keep scratch theories out of the final project, scan deliverable .thy/ROOT files for fake proofs or diagnostic leftovers, and audit target facts with thm_oracles or an equivalent oracle-free check when available.\n")
	b.WriteString("- Isabelle termination workflow: for termination modify_lin, prove the original function's termination with a concrete measure/relation and branch decrease evidence. Do not hide the obligation by changing recursive equations, deleting recursion, importing scratch files, or leaving sorry/quick_and_dirty/oops/sketch/admit.\n")
	return b.String()
}

func systemATaskGuidance(userPrompt string) string {
	if !LooksLikeSystemATask(userPrompt) {
		return ""
	}
	var b strings.Builder
	b.WriteString("- Coq/Rocq workflow: create a self-contained project with _CoqProject/Makefile when converting uploaded sources, keep original semantic names mapped in comments or theorem names, and run make or coqc from the project root.\n")
	b.WriteString("- Coq/Rocq toolchain probe: prefer POSIX-safe short probes such as timeout 20s sh -lc 'type -P coqc || type -P rocq || true' or timeout 20s python3 -c 'import shutil; print(shutil.which(\"coqc\") or shutil.which(\"rocq\") or \"\")'. Do not run bare coqc --version or timeout 20s command -v coqc before a path is known; command is a shell builtin and has caused stale tool events on remote smoke machines.\n")
	b.WriteString("- Coq/Rocq spec-first plan: before coding the final proof, define or document the original Isabelle modify_lin step relation, the intended well-founded relation/measure, and the named theorem that corresponds to termination modify_lin. Use names such as modify_lin_original_terminates, modify_lin_step_decreases, or modify_lin_semantics_equiv so the verifier can audit the target.\n")
	b.WriteString("- Coq/Rocq modeling rule: make the target theorem explicit before coding the proof. For an Isabelle termination benchmark, name the Coq theorem that corresponds to original termination modify_lin, define the intended well-founded relation or semantic equivalence, and keep a traceable mapping from Isabelle constructors/functions to Coq definitions. The theorem must mention modify_lin and the relevant relation/equivalence/decrease invariant; a tautology, length-only lemma, theorem about a bounded evaluator, or helper-only structural recursion totality is not sufficient.\n")
	b.WriteString("- Coq/Rocq audit: scan only project source for Axiom, Parameter, Variable, Hypothesis, Conjecture, Admitted, admit, Abort, sorry, TODO, placeholder, quick_and_dirty, Guard Checking, bypass_check, modify_lin_fuel, default_fuel, fixed_fuel, fuel_wrapper, and fuel wrappers; then run Print Assumptions on the target theorem(s) and require Closed under the global context.\n")
	b.WriteString("- Coq/Rocq verifier checks: run Print modify_lin or otherwise inspect the final definition, run Print Assumptions <target>, and reject results where recursive behavior was replaced by modify_loop/structural helper code unless a named equivalence/bisimulation theorem connects it to the original Isabelle step relation.\n")
	b.WriteString("- Coq/Rocq termination rule: structural recursion, Program Fixpoint, Function, Equations, or well-founded recursion is acceptable only when the visible theorem proves the original modify_lin termination/equivalence obligation; fixed fuel wrappers are unresolved unless equivalence, decrease, and fuel sufficiency are proved.\n")
	return b.String()
}

func systemBTaskGuidance(userPrompt string) string {
	if !LooksLikeSystemBTask(userPrompt) {
		return ""
	}
	var b strings.Builder
	b.WriteString("- Isabelle workflow: keep the uploaded ROOT/Model.thy/Termination.thy semantics intact unless the user explicitly asks for a new session; when ROOT names subdirectories such as directories \"HWQ-U\", either recreate that layout or minimally adjust ROOT to the visible project layout before proving, and document the mapping. Write any new proof project under the requested cwd. For any full `isabelle build -D` / `isabelle build -d` check, use the controlled background build template below instead of a foreground build command.\n")
	b.WriteString("- Isabelle scratch discipline: use a scratch directory or temporary theory outside the final ROOT session for extracting generated subgoals. Scratch probes must not use sorry/quick_and_dirty/oops/sketch/admit as fake proof steps; obtain subgoals by running an incomplete candidate proof and capturing Isabelle's failure output. If you create Repro.thy, *_original.thy, scratch theories, or diagnostic ROOT imports, remove them from the final project and restore ROOT before the final source scan/build. The final source scan must cover only deliverable ROOT/.thy files and must not report leftovers from failed attempts.\n")
	b.WriteString("- Isabelle audit: scan source-only deliverable files for sorry, quick_and_dirty, oops, sketch, admit, TODO, placeholder, disabled checks, Repro.thy, *_original.thy, and scratch leftovers; inspect ROOT for quick_and_dirty and diagnostic imports; run thm_oracles or an equivalent oracle audit on the target theorem(s) and report the result.\n")
	b.WriteString("- Isabelle full-build visibility rule: every full `isabelle build -D` or `isabelle build -d` check must use controlled background execution so the browser sees progress. Do not run `timeout ... isabelle build ...`, `isabelle build ... -v`, or `isabelle build ... | tee build.log` as one foreground command. Use this pattern from the project directory, adapting only the timeout and session path: `sh -lc 'rm -f build.log build.pid build.pgid build.exit; setsid sh -lc \"echo \\$\\$ > build.pid; echo \\$\\$ > build.pgid; timeout 45m sh -lc '\\''isabelle build -D .'\\'' >build.log 2>&1; echo \\$? > build.exit\" &'`. Then run separate short polling commands: `tail -n 80 build.log` and `sh -lc 'test -f build.exit && cat build.exit || ps -p \"$(cat build.pid)\" -o pid,stat,etime,cmd'`. To stop or hand off a still-running build, run `sh -lc 'test -f build.exit || kill -- -\"$(cat build.pgid)\"'` and then tail the log plus print build.pid/build.pgid/build.exit state. Stop polling after a bounded wait and either report the final exit code or hand off the manual command/log/PID/PGID/exit state. Controlled background is only for this one Isabelle build; do not start other proof probes or a second build while it is active.\n")
	b.WriteString("- Isabelle manual-build handoff rule: if a full isabelle build exceeds the turn's practical waiting window or must be left for the user, stop the build or leave a clearly named log/PID/exit-status file, then end with status=needs_next or blocked. The visible final answer must tell the user the exact manual command, log path, how to tail the log, and whether a PID/exit file exists. Later CLI turns must not rerun the same long isabelle build automatically; they should inspect source, existing logs, PID/exit files, and summarize the manual follow-up instead.\n")
	b.WriteString("- Isabelle termination workflow: first extract the generated termination subgoals in scratch, then restore the final project and try Isabelle's lexicographic_order once, followed by at most two concrete relation/measure/measures attempts; before using guessed simp facts such as *_def rules, confirm them with find_theorems name:<pattern> or thm <fact>. After the bounded attempts, summarize the exact recursive calls, undefined facts, and failed decrease goals instead of continuing blind search.\n")
	b.WriteString("- Isabelle verifier checks: inspect the final ROOT imports, run a source-only scan on deliverable files, confirm the named target fact with thm/thm_oracles when available, and reject any result where termination modify_lin was hidden by changing the function, deleting recursive equations, or leaving a scratch theory imported.\n")
	b.WriteString("- Isabelle termination rule: for termination modify_lin, prove the original function termination obligation with a concrete measure/relation and branch decrease lemmas; a compile-only framework, weakened theorem, removed recursion, changed function semantics, or remaining sorry is unresolved.\n")
	return b.String()
}

func LooksLikeFormalTask(text string) bool {
	lower := strings.ToLower(text)
	return containsAny(lower, []string{
		"coq", "isabelle", "lean", ".v", ".thy", ".lean", "_coqproject",
		"theorem", "lemma", "proof", "termination", "well-founded", "well founded",
		"sorry", "admitted", "admit", "axiom", "parameter", "conjecture", "quick_and_dirty", "bypass_check", "placeholder",
		"定理", "引理", "证明", "终止", "递归", "补全缺失的证明", "占位符",
	})
}

func LooksLikeSystemATask(text string) bool {
	lower := strings.ToLower(text)
	return containsAny(lower, []string{"coq", "rocq", ".v", "_coqproject", "coqc", "print assumptions"})
}

func looksLikeExplicitSystemAConversion(lower string) bool {
	if !containsAny(lower, []string{"coq", "rocq", "_coqproject", ".v", "coqc"}) {
		return false
	}
	return containsAny(lower, []string{
		"做成coq", "coq 证明项目", "coq证明项目", "coq 项目", "coq项目",
		"new coq project", "coq proof project", "convert", "translation", "translate", "转换", "转成", "翻译",
	})
}

func LooksLikeSystemBRuntimeTask(text string) bool {
	lower := strings.ToLower(text)
	if looksLikeExplicitSystemAConversion(lower) {
		return false
	}
	return LooksLikeSystemBTask(text)
}

func LooksLikeSystemBTask(text string) bool {
	lower := strings.ToLower(text)
	return containsAny(lower, []string{"isabelle", ".thy", "thm_oracles", "quick_and_dirty"}) ||
		(strings.Contains(lower, "termination.thy") && strings.Contains(lower, "root") && strings.Contains(lower, "model.thy") && containsAny(lower, []string{"termination", "modify_lin", "sorry"}))
}

func LooksLikeSystemAUploadBenchmark(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "coq") &&
		strings.Contains(lower, "model.thy") &&
		strings.Contains(lower, "termination.thy") &&
		strings.Contains(lower, "root") &&
		containsAny(lower, []string{"补全缺失的证明", "占位符", "placeholder", "modify_lin", "termination"})
}

func LooksLikeSystemBUploadBenchmark(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "model.thy") &&
		strings.Contains(lower, "termination.thy") &&
		strings.Contains(lower, "root") &&
		containsAny(lower, []string{"isabelle", "termination modify_lin", "modify_lin", "sorry", "主定理", "终止"})
}

func AssessmentGap(userPrompt string, history []profiles.Turn, changes profiles.WorkspaceChangeReport, memory map[string]profiles.CommandFingerprint) (string, bool) {
	if !LooksLikeFormalTask(userPrompt) {
		return "", false
	}
	if reason, ok := AcceptanceFailure(userPrompt, history); ok {
		return reason, true
	}
	if LooksLikeSystemAUploadBenchmark(userPrompt) {
		return systemAUploadAssessmentGap(history, changes)
	}
	if LooksLikeSystemBUploadBenchmark(userPrompt) {
		return systemBUploadAssessmentGap(history, changes, memory)
	}
	if !requiresStrictAssessment(userPrompt) {
		evidence := CollectEvidence(history, changes)
		if evidence.FuelShortcut && !evidence.FuelJustified {
			return "formal proof assessment failed: bounded/fuel wrapper is present without explicit equivalence, decrease, and sufficiency evidence", true
		}
		return "", false
	}
	return genericAssessmentGap(userPrompt, history)
}

func requiresStrictAssessment(userPrompt string) bool {
	lower := strings.ToLower(userPrompt)
	return containsAny(lower, []string{
		"不能用", "不使用", "不要用", "无占位", "占位符", "补全缺失的证明", "完整证明", "正式 proof", "正式证明",
		"no placeholder", "without placeholder", "without any placeholder", "no admitted", "without admitted",
		"no axiom", "without axiom", "no sorry", "without sorry", "complete proof",
	})
}

func systemAUploadAssessmentGap(history []profiles.Turn, changes profiles.WorkspaceChangeReport) (string, bool) {
	evidence := CollectEvidence(history, changes)
	var missing []string
	if !evidence.SystemAInputs {
		missing = append(missing, "uploaded Model.thy/Termination.thy/ROOT were not accounted for")
	}
	if !evidence.ProjectPath {
		missing = append(missing, "new Coq project folder under the requested cwd is not evidenced")
	}
	if !evidence.SystemABuild {
		missing = append(missing, "Coq build evidence is missing")
	}
	if !evidence.PlaceholderScan {
		missing = append(missing, "source placeholder scan evidence is missing")
	}
	if !evidence.AssumptionAudit {
		missing = append(missing, "Coq Print Assumptions/global-context audit evidence is missing")
	}
	if !evidence.OriginalObligation {
		missing = append(missing, "original termination/modify_lin obligation audit evidence is missing")
	}
	if !evidence.NamedTarget {
		missing = append(missing, "named Coq target theorem for termination/modify_lin is missing")
	}
	if !evidence.BranchAudit {
		missing = append(missing, "branch-decrease or original-semantics equivalence audit evidence is missing")
	}
	if evidence.SemanticWeakening {
		return "formal proof assessment failed: target definition/theorem appears semantically weakened or lacks equivalence to the original recursive modify_lin obligation", true
	}
	if evidence.TrivialObligation {
		return "formal proof assessment failed: tautological/reflexivity theorem does not discharge the original termination/modify_lin obligation", true
	}
	if evidence.FuelShortcut && !evidence.FuelJustified {
		return "formal proof assessment failed: modify_lin_fuel/default_fuel or fuel shortcut is present without explicit equivalence, decrease, and fuel-sufficiency evidence", true
	}
	if len(missing) > 0 {
		return "formal proof assessment incomplete: " + strings.Join(missing, "; "), true
	}
	return "", false
}

func systemBUploadAssessmentGap(history []profiles.Turn, changes profiles.WorkspaceChangeReport, memory map[string]profiles.CommandFingerprint) (string, bool) {
	evidence := CollectEvidence(history, changes)
	var missing []string
	if !evidence.SystemAInputs {
		missing = append(missing, "uploaded Model.thy/Termination.thy/ROOT were not accounted for")
	}
	if !evidence.ProjectPath {
		missing = append(missing, "new Isabelle project folder under the requested cwd is not evidenced")
	}
	if !evidence.Build {
		missing = append(missing, "Isabelle build evidence is missing")
	}
	if !evidence.PlaceholderScan {
		missing = append(missing, "source scan for sorry/quick_and_dirty/oops/sketch/admit/placeholders is missing")
	}
	if !evidence.AssumptionAudit {
		missing = append(missing, "Isabelle thm_oracles/oracle-free audit evidence is missing")
	}
	if !evidence.OriginalObligation {
		missing = append(missing, "original termination/modify_lin obligation audit evidence is missing")
	}
	if !evidence.NamedTarget {
		missing = append(missing, "named Isabelle target fact for termination/modify_lin is missing")
	}
	if !evidence.BranchAudit {
		missing = append(missing, "branch-decrease audit evidence is missing")
	}
	if evidence.SemanticWeakening {
		return "formal proof assessment failed: target theorem/function appears weakened or replaced instead of proving original termination modify_lin", true
	}
	if evidence.TrivialObligation {
		return "formal proof assessment failed: tautological/reflexivity theorem does not discharge the original termination/modify_lin obligation", true
	}
	if len(missing) > 0 {
		if reason := ManualBuildReason("", history, memory); reason != "" {
			return reason, true
		}
		return "formal proof assessment incomplete: " + strings.Join(missing, "; "), true
	}
	return "", false
}

func genericAssessmentGap(userPrompt string, history []profiles.Turn) (string, bool) {
	evidence := CollectEvidence(history, profiles.WorkspaceChangeReport{})
	var missing []string
	lowerPrompt := strings.ToLower(userPrompt)
	if !evidence.Build {
		missing = append(missing, "proof-assistant build evidence is missing")
	}
	if containsAny(lowerPrompt, []string{"占位符", "placeholder", "sorry", "admitted", "admit", "axiom", "quick_and_dirty"}) && !evidence.PlaceholderScan {
		missing = append(missing, "placeholder/assumption scan evidence is missing")
	}
	if containsAny(lowerPrompt, []string{"coq", ".v"}) && !evidence.AssumptionAudit {
		missing = append(missing, "Coq Print Assumptions/global-context audit evidence is missing")
	}
	if containsAny(lowerPrompt, []string{"lean", ".lean"}) && !evidence.AssumptionAudit {
		missing = append(missing, "Lean #print axioms audit evidence is missing")
	}
	if containsAny(lowerPrompt, []string{"isabelle", ".thy"}) && !evidence.AssumptionAudit && containsAny(lowerPrompt, []string{"oracle", "quick_and_dirty", "sorry", "占位符", "placeholder"}) {
		missing = append(missing, "Isabelle thm_oracles or placeholder audit evidence is missing")
	}
	if evidence.FuelShortcut && !evidence.FuelJustified {
		return "formal proof assessment failed: bounded/fuel wrapper is present without explicit equivalence, decrease, and sufficiency evidence", true
	}
	if evidence.SemanticWeakening {
		return "formal proof assessment failed: target theorem/function appears weakened or lacks required equivalence evidence", true
	}
	if evidence.TrivialObligation {
		return "formal proof assessment failed: tautological/reflexivity theorem does not discharge the requested proof obligation", true
	}
	if len(missing) > 0 {
		return "formal proof assessment incomplete: " + strings.Join(missing, "; "), true
	}
	return "", false
}

type Evidence struct {
	SystemAInputs      bool
	ProjectPath        bool
	Build              bool
	SystemABuild       bool
	PlaceholderScan    bool
	AssumptionAudit    bool
	OriginalObligation bool
	NamedTarget        bool
	BranchAudit        bool
	SemanticWeakening  bool
	TrivialObligation  bool
	FuelShortcut       bool
	FuelJustified      bool
}

func CollectEvidence(history []profiles.Turn, changes profiles.WorkspaceChangeReport) Evidence {
	text := strings.ToLower(assessmentText(history))
	commandText, outputText := assessmentCommandText(history)
	commandLower := strings.ToLower(commandText)
	outputLower := strings.ToLower(outputText)
	combined := strings.Join([]string{text, commandLower, outputLower, strings.ToLower(strings.Join(changes.Changed, "\n"))}, "\n")
	return Evidence{
		SystemAInputs:      containsAll(combined, []string{"model.thy", "termination.thy", "root"}),
		ProjectPath:        projectPathEvidence(combined, changes),
		Build:              buildEvidence(combined),
		SystemABuild:       systemABuildEvidence(combined),
		PlaceholderScan:    PlaceholderScanEvidenceForHistory(history, text),
		AssumptionAudit:    assumptionAuditEvidence(history, combined),
		OriginalObligation: originalObligationEvidence(combined),
		NamedTarget:        namedTargetEvidence(combined),
		BranchAudit:        branchDecreaseOrEquivalenceEvidence(combined),
		SemanticWeakening:  semanticWeakeningEvidence(combined),
		TrivialObligation:  trivialObligationEvidence(combined),
		FuelShortcut:       fuelShortcutEvidence(outputLower, text),
		FuelJustified:      fuelJustificationEvidence(combined),
	}
}

func assessmentText(history []profiles.Turn) string {
	var b strings.Builder
	for _, item := range history {
		b.WriteString(item.Content)
		b.WriteByte('\n')
		b.WriteString(item.Handoff)
		b.WriteByte('\n')
		b.WriteString(item.HandoffFields.Changed)
		b.WriteByte('\n')
		b.WriteString(item.HandoffFields.Verified)
		b.WriteByte('\n')
		b.WriteString(item.HandoffFields.Next)
		b.WriteByte('\n')
		b.WriteString(item.HandoffFields.Risks)
		b.WriteByte('\n')
	}
	return b.String()
}

func assessmentCommandText(history []profiles.Turn) (string, string) {
	var commands strings.Builder
	var outputs strings.Builder
	for _, command := range commandStates(history) {
		commands.WriteString(command.Command)
		commands.WriteByte('\n')
		outputs.WriteString(command.Output)
		outputs.WriteByte('\n')
	}
	return commands.String(), outputs.String()
}

func projectPathEvidence(text string, changes profiles.WorkspaceChangeReport) bool {
	if projectPathInWorkspaceChanges(changes) {
		return true
	}
	return containsAny(text, []string{
		"new coq project", "new coq project folder", "coq project folder", "coq project", "新建文件夹", "新建 coq", "新建 coq 项目", "项目目录", "项目文件夹", "项目路径", "project=", "/root/tencent/coq-", "/home/zy/study/coq-", "coq-lin-lattice",
		"new isabelle project", "new isabelle project folder", "isabelle project folder", "isabelle project", "新建 isabelle", "新建 isabelle 项目", "isabelle 项目", "/home/zy/study/isabelle", "termination_framework", "isabelle_modified_project",
	})
}

func projectPathInWorkspaceChanges(changes profiles.WorkspaceChangeReport) bool {
	for _, changed := range changes.Changed {
		rel := filepath.ToSlash(strings.TrimSpace(changed))
		if rel == "" || strings.HasPrefix(rel, ".codex-bridge/") || strings.HasPrefix(rel, "./") {
			continue
		}
		parts := strings.Split(rel, "/")
		if len(parts) < 2 || parts[0] == "" || strings.HasPrefix(parts[0], ".") {
			continue
		}
		if projectFileName(parts[len(parts)-1]) {
			return true
		}
	}
	return false
}

func projectFileName(name string) bool {
	switch strings.ToLower(name) {
	case "model.v", "termination.v", "makefile", "_coqproject", "root", "model.thy", "termination.thy":
		return true
	default:
		return strings.HasSuffix(strings.ToLower(name), ".thy") || strings.HasSuffix(strings.ToLower(name), ".v")
	}
}

func buildEvidence(text string) bool {
	return containsAny(text, []string{
		"make", "coqc", "coq_makefile", "dune build", "lake build", "lean --make", "isabelle build",
		"build completed", "compiled", "构建通过", "编译通过",
	})
}

func systemABuildEvidence(text string) bool {
	return containsAny(text, []string{
		"coq build", "coqc", "coq_makefile", "make", "rocq make", "rocq compile", "构建通过", "编译通过",
	})
}

func PlaceholderScanEvidence(commandText, outputText, proseText string) bool {
	commandText = strings.ToLower(commandText)
	outputText = strings.ToLower(outputText)
	proseText = strings.ToLower(proseText)
	if placeholderScanFoundForbiddenOutput(outputText) {
		return false
	}
	if containsAny(proseText, []string{
		"no placeholders", "no forbidden tokens", "没有占位符", "未发现占位符", "无占位符",
		"未发现 admitted", "未发现 axiom", "未发现 sorry", "未发现 quick_and_dirty", "no admitted", "no axiom", "no sorry", "no quick_and_dirty",
		"source-only placeholder scan",
	}) {
		return true
	}
	if isShortcutScanCommand(commandText) && strings.TrimSpace(outputText) == "" {
		return true
	}
	return false
}

func PlaceholderScanEvidenceForHistory(history []profiles.Turn, proseText string) bool {
	sawScan := false
	emptyScan := false
	for _, command := range commandStates(history) {
		commandText := strings.ToLower(command.Command)
		if !isShortcutScanCommand(commandText) {
			continue
		}
		sawScan = true
		outputText := strings.ToLower(command.Output)
		if placeholderScanFoundForbiddenOutput(outputText) {
			return false
		}
		if strings.TrimSpace(outputText) == "" || scanOutputSaysNoMatches(outputText) {
			emptyScan = true
		}
	}
	if sawScan {
		return emptyScan
	}
	return PlaceholderScanEvidence("", "", proseText)
}

func isShortcutScanCommand(commandText string) bool {
	return containsAny(commandText, []string{"rg ", "grep ", "ripgrep"}) &&
		containsAny(commandText, forbiddenShortcutSignals())
}

func scanOutputSaysNoMatches(outputText string) bool {
	lower := strings.ToLower(strings.TrimSpace(outputText))
	return lower == "" || containsAny(lower, []string{"no matches", "no output", "not found", "无输出"})
}

func placeholderScanFoundForbiddenOutput(outputText string) bool {
	if strings.TrimSpace(outputText) == "" {
		return false
	}
	lines := strings.Split(outputText, "\n")
	for _, line := range lines {
		lower := strings.ToLower(strings.TrimSpace(line))
		if lower == "" {
			continue
		}
		if strings.Contains(lower, "no matches") || strings.Contains(lower, "no output") || strings.Contains(lower, "not found") {
			continue
		}
		if containsAny(lower, forbiddenShortcutSignals()) {
			return true
		}
	}
	return false
}

func PlaceholderScanFoundForbiddenOutputForTest(outputText string) bool {
	return placeholderScanFoundForbiddenOutput(outputText)
}

func forbiddenShortcutSignals() []string {
	return []string{
		"admitted", "admit", "axiom", "parameter", "variable", "hypothesis", "conjecture", "abort", "sorry", "todo", "placeholder",
		"quick_and_dirty", "oops", "sketch", "guard checking", "guardchecking", "bypass_check", "bypass check",
		"repro.thy", "_original.thy", "scratch.thy", "scratch_", "diagnostic-only", "diagnostic only",
	}
}

func assumptionAuditEvidence(history []profiles.Turn, text string) bool {
	if sawAssumptionAuditCommand(history) {
		return successfulAssumptionAuditCommand(history)
	}
	for _, line := range strings.Split(text, "\n") {
		lower := strings.ToLower(strings.TrimSpace(line))
		if lower == "" || lineNegatesAssumptionAudit(lower) {
			continue
		}
		if containsAny(lower, []string{
			"print assumptions", "closed under the global context", "closed under global context",
			"#print axioms", "no axioms", "no unexpected axioms", "thm_oracles", "no oracle", "no oracles",
			"oracle-free", "oracle free", "no isabelle oracles", "oracles: none", "oracles = []",
			"无额外公理", "没有额外公理", "无 oracle", "无 oracles", "无 oracle 依赖", "无 oracles 依赖",
		}) {
			return true
		}
	}
	return false
}

func sawAssumptionAuditCommand(history []profiles.Turn) bool {
	for _, command := range commandStates(history) {
		if commandLooksLikeAssumptionAudit(command.Command) {
			return true
		}
	}
	return false
}

func successfulAssumptionAuditCommand(history []profiles.Turn) bool {
	for _, command := range commandStates(history) {
		if !commandLooksLikeAssumptionAudit(command.Command) {
			continue
		}
		if commandFailed(command) || commandOutputLooksErroneous(command.Output) {
			continue
		}
		output := strings.ToLower(command.Output)
		if containsAny(output, []string{
			"closed under the global context", "closed under global context",
			"no axioms", "no unexpected axioms", "no oracle", "no oracles", "oracle-free", "oracle free",
			"oracles: none", "oracles = []", "无额外公理", "没有额外公理", "无 oracle", "无 oracles",
		}) {
			return true
		}
	}
	return false
}

func commandLooksLikeAssumptionAudit(command string) bool {
	lower := strings.ToLower(command)
	return containsAny(lower, []string{"print assumptions", "#print axioms", "thm_oracles", "print axioms"})
}

func commandOutputLooksErroneous(output string) bool {
	lower := strings.ToLower(output)
	return containsAny(lower, []string{
		"error:", "toplevel input", "reference ", " was not found", "cannot find a physical path",
		"syntax error", "failed", "exception", "错误", "失败",
	})
}

func lineNegatesAssumptionAudit(lower string) bool {
	return containsAny(lower, []string{
		"没有执行 print assumptions", "未执行 print assumptions", "未运行 print assumptions",
		"缺少 print assumptions", "没有 print assumptions 审计", "缺少 coq print assumptions",
		"print assumptions missing", "missing print assumptions", "without print assumptions",
		"did not run print assumptions", "not run print assumptions", "print assumptions not run",
		"not executed print assumptions", "print assumptions was not executed",
		"缺少 global context", "global-context audit evidence is missing", "global context audit evidence is missing",
		"assumption audit evidence is missing", "missing assumption audit", "without assumption audit",
	})
}

func originalObligationEvidence(text string) bool {
	if semanticWeakeningEvidence(text) || trivialObligationEvidence(text) {
		return false
	}
	if containsAny(text, []string{
		"original proof obligation", "original recursive semantics", "original semantics",
		"termination modify_lin", "modify_lin termination", "well-founded", "well founded",
		"decrease", "decreases", "measure", "distance", "structural recursion",
		"原始证明义务", "原始递归语义", "终止性", "下降", "良基", "度量", "等价",
	}) {
		return true
	}
	return false
}

func namedTargetEvidence(text string) bool {
	lower := strings.ToLower(text)
	if semanticWeakeningEvidence(lower) || trivialObligationEvidence(lower) {
		return false
	}
	if regexp.MustCompile(`\b(print assumptions|check|about|thm|thm_oracles|print)\s+[a-z0-9_'.]*modify_lin[a-z0-9_'.]*`).MatchString(lower) {
		return true
	}
	return containsAny(lower, []string{
		"target theorem", "target lemma", "named theorem", "named target theorem", "target fact", "named target fact",
		"modify_lin_termination", "modify_lin_terminates", "modify_lin_original_terminates",
		"modify_lin_step_decreases", "modify_lin_semantics_equiv", "modify_lin_wf", "termination_modify_lin",
		"目标定理", "目标引理", "命名定理", "命名目标", "目标事实", "命名事实",
	})
}

func branchDecreaseOrEquivalenceEvidence(text string) bool {
	lower := strings.ToLower(text)
	if semanticWeakeningEvidence(lower) || trivialObligationEvidence(lower) {
		return false
	}
	if containsAny(lower, []string{
		"branch-decrease", "branch decrease", "recursive-call decrease", "recursive call decrease",
		"step decreases", "decrease lemma", "decreases by", "well-founded relation", "well founded relation",
		"semantic equivalence", "semantics equivalence", "bisimulation", "equivalence theorem",
		"original-step", "original step", "original recursive step", "distance decreases",
		"分支下降", "递归调用下降", "下降引理", "每个分支", "每个递归分支", "良基关系",
		"语义等价", "等价定理", "原始步进", "原始递归步", "distance 下降",
	}) {
		return true
	}
	return containsAny(lower, []string{"decrease", "下降", "等价", "equivalence"}) &&
		containsAny(lower, []string{"modify_lin", "termination", "recursive", "branch", "distance", "measure", "well-founded", "well founded", "终止", "递归", "分支", "度量", "良基"})
}

func semanticWeakeningEvidence(text string) bool {
	lower := strings.ToLower(text)
	return containsAny(lower, []string{
		"semantically weakens", "semantic weakening", "weakened theorem", "weakened statement",
		"changed function semantics", "lacks equivalence", "without equivalence", "missing equivalence",
		"does not prove equivalence", "not equivalent", "not the original recursive semantics",
		"not original recursive semantics", "helper-only", "compile-only", "not just structural helper totality",
		"改成了结构递归 helper", "改成了结构化 helper", "一次性结构递归", "不是原始", "不是原始递归语义",
		"缺少与原", "缺少等价", "没有证明它与原", "没有证明原递归", "语义弱化", "不能直接算作完成",
		"不能判定完成", "不满足用户要求",
	})
}

func trivialObligationEvidence(text string) bool {
	lower := strings.ToLower(text)
	return containsAny(lower, []string{
		"exists r, modify_lin", "exists r, modify_loop", "exists r,",
		"eexists. reflexivity", "exists (modify_lin", "reflexivity theorem",
		"tautology", "tautological", "length-only lemma",
		"反身性定理", "平凡定理", "重言式", "只证明 exists", "只证明存在",
	})
}

func fuelShortcutEvidence(outputText, proseText string) bool {
	combined := strings.Join([]string{outputText, proseText}, "\n")
	var affirmative []string
	for _, line := range strings.Split(combined, "\n") {
		lower := strings.ToLower(strings.TrimSpace(line))
		if lower == "" || lineNegatesFuelShortcut(lower) {
			continue
		}
		affirmative = append(affirmative, lower)
	}
	combined = strings.Join(affirmative, "\n")
	return containsAny(combined, []string{"modify_lin_fuel", "default_fuel", "bounded fuel", "fuel wrapper", "固定 fuel", "燃料包装"})
}

func fuelJustificationEvidence(text string) bool {
	return containsAny(text, []string{"equivalence", "fuel sufficiency", "sufficient fuel", "decrease", "well-founded", "well founded", "等价", "燃料足够", "足够模拟", "下降", "良基"}) &&
		!containsAny(text, []string{"without equivalence", "lacks equivalence", "缺少等价", "没有证明", "未证明", "not proved", "not prove"})
}

func AcceptanceFailure(userPrompt string, history []profiles.Turn) (string, bool) {
	if len(history) == 0 {
		return "", false
	}
	if reason := acceptanceFailureInTurn(userPrompt, history[len(history)-1]); reason != "" {
		return reason, true
	}
	return "", false
}

func acceptanceFailureInTurn(userPrompt string, item profiles.Turn) string {
	prose := strings.Join([]string{
		item.Content,
		item.Handoff,
		item.HandoffFields.Next,
		item.HandoffFields.Risks,
	}, "\n")
	context, score := acceptanceFailureContextScore(prose, userPrompt)
	var commandText strings.Builder
	for _, command := range commandStates([]profiles.Turn{item}) {
		output := strings.TrimSpace(command.Output)
		if output == "" {
			continue
		}
		commandText.WriteString("\n")
		commandText.WriteString(output)
	}
	if commandContext, commandScore := acceptanceFailureContextScore(commandText.String(), userPrompt); commandScore > score {
		context, score = commandContext, commandScore
	}
	if context != "" {
		return "acceptance check failed: " + trimForPrompt(oneLine(context), 500)
	}
	return ""
}

func acceptanceFailureContextScore(text, userPrompt string) (string, int) {
	text = strings.TrimSpace(text)
	lower := strings.ToLower(text)
	if lower == "" {
		return "", 0
	}
	if context := fuelTerminationGapContext(text); context != "" {
		return context, 100
	}
	for _, signal := range acceptanceFailureSignals {
		if strings.Contains(lower, strings.ToLower(signal)) {
			return signalContext(text, signal), acceptanceFailureSignalScore(signal)
		}
	}
	if HasUnresolvedAcceptanceSignal(text, userPrompt) {
		return unresolvedAcceptanceContext(text, userPrompt), 80
	}
	return "", 0
}

var acceptanceFailureSignals = []string{
	"acceptance check failed",
	"acceptance criterion failed",
	"acceptance criterion is not satisfied",
	"user request is not satisfied",
	"does not satisfy the user request",
	"cannot be considered complete",
	"cannot be marked complete",
	"must not be treated as complete",
	"should not be marked complete",
	"验收失败",
	"验收标准未满足",
	"没有满足用户要求",
	"未满足用户要求",
	"不能把当前状态视为完成",
	"不能视为完成",
	"不能算完成",
	"不能判为完成",
	"不能判定为完成",
	"不应标记完成",
	"不应该标记完成",
	"不能把验收标为 resolved",
	"不能标为 resolved",
	"不能把验收标为完成",
	"没有实质进展",
}

func acceptanceFailureSignalScore(signal string) int {
	lower := strings.ToLower(signal)
	if strings.Contains(lower, "acceptance criterion is not satisfied") ||
		strings.Contains(lower, "acceptance criterion failed") {
		return 90
	}
	if strings.Contains(lower, "验收标准未满足") ||
		strings.Contains(lower, "没有满足用户要求") ||
		strings.Contains(lower, "未满足用户要求") {
		return 70
	}
	if strings.Contains(lower, "不能") || strings.Contains(lower, "resolved") {
		return 65
	}
	return 60
}

func AcceptanceBlockerSummary(userPrompt string, history []profiles.Turn) string {
	for i := len(history) - 1; i >= 0; i-- {
		if reason := acceptanceFailureInTurn(userPrompt, history[i]); reason != "" {
			return reason
		}
	}
	return ""
}

func HasUnresolvedAcceptanceSignal(text, userPrompt string) bool {
	lower := strings.ToLower(text)
	if lower == "" {
		return false
	}
	if hasResolvedSorrySignal(lower) && !hasExplicitUnresolvedSorryRisk(lower) {
		return false
	}
	promptLower := strings.ToLower(userPrompt)
	promptRequiresSorryRemoval := containsAny(promptLower, []string{
		"消除", "去掉", "移除", "删除", "补全", "填上", "主定理", "完全证明", "完整证明",
		"remove", "eliminate", "fill", "replace", "complete proof", "main theorem",
	}) && containsAny(promptLower, []string{"sorry", "quick_and_dirty", "termination modify_lin", "modify_lin", "主定理"})
	if containsAny(lower, []string{
		"main theorem", "主定理", "termination modify_lin", "modify_lin",
	}) && containsAny(lower, []string{
		"sorry", "oops", "sketch", "admit", "未消除", "没有消除", "还保留", "placeholder", "占位",
	}) {
		if lineNegatesFuelShortcut(lower) || containsAny(lower, []string{"no placeholders", "no forbidden tokens", "没有占位符", "无占位符", "source-only placeholder scan"}) {
			return false
		}
		return true
	}
	if containsAny(lower, []string{
		"只是通过编译",
		"只能说通过编译",
		"没有实质上的进展",
		"not a completed proof",
	}) {
		return true
	}
	if promptRequiresSorryRemoval && hasExplicitUnresolvedSorryRisk(lower) {
		return true
	}
	return false
}

func hasExplicitUnresolvedSorryRisk(lower string) bool {
	return containsAny(lower, []string{
		"sorry placeholder", "sorry placeholders", "still contains sorry", "contains sorry",
		"still contains oops", "contains oops", "quick_and_dirty", "可编译的证明框架", "证明框架可编译", "不是完整证明",
		"不是完全无 sorry", "not without sorry", "not fully without sorry", "not a completed proof",
	})
}

func hasResolvedSorrySignal(lower string) bool {
	if containsAny(lower, []string{
		"without sorry", "without any sorry", "no sorry placeholders", "no remaining sorry",
		"无 sorry", "无sorry", "没有 sorry", "without quick_and_dirty", "quick_and_dirty = false",
	}) {
		return true
	}
	return regexp.MustCompile(`\bno\s+sorry\b`).MatchString(lower)
}

func unresolvedAcceptanceContext(text, userPrompt string) string {
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		if HasUnresolvedAcceptanceSignal(line, userPrompt) {
			return strings.TrimSpace(line)
		}
	}
	if context := fuelTerminationGapContext(text); context != "" {
		return context
	}
	for _, line := range lines {
		if lineLooksLikeAcceptanceExplanation(line) {
			return strings.TrimSpace(line)
		}
	}
	return "unresolved acceptance criterion remains"
}

func fuelTerminationGapContext(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lower := strings.ToLower(line)
		if !containsAny(lower, []string{"modify_lin", "default_fuel", "fuel", "燃料", "termination", "distance"}) {
			continue
		}
		if lineNegatesFuelShortcut(lower) {
			continue
		}
		if lineHasExplicitGap(lower) || lineHasFuelBypassGap(lower) {
			return fuelTerminationContextLines(lines, i)
		}
	}
	return ""
}

func fuelTerminationContextLines(lines []string, index int) string {
	selected := []string{strings.TrimSpace(lines[index])}
	if index+1 < len(lines) {
		next := strings.TrimSpace(lines[index+1])
		if lineLooksLikeFuelTerminationDetail(next) {
			selected = append(selected, next)
		}
	}
	if len(selected) == 1 && index > 0 {
		prev := strings.TrimSpace(lines[index-1])
		if lineLooksLikeFuelTerminationDetail(prev) {
			selected = append([]string{prev}, selected...)
		}
	}
	return strings.TrimSpace(strings.Join(selected, " "))
}

func lineLooksLikeFuelTerminationDetail(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "" {
		return false
	}
	if lineNegatesFuelShortcut(lower) {
		return false
	}
	return containsAny(lower, []string{"modify_lin", "default_fuel", "fuel", "燃料", "termination", "distance", "递归", "证明"}) &&
		(lineHasExplicitGap(lower) || lineHasFuelBypassGap(lower))
}

func lineHasExplicitGap(lower string) bool {
	return containsAny(lower, []string{
		"没有证明", "未证明", "没有证", "缺少证明", "缺少等价", "缺少下降", "缺少足够", "未提供证明",
		"not prove", "not proved", "without proving", "lacks proof", "missing proof", "missing equivalence",
		"missing decrease", "missing sufficiency", "not sufficient", "insufficient",
	})
}

func lineHasFuelBypassGap(lower string) bool {
	return containsAny(lower, []string{"default_fuel", "modify_lin_fuel", "bounded fuel", "fuel wrapper", "固定 fuel", "固定燃料", "燃料包装", "绕过"}) &&
		containsAny(lower, []string{"固定", "wrapper", "包装", "绕过", "without", "lacks", "missing", "equivalence", "decrease", "sufficient", "sufficiency", "等价", "下降", "足够模拟", "足够性", "未证明", "没有证明", "缺少"})
}

func lineNegatesFuelShortcut(lower string) bool {
	return containsAny(lower, []string{
		"没有 modify_lin_fuel", "没有 default_fuel", "没有 fuel wrapper", "没有 bounded/default fuel",
		"没有 modify_lin_fuel/default_fuel", "没有 modify_lin_fuel/default_fuel/fuel",
		"无 modify_lin_fuel", "无 default_fuel", "无 fuel wrapper",
		"no modify_lin_fuel", "no default_fuel", "no fuel wrapper", "without modify_lin_fuel", "without default_fuel",
		"without fuel wrapper", "no bounded/default fuel", "没有 runtime distance guard",
	})
}

func lineLooksLikeAcceptanceExplanation(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "" {
		return false
	}
	return containsAny(lower, []string{"验收", "acceptance", "modify_lin", "termination", "证明", "proof", "distance", "fuel", "燃料"}) &&
		containsAny(lower, []string{"不能", "未", "没有", "not", "failed", "incomplete", "风险", "risk", "缺失", "missing"})
}

func RecordCommandFingerprints(memory map[string]profiles.CommandFingerprint, cwd string, commands []profiles.Command) {
	if memory == nil {
		return
	}
	for _, command := range commandStates([]profiles.Turn{{Commands: commands}}) {
		label := strings.TrimSpace(command.Command)
		if label == "" || !isLongBuildCommandForFingerprint(label) {
			continue
		}
		key := commandFingerprintKey(cwd, label)
		record := profiles.CommandFingerprint{
			CWD:          strings.TrimSpace(cwd),
			Command:      normalizeCommandFingerprint(label),
			LastStatus:   command.Status,
			LastExitCode: command.ExitCode,
			RanAtLeast:   commandDuration(command),
			TimedOut:     toolTimedOut(command),
			LastSeen:     time.Now(),
		}
		if prev, ok := memory[key]; ok {
			record.TimedOut = record.TimedOut || prev.TimedOut
			if record.RanAtLeast < prev.RanAtLeast {
				record.RanAtLeast = prev.RanAtLeast
			}
			if record.LastExitCode == nil {
				record.LastExitCode = prev.LastExitCode
			}
			if record.LastStatus == "" {
				record.LastStatus = prev.LastStatus
			}
		}
		memory[key] = record
	}
}

func ManualBuildRequired(reason string, history []profiles.Turn, memory map[string]profiles.CommandFingerprint) bool {
	return fingerprintManualBuildSignal(memory) || ManualBuildSignal(reason) || ManualBuildSignal(manualBuildCorpus(history))
}

func ManualBuildReason(userPrompt string, history []profiles.Turn, memory map[string]profiles.CommandFingerprint) string {
	if strings.TrimSpace(userPrompt) != "" && !LooksLikeSystemBTask(userPrompt) {
		return ""
	}
	if fingerprintManualBuildSignal(memory) {
		return "Isabelle build requires manual follow-up; Bridge command fingerprint tracking remembers that a long build timed out, so later CLI turns should not rerun the same build automatically"
	}
	if !ManualBuildSignal(manualBuildCorpus(history)) {
		return ""
	}
	return "Isabelle build requires manual follow-up; a long build timed out or was handed off with a log/manual command, so later CLI turns should not rerun the same build automatically"
}

func manualBuildCorpus(history []profiles.Turn) string {
	commandText, outputText := assessmentCommandText(history)
	return strings.Join([]string{assessmentText(history), commandText, outputText}, "\n")
}

func ManualBuildSignal(text string) bool {
	lower := strings.ToLower(text)
	if !strings.Contains(lower, "isabelle") {
		return false
	}
	if containsAny(lower, []string{
		"manual build", "manual follow-up", "manual command", "run manually",
		"do not rerun", "do not repeat", "skip rerunning", "skip the build",
		"手动执行", "手工执行", "用户手动", "不要重复", "不需要执行这个build",
		"无需重复执行", "不要再跑", "跳过重复 build",
	}) {
		return true
	}
	if !strings.Contains(lower, "isabelle build") || !containsAny(lower, []string{"build.log", "log", "日志"}) {
		return false
	}
	if containsAny(lower, []string{
		"timed out", "timeout expired", "exit 124", "exit code 124", "status=124", "超时",
		"manual build pending", "build pending", "manual follow-up pending",
	}) {
		return true
	}
	return containsAny(lower, []string{"canceled", "cancelled", "context canceled", "run.cancelled"}) &&
		containsAny(lower, []string{"running ", "running linlattice", "build.pid", "build.pgid", "pid stat"})
}

func ManualBuildVisibleSummary(userPrompt, contextSummary string, history []profiles.Turn, memory map[string]profiles.CommandFingerprint) string {
	if !LooksLikeSystemBTask(userPrompt) || !manualBuildContinuationSignal(contextSummary, history, memory) {
		return ""
	}
	manualCommand, logPath, pidPath, pgidPath, exitPath, tail := manualBuildArtifacts(contextSummary, history)
	var b strings.Builder
	b.WriteString("Isabelle 长时间 build 交接：上一轮已经启动、超时或交给用户手动执行的 Isabelle build，本轮不会自动重复执行同一个 `isabelle build -D` / `isabelle build -d`。")
	if logPath != "" {
		b.WriteString("\n日志路径：")
		b.WriteString(logPath)
	}
	if pidPath != "" || pgidPath != "" || exitPath != "" {
		b.WriteString("\n状态文件：")
		var files []string
		if pidPath != "" {
			files = append(files, "pid="+pidPath)
		}
		if pgidPath != "" {
			files = append(files, "pgid="+pgidPath)
		}
		if exitPath != "" {
			files = append(files, "exit="+exitPath)
		}
		b.WriteString(strings.Join(files, ", "))
	}
	if tail != "" {
		b.WriteString("\n当前可见日志/状态摘要：")
		b.WriteString(tail)
	}
	if manualCommand != "" {
		b.WriteString("\n用户可手动执行：")
		b.WriteString(manualCommand)
	}
	if fingerprintManualBuildSignal(memory) && manualCommand == "" && logPath == "" {
		b.WriteString("\nBridge 已记录同一命令指纹发生过超时。")
	}
	b.WriteString("\n后续 CLI 只读取源码、日志和 PID/PGID/exit 状态；最终验收等待用户的手动 Isabelle build 结果。")
	return b.String()
}

func manualBuildContinuationSignal(contextSummary string, history []profiles.Turn, memory map[string]profiles.CommandFingerprint) bool {
	return ManualBuildRequired("", history, memory) || ManualBuildSignal(contextSummary)
}

func manualBuildArtifacts(contextSummary string, history []profiles.Turn) (manualCommand, logPath, pidPath, pgidPath, exitPath, tail string) {
	manualCommand = extractManualCommand(contextSummary)
	contextProjectDir := extractBuildProjectDir(contextSummary)
	logPath = qualifyBuildArtifactPath(extractFirstBuildArtifactPath(contextSummary, "build.log"), contextProjectDir)
	pidPath = qualifyBuildArtifactPath(extractFirstBuildArtifactPath(contextSummary, "build.pid"), contextProjectDir)
	pgidPath = qualifyBuildArtifactPath(extractFirstBuildArtifactPath(contextSummary, "build.pgid"), contextProjectDir)
	exitPath = qualifyBuildArtifactPath(extractFirstBuildArtifactPath(contextSummary, "build.exit"), contextProjectDir)
	tail = manualBuildTailFromText(contextSummary)
	for i := len(history) - 1; i >= 0; i-- {
		item := history[i]
		if tail == "" {
			tail = manualBuildTailFromTurn(item)
		}
		text := strings.Join([]string{
			item.Content,
			item.Handoff,
			item.HandoffFields.Verified,
			item.HandoffFields.Next,
			item.HandoffFields.Risks,
		}, "\n")
		projectDir := extractBuildProjectDir(text)
		if projectDir == "" {
			projectDir = contextProjectDir
		}
		if manualCommand == "" {
			manualCommand = extractManualCommand(text)
		}
		if logPath == "" {
			logPath = qualifyBuildArtifactPath(extractFirstBuildArtifactPath(text, "build.log"), projectDir)
		}
		if pidPath == "" {
			pidPath = qualifyBuildArtifactPath(extractFirstBuildArtifactPath(text, "build.pid"), projectDir)
		}
		if pgidPath == "" {
			pgidPath = qualifyBuildArtifactPath(extractFirstBuildArtifactPath(text, "build.pgid"), projectDir)
		}
		if exitPath == "" {
			exitPath = qualifyBuildArtifactPath(extractFirstBuildArtifactPath(text, "build.exit"), projectDir)
		}
		for _, command := range commandStates([]profiles.Turn{item}) {
			combined := command.Command + "\n" + command.Output
			commandProjectDir := extractBuildProjectDir(combined)
			if commandProjectDir == "" {
				commandProjectDir = projectDir
			}
			if manualCommand == "" {
				manualCommand = extractManualCommand(combined)
			}
			if logPath == "" {
				logPath = qualifyBuildArtifactPath(extractFirstBuildArtifactPath(combined, "build.log"), commandProjectDir)
			}
			if pidPath == "" {
				pidPath = qualifyBuildArtifactPath(extractFirstBuildArtifactPath(combined, "build.pid"), commandProjectDir)
			}
			if pgidPath == "" {
				pgidPath = qualifyBuildArtifactPath(extractFirstBuildArtifactPath(combined, "build.pgid"), commandProjectDir)
			}
			if exitPath == "" {
				exitPath = qualifyBuildArtifactPath(extractFirstBuildArtifactPath(combined, "build.exit"), commandProjectDir)
			}
		}
		if manualCommand != "" && logPath != "" && pidPath != "" && pgidPath != "" && exitPath != "" && tail != "" {
			break
		}
	}
	return manualCommand, logPath, pidPath, pgidPath, exitPath, tail
}

func manualBuildTailFromText(text string) string {
	var selected []string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		if strings.Contains(lower, "build.log") || strings.Contains(lower, "running ") || strings.Contains(lower, "timed out") || strings.Contains(lower, "timeout") || strings.Contains(lower, "build.exit") || strings.Contains(lower, "build.pid") {
			selected = append(selected, trimmed)
		}
		if len(selected) >= 4 {
			break
		}
	}
	if len(selected) == 0 {
		return ""
	}
	return trimForPrompt(oneLine(strings.Join(selected, " ")), 500)
}

func manualBuildTailFromTurn(item profiles.Turn) string {
	for i := len(item.Commands) - 1; i >= 0; i-- {
		tool := item.Commands[i]
		combined := strings.ToLower(tool.Command + "\n" + tool.Output)
		if !strings.Contains(combined, "isabelle") && !strings.Contains(combined, "build.log") && !strings.Contains(combined, "build.exit") && !strings.Contains(combined, "build.pid") {
			continue
		}
		output := strings.TrimSpace(tool.Output)
		if output == "" {
			output = strings.TrimSpace(tool.Command)
		}
		if output != "" {
			return trimForPrompt(oneLine(output), 500)
		}
	}
	return ""
}

func extractManualCommand(text string) string {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if !strings.Contains(lower, "isabelle build") {
			continue
		}
		if !containsAny(lower, []string{"timeout ", "sh -lc", "isabelle build -d", "isabelle build -D", ">", "|", "tee "}) {
			continue
		}
		if index := strings.IndexAny(trimmed, ":："); index >= 0 && containsAny(strings.ToLower(trimmed[:index]), []string{"manual", "command", "手动", "手工", "命令"}) {
			trimmed = strings.TrimSpace(trimmed[index+len(string([]rune(trimmed[index:])[0])):])
		}
		return trimForPrompt(oneLine(strings.Trim(trimmed, "`")), 500)
	}
	return ""
}

func extractFirstBuildArtifactPath(text, artifact string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	replacer := strings.NewReplacer(
		"\n", " ",
		"\r", " ",
		"\t", " ",
		"'", " ",
		`"`, " ",
		"`", " ",
		",", " ",
		";", " ",
		":", " ",
		"：", " ",
		"(", " ",
		")", " ",
	)
	fields := strings.Fields(replacer.Replace(text))
	fallback := ""
	for _, field := range fields {
		field = normalizeBuildArtifactCandidate(strings.Trim(field, `"'`), artifact)
		if field == "" {
			continue
		}
		if field == artifact {
			if fallback == "" {
				fallback = field
			}
			continue
		}
		if strings.HasSuffix(field, artifact) {
			return field
		}
		if fallback == "" {
			fallback = field
		}
	}
	return fallback
}

func normalizeBuildArtifactCandidate(field, artifact string) string {
	field = strings.TrimSpace(strings.TrimLeft(field, ">"))
	if field == "" || !strings.Contains(field, artifact) {
		return ""
	}
	if !filepath.IsAbs(field) && strings.Contains(field, "/") {
		for _, part := range strings.Split(field, "/") {
			if part == artifact {
				return artifact
			}
		}
	}
	if field == artifact || strings.HasSuffix(field, "/"+artifact) {
		return field
	}
	if strings.HasSuffix(field, artifact) && !strings.Contains(field, "/") {
		return artifact
	}
	return ""
}

func extractBuildProjectDir(text string) string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`cd\s+['"]([^'"]+)['"]\s*&&`),
		regexp.MustCompile(`cd\s+([^\s;&|]+)\s*&&`),
	}
	for _, pattern := range patterns {
		for _, match := range pattern.FindAllStringSubmatch(text, -1) {
			if len(match) > 1 && strings.TrimSpace(match[1]) != "" {
				return strings.TrimSpace(match[1])
			}
		}
	}
	return ""
}

func qualifyBuildArtifactPath(path, projectDir string) string {
	path = strings.TrimSpace(strings.Trim(path, `"'`))
	path = strings.TrimLeft(path, ">")
	if path == "" {
		return ""
	}
	if filepath.IsAbs(path) || strings.HasPrefix(path, "~/") || projectDir == "" {
		return path
	}
	if strings.HasPrefix(path, "./") {
		path = strings.TrimPrefix(path, "./")
	}
	if path == "." || strings.HasPrefix(path, "../") {
		return path
	}
	return filepath.ToSlash(filepath.Join(projectDir, path))
}

func ForegroundBuildError(userPrompt string, commands []profiles.Command, existing error) error {
	if existing != nil || !LooksLikeSystemBTask(userPrompt) {
		return existing
	}
	for _, command := range commands {
		if !isForbiddenForegroundBuild(command.Command) {
			continue
		}
		return fmt.Errorf("foreground Isabelle build is not allowed for this web-visible proof smoke; use controlled background build.log/build.pid/build.exit polling instead: %s", trimForPrompt(command.Command, 240))
	}
	return nil
}

func isForbiddenForegroundBuild(command string) bool {
	lower := strings.ToLower(command)
	if !strings.Contains(lower, "isabelle build") {
		return false
	}
	if !hasBuildDirectoryOption(lower) {
		return false
	}
	if strings.Contains(lower, "setsid") && strings.Contains(lower, "build.pid") && strings.Contains(lower, "build.pgid") && strings.Contains(lower, "build.exit") && strings.Contains(lower, "build.log") && strings.Contains(lower, "&") {
		return false
	}
	return true
}

func hasBuildDirectoryOption(lowerCommand string) bool {
	for _, field := range strings.Fields(lowerCommand) {
		field = strings.Trim(field, `'"`)
		if field == "-d" || strings.HasPrefix(field, "-d.") || strings.HasPrefix(field, "-d/") {
			return true
		}
	}
	return false
}

func fingerprintManualBuildSignal(memory map[string]profiles.CommandFingerprint) bool {
	for _, item := range memory {
		if item.TimedOut && isLongBuildCommandForFingerprint(item.Command) {
			return true
		}
	}
	return false
}

func commandFingerprintKey(cwd, command string) string {
	return strings.TrimSpace(filepath.Clean(cwd)) + "\x00" + normalizeCommandFingerprint(command)
}

func normalizeCommandFingerprint(command string) string {
	lower := strings.ToLower(strings.TrimSpace(command))
	lower = regexp.MustCompile(`\s+`).ReplaceAllString(lower, " ")
	lower = regexp.MustCompile(`\btimeout\s+[0-9]+[smhd]?\s+`).ReplaceAllString(lower, "")
	return strings.TrimSpace(lower)
}

func isLongBuildCommandForFingerprint(command string) bool {
	lower := normalizeCommandFingerprint(command)
	return strings.Contains(lower, "isabelle build") && hasBuildDirectoryOption(lower)
}

func toolTimedOut(command profiles.Command) bool {
	if command.ExitCode != nil && *command.ExitCode == 124 {
		return true
	}
	lower := strings.ToLower(command.Status + "\n" + command.Output)
	return containsAny(lower, []string{"timed out", "timeout", "超时"}) &&
		containsAny(lower, []string{"failed", "error", "exit 124", "status=124"})
}

func commandDuration(command profiles.Command) time.Duration {
	if command.StartedAt.IsZero() || command.CompletedAt.IsZero() || command.CompletedAt.Before(command.StartedAt) {
		return 0
	}
	return command.CompletedAt.Sub(command.StartedAt)
}

type commandState struct {
	ID       string
	Status   string
	Command  string
	Output   string
	ExitCode *int
}

func commandStates(turns []profiles.Turn) []profiles.Command {
	var states []profiles.Command
	indexes := map[string]int{}
	for _, turn := range turns {
		for _, tool := range turn.Commands {
			toolKey := tool.ID
			if toolKey == "" {
				toolKey = tool.Command
			}
			if toolKey == "" {
				toolKey = fmt.Sprintf("tool-%d", len(states))
			}
			key := turn.TurnID + ":" + toolKey
			index, ok := indexes[key]
			if !ok {
				index = len(states)
				indexes[key] = index
				states = append(states, profiles.Command{ID: tool.ID})
			}
			state := states[index]
			if tool.ID != "" {
				state.ID = tool.ID
			}
			if tool.Status != "" {
				state.Status = tool.Status
			}
			if tool.Command != "" {
				state.Command = tool.Command
			}
			if tool.Output != "" {
				state.Output = tool.Output
			}
			if tool.ExitCode != nil {
				state.ExitCode = tool.ExitCode
			}
			if !tool.StartedAt.IsZero() {
				state.StartedAt = tool.StartedAt
			}
			if !tool.CompletedAt.IsZero() {
				state.CompletedAt = tool.CompletedAt
			}
			states[index] = state
		}
	}
	return states
}

func commandFailed(command profiles.Command) bool {
	if strings.EqualFold(command.Status, "failed") || strings.EqualFold(command.Status, "error") {
		return true
	}
	return command.ExitCode != nil && *command.ExitCode != 0
}

func normalizeFirstCLI(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "codex":
		return "codex"
	default:
		return "claude"
	}
}

func containsAll(value string, signals []string) bool {
	value = strings.ToLower(value)
	for _, signal := range signals {
		if !strings.Contains(value, strings.ToLower(signal)) {
			return false
		}
	}
	return true
}

func containsAny(value string, signals []string) bool {
	for _, signal := range signals {
		if strings.Contains(value, strings.ToLower(signal)) {
			return true
		}
	}
	return false
}

func signalContext(text, signal string) string {
	lines := strings.Split(text, "\n")
	signal = strings.ToLower(signal)
	for _, line := range lines {
		if strings.Contains(strings.ToLower(line), signal) {
			return strings.TrimSpace(line)
		}
	}
	return strings.TrimSpace(text)
}

func trimForPrompt(value string, max int) string {
	value = sanitizePromptText(strings.TrimSpace(value))
	if max <= 0 || len(value) <= max {
		return value
	}
	if utf8.ValidString(value[:max]) {
		return value[:max] + "\n[truncated]"
	}
	end := 0
	for i := range value {
		if i > max {
			break
		}
		end = i
	}
	return value[:end] + "\n[truncated]"
}

func sanitizePromptText(value string) string {
	return strings.ToValidUTF8(value, "\uFFFD")
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
