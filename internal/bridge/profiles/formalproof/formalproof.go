package formalproof

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

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

func hasBuildDirectoryOption(lowerCommand string) bool {
	for _, field := range strings.Fields(lowerCommand) {
		field = strings.Trim(field, `'"`)
		if field == "-d" || strings.HasPrefix(field, "-d.") || strings.HasPrefix(field, "-d/") {
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

func normalizeFirstCLI(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "codex":
		return "codex"
	default:
		return "claude"
	}
}

func containsAny(value string, signals []string) bool {
	for _, signal := range signals {
		if strings.Contains(value, strings.ToLower(signal)) {
			return true
		}
	}
	return false
}
