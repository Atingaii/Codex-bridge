package bridge

import (
	"testing"

	"github.com/tencent/codex-bridge/internal/config"
)

func TestBridgeCapabilitiesCheckCLIPaths(t *testing.T) {
	cfg := config.Default()
	cfg.Bridge.Runner = "codex-app-server"
	cfg.Bridge.Sandbox = "workspace-write"
	cfg.Bridge.ApprovalPolicy = "untrusted"
	cfg.Bridge.CodexPath = "/definitely/missing/codex"
	cfg.Bridge.ClaudePath = "/definitely/missing/claude"

	caps := BridgeCapabilities(&cfg)
	if caps.Chat["codex"].Available {
		t.Fatalf("chat codex should be unavailable: %#v", caps.Chat["codex"])
	}
	if caps.Orchestration["codex"].Available || caps.Orchestration["codex"].BrowserApproval {
		t.Fatalf("orchestration codex should be unavailable: %#v", caps.Orchestration["codex"])
	}
	if caps.Orchestration["claude"].Available || caps.Orchestration["claude"].BrowserApproval {
		t.Fatalf("orchestration claude should be unavailable: %#v", caps.Orchestration["claude"])
	}
}
