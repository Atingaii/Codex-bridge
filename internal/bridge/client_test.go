package bridge

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
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

func TestHubProxyReturnedPlaintextTLS(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "plaintext TLS response", err: fmt.Errorf("tls: first record does not look like a TLS handshake"), want: true},
		{name: "wrapped plaintext TLS response", err: fmt.Errorf("websocket: %w", fmt.Errorf("tls: first record does not look like a TLS handshake")), want: true},
		{name: "ordinary connection refusal", err: fmt.Errorf("dial tcp: connection refused"), want: false},
		{name: "nil", err: nil, want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := hubProxyReturnedPlaintextTLS(tt.err); got != tt.want {
				t.Fatalf("hubProxyReturnedPlaintextTLS(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestBridgeCapabilitiesKeepPermissionProfilesIsolated(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name             string
		runner           string
		sandbox          string
		approvalPolicy   string
		strictWorkspace  bool
		approvalMode     string
		chatCodex        protocol.BridgeCLICapability
		orchestrationCLI map[string]protocol.BridgeCLICapability
	}{
		{
			name:           "review-required",
			runner:         "codex-app-server",
			sandbox:        "workspace-write",
			approvalPolicy: "untrusted",
			approvalMode:   "review-required",
			chatCodex:      protocol.BridgeCLICapability{Available: true, Execution: "codex-app-server", BrowserApproval: true, ApprovalMode: "review-required"},
			orchestrationCLI: map[string]protocol.BridgeCLICapability{
				"claude": {Available: true, Execution: "claude --print", BrowserApproval: true, ApprovalMode: "review-required"},
				"codex":  {Available: true, Execution: "codex app-server", BrowserApproval: true, ApprovalMode: "review-required"},
			},
		},
		{
			name:           "auto-execute",
			runner:         "codex",
			sandbox:        "danger-full-access",
			approvalPolicy: "never",
			approvalMode:   "auto-execute",
			chatCodex:      protocol.BridgeCLICapability{Available: false, Execution: "codex", BrowserApproval: false, ApprovalMode: "auto-execute"},
			orchestrationCLI: map[string]protocol.BridgeCLICapability{
				"claude": {Available: true, Execution: "claude --print", BrowserApproval: false, ApprovalMode: "auto-execute"},
				"codex":  {Available: true, Execution: "codex exec --json", BrowserApproval: false, ApprovalMode: "auto-execute"},
			},
		},
		{
			name:            "strict-workspace",
			runner:          "codex",
			sandbox:         "workspace-write",
			approvalPolicy:  "never",
			strictWorkspace: true,
			approvalMode:    "strict-workspace",
			chatCodex:       protocol.BridgeCLICapability{Available: false, Execution: "codex", BrowserApproval: false, ApprovalMode: "strict-workspace"},
			orchestrationCLI: map[string]protocol.BridgeCLICapability{
				"claude": {Available: true, Execution: "claude --print", BrowserApproval: false, ApprovalMode: "strict-workspace"},
				"codex":  {Available: true, Execution: "codex exec --json", BrowserApproval: false, ApprovalMode: "strict-workspace"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Bridge.Runner = tt.runner
			cfg.Bridge.Sandbox = tt.sandbox
			cfg.Bridge.ApprovalPolicy = tt.approvalPolicy
			cfg.Bridge.StrictWorkspace = tt.strictWorkspace
			cfg.Bridge.CodexPath = executable
			cfg.Bridge.ClaudePath = executable

			caps := BridgeCapabilities(&cfg)
			if caps.Runner != tt.runner || caps.Sandbox != tt.sandbox || caps.ApprovalPolicy != tt.approvalPolicy {
				t.Fatalf("top-level capabilities = %#v", caps)
			}
			if got := caps.Metadata["approvalMode"]; got != tt.approvalMode {
				t.Fatalf("approval mode = %q, want %q", got, tt.approvalMode)
			}
			if got := caps.Chat["codex"]; !reflect.DeepEqual(got, tt.chatCodex) {
				t.Fatalf("chat codex = %#v, want %#v", got, tt.chatCodex)
			}
			if !reflect.DeepEqual(caps.Orchestration, tt.orchestrationCLI) {
				t.Fatalf("orchestration = %#v, want %#v", caps.Orchestration, tt.orchestrationCLI)
			}
		})
	}
}

func TestBridgeUserServiceNameUsesMachineIDFileHash(t *testing.T) {
	got := bridgeUserServiceName("~/.codex-bridge/machines/123456789")
	if got != "codex-bridge-123456789.service" {
		t.Fatalf("service name = %q", got)
	}
	if got := bridgeUserServiceName("~/.codex-bridge/machine_id"); got != "" {
		t.Fatalf("global machine id should not map to generated service, got %q", got)
	}
}

func TestConnectOnceKeepsActiveOrchestrationsOnWebSocketDisconnect(t *testing.T) {
	registered := make(chan struct{})
	disconnect := make(chan struct{})
	serverErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agents/connect" {
			serverErr <- fmt.Errorf("unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer ws.Close()

		var env protocol.Envelope
		if err := ws.ReadJSON(&env); err != nil {
			serverErr <- err
			return
		}
		if env.Type != protocol.TypeRegister {
			serverErr <- fmt.Errorf("first frame type = %q, want %q", env.Type, protocol.TypeRegister)
			return
		}
		if err := ws.WriteJSON(protocol.MustEnvelope(protocol.TypeRegistered, "", protocol.RegisteredPayload{AgentID: "agent_test"})); err != nil {
			serverErr <- err
			return
		}
		close(registered)
		<-disconnect
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.Bridge.HubURL = server.URL
	cfg.Bridge.HeartbeatInterval = config.Duration{Duration: time.Hour}
	client := NewClient(&cfg, "test")
	client.machineID = "machine_test"
	client.hostname = "host_test"
	client.instance = "bin_test"
	client.sessions = NewSessionManager(&cfg)
	client.orchestrations = NewOrchestrationManager(&cfg)

	client.orchestrations.runs["orc_disconnect"] = &orchestrationRunHandle{cancel: func() {}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- client.connectOnce(ctx, "token_test")
	}()

	select {
	case <-registered:
	case err := <-serverErr:
		t.Fatalf("fake hub failed before registration: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for bridge registration")
	}
	close(disconnect)

	select {
	case err := <-result:
		if err == nil {
			t.Fatal("connectOnce returned nil after websocket disconnect")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("connectOnce did not return after websocket disconnect")
	}
	if len(client.orchestrations.runs) != 1 {
		t.Fatalf("active orchestration handles should remain for reconnect, got %#v", client.orchestrations.runs)
	}
}
