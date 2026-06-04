package bridge

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
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

func TestBridgeUserServiceNameUsesMachineIDFileHash(t *testing.T) {
	got := bridgeUserServiceName("~/.codex-bridge/machines/123456789")
	if got != "codex-bridge-123456789.service" {
		t.Fatalf("service name = %q", got)
	}
	if got := bridgeUserServiceName("~/.codex-bridge/machine_id"); got != "" {
		t.Fatalf("global machine id should not map to generated service, got %q", got)
	}
}

func TestConnectOnceClosesActiveOrchestrationsOnWebSocketDisconnect(t *testing.T) {
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

	cancelled := make(chan struct{})
	var cancelOnce sync.Once
	client.orchestrations.runs["orc_disconnect"] = &orchestrationRunHandle{cancel: func() {
		cancelOnce.Do(func() {
			close(cancelled)
		})
	}}

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
	case <-cancelled:
	case err := <-serverErr:
		t.Fatalf("fake hub failed before disconnect cleanup: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("active orchestration was not cancelled after websocket disconnect")
	}

	select {
	case err := <-result:
		if err == nil {
			t.Fatal("connectOnce returned nil after websocket disconnect")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("connectOnce did not return after websocket disconnect")
	}
	if len(client.orchestrations.runs) != 0 {
		t.Fatalf("active orchestration handles were not cleared: %#v", client.orchestrations.runs)
	}
}
