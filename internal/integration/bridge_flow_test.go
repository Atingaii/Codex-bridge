package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tencent/codex-bridge/internal/bridge"
	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/hub"
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

var (
	freePortMu   sync.Mutex
	freePortUsed = map[int]struct{}{}
)

func TestEchoBridgeEndToEnd(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tmp := t.TempDir()
	port := freePort(t)
	cfg := config.Default()
	cfg.Gateway.Host = "127.0.0.1"
	cfg.Gateway.Port = port
	cfg.Gateway.ReadTimeout.Duration = 5 * time.Second
	cfg.Gateway.WriteTimeout.Duration = 0
	cfg.Hub.DBPath = tmp + "/bridge.db"
	cfg.Hub.HeartbeatInterval.Duration = 200 * time.Millisecond
	cfg.Hub.BrowserCloseGrace.Duration = 20 * time.Millisecond
	cfg.Auth.JWTSecret = "integration-test-secret-32-byte-minimum"
	cfg.Auth.AccessTokenTTL.Duration = time.Hour
	cfg.Bridge.HubURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	cfg.Bridge.Token = store.NewToken("enr")
	cfg.Bridge.Name = "integration-bridge"
	cfg.Bridge.MachineIDFile = tmp + "/machine_id"
	cfg.Bridge.CWD = tmp
	cfg.Bridge.Runner = "echo"
	cfg.Bridge.ReconnectMin.Duration = 50 * time.Millisecond
	cfg.Bridge.ReconnectMax.Duration = 100 * time.Millisecond
	cfg.Bridge.HeartbeatInterval.Duration = 200 * time.Millisecond
	cfg.Bridge.MaxSessions = 2

	st, err := store.Open(cfg.Hub.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertUser(ctx, "admin", "secret"); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	if err := st.CreateEnrollToken(ctx, cfg.Bridge.Token, &expires); err != nil {
		t.Fatal(err)
	}

	srv := hub.NewServer(&cfg, st, hub.BuildInfo{Version: "test", BuildTime: "test"})
	serverErr := make(chan error, 1)
	go func() { serverErr <- srv.Run(ctx) }()
	waitHTTP(t, cfg.Bridge.HubURL+"/health")

	bridgeErr := make(chan error, 1)
	go func() { bridgeErr <- bridge.NewClient(&cfg, "test").Run(ctx) }()

	client := httpClient(t)
	postJSON(t, client, cfg.Bridge.HubURL+"/api/login", map[string]string{
		"username": "admin",
		"password": "secret",
	}, http.StatusOK)
	waitAgents(t, client, cfg.Bridge.HubURL)

	sessionBody := postJSON(t, client, cfg.Bridge.HubURL+"/api/sessions", map[string]string{
		"title": "integration",
	}, http.StatusCreated)
	session := sessionBody["session"].(map[string]any)
	sid := session["id"].(string)

	ws := dialBrowserWS(t, client, cfg.Bridge.HubURL, sid)
	defer ws.Close()

	expectPrompt(t, ws, sid, "ping", "echo: ping")

	messagesBody := getJSON(t, client, cfg.Bridge.HubURL+"/api/sessions/"+url.PathEscape(sid)+"/messages", http.StatusOK)
	messages := messagesBody["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("expected 2 persisted messages, got %d: %#v", len(messages), messages)
	}

	cancel()
	select {
	case err := <-serverErr:
		if err != nil && err != context.Canceled {
			t.Fatalf("server error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not stop")
	}
	select {
	case err := <-bridgeErr:
		if err != nil && err != context.Canceled {
			t.Fatalf("bridge error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("bridge did not stop")
	}
}

func TestEchoBridgeTwoSessions(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tmp := t.TempDir()
	port := freePort(t)
	cfg := config.Default()
	cfg.Gateway.Host = "127.0.0.1"
	cfg.Gateway.Port = port
	cfg.Gateway.ReadTimeout.Duration = 5 * time.Second
	cfg.Gateway.WriteTimeout.Duration = 0
	cfg.Hub.DBPath = tmp + "/bridge.db"
	cfg.Hub.HeartbeatInterval.Duration = 200 * time.Millisecond
	cfg.Hub.BrowserCloseGrace.Duration = 20 * time.Millisecond
	cfg.Auth.JWTSecret = "integration-test-secret-32-byte-minimum"
	cfg.Auth.AccessTokenTTL.Duration = time.Hour
	cfg.Bridge.HubURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	cfg.Bridge.Token = store.NewToken("enr")
	cfg.Bridge.Name = "integration-bridge"
	cfg.Bridge.MachineIDFile = tmp + "/machine_id"
	cfg.Bridge.CWD = tmp
	cfg.Bridge.Runner = "echo"
	cfg.Bridge.ReconnectMin.Duration = 50 * time.Millisecond
	cfg.Bridge.ReconnectMax.Duration = 100 * time.Millisecond
	cfg.Bridge.HeartbeatInterval.Duration = 200 * time.Millisecond
	cfg.Bridge.MaxSessions = 4

	st, err := store.Open(cfg.Hub.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertUser(ctx, "admin", "secret"); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	if err := st.CreateEnrollToken(ctx, cfg.Bridge.Token, &expires); err != nil {
		t.Fatal(err)
	}

	srv := hub.NewServer(&cfg, st, hub.BuildInfo{Version: "test", BuildTime: "test"})
	go func() { _ = srv.Run(ctx) }()
	waitHTTP(t, cfg.Bridge.HubURL+"/health")
	go func() { _ = bridge.NewClient(&cfg, "test").Run(ctx) }()

	client := httpClient(t)
	postJSON(t, client, cfg.Bridge.HubURL+"/api/login", map[string]string{"username": "admin", "password": "secret"}, http.StatusOK)
	waitAgents(t, client, cfg.Bridge.HubURL)

	sidA := createSession(t, client, cfg.Bridge.HubURL, "a")
	sidB := createSession(t, client, cfg.Bridge.HubURL, "b")
	wsA := dialBrowserWS(t, client, cfg.Bridge.HubURL, sidA)
	defer wsA.Close()
	wsB := dialBrowserWS(t, client, cfg.Bridge.HubURL, sidB)
	defer wsB.Close()

	expectPrompt(t, wsA, sidA, "one", "echo: one")
	expectPrompt(t, wsB, sidB, "two", "echo: two")
}

func TestBrowserCloseDoesNotStopActivePrompt(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tmp := t.TempDir()
	port := freePort(t)
	cfg := config.Default()
	cfg.Gateway.Host = "127.0.0.1"
	cfg.Gateway.Port = port
	cfg.Gateway.ReadTimeout.Duration = 5 * time.Second
	cfg.Gateway.WriteTimeout.Duration = 0
	cfg.Hub.DBPath = tmp + "/bridge.db"
	cfg.Hub.HeartbeatInterval.Duration = 200 * time.Millisecond
	cfg.Hub.BrowserCloseSession = false
	cfg.Hub.BrowserCloseGrace.Duration = 20 * time.Millisecond
	cfg.Auth.JWTSecret = "integration-test-secret-32-byte-minimum"
	cfg.Auth.AccessTokenTTL.Duration = time.Hour
	cfg.Bridge.HubURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	cfg.Bridge.Token = store.NewToken("enr")

	st, err := store.Open(cfg.Hub.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertUser(ctx, "admin", "secret"); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	if err := st.CreateEnrollToken(ctx, cfg.Bridge.Token, &expires); err != nil {
		t.Fatal(err)
	}

	srv := hub.NewServer(&cfg, st, hub.BuildInfo{Version: "test", BuildTime: "test"})
	go func() { _ = srv.Run(ctx) }()
	waitHTTP(t, cfg.Bridge.HubURL+"/health")

	fakeBridge := dialFakeBridge(t, cfg.Bridge.HubURL, cfg.Bridge.Token)
	defer fakeBridge.Close()

	client := httpClient(t)
	postJSON(t, client, cfg.Bridge.HubURL+"/api/login", map[string]string{"username": "admin", "password": "secret"}, http.StatusOK)
	waitAgents(t, client, cfg.Bridge.HubURL)

	sid := createSession(t, client, cfg.Bridge.HubURL, "detached")
	ws := dialBrowserWS(t, client, cfg.Bridge.HubURL, sid)
	waitBridgeEnvelope(t, fakeBridge, protocol.TypeOpenSession, sid)
	if err := fakeBridge.WriteJSON(protocol.MustEnvelope(protocol.TypeSessionOpened, sid, protocol.SessionOpenedPayload{Runner: "fake"})); err != nil {
		t.Fatal(err)
	}
	if err := ws.WriteJSON(protocol.MustEnvelope(protocol.TypePrompt, sid, protocol.PromptPayload{Content: "keep running"})); err != nil {
		t.Fatal(err)
	}
	promptEnv := waitBridgeEnvelope(t, fakeBridge, protocol.TypePrompt, sid)
	promptPayload, err := protocol.Decode[protocol.PromptPayload](promptEnv)
	if err != nil {
		t.Fatal(err)
	}
	if promptPayload.RunID == "" || promptPayload.PromptID == "" {
		t.Fatalf("prompt missing run ids: %#v", promptPayload)
	}
	if err := ws.Close(); err != nil {
		t.Fatal(err)
	}

	assertNoBridgeCloseSession(t, fakeBridge, sid, 75*time.Millisecond)

	want := "finished after browser close"
	if err := fakeBridge.WriteJSON(protocol.MustEnvelope(protocol.TypeSessionUpdate, sid, protocol.SessionUpdatePayload{
		Delta:    want,
		RunID:    promptPayload.RunID,
		PromptID: promptPayload.PromptID,
	})); err != nil {
		t.Fatal(err)
	}
	if err := fakeBridge.WriteJSON(protocol.MustEnvelope(protocol.TypePromptComplete, sid, protocol.PromptCompletePayload{
		Content:  want,
		RunID:    promptPayload.RunID,
		PromptID: promptPayload.PromptID,
	})); err != nil {
		t.Fatal(err)
	}
	waitMessages(t, client, cfg.Bridge.HubURL, sid, 2)

	messagesBody := getJSON(t, client, cfg.Bridge.HubURL+"/api/sessions/"+url.PathEscape(sid)+"/messages", http.StatusOK)
	messages := messagesBody["messages"].([]any)
	if got := messages[len(messages)-1].(map[string]any)["content"]; got != want {
		t.Fatalf("persisted assistant content = %q, want %q", got, want)
	}
	waitRunStatus(t, client, cfg.Bridge.HubURL, sid, promptPayload.RunID, "succeeded")

	cancel()
}

func TestDuplicatePromptRejectedWhileRunActive(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tmp := t.TempDir()
	port := freePort(t)
	cfg := config.Default()
	cfg.Gateway.Host = "127.0.0.1"
	cfg.Gateway.Port = port
	cfg.Gateway.ReadTimeout.Duration = 5 * time.Second
	cfg.Gateway.WriteTimeout.Duration = 0
	cfg.Hub.DBPath = tmp + "/bridge.db"
	cfg.Hub.HeartbeatInterval.Duration = 200 * time.Millisecond
	cfg.Hub.BrowserCloseSession = false
	cfg.Auth.JWTSecret = "integration-test-secret-32-byte-minimum"
	cfg.Auth.AccessTokenTTL.Duration = time.Hour
	cfg.Bridge.HubURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	cfg.Bridge.Token = store.NewToken("enr")

	st, err := store.Open(cfg.Hub.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertUser(ctx, "admin", "secret"); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	if err := st.CreateEnrollToken(ctx, cfg.Bridge.Token, &expires); err != nil {
		t.Fatal(err)
	}

	srv := hub.NewServer(&cfg, st, hub.BuildInfo{Version: "test", BuildTime: "test"})
	go func() { _ = srv.Run(ctx) }()
	waitHTTP(t, cfg.Bridge.HubURL+"/health")

	fakeBridge := dialFakeBridge(t, cfg.Bridge.HubURL, cfg.Bridge.Token)
	defer fakeBridge.Close()

	client := httpClient(t)
	postJSON(t, client, cfg.Bridge.HubURL+"/api/login", map[string]string{"username": "admin", "password": "secret"}, http.StatusOK)
	waitAgents(t, client, cfg.Bridge.HubURL)

	sid := createSession(t, client, cfg.Bridge.HubURL, "duplicate")
	ws := dialBrowserWS(t, client, cfg.Bridge.HubURL, sid)
	defer ws.Close()
	waitBridgeEnvelope(t, fakeBridge, protocol.TypeOpenSession, sid)
	if err := fakeBridge.WriteJSON(protocol.MustEnvelope(protocol.TypeSessionOpened, sid, protocol.SessionOpenedPayload{Runner: "fake"})); err != nil {
		t.Fatal(err)
	}
	if err := ws.WriteJSON(protocol.MustEnvelope(protocol.TypePrompt, sid, protocol.PromptPayload{Content: "first", PromptID: "client-prompt-1"})); err != nil {
		t.Fatal(err)
	}
	promptEnv := waitBridgeEnvelope(t, fakeBridge, protocol.TypePrompt, sid)
	firstPayload, err := protocol.Decode[protocol.PromptPayload](promptEnv)
	if err != nil {
		t.Fatal(err)
	}
	if firstPayload.RunID == "" {
		t.Fatalf("first prompt missing run id: %#v", firstPayload)
	}
	if err := ws.WriteJSON(protocol.MustEnvelope(protocol.TypePrompt, sid, protocol.PromptPayload{Content: "second", PromptID: "client-prompt-2"})); err != nil {
		t.Fatal(err)
	}
	errEnv := waitBrowserEnvelope(t, ws, protocol.TypeError, sid)
	errPayload, err := protocol.Decode[protocol.ErrorPayload](errEnv)
	if err != nil {
		t.Fatal(err)
	}
	if errPayload.Code != "RUN_ACTIVE" || errPayload.RunID != firstPayload.RunID {
		t.Fatalf("duplicate error = %#v, first = %#v", errPayload, firstPayload)
	}

	runsBody := getJSON(t, client, cfg.Bridge.HubURL+"/api/sessions/"+url.PathEscape(sid)+"/runs", http.StatusOK)
	runs := runsBody["runs"].([]any)
	if len(runs) != 1 {
		t.Fatalf("runs = %#v, want one active run", runs)
	}
}

func TestBrowserApprovalResponseRoutesToBridge(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tmp := t.TempDir()
	port := freePort(t)
	cfg := config.Default()
	cfg.Gateway.Host = "127.0.0.1"
	cfg.Gateway.Port = port
	cfg.Gateway.ReadTimeout.Duration = 5 * time.Second
	cfg.Hub.DBPath = tmp + "/bridge.db"
	cfg.Auth.JWTSecret = "integration-test-secret-32-byte-minimum"
	cfg.Auth.AccessTokenTTL.Duration = time.Hour
	cfg.Bridge.HubURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	cfg.Bridge.Token = store.NewToken("enr")

	st, err := store.Open(cfg.Hub.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertUser(ctx, "admin", "secret"); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	if err := st.CreateEnrollToken(ctx, cfg.Bridge.Token, &expires); err != nil {
		t.Fatal(err)
	}

	srv := hub.NewServer(&cfg, st, hub.BuildInfo{Version: "test", BuildTime: "test"})
	go func() { _ = srv.Run(ctx) }()
	waitHTTP(t, cfg.Bridge.HubURL+"/health")

	fakeBridge := dialFakeBridge(t, cfg.Bridge.HubURL, cfg.Bridge.Token)
	defer fakeBridge.Close()

	client := httpClient(t)
	postJSON(t, client, cfg.Bridge.HubURL+"/api/login", map[string]string{"username": "admin", "password": "secret"}, http.StatusOK)
	waitAgents(t, client, cfg.Bridge.HubURL)

	sid := createSession(t, client, cfg.Bridge.HubURL, "approval")
	ws := dialBrowserWS(t, client, cfg.Bridge.HubURL, sid)
	defer ws.Close()
	waitBridgeEnvelope(t, fakeBridge, protocol.TypeOpenSession, sid)

	req := protocol.ApprovalRequestPayload{RequestID: "apr_1", Kind: "item/commandExecution/requestApproval", Command: "rm -rf build", CWD: "/repo"}
	if err := fakeBridge.WriteJSON(protocol.MustEnvelope(protocol.TypeApprovalRequest, sid, req)); err != nil {
		t.Fatal(err)
	}
	browserEnv := waitBrowserEnvelope(t, ws, protocol.TypeApprovalRequest, sid)
	gotReq, err := protocol.Decode[protocol.ApprovalRequestPayload](browserEnv)
	if err != nil {
		t.Fatal(err)
	}
	if gotReq.RequestID != "apr_1" || gotReq.Command != "rm -rf build" {
		t.Fatalf("approval request = %#v", gotReq)
	}
	if err := ws.WriteJSON(protocol.MustEnvelope(protocol.TypeApprovalResponse, sid, protocol.ApprovalResponsePayload{RequestID: "apr_1", Decision: "accept"})); err != nil {
		t.Fatal(err)
	}
	bridgeEnv := waitBridgeEnvelope(t, fakeBridge, protocol.TypeApprovalResponse, sid)
	gotRes, err := protocol.Decode[protocol.ApprovalResponsePayload](bridgeEnv)
	if err != nil {
		t.Fatal(err)
	}
	if gotRes.RequestID != "apr_1" || gotRes.Decision != "accept" {
		t.Fatalf("approval response = %#v", gotRes)
	}
}

func TestOrchestrationApprovalResponseRoutesToBridge(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tmp := t.TempDir()
	port := freePort(t)
	cfg := config.Default()
	cfg.Gateway.Host = "127.0.0.1"
	cfg.Gateway.Port = port
	cfg.Gateway.ReadTimeout.Duration = 5 * time.Second
	cfg.Hub.DBPath = tmp + "/bridge.db"
	cfg.Auth.JWTSecret = "integration-test-secret-32-byte-minimum"
	cfg.Auth.AccessTokenTTL.Duration = time.Hour
	cfg.Bridge.HubURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	cfg.Bridge.Token = store.NewToken("enr")

	st, err := store.Open(cfg.Hub.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	user, err := st.UpsertUser(ctx, "admin", "secret")
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	if err := st.CreateEnrollToken(ctx, cfg.Bridge.Token, &expires); err != nil {
		t.Fatal(err)
	}

	srv := hub.NewServer(&cfg, st, hub.BuildInfo{Version: "test", BuildTime: "test"})
	go func() { _ = srv.Run(ctx) }()
	waitHTTP(t, cfg.Bridge.HubURL+"/health")

	fakeBridge := dialFakeBridge(t, cfg.Bridge.HubURL, cfg.Bridge.Token)
	defer fakeBridge.Close()
	registered := waitRegisteredAgent(t, st, "fake-bridge")

	client := httpClient(t)
	postJSON(t, client, cfg.Bridge.HubURL+"/api/login", map[string]string{"username": "admin", "password": "secret"}, http.StatusOK)
	waitAgents(t, client, cfg.Bridge.HubURL)
	run, err := st.CreateOrchestrationRun(ctx, store.CreateOrchestrationRunParams{
		UserID:   user.ID,
		AgentID:  registered.ID,
		Title:    "approval",
		Mode:     "collaboration",
		Prompt:   "approval",
		MaxTurns: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	ws := dialOrchestrationWS(t, client, cfg.Bridge.HubURL, run.ID)
	defer ws.Close()
	req := protocol.ApprovalRequestPayload{RequestID: "apr_orc_1", Kind: "claude.permission_prompt", RunID: run.ID, TurnID: "turn_1", Command: "rm -rf build"}
	if err := fakeBridge.WriteJSON(protocol.MustEnvelope(protocol.TypeApprovalRequest, "", req)); err != nil {
		t.Fatal(err)
	}
	browserEnv := waitBrowserEnvelope(t, ws, protocol.TypeApprovalRequest, "")
	gotReq, err := protocol.Decode[protocol.ApprovalRequestPayload](browserEnv)
	if err != nil {
		t.Fatal(err)
	}
	if gotReq.RequestID != "apr_orc_1" || gotReq.RunID != run.ID || gotReq.Command != "rm -rf build" {
		t.Fatalf("orchestration approval request = %#v", gotReq)
	}
	if err := ws.WriteJSON(protocol.MustEnvelope(protocol.TypeApprovalResponse, "", protocol.ApprovalResponsePayload{RequestID: "apr_orc_1", Decision: "accept"})); err != nil {
		t.Fatal(err)
	}
	bridgeEnv := waitBridgeEnvelope(t, fakeBridge, protocol.TypeApprovalResponse, "")
	gotRes, err := protocol.Decode[protocol.ApprovalResponsePayload](bridgeEnv)
	if err != nil {
		t.Fatal(err)
	}
	if gotRes.RequestID != "apr_orc_1" || gotRes.Decision != "accept" {
		t.Fatalf("orchestration approval response = %#v", gotRes)
	}
}

func TestCCBConfiguredBridgeCannotBypassManualOrchestrationRequirements(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "ccb"), []byte(fakeCCBTerminalApprovalScript()), 0o755); err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	cfg := config.Default()
	cfg.Gateway.Host = "127.0.0.1"
	cfg.Gateway.Port = port
	cfg.Gateway.ReadTimeout.Duration = 5 * time.Second
	cfg.Gateway.WriteTimeout.Duration = 0
	cfg.Hub.DBPath = tmp + "/bridge.db"
	cfg.Hub.HeartbeatInterval.Duration = 100 * time.Millisecond
	cfg.Hub.BrowserCloseGrace.Duration = 20 * time.Millisecond
	cfg.Auth.JWTSecret = "integration-test-secret-32-byte-minimum"
	cfg.Auth.AccessTokenTTL.Duration = time.Hour
	cfg.Bridge.HubURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	cfg.Bridge.Token = store.NewToken("enr")
	cfg.Bridge.Name = "ccb-terminal-approval"
	cfg.Bridge.MachineIDFile = tmp + "/machine_id"
	cfg.Bridge.CWD = tmp
	cfg.Bridge.Runner = "codex"
	cfg.Bridge.OrchestrationRunner = "ccb"
	cfg.Bridge.CCBPath = filepath.Join(binDir, "ccb")
	cfg.Bridge.CCBTarget = "codex"
	cfg.Bridge.CCBTimeout.Duration = 5 * time.Second
	cfg.Bridge.CodexPath = filepath.Join(binDir, "missing-codex")
	cfg.Bridge.ClaudePath = filepath.Join(binDir, "missing-claude")
	cfg.Bridge.ReconnectMin.Duration = 50 * time.Millisecond
	cfg.Bridge.ReconnectMax.Duration = 100 * time.Millisecond
	cfg.Bridge.HeartbeatInterval.Duration = 200 * time.Millisecond

	st, err := store.Open(cfg.Hub.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertUser(ctx, "admin", "secret"); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	if err := st.CreateEnrollToken(ctx, cfg.Bridge.Token, &expires); err != nil {
		t.Fatal(err)
	}

	srv := hub.NewServer(&cfg, st, hub.BuildInfo{Version: "test", BuildTime: "test"})
	go func() { _ = srv.Run(ctx) }()
	waitHTTP(t, cfg.Bridge.HubURL+"/health")
	go func() { _ = bridge.NewClient(&cfg, "test").Run(ctx) }()

	client := httpClient(t)
	postJSON(t, client, cfg.Bridge.HubURL+"/api/login", map[string]string{"username": "admin", "password": "secret"}, http.StatusOK)
	waitAgents(t, client, cfg.Bridge.HubURL)
	body := postJSON(t, client, cfg.Bridge.HubURL+"/api/orchestrations", map[string]any{
		"prompt":   "try ccb bypass",
		"title":    "manual orchestration requirements",
		"mode":     "collaboration",
		"cwd":      tmp,
		"maxTurns": 2,
	}, http.StatusConflict)
	if body["code"] != "ORCHESTRATION_CAPABILITY_UNAVAILABLE" || !strings.Contains(body["message"].(string), "Claude") || !strings.Contains(body["message"].(string), "Codex") {
		t.Fatalf("capability error body = %#v", body)
	}
}

func TestWebOrchestrationInitialRequestApprovesAndRemediatesMissingFileChange(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	codexPath := filepath.Join(binDir, "codex")
	claudePath := filepath.Join(binDir, "claude")
	if err := os.WriteFile(codexPath, []byte(fakeApprovalCodexAppServerScript()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudePath, []byte(fakeUnresolvedSorryClaudeScript()), 0o755); err != nil {
		t.Fatal(err)
	}

	port := freePort(t)
	cfg := config.Default()
	cfg.Gateway.Host = "127.0.0.1"
	cfg.Gateway.Port = port
	cfg.Gateway.ReadTimeout.Duration = 5 * time.Second
	cfg.Gateway.WriteTimeout.Duration = 0
	cfg.Hub.DBPath = tmp + "/bridge.db"
	cfg.Hub.HeartbeatInterval.Duration = 200 * time.Millisecond
	cfg.Auth.JWTSecret = "integration-test-secret-32-byte-minimum"
	cfg.Auth.AccessTokenTTL.Duration = time.Hour
	cfg.Bridge.HubURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	cfg.Bridge.Token = store.NewToken("enr")
	cfg.Bridge.Name = "web-approval-orchestration-bridge"
	cfg.Bridge.MachineIDFile = tmp + "/machine_id"
	cfg.Bridge.CWD = tmp
	cfg.Bridge.Runner = "codex-app-server"
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.ClaudePath = claudePath
	cfg.Bridge.Sandbox = "workspace-write"
	cfg.Bridge.ApprovalPolicy = "untrusted"
	cfg.Bridge.ReconnectMin.Duration = 50 * time.Millisecond
	cfg.Bridge.ReconnectMax.Duration = 100 * time.Millisecond
	cfg.Bridge.HeartbeatInterval.Duration = 200 * time.Millisecond

	st, err := store.Open(cfg.Hub.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertUser(ctx, "admin", "secret"); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	if err := st.CreateEnrollToken(ctx, cfg.Bridge.Token, &expires); err != nil {
		t.Fatal(err)
	}

	srv := hub.NewServer(&cfg, st, hub.BuildInfo{Version: "test", BuildTime: "test"})
	go func() { _ = srv.Run(ctx) }()
	waitHTTP(t, cfg.Bridge.HubURL+"/health")
	go func() { _ = bridge.NewClient(&cfg, "test").Run(ctx) }()

	client := httpClient(t)
	postJSON(t, client, cfg.Bridge.HubURL+"/api/login", map[string]string{"username": "admin", "password": "secret"}, http.StatusOK)
	waitAgents(t, client, cfg.Bridge.HubURL)

	body := postJSON(t, client, cfg.Bridge.HubURL+"/api/orchestrations", map[string]any{
		"mode":     "collaboration",
		"title":    "remove main theorem sorry",
		"prompt":   unresolvedSorryInitialPrompt(),
		"cwd":      tmp,
		"maxTurns": 2,
		"files": []map[string]any{
			{"name": "Model.thy", "mimeType": "text/plain", "size": 17, "data": "dGhlb3J5IE1vZGVsCg=="},
			{"name": "Termination.thy", "mimeType": "text/plain", "size": 23, "data": "dGhlb3J5IFRlcm1pbmF0aW9uCg=="},
			{"name": "ROOT", "mimeType": "text/plain", "size": 28, "data": "c2Vzc2lvbiB0ZXJtaW5hdGlvbl9mcmFtZXdvcmsK"},
		},
	}, http.StatusCreated)
	run := body["run"].(map[string]any)
	runID := run["id"].(string)
	ws := dialOrchestrationWS(t, client, cfg.Bridge.HubURL, runID)
	defer ws.Close()

	streamed := observeOrchestrationAndApprove(t, ws, runID)
	waitOrchestrationStatus(t, client, cfg.Bridge.HubURL, runID, store.OrchestrationCompleted)
	eventsBody := getJSON(t, client, cfg.Bridge.HubURL+"/api/orchestrations/"+url.PathEscape(runID)+"/events", http.StatusOK)
	events := eventsBody["events"].([]any)

	if streamed.approvals < 2 || streamed.acceptedApprovals < 2 {
		t.Fatalf("approval was not observed and accepted: %#v", streamed)
	}
	if !streamed.sawCodexCommand || !eventsContainCommand(events, "isabelle build -D /root/Isabelle") {
		t.Fatalf("codex command missing; streamed=%#v events=%#v", streamed, events)
	}
	if !streamed.sawUnresolvedRisk || !eventsContainContent(events, "主定理 sorry 仍未消除") {
		t.Fatalf("unresolved main-goal risk missing; streamed=%#v events=%#v", streamed, events)
	}
	if !streamed.sawRemediationCommand || !eventsContainCommand(events, "mkdir -p Isabelle && write Termination.thy") {
		t.Fatalf("remediation write command missing; streamed=%#v events=%#v", streamed, events)
	}
	if !eventsContainContent(events, "补救轮已写入 Isabelle/Termination.thy") {
		t.Fatalf("remediation final content missing; streamed=%#v events=%#v", streamed, events)
	}
	if _, err := os.Stat(filepath.Join(tmp, "Isabelle", "Termination.thy")); err != nil {
		t.Fatalf("remediation file was not created: %v", err)
	}
	for _, raw := range events {
		event := raw.(map[string]any)
		if event["kind"] == "run.error" {
			t.Fatalf("run should not fail after remediation: %#v", event)
		}
		if event["kind"] != "turn.end" {
			continue
		}
		content := fmt.Sprint(event["content"])
		if strings.Contains(content, "本次编排已完成") || strings.Contains(content, "this orchestration completed") {
			t.Fatalf("turn.end wrongly claimed completion despite unresolved sorry: %q", content)
		}
	}
}

func TestOrchestrationEndToEndWithFakeCLIs(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	codexPath := filepath.Join(binDir, "codex")
	claudePath := filepath.Join(binDir, "claude")
	if err := os.WriteFile(codexPath, []byte(fakeCodexScript()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudePath, []byte(fakeClaudeScript()), 0o755); err != nil {
		t.Fatal(err)
	}

	port := freePort(t)
	cfg := config.Default()
	cfg.Gateway.Host = "127.0.0.1"
	cfg.Gateway.Port = port
	cfg.Gateway.ReadTimeout.Duration = 5 * time.Second
	cfg.Gateway.WriteTimeout.Duration = 0
	cfg.Hub.DBPath = tmp + "/bridge.db"
	cfg.Hub.HeartbeatInterval.Duration = 200 * time.Millisecond
	cfg.Auth.JWTSecret = "integration-test-secret-32-byte-minimum"
	cfg.Auth.AccessTokenTTL.Duration = time.Hour
	cfg.Bridge.HubURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	cfg.Bridge.Token = store.NewToken("enr")
	cfg.Bridge.Name = "orchestration-bridge"
	cfg.Bridge.MachineIDFile = tmp + "/machine_id"
	cfg.Bridge.CWD = tmp
	cfg.Bridge.Runner = "echo"
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.ClaudePath = claudePath
	cfg.Bridge.Sandbox = "danger-full-access"
	cfg.Bridge.ApprovalPolicy = "never"
	cfg.Bridge.ReconnectMin.Duration = 50 * time.Millisecond
	cfg.Bridge.ReconnectMax.Duration = 100 * time.Millisecond
	cfg.Bridge.HeartbeatInterval.Duration = 200 * time.Millisecond

	st, err := store.Open(cfg.Hub.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertUser(ctx, "admin", "secret"); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	if err := st.CreateEnrollToken(ctx, cfg.Bridge.Token, &expires); err != nil {
		t.Fatal(err)
	}

	srv := hub.NewServer(&cfg, st, hub.BuildInfo{Version: "test", BuildTime: "test"})
	go func() { _ = srv.Run(ctx) }()
	waitHTTP(t, cfg.Bridge.HubURL+"/health")
	go func() { _ = bridge.NewClient(&cfg, "test").Run(ctx) }()

	client := httpClient(t)
	postJSON(t, client, cfg.Bridge.HubURL+"/api/login", map[string]string{"username": "admin", "password": "secret"}, http.StatusOK)
	waitAgents(t, client, cfg.Bridge.HubURL)

	body := postJSON(t, client, cfg.Bridge.HubURL+"/api/orchestrations", map[string]any{
		"mode":     "collaboration",
		"title":    "fake orchestration",
		"prompt":   "coordinate fake CLIs",
		"maxTurns": 2,
		"files": []map[string]any{
			{"name": "Goal.v", "mimeType": "text/plain", "size": 12, "data": "VGhlb3JlbS4K"},
		},
	}, http.StatusCreated)
	run := body["run"].(map[string]any)
	runID := run["id"].(string)

	waitOrchestrationStatus(t, client, cfg.Bridge.HubURL, runID, "completed")
	eventsBody := getJSON(t, client, cfg.Bridge.HubURL+"/api/orchestrations/"+url.PathEscape(runID)+"/events", http.StatusOK)
	events := eventsBody["events"].([]any)
	var sawClaude, sawCodex bool
	for _, raw := range events {
		event := raw.(map[string]any)
		if event["cli"] == "claude" && strings.Contains(fmt.Sprint(event["content"]), "fake claude") {
			sawClaude = true
		}
		if event["cli"] == "codex" && strings.Contains(fmt.Sprint(event["content"]), "fake codex") {
			sawCodex = true
		}
	}
	if !sawClaude || !sawCodex {
		t.Fatalf("missing fake CLI events: sawClaude=%v sawCodex=%v events=%#v", sawClaude, sawCodex, events)
	}
}

func TestOrchestrationIsabellePromptStreamsUsefulCommandEvents(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	codexPath := filepath.Join(binDir, "codex")
	claudePath := filepath.Join(binDir, "claude")
	if err := os.WriteFile(codexPath, []byte(fakeIsabelleCodexScript()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudePath, []byte(fakeIsabelleClaudeScript()), 0o755); err != nil {
		t.Fatal(err)
	}

	port := freePort(t)
	cfg := config.Default()
	cfg.Gateway.Host = "127.0.0.1"
	cfg.Gateway.Port = port
	cfg.Gateway.ReadTimeout.Duration = 5 * time.Second
	cfg.Gateway.WriteTimeout.Duration = 0
	cfg.Hub.DBPath = tmp + "/bridge.db"
	cfg.Hub.HeartbeatInterval.Duration = 200 * time.Millisecond
	cfg.Auth.JWTSecret = "integration-test-secret-32-byte-minimum"
	cfg.Auth.AccessTokenTTL.Duration = time.Hour
	cfg.Bridge.HubURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	cfg.Bridge.Token = store.NewToken("enr")
	cfg.Bridge.Name = "isabelle-orchestration-bridge"
	cfg.Bridge.MachineIDFile = tmp + "/machine_id"
	cfg.Bridge.CWD = tmp
	cfg.Bridge.Runner = "echo"
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.ClaudePath = claudePath
	cfg.Bridge.Sandbox = "danger-full-access"
	cfg.Bridge.ApprovalPolicy = "never"
	cfg.Bridge.ReconnectMin.Duration = 50 * time.Millisecond
	cfg.Bridge.ReconnectMax.Duration = 100 * time.Millisecond
	cfg.Bridge.HeartbeatInterval.Duration = 200 * time.Millisecond

	st, err := store.Open(cfg.Hub.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertUser(ctx, "admin", "secret"); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	if err := st.CreateEnrollToken(ctx, cfg.Bridge.Token, &expires); err != nil {
		t.Fatal(err)
	}

	srv := hub.NewServer(&cfg, st, hub.BuildInfo{Version: "test", BuildTime: "test"})
	go func() { _ = srv.Run(ctx) }()
	waitHTTP(t, cfg.Bridge.HubURL+"/health")
	go func() { _ = bridge.NewClient(&cfg, "test").Run(ctx) }()

	client := httpClient(t)
	postJSON(t, client, cfg.Bridge.HubURL+"/api/login", map[string]string{"username": "admin", "password": "secret"}, http.StatusOK)
	waitAgents(t, client, cfg.Bridge.HubURL)

	body := postJSON(t, client, cfg.Bridge.HubURL+"/api/orchestrations", map[string]any{
		"mode":     "collaboration",
		"title":    "Isabelle BridgeDemo",
		"prompt":   isabelleBridgeDemoPrompt(),
		"cwd":      tmp,
		"maxTurns": 2,
	}, http.StatusCreated)
	run := body["run"].(map[string]any)
	runID := run["id"].(string)

	ws := dialOrchestrationWS(t, client, cfg.Bridge.HubURL, runID)
	defer ws.Close()
	streamed := waitOrchestrationCommandEvents(t, ws, runID, []string{
		"mkdir -p isabelle_bridge_demo",
		"isabelle build -D isabelle_bridge_demo",
	})

	waitOrchestrationStatus(t, client, cfg.Bridge.HubURL, runID, "completed")
	eventsBody := getJSON(t, client, cfg.Bridge.HubURL+"/api/orchestrations/"+url.PathEscape(runID)+"/events", http.StatusOK)
	events := eventsBody["events"].([]any)
	if !eventsContainCommand(events, "mkdir -p isabelle_bridge_demo") || !eventsContainCommand(events, "isabelle build -D isabelle_bridge_demo") {
		t.Fatalf("persisted events missing useful commands: %#v", events)
	}
	if !streamed["mkdir -p isabelle_bridge_demo"] || !streamed["isabelle build -D isabelle_bridge_demo"] {
		t.Fatalf("streamed commands = %#v", streamed)
	}
}

func TestCoqTaskWebSmokeWithFakeBridge(t *testing.T) {
	for _, mode := range []string{"collaboration", "debate"} {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			tmp := t.TempDir()
			port := freePort(t)
			cfg := config.Default()
			cfg.Gateway.Host = "127.0.0.1"
			cfg.Gateway.Port = port
			cfg.Gateway.ReadTimeout.Duration = 5 * time.Second
			cfg.Gateway.WriteTimeout.Duration = 0
			cfg.Hub.DBPath = tmp + "/bridge.db"
			cfg.Hub.HeartbeatInterval.Duration = 200 * time.Millisecond
			cfg.Auth.JWTSecret = "integration-test-secret-32-byte-minimum"
			cfg.Auth.AccessTokenTTL.Duration = time.Hour
			cfg.Bridge.HubURL = fmt.Sprintf("http://127.0.0.1:%d", port)
			cfg.Bridge.Token = store.NewToken("enr")

			st, err := store.Open(cfg.Hub.DBPath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close() })
			if err := st.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := st.UpsertUser(ctx, "admin", "secret"); err != nil {
				t.Fatal(err)
			}
			expires := time.Now().Add(time.Hour)
			if err := st.CreateEnrollToken(ctx, cfg.Bridge.Token, &expires); err != nil {
				t.Fatal(err)
			}

			srv := hub.NewServer(&cfg, st, hub.BuildInfo{Version: "test", BuildTime: "test"})
			go func() { _ = srv.Run(ctx) }()
			waitHTTP(t, cfg.Bridge.HubURL+"/health")
			fakeBridge := dialFakeBridgeWithOptions(t, cfg.Bridge.HubURL, cfg.Bridge.Token, fakeBridgeOptions{
				WorkingDirs: []string{"/root/tencent", "/root/tencent/bridge"},
			})
			defer fakeBridge.Close()

			client := httpClient(t)
			postJSON(t, client, cfg.Bridge.HubURL+"/api/login", map[string]string{"username": "admin", "password": "secret"}, http.StatusOK)
			waitAgents(t, client, cfg.Bridge.HubURL)

			task := coqSmokeTaskPrompt()
			body := postJSON(t, client, cfg.Bridge.HubURL+"/api/orchestrations", map[string]any{
				"mode":     mode,
				"title":    "coq proof smoke",
				"prompt":   task,
				"cwd":      "/root/tencent",
				"maxTurns": 4,
				"files": []map[string]any{
					{"name": "Model.thy", "mimeType": "application/octet-stream", "size": 17, "data": "dGhlb3J5IE1vZGVsCg=="},
					{"name": "Termination.thy", "mimeType": "application/octet-stream", "size": 23, "data": "dGhlb3J5IFRlcm1pbmF0aW9uCg=="},
					{"name": "ROOT", "mimeType": "application/octet-stream", "size": 28, "data": "c2Vzc2lvbiB0ZXJtaW5hdGlvbl9mcmFtZXdvcmsK"},
				},
			}, http.StatusCreated)
			run := body["run"].(map[string]any)
			runID := run["id"].(string)
			assertRunHasFiles(t, run, []string{"Model.thy", "Termination.thy", "ROOT"})

			env := waitBridgeEnvelope(t, fakeBridge, protocol.TypeOrchestrationStart, "")
			start, err := protocol.Decode[protocol.OrchestrationStartPayload](env)
			if err != nil {
				t.Fatal(err)
			}
			if start.RunID != runID || start.Mode != mode || start.Prompt != task || start.CWD != "/root/tencent" || start.MaxTurns != 4 {
				t.Fatalf("orchestration_start = %#v", start)
			}
			if len(start.Files) != 3 {
				t.Fatalf("start files = %#v", start.Files)
			}
			startFiles := make(map[string]protocol.AttachmentPayload, len(start.Files))
			for _, file := range start.Files {
				startFiles[file.Name] = file
			}
			for _, want := range []string{"Model.thy", "Termination.thy", "ROOT"} {
				file, ok := startFiles[want]
				if !ok || file.Data == "" {
					t.Fatalf("start file %q missing or has no data: %#v", want, start.Files)
				}
			}

			failure := "acceptance check failed: 当前 Coq 版本改成 modify_lin_fuel，并用固定 default_fuel 包装；没有证明原递归每步下降、Distance 下降或默认燃料足够模拟原 Isabelle 递归到停止态。"
			if err := fakeBridge.WriteJSON(protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", protocol.OrchestrationEventPayload{
				RunID:  runID,
				Kind:   "run.error",
				Status: store.OrchestrationFailed,
				Error:  failure,
			})); err != nil {
				t.Fatal(err)
			}
			waitOrchestrationStatus(t, client, cfg.Bridge.HubURL, runID, store.OrchestrationFailed)
			eventsBody := getJSON(t, client, cfg.Bridge.HubURL+"/api/orchestrations/"+url.PathEscape(runID)+"/events", http.StatusOK)
			events := eventsBody["events"].([]any)
			for _, want := range []string{"modify_lin_fuel", "default_fuel", "没有证明"} {
				if !eventsContainContent(events, want) {
					t.Fatalf("web-visible events missing %q: %#v", want, events)
				}
			}
		})
	}
}

func TestNonAdminCanOnlyUseOrchestrationAPIs(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tmp := t.TempDir()
	port := freePort(t)
	cfg := config.Default()
	cfg.Gateway.Host = "127.0.0.1"
	cfg.Gateway.Port = port
	cfg.Gateway.ReadTimeout.Duration = 5 * time.Second
	cfg.Hub.DBPath = tmp + "/bridge.db"
	cfg.Auth.JWTSecret = "integration-test-secret-32-byte-minimum"
	cfg.Auth.AccessTokenTTL.Duration = time.Hour
	cfg.Bridge.HubURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	cfg.Bridge.Token = store.NewToken("enr")

	st, err := store.Open(cfg.Hub.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertUser(ctx, "admin", "secret"); err != nil {
		t.Fatal(err)
	}
	worker, err := st.UpsertUser(ctx, "worker", "secret")
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	if err := st.CreateEnrollTokenForUser(ctx, cfg.Bridge.Token, worker.ID, "worker-cli", &expires); err != nil {
		t.Fatal(err)
	}

	srv := hub.NewServer(&cfg, st, hub.BuildInfo{Version: "test", BuildTime: "test"})
	go func() { _ = srv.Run(ctx) }()
	waitHTTP(t, cfg.Bridge.HubURL+"/health")
	fakeBridge := dialFakeBridge(t, cfg.Bridge.HubURL, cfg.Bridge.Token)
	defer fakeBridge.Close()

	adminClient := httpClient(t)
	adminLogin := postJSON(t, adminClient, cfg.Bridge.HubURL+"/api/login", map[string]string{"username": "admin", "password": "secret"}, http.StatusOK)
	if user := adminLogin["user"].(map[string]any); user["isAdmin"] != true {
		t.Fatalf("admin login user = %#v", user)
	}
	waitAgents(t, adminClient, cfg.Bridge.HubURL)
	postJSON(t, adminClient, cfg.Bridge.HubURL+"/api/sessions", map[string]string{"title": "admin-ok"}, http.StatusCreated)

	workerClient := httpClient(t)
	workerLogin := postJSON(t, workerClient, cfg.Bridge.HubURL+"/api/login", map[string]string{"username": "worker", "password": "secret"}, http.StatusOK)
	if user := workerLogin["user"].(map[string]any); user["isAdmin"] == true {
		t.Fatalf("worker login user = %#v", user)
	}
	getJSON(t, workerClient, cfg.Bridge.HubURL+"/api/sessions", http.StatusForbidden)
	workerAgents := getJSON(t, workerClient, cfg.Bridge.HubURL+"/api/agents", http.StatusOK)["agents"].([]any)
	if len(workerAgents) != 1 || workerAgents[0].(map[string]any)["userId"] != worker.ID {
		t.Fatalf("worker agents = %#v", workerAgents)
	}
	getJSON(t, workerClient, cfg.Bridge.HubURL+"/api/orchestrations", http.StatusOK)
	orcBody := postJSON(t, workerClient, cfg.Bridge.HubURL+"/api/orchestrations", map[string]any{
		"mode":     "collaboration",
		"title":    "worker orchestration",
		"prompt":   "worker can use orchestration",
		"maxTurns": 2,
	}, http.StatusCreated)
	run := orcBody["run"].(map[string]any)
	env := waitBridgeEnvelope(t, fakeBridge, protocol.TypeOrchestrationStart, "")
	start, err := protocol.Decode[protocol.OrchestrationStartPayload](env)
	if err != nil {
		t.Fatal(err)
	}
	if start.RunID != run["id"] || start.Prompt != "worker can use orchestration" {
		t.Fatalf("orchestration start = %#v, run = %#v", start, run)
	}
}

func TestExistingUserBridgeTokenBindsAgentToUser(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tmp := t.TempDir()
	port := freePort(t)
	cfg := config.Default()
	cfg.Gateway.Host = "127.0.0.1"
	cfg.Gateway.Port = port
	cfg.Gateway.ReadTimeout.Duration = 5 * time.Second
	cfg.Hub.DBPath = tmp + "/bridge.db"
	cfg.Hub.BridgeDownloadURL = "https://example.com/codex-bridge-linux-amd64"
	cfg.Auth.JWTSecret = "integration-test-secret-32-byte-minimum"
	cfg.Auth.AccessTokenTTL.Duration = time.Hour
	cfg.Bridge.HubURL = fmt.Sprintf("http://127.0.0.1:%d", port)

	st, err := store.Open(cfg.Hub.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertUser(ctx, "admin", "secret"); err != nil {
		t.Fatal(err)
	}
	worker, err := st.UpsertUser(ctx, "bridge-user", "long-secret-123")
	if err != nil {
		t.Fatal(err)
	}

	srv := hub.NewServer(&cfg, st, hub.BuildInfo{Version: "test", BuildTime: "test"})
	go func() { _ = srv.Run(ctx) }()
	waitHTTP(t, cfg.Bridge.HubURL+"/health")

	workerClient := httpClient(t)
	loginBody := postJSON(t, workerClient, cfg.Bridge.HubURL+"/api/login", map[string]string{
		"username": "bridge-user",
		"password": "long-secret-123",
	}, http.StatusOK)
	workerLogin := loginBody["user"].(map[string]any)
	if workerLogin["id"] != worker.ID {
		t.Fatalf("worker login = %#v, user = %#v", workerLogin, worker)
	}
	tokenBody := postJSON(t, workerClient, cfg.Bridge.HubURL+"/api/bridge-tokens", map[string]string{
		"label": "wsl2-cli",
	}, http.StatusCreated)
	token := tokenBody["token"].(string)
	if tokenBody["permissionProfile"] != "review-required" {
		t.Fatalf("default permission profile = %#v", tokenBody["permissionProfile"])
	}
	profiles := tokenBody["permissionProfiles"].([]any)
	if len(profiles) != 2 {
		t.Fatalf("permissionProfiles = %#v", profiles)
	}
	profileCommands := map[string]string{}
	for _, raw := range profiles {
		profile := raw.(map[string]any)
		id := profile["id"].(string)
		setup := profile["setupCommand"].(string)
		connect := profile["connectCommand"].(string)
		sudoConnect := profile["sudoConnectCommand"].(string)
		if strings.Contains(setup, "\n") {
			t.Fatalf("profile setup command should be one line: %q", setup)
		}
		if out, err := exec.Command("sh", "-n", "-c", setup).CombinedOutput(); err != nil {
			t.Fatalf("profile setup command shell syntax: %v\n%s\n%s", err, out, setup)
		}
		if strings.Contains(connect, "\n") {
			t.Fatalf("profile connect command should be one line: %q", connect)
		}
		if out, err := exec.Command("sh", "-n", "-c", connect).CombinedOutput(); err != nil {
			t.Fatalf("profile connect command shell syntax: %v\n%s\n%s", err, out, connect)
		}
		if !strings.Contains(sudoConnect, `sudo -H env PATH="$PATH" /root/.local/bin/codex-bridge link`) {
			t.Fatalf("profile sudo connect command missing sudo link: %s", sudoConnect)
		}
		if out, err := exec.Command("sh", "-n", "-c", sudoConnect).CombinedOutput(); err != nil {
			t.Fatalf("profile sudo connect command shell syntax: %v\n%s\n%s", err, out, sudoConnect)
		}
		profileCommands[id] = connect
	}
	if !strings.Contains(profileCommands["review-required"], "codex-bridge link") || !strings.Contains(profileCommands["review-required"], "--profile 'review-required'") {
		t.Fatalf("review-required command missing link profile: %s", profileCommands["review-required"])
	}
	if !strings.Contains(profileCommands["auto-execute"], "codex-bridge link") || !strings.Contains(profileCommands["auto-execute"], "--profile 'auto-execute'") {
		t.Fatalf("auto-execute command missing link profile: %s", profileCommands["auto-execute"])
	}
	if strings.Contains(profileCommands["review-required"], "--machine-id ") {
		t.Fatalf("new endpoint command should not pin a machine id: %s", profileCommands["review-required"])
	}
	commands := tokenBody["commands"].([]any)
	if len(commands) != 2 || !strings.Contains(commands[0].(string), "/install.sh") || !strings.Contains(commands[1].(string), "codex-bridge link") {
		t.Fatalf("commands = %#v", commands)
	}
	installCommand := commands[0].(string)
	if strings.Contains(installCommand, "\n") {
		t.Fatalf("install command should be one line: %q", installCommand)
	}
	if out, err := exec.Command("sh", "-n", "-c", installCommand).CombinedOutput(); err != nil {
		t.Fatalf("install command shell syntax: %v\n%s\n%s", err, out, installCommand)
	}
	connectCommand := commands[1].(string)
	if strings.Contains(connectCommand, "\n") {
		t.Fatalf("connect command should be one line: %q", connectCommand)
	}
	if out, err := exec.Command("sh", "-n", "-c", connectCommand).CombinedOutput(); err != nil {
		t.Fatalf("connect command shell syntax: %v\n%s\n%s", err, out, connectCommand)
	}
	setupCommand := tokenBody["setupCommand"].(string)
	if strings.Contains(setupCommand, "\n") {
		t.Fatalf("setup command should be one line: %q", setupCommand)
	}
	if out, err := exec.Command("sh", "-n", "-c", setupCommand).CombinedOutput(); err != nil {
		t.Fatalf("setup command shell syntax: %v\n%s\n%s", err, out, setupCommand)
	}
	if tokenBody["installCommand"] != installCommand || tokenBody["connectCommand"] != connectCommand {
		t.Fatalf("command mismatch: install=%#v connect=%#v commands=%#v", tokenBody["installCommand"], tokenBody["connectCommand"], commands)
	}
	sudoCommands := tokenBody["sudoCommands"].([]any)
	if len(sudoCommands) != 2 || !strings.Contains(sudoCommands[0].(string), "sudo -H sh") || !strings.Contains(sudoCommands[1].(string), `sudo -H env PATH="$PATH" /root/.local/bin/codex-bridge link`) {
		t.Fatalf("sudoCommands = %#v", sudoCommands)
	}
	for _, command := range []string{
		tokenBody["sudoSetupCommand"].(string),
		tokenBody["sudoInstallCommand"].(string),
		tokenBody["sudoConnectCommand"].(string),
	} {
		if strings.Contains(command, "\n") {
			t.Fatalf("sudo command should be one line: %q", command)
		}
		if out, err := exec.Command("sh", "-n", "-c", command).CombinedOutput(); err != nil {
			t.Fatalf("sudo command shell syntax: %v\n%s\n%s", err, out, command)
		}
	}
	for _, want := range []string{
		`~/.local/bin/codex-bridge link`,
		`--profile 'review-required'`,
		token,
	} {
		if !strings.Contains(connectCommand, want) {
			t.Fatalf("connect command missing %q: %s", want, connectCommand)
		}
	}
	autoBody := postJSON(t, workerClient, cfg.Bridge.HubURL+"/api/bridge-tokens", map[string]string{
		"label":             "auto-cli",
		"permissionProfile": "auto-execute",
	}, http.StatusCreated)
	if autoBody["permissionProfile"] != "auto-execute" {
		t.Fatalf("auto permission profile = %#v", autoBody["permissionProfile"])
	}
	autoConnect := autoBody["connectCommand"].(string)
	if !strings.Contains(autoConnect, "codex-bridge link") || !strings.Contains(autoConnect, "--profile 'auto-execute'") {
		t.Fatalf("auto connect command missing link profile: %s", autoConnect)
	}
	postJSON(t, workerClient, cfg.Bridge.HubURL+"/api/bridge-tokens", map[string]string{
		"permissionProfile": "surprise-me",
	}, http.StatusBadRequest)

	fakeBridge := dialFakeBridgeWithOptions(t, cfg.Bridge.HubURL, token, fakeBridgeOptions{WorkingDirs: []string{tmp}})
	defer fakeBridge.Close()
	waitAgents(t, workerClient, cfg.Bridge.HubURL)
	agentsBody := getJSON(t, workerClient, cfg.Bridge.HubURL+"/api/agents", http.StatusOK)
	agents := agentsBody["agents"].([]any)
	if len(agents) != 1 {
		t.Fatalf("worker agents = %#v", agents)
	}
	agent := agents[0].(map[string]any)
	if agent["userId"] != worker.ID || agent["online"] != true {
		t.Fatalf("bound agent = %#v, user = %#v", agent, worker)
	}
	repairBody := postJSON(t, workerClient, cfg.Bridge.HubURL+"/api/agents/"+url.PathEscape(agent["id"].(string))+"/repair-token", map[string]string{}, http.StatusCreated)
	if repairBody["permissionProfile"] != "review-required" || repairBody["agentId"] != agent["id"] || repairBody["machineId"] != agent["machineId"] {
		t.Fatalf("repair body = %#v, agent = %#v", repairBody, agent)
	}
	repairSetup := repairBody["setupCommand"].(string)
	if strings.Contains(repairSetup, "\n") {
		t.Fatalf("repair setup command should be one line: %q", repairSetup)
	}
	if out, err := exec.Command("sh", "-n", "-c", repairSetup).CombinedOutput(); err != nil {
		t.Fatalf("repair setup command shell syntax: %v\n%s\n%s", err, out, repairSetup)
	}
	repairConnect := repairBody["connectCommand"].(string)
	repairSudoConnect := repairBody["sudoConnectCommand"].(string)
	for _, want := range []string{
		`--cwd ` + shellSingleQuote(tmp),
		`--name ` + shellSingleQuote("fake-bridge"),
		`--machine-id`,
		agent["machineId"].(string),
		`--profile 'review-required'`,
	} {
		if !strings.Contains(repairConnect, want) {
			t.Fatalf("repair connect command missing %q: %s", want, repairConnect)
		}
	}
	if !strings.Contains(repairSudoConnect, `sudo -H env PATH="$PATH" /root/.local/bin/codex-bridge link`) || !strings.Contains(repairSudoConnect, `--machine-id`) {
		t.Fatalf("repair sudo connect command missing sudo link or machine id: %s", repairSudoConnect)
	}
	repairProfiles := repairBody["permissionProfiles"].([]any)
	if len(repairProfiles) != 2 {
		t.Fatalf("repair profiles = %#v", repairProfiles)
	}
	var sawPinnedAuto bool
	for _, raw := range repairProfiles {
		profile := raw.(map[string]any)
		connect := profile["connectCommand"].(string)
		if !strings.Contains(connect, `--machine-id`) || !strings.Contains(connect, agent["machineId"].(string)) {
			t.Fatalf("repair profile missing machine id: %#v", profile)
		}
		if profile["id"] == "auto-execute" && strings.Contains(connect, "--profile 'auto-execute'") {
			sawPinnedAuto = true
		}
	}
	if !sawPinnedAuto {
		t.Fatalf("repair profiles missing auto-execute fallback: %#v", repairProfiles)
	}
	deleteJSON(t, workerClient, cfg.Bridge.HubURL+"/api/agents/"+url.PathEscape(agent["id"].(string)), http.StatusOK)
	env := waitBridgeEnvelope(t, fakeBridge, protocol.TypeAgentShutdown, "")
	shutdown, err := protocol.Decode[protocol.AgentShutdownPayload](env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shutdown.Reason, "deleted") {
		t.Fatalf("shutdown payload = %#v", shutdown)
	}

	adminClient := httpClient(t)
	postJSON(t, adminClient, cfg.Bridge.HubURL+"/api/login", map[string]string{"username": "admin", "password": "secret"}, http.StatusOK)
	adminAgents := getJSON(t, adminClient, cfg.Bridge.HubURL+"/api/agents", http.StatusOK)["agents"].([]any)
	if len(adminAgents) != 0 {
		t.Fatalf("admin agents = %#v", adminAgents)
	}
	postJSON(t, adminClient, cfg.Bridge.HubURL+"/api/agents/"+url.PathEscape(agent["id"].(string))+"/repair-token", map[string]string{
		"permissionProfile": "auto-execute",
	}, http.StatusNotFound)
	other, err := st.UpsertUser(ctx, "other-user", "long-secret-456")
	if err != nil {
		t.Fatal(err)
	}
	_ = other
	otherClient := httpClient(t)
	postJSON(t, otherClient, cfg.Bridge.HubURL+"/api/login", map[string]string{"username": "other-user", "password": "long-secret-456"}, http.StatusOK)
	postJSON(t, otherClient, cfg.Bridge.HubURL+"/api/agents/"+url.PathEscape(agent["id"].(string))+"/repair-token", map[string]string{}, http.StatusNotFound)
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func TestOrchestrationContinueReusesRunAndSendsContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tmp := t.TempDir()
	port := freePort(t)
	cfg := config.Default()
	cfg.Gateway.Host = "127.0.0.1"
	cfg.Gateway.Port = port
	cfg.Gateway.ReadTimeout.Duration = 5 * time.Second
	cfg.Hub.DBPath = tmp + "/bridge.db"
	cfg.Auth.JWTSecret = "integration-test-secret-32-byte-minimum"
	cfg.Auth.AccessTokenTTL.Duration = time.Hour
	cfg.Bridge.HubURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	cfg.Bridge.Token = store.NewToken("enr")

	st, err := store.Open(cfg.Hub.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertUser(ctx, "admin", "secret"); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	if err := st.CreateEnrollToken(ctx, cfg.Bridge.Token, &expires); err != nil {
		t.Fatal(err)
	}

	srv := hub.NewServer(&cfg, st, hub.BuildInfo{Version: "test", BuildTime: "test"})
	go func() { _ = srv.Run(ctx) }()
	waitHTTP(t, cfg.Bridge.HubURL+"/health")
	fakeBridge := dialFakeBridge(t, cfg.Bridge.HubURL, cfg.Bridge.Token)
	defer fakeBridge.Close()

	client := httpClient(t)
	postJSON(t, client, cfg.Bridge.HubURL+"/api/login", map[string]string{"username": "admin", "password": "secret"}, http.StatusOK)
	waitAgents(t, client, cfg.Bridge.HubURL)

	body := postJSON(t, client, cfg.Bridge.HubURL+"/api/orchestrations", map[string]any{
		"mode":     "collaboration",
		"title":    "continuity",
		"prompt":   "first task",
		"maxTurns": 2,
	}, http.StatusCreated)
	run := body["run"].(map[string]any)
	runID := run["id"].(string)
	firstStart := waitBridgeEnvelope(t, fakeBridge, protocol.TypeOrchestrationStart, "")
	firstPayload, err := protocol.Decode[protocol.OrchestrationStartPayload](firstStart)
	if err != nil {
		t.Fatal(err)
	}
	if firstPayload.RunID != runID || firstPayload.Resume {
		t.Fatalf("first payload = %#v", firstPayload)
	}
	if err := fakeBridge.WriteJSON(protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", protocol.OrchestrationEventPayload{
		RunID:   runID,
		Kind:    "turn.delta",
		Role:    "implementer",
		CLI:     "claude",
		Content: "first task changed app.go",
	})); err != nil {
		t.Fatal(err)
	}
	if err := fakeBridge.WriteJSON(protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", protocol.OrchestrationEventPayload{
		RunID:  runID,
		Kind:   "run.end",
		Status: store.OrchestrationCompleted,
	})); err != nil {
		t.Fatal(err)
	}
	waitOrchestrationStatus(t, client, cfg.Bridge.HubURL, runID, store.OrchestrationCompleted)

	body = postJSON(t, client, cfg.Bridge.HubURL+"/api/orchestrations/"+url.PathEscape(runID)+"/prompts", map[string]any{
		"prompt":   "second task",
		"maxTurns": 2,
	}, http.StatusOK)
	continuedRun := body["run"].(map[string]any)
	if continuedRun["id"] != runID || continuedRun["status"] != store.OrchestrationRunning {
		t.Fatalf("continued run = %#v", continuedRun)
	}
	secondStart := waitBridgeEnvelope(t, fakeBridge, protocol.TypeOrchestrationStart, "")
	secondPayload, err := protocol.Decode[protocol.OrchestrationStartPayload](secondStart)
	if err != nil {
		t.Fatal(err)
	}
	if secondPayload.RunID != runID || !secondPayload.Resume || secondPayload.Prompt != "second task" {
		t.Fatalf("second payload = %#v", secondPayload)
	}
	if !strings.Contains(secondPayload.Context, "first task") || !strings.Contains(secondPayload.Context, "first task changed app.go") {
		t.Fatalf("second context = %q", secondPayload.Context)
	}
}

func TestTerminationFrameworkOrchestrationFullSnapshotFlow(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	codexPath := filepath.Join(binDir, "codex")
	claudePath := filepath.Join(binDir, "claude")
	if err := os.WriteFile(codexPath, []byte(fakeTerminationCodexScript()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudePath, []byte(fakeTerminationClaudeScript()), 0o755); err != nil {
		t.Fatal(err)
	}

	port := freePort(t)
	cfg := config.Default()
	cfg.Gateway.Host = "127.0.0.1"
	cfg.Gateway.Port = port
	cfg.Gateway.ReadTimeout.Duration = 5 * time.Second
	cfg.Gateway.WriteTimeout.Duration = 0
	cfg.Hub.DBPath = tmp + "/bridge.db"
	cfg.Hub.HeartbeatInterval.Duration = 200 * time.Millisecond
	cfg.Auth.JWTSecret = "integration-test-secret-32-byte-minimum"
	cfg.Auth.AccessTokenTTL.Duration = time.Hour
	cfg.Bridge.HubURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	cfg.Bridge.Token = store.NewToken("enr")
	cfg.Bridge.Name = "termination-framework-bridge"
	cfg.Bridge.MachineIDFile = tmp + "/machine_id"
	cfg.Bridge.CWD = tmp
	cfg.Bridge.Runner = "echo"
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.ClaudePath = claudePath
	cfg.Bridge.Sandbox = "danger-full-access"
	cfg.Bridge.ApprovalPolicy = "never"
	cfg.Bridge.ReconnectMin.Duration = 50 * time.Millisecond
	cfg.Bridge.ReconnectMax.Duration = 100 * time.Millisecond
	cfg.Bridge.HeartbeatInterval.Duration = 200 * time.Millisecond

	st, err := store.Open(cfg.Hub.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertUser(ctx, "admin", "secret"); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	if err := st.CreateEnrollToken(ctx, cfg.Bridge.Token, &expires); err != nil {
		t.Fatal(err)
	}

	srv := hub.NewServer(&cfg, st, hub.BuildInfo{Version: "test", BuildTime: "test"})
	go func() { _ = srv.Run(ctx) }()
	waitHTTP(t, cfg.Bridge.HubURL+"/health")
	go func() { _ = bridge.NewClient(&cfg, "test").Run(ctx) }()

	client := httpClient(t)
	postJSON(t, client, cfg.Bridge.HubURL+"/api/login", map[string]string{"username": "admin", "password": "secret"}, http.StatusOK)
	waitAgents(t, client, cfg.Bridge.HubURL)

	fileNames := []string{"Model.thy", "Termination.thy", "ROOT"}
	body := postJSON(t, client, cfg.Bridge.HubURL+"/api/orchestrations", map[string]any{
		"mode":     "collaboration",
		"title":    "termination_framework",
		"prompt":   terminationFrameworkPrompt(),
		"cwd":      tmp,
		"maxTurns": 2,
		"files": []map[string]any{
			{"name": "Model.thy", "mimeType": "text/plain", "size": 17, "data": "dGhlb3J5IE1vZGVsCg=="},
			{"name": "Termination.thy", "mimeType": "text/plain", "size": 23, "data": "dGhlb3J5IFRlcm1pbmF0aW9uCg=="},
			{"name": "ROOT", "mimeType": "text/plain", "size": 28, "data": "c2Vzc2lvbiB0ZXJtaW5hdGlvbl9mcmFtZXdvcmsK"},
		},
	}, http.StatusCreated)
	run := body["run"].(map[string]any)
	runID := run["id"].(string)
	assertRunHasFiles(t, run, fileNames)

	waitOrchestrationStatus(t, client, cfg.Bridge.HubURL, runID, store.OrchestrationCompleted)
	runBody := getJSON(t, client, cfg.Bridge.HubURL+"/api/orchestrations/"+url.PathEscape(runID), http.StatusOK)
	assertRunHasFiles(t, runBody["run"].(map[string]any), fileNames)
	eventsBody := getJSON(t, client, cfg.Bridge.HubURL+"/api/orchestrations/"+url.PathEscape(runID)+"/events", http.StatusOK)
	events := eventsBody["events"].([]any)
	assertTerminationConclusionAfterCommand(t, events)

	body = postJSON(t, client, cfg.Bridge.HubURL+"/api/orchestrations/"+url.PathEscape(runID)+"/prompts", map[string]any{
		"prompt":   "继续检查剩余 sorry 占位，并再次确认最终对话结尾。",
		"maxTurns": 2,
	}, http.StatusOK)
	assertRunHasFiles(t, body["run"].(map[string]any), fileNames)
	waitOrchestrationStatus(t, client, cfg.Bridge.HubURL, runID, store.OrchestrationCompleted)
	runBody = getJSON(t, client, cfg.Bridge.HubURL+"/api/orchestrations/"+url.PathEscape(runID), http.StatusOK)
	assertRunHasFiles(t, runBody["run"].(map[string]any), fileNames)
	eventsBody = getJSON(t, client, cfg.Bridge.HubURL+"/api/orchestrations/"+url.PathEscape(runID)+"/events", http.StatusOK)
	events = eventsBody["events"].([]any)
	assertTerminationConclusionAfterCommand(t, events)

	shareBody := postJSON(t, client, cfg.Bridge.HubURL+"/api/orchestrations/"+url.PathEscape(runID)+"/share", nil, http.StatusCreated)
	share := shareBody["share"].(map[string]any)
	publicBody := getJSON(t, client, cfg.Bridge.HubURL+"/api/public/shares/"+url.PathEscape(share["id"].(string)), http.StatusOK)
	assertRunHasFiles(t, publicBody["run"].(map[string]any), fileNames)
	assertTerminationConclusionAfterCommand(t, publicBody["events"].([]any))
	if raw, err := json.Marshal(publicBody); err != nil {
		t.Fatal(err)
	} else {
		for _, forbidden := range []string{"agentId", "userId", "remoteThreadId", "file-body-secret"} {
			if strings.Contains(string(raw), forbidden) {
				t.Fatalf("public snapshot leaked %q: %s", forbidden, raw)
			}
		}
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	for attempts := 0; attempts < 100; attempts++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		if err := ln.Close(); err != nil {
			t.Fatal(err)
		}

		freePortMu.Lock()
		_, used := freePortUsed[port]
		if !used {
			freePortUsed[port] = struct{}{}
		}
		freePortMu.Unlock()
		if !used {
			return port
		}
	}
	t.Fatal("could not allocate a unique test port")
	return 0
}

func waitHTTP(t *testing.T, target string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		res, err := http.Get(target)
		if err == nil {
			_ = res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("service did not become healthy: %s", target)
}

func httpClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar, Timeout: 5 * time.Second}
}

func waitAgents(t *testing.T, client *http.Client, baseURL string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		body := getJSON(t, client, baseURL+"/api/agents", http.StatusOK)
		if agents, ok := body["agents"].([]any); ok && len(agents) > 0 {
			if agent, ok := agents[0].(map[string]any); ok && agent["online"] == true {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("bridge agent did not come online")
}

func waitRegisteredAgent(t *testing.T, st *store.Store, name string) store.Agent {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		agents, err := st.ListAgents(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, agent := range agents {
			if agent.Name == name {
				return agent
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for registered agent %q", name)
	return store.Agent{}
}

func dialBrowserWS(t *testing.T, client *http.Client, baseURL, sid string) *websocket.Conn {
	t.Helper()
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	scheme := "ws"
	if parsed.Scheme == "https" {
		scheme = "wss"
	}
	wsURL := fmt.Sprintf("%s://%s/ws/chat?sid=%s", scheme, parsed.Host, url.QueryEscape(sid))
	header := http.Header{}
	for _, cookie := range client.Jar.Cookies(parsed) {
		header.Add("Cookie", cookie.String())
	}
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func dialOrchestrationWS(t *testing.T, client *http.Client, baseURL, runID string) *websocket.Conn {
	t.Helper()
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	scheme := "ws"
	if parsed.Scheme == "https" {
		scheme = "wss"
	}
	wsURL := fmt.Sprintf("%s://%s/ws/orchestrations?runId=%s", scheme, parsed.Host, url.QueryEscape(runID))
	header := http.Header{}
	for _, cookie := range client.Jar.Cookies(parsed) {
		header.Add("Cookie", cookie.String())
	}
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func createSession(t *testing.T, client *http.Client, baseURL, title string) string {
	t.Helper()
	body := postJSON(t, client, baseURL+"/api/sessions", map[string]string{"title": title}, http.StatusCreated)
	return body["session"].(map[string]any)["id"].(string)
}

type fakeBridgeConn struct {
	ws   *websocket.Conn
	envc chan protocol.Envelope
	errc chan error
}

func (c *fakeBridgeConn) WriteJSON(v any) error {
	return c.ws.WriteJSON(v)
}

func (c *fakeBridgeConn) Close() error {
	return c.ws.Close()
}

type fakeBridgeOptions struct {
	WorkingDirs []string
}

func dialFakeBridge(t *testing.T, baseURL, token string) *fakeBridgeConn {
	return dialFakeBridgeWithOptions(t, baseURL, token, fakeBridgeOptions{})
}

func dialFakeBridgeWithOptions(t *testing.T, baseURL, token string, opts fakeBridgeOptions) *fakeBridgeConn {
	t.Helper()
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	scheme := "ws"
	if parsed.Scheme == "https" {
		scheme = "wss"
	}
	wsURL := fmt.Sprintf("%s://%s/api/agents/connect?token=%s", scheme, parsed.Host, url.QueryEscape(token))
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	reg := protocol.RegisterPayload{
		Name:        "fake-bridge",
		MachineID:   "fake-machine-" + store.NewID("test"),
		Hostname:    "test-host",
		Version:     "test",
		WorkingDirs: opts.WorkingDirs,
		Capabilities: &protocol.BridgeCapabilities{
			Sandbox:        "danger-full-access",
			ApprovalPolicy: "never",
			Metadata:       map[string]string{"approvalMode": "auto-execute"},
			Orchestration: map[string]protocol.BridgeCLICapability{
				"claude": {Available: true},
				"codex":  {Available: true},
			},
		},
	}
	if err := ws.WriteJSON(protocol.MustEnvelope(protocol.TypeRegister, "", reg)); err != nil {
		t.Fatal(err)
	}
	var ack protocol.Envelope
	if err := ws.ReadJSON(&ack); err != nil {
		t.Fatal(err)
	}
	if ack.Type != protocol.TypeRegistered {
		t.Fatalf("bridge register got %q, want %q", ack.Type, protocol.TypeRegistered)
	}

	conn := &fakeBridgeConn{
		ws:   ws,
		envc: make(chan protocol.Envelope, 16),
		errc: make(chan error, 1),
	}
	go func() {
		for {
			var env protocol.Envelope
			if err := ws.ReadJSON(&env); err != nil {
				conn.errc <- err
				return
			}
			conn.envc <- env
		}
	}()
	return conn
}

func waitBridgeEnvelope(t *testing.T, conn *fakeBridgeConn, typ, sid string) protocol.Envelope {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case env := <-conn.envc:
			if env.Type == protocol.TypeHeartbeat {
				continue
			}
			if env.Type == typ && env.Sid == sid {
				return env
			}
			t.Fatalf("unexpected bridge envelope: type=%q sid=%q, want type=%q sid=%q", env.Type, env.Sid, typ, sid)
		case err := <-conn.errc:
			t.Fatal(err)
		case <-deadline:
			t.Fatalf("timed out waiting for bridge envelope type=%q sid=%q", typ, sid)
		}
	}
}

func waitBrowserEnvelope(t *testing.T, ws *websocket.Conn, typ, sid string) protocol.Envelope {
	t.Helper()
	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	defer ws.SetReadDeadline(time.Time{})
	for {
		var env protocol.Envelope
		if err := ws.ReadJSON(&env); err != nil {
			t.Fatal(err)
		}
		if env.Sid != "" && env.Sid != sid {
			t.Fatalf("sid leak: got %q want %q", env.Sid, sid)
		}
		if env.Type == protocol.TypeHeartbeat {
			continue
		}
		if env.Type == typ {
			return env
		}
	}
}

func assertNoBridgeCloseSession(t *testing.T, conn *fakeBridgeConn, sid string, d time.Duration) {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case env := <-conn.envc:
			if env.Type == protocol.TypeHeartbeat {
				continue
			}
			if env.Type == protocol.TypeCloseSession && env.Sid == sid {
				t.Fatalf("browser close sent close_session for sid %s", sid)
			}
			t.Fatalf("unexpected bridge envelope after browser close: type=%q sid=%q", env.Type, env.Sid)
		case err := <-conn.errc:
			t.Fatal(err)
		case <-deadline:
			return
		}
	}
}

func waitMessages(t *testing.T, client *http.Client, baseURL, sid string, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		messagesBody := getJSON(t, client, baseURL+"/api/sessions/"+url.PathEscape(sid)+"/messages", http.StatusOK)
		messages := messagesBody["messages"].([]any)
		if len(messages) >= want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d persisted messages", want)
}

func waitRunStatus(t *testing.T, client *http.Client, baseURL, sid, runID, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		body := getJSON(t, client, baseURL+"/api/sessions/"+url.PathEscape(sid)+"/runs", http.StatusOK)
		for _, raw := range body["runs"].([]any) {
			run := raw.(map[string]any)
			if run["id"] == runID && run["status"] == want {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for run %s status %s", runID, want)
}

func waitOrchestrationStatus(t *testing.T, client *http.Client, baseURL, runID, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		body := getJSON(t, client, baseURL+"/api/orchestrations/"+url.PathEscape(runID), http.StatusOK)
		run := body["run"].(map[string]any)
		if run["status"] == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for orchestration run %s status %s", runID, want)
}

func waitOrchestrationCommandEvents(t *testing.T, ws *websocket.Conn, runID string, commands []string) map[string]bool {
	t.Helper()
	want := make(map[string]bool, len(commands))
	for _, command := range commands {
		want[command] = false
	}
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer ws.SetReadDeadline(time.Time{})
	for {
		var env protocol.Envelope
		if err := ws.ReadJSON(&env); err != nil {
			t.Fatal(err)
		}
		if env.Type == protocol.TypeHeartbeat || env.Type == protocol.TypeStatus {
			continue
		}
		if env.Type != protocol.TypeOrchestrationEvent {
			continue
		}
		event, err := protocol.Decode[protocol.OrchestrationEventPayload](env)
		if err != nil {
			t.Fatal(err)
		}
		if event.RunID != runID || !strings.HasPrefix(event.Kind, "command.") {
			continue
		}
		command, _ := event.Data["command"].(string)
		if _, ok := want[command]; ok {
			want[command] = true
		}
		all := true
		for _, saw := range want {
			all = all && saw
		}
		if all {
			return want
		}
	}
}

type orchestrationWebObservation struct {
	approvals             int
	acceptedApprovals     int
	sawCodexCommand       bool
	sawRemediationCommand bool
	sawUnresolvedRisk     bool
}

type ccbTerminalApprovalObservation struct {
	approvals         int
	acceptedApprovals int
	sawTrustPrompt    bool
	sawForwardedInput bool
	sawCompleted      bool
}

func observeCCBTerminalApprovalAndApprove(t *testing.T, ws *websocket.Conn, runID string) ccbTerminalApprovalObservation {
	t.Helper()
	var obs ccbTerminalApprovalObservation
	_ = ws.SetReadDeadline(time.Now().Add(8 * time.Second))
	defer ws.SetReadDeadline(time.Time{})
	for {
		var env protocol.Envelope
		if err := ws.ReadJSON(&env); err != nil {
			t.Fatal(err)
		}
		switch env.Type {
		case protocol.TypeHeartbeat, protocol.TypeStatus:
			continue
		case protocol.TypeApprovalRequest:
			req, err := protocol.Decode[protocol.ApprovalRequestPayload](env)
			if err != nil {
				t.Fatal(err)
			}
			if req.RunID != runID {
				t.Fatalf("approval for wrong run: %#v", req)
			}
			if req.Kind != "ccb.terminal_prompt" || !strings.Contains(req.Reason, "Do you trust the contents of this directory") {
				t.Fatalf("unexpected CCB approval request: %#v", req)
			}
			obs.approvals++
			obs.sawTrustPrompt = true
			if err := ws.WriteJSON(protocol.MustEnvelope(protocol.TypeApprovalResponse, "", protocol.ApprovalResponsePayload{RequestID: req.RequestID, Decision: "accept"})); err != nil {
				t.Fatal(err)
			}
			obs.acceptedApprovals++
		case protocol.TypeOrchestrationEvent:
			event, err := protocol.Decode[protocol.OrchestrationEventPayload](env)
			if err != nil {
				t.Fatal(err)
			}
			if event.RunID != runID {
				t.Fatalf("event for wrong run: %#v", event)
			}
			if strings.Contains(event.Content, "forwarded press Enter to trust and continue") {
				obs.sawForwardedInput = true
			}
			if strings.Contains(event.Content, "completed after browser approval") {
				obs.sawCompleted = true
			}
			if event.Kind == "run.end" || event.Kind == "run.error" || event.Kind == "run.cancelled" {
				return obs
			}
		}
	}
}

func observeOrchestrationAndApprove(t *testing.T, ws *websocket.Conn, runID string) orchestrationWebObservation {
	t.Helper()
	var obs orchestrationWebObservation
	_ = ws.SetReadDeadline(time.Now().Add(8 * time.Second))
	defer ws.SetReadDeadline(time.Time{})
	for {
		var env protocol.Envelope
		if err := ws.ReadJSON(&env); err != nil {
			t.Fatal(err)
		}
		switch env.Type {
		case protocol.TypeHeartbeat, protocol.TypeStatus:
			continue
		case protocol.TypeApprovalRequest:
			req, err := protocol.Decode[protocol.ApprovalRequestPayload](env)
			if err != nil {
				t.Fatal(err)
			}
			if req.RunID != runID {
				t.Fatalf("approval for wrong run: %#v", req)
			}
			obs.approvals++
			if err := ws.WriteJSON(protocol.MustEnvelope(protocol.TypeApprovalResponse, "", protocol.ApprovalResponsePayload{RequestID: req.RequestID, Decision: "accept"})); err != nil {
				t.Fatal(err)
			}
			obs.acceptedApprovals++
		case protocol.TypeOrchestrationEvent:
			event, err := protocol.Decode[protocol.OrchestrationEventPayload](env)
			if err != nil {
				t.Fatal(err)
			}
			if event.RunID != runID {
				t.Fatalf("event for wrong run: %#v", event)
			}
			if strings.HasPrefix(event.Kind, "command.") && event.Data != nil {
				command, _ := event.Data["command"].(string)
				if command == "isabelle build -D /root/Isabelle" {
					obs.sawCodexCommand = true
				}
				if command == "mkdir -p Isabelle && write Termination.thy" {
					obs.sawRemediationCommand = true
				}
			}
			if strings.Contains(event.Content, "主定理 sorry 仍未消除") {
				obs.sawUnresolvedRisk = true
			}
			if event.Kind == "run.end" || event.Kind == "run.error" || event.Kind == "run.cancelled" {
				return obs
			}
		}
	}
}

func eventsContainCommand(events []any, want string) bool {
	for _, raw := range events {
		event, _ := raw.(map[string]any)
		data, _ := event["data"].(map[string]any)
		if data == nil {
			continue
		}
		if command, _ := data["command"].(string); command == want {
			return true
		}
	}
	return false
}

func eventsContainContent(events []any, want string) bool {
	for _, raw := range events {
		event, _ := raw.(map[string]any)
		if strings.Contains(fmt.Sprint(event["content"]), want) || strings.Contains(fmt.Sprint(event["error"]), want) {
			return true
		}
		data, _ := event["data"].(map[string]any)
		if data != nil && strings.Contains(fmt.Sprint(data["output"]), want) {
			return true
		}
	}
	return false
}

func assertRunHasFiles(t *testing.T, run map[string]any, names []string) {
	t.Helper()
	rawFiles, ok := run["files"].([]any)
	if !ok {
		t.Fatalf("run has no files array: %#v", run)
	}
	seen := make(map[string]bool, len(rawFiles))
	for _, raw := range rawFiles {
		file, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("file entry = %#v", raw)
		}
		name, _ := file["name"].(string)
		seen[name] = true
		if _, leaked := file["data"]; leaked {
			t.Fatalf("file metadata leaked body: %#v", file)
		}
	}
	for _, name := range names {
		if !seen[name] {
			t.Fatalf("run files missing %q: %#v", name, rawFiles)
		}
	}
}

func assertTerminationConclusionAfterCommand(t *testing.T, events []any) {
	t.Helper()
	lastCommandIndex := -1
	lastConclusionIndex := -1
	var lastConclusion string
	for i, raw := range events {
		event, _ := raw.(map[string]any)
		kind, _ := event["kind"].(string)
		if strings.HasPrefix(kind, "command.") {
			lastCommandIndex = i
		}
		if kind == "turn.end" {
			content := fmt.Sprint(event["content"])
			if strings.Contains(content, "最终结论") || strings.Contains(content, "本轮结论") {
				lastConclusionIndex = i
				lastConclusion = content
			}
		}
	}
	if lastCommandIndex < 0 {
		t.Fatalf("events missing command card: %#v", events)
	}
	if lastConclusionIndex < 0 {
		t.Fatalf("events missing readable turn.end conclusion after command: %#v", events)
	}
	if lastConclusionIndex < lastCommandIndex {
		t.Fatalf("last readable conclusion appears before last command: command=%d conclusion=%d content=%q events=%#v", lastCommandIndex, lastConclusionIndex, lastConclusion, events)
	}
	for _, want := range []string{"isabelle build -c -D /home/zy/os/termination_framework", "sorry"} {
		if !strings.Contains(lastConclusion, want) {
			t.Fatalf("last conclusion missing %q: %q", want, lastConclusion)
		}
	}
}

func fakeCodexScript() string {
	return `#!/bin/sh
cat >/dev/null
printf '%s\n' '{"type":"thread.started","thread_id":"fake-thread"}'
printf '%s\n' '{"type":"item.agent_message.delta","delta":"fake codex reviewed the previous turn."}'
printf '%s\n' '{"type":"turn.completed","usage":{"output_tokens":6}}'
`
}

func fakeClaudeScript() string {
	return `#!/bin/sh
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"fake claude implemented the first turn."}]}}'
printf '%s\n' '{"type":"result","result":"fake claude implemented the first turn."}'
`
}

func fakeTerminationCodexScript() string {
	return `#!/bin/sh
cat >/dev/null
printf '%s\n' 'theory Termination_Generated imports Main begin lemma generated_framework: True sorry end' > Termination_Generated.thy
printf '%s\n' '{"type":"thread.started","thread_id":"termination-thread"}'
printf '%s\n' '{"type":"item.started","item":{"id":"cmd_1","type":"command_execution","command":"isabelle build -c -D /home/zy/os/termination_framework","status":"running"}}'
printf '%s\n' '{"type":"item.completed","item":{"id":"cmd_1","type":"command_execution","command":"isabelle build -c -D /home/zy/os/termination_framework","status":"completed","exit_code":0,"aggregated_output":"Build completed successfully.\n"}}'
printf '%s\n' '{"type":"item.agent_message.delta","delta":"Msg: to=user; intent=final; need=none\nHandoff: status=resolved; changed=Termination_Generated.thy; verified=isabelle build -c -D /home/zy/os/termination_framework; next=prove remaining sorry placeholders; risks=proof framework still contains sorry placeholders"}'
printf '%s\n' '{"type":"turn.completed","usage":{"output_tokens":32}}'
`
}

func fakeTerminationClaudeScript() string {
	return `#!/bin/sh
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"我会先整理 Termination.thy 的可编译证明框架，并让下一轮独立验证。"}]}}'
printf '%s\n' '{"type":"result","result":"我会先整理 Termination.thy 的可编译证明框架，并让下一轮独立验证。"}'
`
}

func fakeUnresolvedSorryClaudeScript() string {
	return `#!/bin/sh
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"结论：我只确认了当前项目状态，主定理 sorry 尚未消除，下一轮必须直接处理该验收标准。\n\nMsg: to=reviewer; intent=review; need=verify main theorem sorry removal\nHandoff: status=needs_next; changed=none; verified=none; next=remove main theorem sorry; risks=主定理 sorry 仍未消除"}]}}'
printf '%s\n' '{"type":"result","result":"结论：我只确认了当前项目状态，主定理 sorry 尚未消除，下一轮必须直接处理该验收标准。\n\nMsg: to=reviewer; intent=review; need=verify main theorem sorry removal\nHandoff: status=needs_next; changed=none; verified=none; next=remove main theorem sorry; risks=主定理 sorry 仍未消除"}'
`
}

func fakeCCBTerminalApprovalScript() string {
	return `#!/usr/bin/env python3
import json
import os
import socket
import subprocess
import sys
import time

root = os.getcwd()
runtime_root = os.path.join(root, ".ccb")
agent_root = os.path.join(runtime_root, "agents", "codex")
sock_path = os.path.join(runtime_root, "ccbd", "ccbd.sock")
job_id = "job_terminal"
cursor = 0
completed = False

def write_runtime():
    os.makedirs(agent_root, exist_ok=True)
    with open(os.path.join(agent_root, "runtime.json"), "w", encoding="utf-8") as f:
        json.dump({
            "agent_name": "codex",
            "pane_id": "%7",
            "state": "running",
            "health": "waiting",
            "tmux_socket_path": os.path.join(root, "tmux.sock"),
        }, f)
    os.makedirs(os.path.join(runtime_root, "agents", "claude"), exist_ok=True)
    with open(os.path.join(runtime_root, "agents", "claude", "runtime.json"), "w", encoding="utf-8") as f:
        json.dump({"agent_name": "claude"}, f)
    with open(os.path.join(root, "pane.txt"), "w", encoding="utf-8") as f:
        f.write("\n".join([
            "> You are in /home/zy/study",
            "Do you trust the contents of this directory? Working with untrusted contents comes with higher risk of prompt injection.",
            "› 1. Yes, continue",
            "2. No, quit",
            "Press enter to continue",
            "",
        ]))

def handle(conn):
    global cursor, completed
    line = conn.recv(1048576)
    if not line:
        return
    req = json.loads(line.decode("utf-8"))
    op = req.get("op")
    if op == "watch":
        events = []
        if cursor == 0:
            events.append({"event_id": "evt_1", "job_id": job_id, "agent_name": "ccb", "target_name": "codex", "type": "job_started", "timestamp": "2026-05-26T00:00:00Z", "payload": {"status": "started"}})
            cursor = 1
        if os.path.exists(os.path.join(root, "send_keys.log")):
            completed = True
        payload = {
            "api_version": 2,
            "ok": True,
            "job_id": job_id,
            "agent_name": "ccb",
            "target_name": "codex",
            "cursor": cursor,
            "terminal": completed,
            "status": "completed" if completed else "running",
            "reply": "completed after browser approval" if completed else "",
            "events": events,
        }
    elif op == "trace":
        payload = {
            "api_version": 2,
            "ok": True,
            "reply": "completed after browser approval",
            "replies": [{"reply_id": "rep_1", "agent_name": "codex", "reply": "completed after browser approval"}],
        }
    else:
        payload = {"api_version": 2, "ok": False, "error": "unknown op"}
    conn.sendall((json.dumps(payload) + "\n").encode("utf-8"))

def serve():
    os.makedirs(os.path.dirname(sock_path), exist_ok=True)
    try:
        os.unlink(sock_path)
    except FileNotFoundError:
        pass
    srv = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    srv.bind(sock_path)
    srv.listen(20)
    while True:
        conn, _ = srv.accept()
        with conn:
            handle(conn)

write_runtime()

if sys.argv[1:2] == ["serve"]:
    serve()

if len(sys.argv) == 1:
    proc = subprocess.Popen([sys.executable, os.path.abspath(sys.argv[0]), "serve"], cwd=root, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    with open(os.path.join(root, "server.pid"), "w", encoding="utf-8") as f:
        f.write(str(proc.pid))
    for _ in range(50):
        try:
            probe = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
            probe.connect(sock_path)
            probe.close()
            break
        except OSError:
            pass
        time.sleep(0.02)
    print("start_status: ok")
    print("socket_path: " + sock_path)
    print("agents: codex, claude")
    time.sleep(0.2)
    sys.exit(0)

if sys.argv[1:4] == ["ask", "--compact", "codex"]:
    sys.stdin.read()
    print("accepted job=" + job_id + " target=codex")
    print("[CCB_ASYNC_SUBMITTED job=" + job_id + " target=codex]")
    sys.exit(0)

if sys.argv[1:2] == ["trace"]:
    if os.path.exists(os.path.join(root, "send_keys.log")):
        print("reply: id=rep_1 message=msg_1 attempt=att_1 agent=codex terminal=completed size=31 notice=false kind=None reason=task_complete finished=2026-05-26T00:00:00Z preview=completed after browser approval")
    sys.exit(0)

if sys.argv[1:3] == ["pend", "--watch"]:
    if os.path.exists(os.path.join(root, "send_keys.log")):
        print("status: completed")
        print("reply: completed after browser approval")
    else:
        print("status:")
        print("reply:")
    sys.exit(0)

sys.exit(0)
`
}

func fakeTmuxTerminalApprovalScript() string {
	return `#!/bin/sh
root="${2%/tmux.sock}"
if [ "$1" = "-S" ] && [ "$3" = "capture-pane" ]; then
  cat "$root/pane.txt"
  exit 0
fi
if [ "$1" = "-S" ] && [ "$3" = "send-keys" ]; then
  printf '%s\n' "$*" >> "$root/send_keys.log"
  printf '%s\n' 'Codex continued after browser approval.' >> "$root/pane.txt"
  exit 0
fi
exit 1
`
}

func fakeApprovalCodexAppServerScript() string {
	return `#!/usr/bin/env python3
import json
import os
import sys

if len(sys.argv) < 2 or sys.argv[1] != "app-server":
    print("unexpected command: " + " ".join(sys.argv[1:]), file=sys.stderr)
    sys.exit(1)

def emit(obj):
    print(json.dumps(obj, separators=(",", ":"), ensure_ascii=False), flush=True)

is_remediation = False

for line in sys.stdin:
    msg = json.loads(line)
    method = msg.get("method")
    if method == "initialize":
        emit({"id": msg["id"], "result": {"userAgent": "fake", "codexHome": "/tmp", "platformFamily": "unix", "platformOs": "linux"}})
    elif method == "thread/start":
        emit({"id": msg["id"], "result": {"thread": {"id": "thr_web_approval"}}})
    elif method == "turn/start":
        emit({"id": msg["id"], "result": {"turn": {"id": "turn_web_approval", "items": [], "itemsView": "notLoaded", "status": "inProgress", "error": None, "startedAt": None, "completedAt": None, "durationMs": None}}})
        input_text = " ".join(part.get("text", "") for part in msg.get("params", {}).get("input", []) if isinstance(part, dict))
        is_remediation = "remediation implementer" in input_text or "workspace file change" in input_text or "workspace-change remediation" in input_text
        if is_remediation:
            emit({"jsonrpc": "2.0", "id": 100, "method": "item/commandExecution/requestApproval", "params": {"threadId": "thr_web_approval", "turnId": "turn_web_approval", "itemId": "cmd_write", "command": "mkdir -p Isabelle && write Termination.thy", "cwd": os.getcwd(), "reason": "make a real workspace file change for the latest user request"}})
        else:
            emit({"jsonrpc": "2.0", "id": 99, "method": "item/commandExecution/requestApproval", "params": {"threadId": "thr_web_approval", "turnId": "turn_web_approval", "itemId": "cmd_build", "command": "isabelle build -D /root/Isabelle", "cwd": "/root", "reason": "validate whether the main theorem sorry was removed"}})
    elif msg.get("id") == 99:
        emit({"method": "item/started", "params": {"item": {"id": "cmd_build", "type": "commandExecution", "command": "isabelle build -D /root/Isabelle", "status": "running"}}})
        emit({"method": "item/completed", "params": {"item": {"id": "cmd_build", "type": "commandExecution", "command": "isabelle build -D /root/Isabelle", "status": "completed", "exitCode": 0, "aggregatedOutput": "Build completed, but Termination.thy still contains sorry placeholders\n"}}})
        text = "结论：网页端审批已通过并完成复查，但主定理 sorry 仍未消除，不能把当前状态视为完成。\n\nMsg: to=user; intent=final; need=none\nHandoff: status=needs_next; changed=none; verified=isabelle build -D /root/Isabelle; next=remove main theorem sorry; risks=主定理 sorry 仍未消除"
        emit({"method": "item/agentMessage/delta", "params": {"threadId": "thr_web_approval", "turnId": "turn_web_approval", "itemId": "msg_1", "delta": text}})
        emit({"method": "turn/completed", "params": {"threadId": "thr_web_approval", "turn": {"id": "turn_web_approval", "items": [], "itemsView": "notLoaded", "status": "completed", "error": None, "startedAt": 1, "completedAt": 2, "durationMs": 1}}})
        sys.exit(0)
    elif msg.get("id") == 100:
        os.makedirs("Isabelle", exist_ok=True)
        with open(os.path.join("Isabelle", "Termination.thy"), "w", encoding="utf-8") as f:
            f.write("theory Termination\nimports Main\nbegin\n\nlemma main_theorem: True\n  by simp\n\nend\n")
        emit({"method": "item/started", "params": {"item": {"id": "cmd_write", "type": "commandExecution", "command": "mkdir -p Isabelle && write Termination.thy", "status": "running"}}})
        emit({"method": "item/completed", "params": {"item": {"id": "cmd_write", "type": "commandExecution", "command": "mkdir -p Isabelle && write Termination.thy", "status": "completed", "exitCode": 0, "aggregatedOutput": "wrote Isabelle/Termination.thy without sorry\n"}}})
        text = "最终结论：补救轮已写入 Isabelle/Termination.thy，并把示例主定理改为无 sorry 的证明。\n\nMsg: to=user; intent=final; need=none\nHandoff: status=resolved; changed=Isabelle/Termination.thy; verified=write file; next=none; risks=none"
        emit({"method": "item/agentMessage/delta", "params": {"threadId": "thr_web_approval", "turnId": "turn_web_approval", "itemId": "msg_2", "delta": text}})
        emit({"method": "turn/completed", "params": {"threadId": "thr_web_approval", "turn": {"id": "turn_web_approval", "items": [], "itemsView": "notLoaded", "status": "completed", "error": None, "startedAt": 1, "completedAt": 2, "durationMs": 1}}})
        sys.exit(0)
`
}

func unresolvedSorryInitialPrompt() string {
	return `这个目前的代码项目状态只能说通过编译了。我是要求先把主定理的 sorry 消除，这一步没做出来，就没有实质上的进展，所以你们协作要完成这个方面，放在 /root 下新建一个文件夹 Isabelle 下。遇到网页审批请求直接批准。`
}

func coqSmokeTaskPrompt() string {
	return `把这三个做成coq的证明项目写到工作路径下的一个新建文件夹中，并补全缺失的证明，不能用某些占位符占住，应该补全`
}

func terminationFrameworkPrompt() string {
	return `已上传了三个文件。Model.thy 是 HWQueue 的 Isabelle 模型，现在想证明 Termination.thy 中的 termination modify_lin。
请先生成一个可以编译通过的证明框架，checker 可以接受证明框架里存在 sorry。
最后需要明确说明这是可编译证明框架，不是完全无 sorry 的最终证明。`
}

func fakeIsabelleCodexScript() string {
	return `#!/bin/sh
cat >/dev/null
printf '%s\n' '{"type":"item.started","item":{"id":"cmd_1","type":"command_execution","command":"mkdir -p isabelle_bridge_demo","status":"running"}}'
mkdir -p isabelle_bridge_demo
cat >isabelle_bridge_demo/BridgeDemo.thy <<'EOF'
theory BridgeDemo
imports Main
begin

lemma append_nil_right:
  "xs @ [] = (xs :: 'a list)"
  by simp

lemma rev_snoc:
  "rev (xs @ [x]) = x # rev xs"
  by simp

lemma map_append_demo:
  "map f (xs @ ys) = map f xs @ map f ys"
  by simp

end
EOF
printf '%s\n' '{"type":"item.completed","item":{"id":"cmd_1","type":"command_execution","command":"mkdir -p isabelle_bridge_demo","status":"completed","exit_code":0,"aggregated_output":""}}'
printf '%s\n' '{"type":"item.started","item":{"id":"cmd_2","type":"command_execution","command":"isabelle build -D isabelle_bridge_demo","status":"running"}}'
printf '%s\n' '{"type":"item.completed","item":{"id":"cmd_2","type":"command_execution","command":"isabelle build -D isabelle_bridge_demo","status":"completed","exit_code":0,"aggregated_output":"Finished BridgeDemo\n0:00:01 elapsed time\n"}}'
printf '%s\n' '{"type":"item.agent_message.delta","delta":"Created isabelle_bridge_demo/BridgeDemo.thy and verified with isabelle build."}'
printf '%s\n' '{"type":"turn.completed","usage":{"output_tokens":12}}'
`
}

func fakeIsabelleClaudeScript() string {
	return `#!/bin/sh
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool_1","name":"Bash","input":{"command":"mkdir -p isabelle_bridge_demo"}}]}}'
printf '%s\n' '{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tool_1","content":"created\n"}]}}'
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"I will create the Isabelle theory in isabelle_bridge_demo and leave verification to the reviewer."}]}}'
printf '%s\n' '{"type":"result","result":"I will create the Isabelle theory in isabelle_bridge_demo and leave verification to the reviewer."}'
`
}

func isabelleBridgeDemoPrompt() string {
	return `请创建一个 Isabelle/HOL 文件 BridgeDemo.thy，放在/root/tencent路径下新建一个合适的文件夹进行放置
完成下面这些定理证明，并尽量运行 isabelle build 或 isabelle process 做验证。如果当前机器没有 Isabelle，请说明无法本地验证，但仍
给出可检查的最终 proof。

目标文件内容：

theory BridgeDemo
imports Main
begin

lemma append_nil_right:
"xs @ [] = (xs :: 'a list)"
sorry

lemma rev_snoc:
"rev (xs @ [x]) = x # rev xs"
sorry

lemma map_append_demo:
"map f (xs @ ys) = map f xs @ map f ys"
sorry

lemma filter_append_demo:
"filter P (xs @ ys) = filter P xs @ filter P ys"
sorry

end

要求：
1. 把所有 sorry 替换成正式 proof。
2. 优先使用简洁 Isabelle proof，例如 by simp、by auto 或 induction。
3. 最后总结每个 lemma 用了什么证明策略。`
}

func expectPrompt(t *testing.T, ws *websocket.Conn, sid, prompt, want string) {
	t.Helper()
	if err := ws.WriteJSON(protocol.MustEnvelope(protocol.TypePrompt, sid, protocol.PromptPayload{Content: prompt})); err != nil {
		t.Fatal(err)
	}
	var sawUpdate bool
	var sawComplete bool
	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	defer ws.SetReadDeadline(time.Time{})
	for !sawComplete {
		var env protocol.Envelope
		if err := ws.ReadJSON(&env); err != nil {
			t.Fatal(err)
		}
		if env.Sid != "" && env.Sid != sid {
			t.Fatalf("sid leak: got %q want %q", env.Sid, sid)
		}
		if env.Type == protocol.TypeHeartbeat {
			continue
		}
		switch env.Type {
		case protocol.TypeSessionUpdate:
			payload, err := protocol.Decode[protocol.SessionUpdatePayload](env)
			if err != nil {
				t.Fatal(err)
			}
			sawUpdate = sawUpdate || strings.Contains(payload.Delta, want) || strings.Contains(payload.Content, want)
		case protocol.TypePromptComplete:
			payload, err := protocol.Decode[protocol.PromptCompletePayload](env)
			if err != nil {
				t.Fatal(err)
			}
			if payload.Content != want {
				t.Fatalf("assistant content = %q, want %q", payload.Content, want)
			}
			sawComplete = true
		}
	}
	if !sawUpdate {
		t.Fatalf("did not receive assistant update %q", want)
	}
}

func postJSON(t *testing.T, client *http.Client, target string, payload any, wantStatus int) map[string]any {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, target, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	return doJSON(t, client, req, wantStatus)
}

func getJSON(t *testing.T, client *http.Client, target string, wantStatus int) map[string]any {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	return doJSON(t, client, req, wantStatus)
}

func deleteJSON(t *testing.T, client *http.Client, target string, wantStatus int) map[string]any {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	return doJSON(t, client, req, wantStatus)
}

func doJSON(t *testing.T, client *http.Client, req *http.Request, wantStatus int) map[string]any {
	t.Helper()
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != wantStatus {
		t.Fatalf("%s %s: got %s, body=%s", req.Method, req.URL, res.Status, string(body))
	}
	var out map[string]any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("decode response: %v, body=%s", err, string(body))
		}
	}
	return out
}
