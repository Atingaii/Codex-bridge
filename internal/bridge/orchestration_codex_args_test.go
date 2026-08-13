package bridge

import (
	"testing"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
)

func TestCodexOrchestrationStrictWorkspaceUsesOuterSandbox(t *testing.T) {
	cfg := config.Default()
	cfg.Bridge.CWD = "/tmp/work"
	cfg.Bridge.Sandbox = "workspace-write"
	cfg.Bridge.ApprovalPolicy = "never"
	cfg.Bridge.StrictWorkspace = true
	manager := NewOrchestrationManager(&cfg)
	defer manager.CloseAll()

	for name, args := range map[string][]string{
		"new":    manager.codexOrchestrationArgs(protocol.OrchestrationStartPayload{}, ""),
		"resume": manager.codexOrchestrationArgs(protocol.OrchestrationStartPayload{}, "thr_strict"),
	} {
		if !containsArg(args, "--dangerously-bypass-approvals-and-sandbox") {
			t.Fatalf("strict orchestration %s args do not bypass nested Codex sandbox: %#v", name, args)
		}
		for _, disallowed := range []string{"--sandbox", `sandbox_mode="workspace-write"`, `approval_policy="never"`} {
			if containsArg(args, disallowed) {
				t.Fatalf("strict orchestration %s args include nested policy %q: %#v", name, disallowed, args)
			}
		}
	}
}
