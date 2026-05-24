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
		if strings.Contains(setup, "\n") {
			t.Fatalf("profile setup command should be one line: %q", setup)
		}
		if out, err := exec.Command("sh", "-n", "-c", setup).CombinedOutput(); err != nil {
			t.Fatalf("profile setup command shell syntax: %v\n%s\n%s", err, out, setup)
		}
		profileCommands[id] = setup
	}
	if !strings.Contains(profileCommands["review-required"], "--runner codex-app-server --sandbox workspace-write --approval-policy untrusted") {
		t.Fatalf("review-required command missing conservative flags: %s", profileCommands["review-required"])
	}
	if !strings.Contains(profileCommands["auto-execute"], "--runner codex --sandbox danger-full-access --approval-policy never") {
		t.Fatalf("auto-execute command missing bypass flags: %s", profileCommands["auto-execute"])
	}
	if strings.Contains(profileCommands["review-required"], "--machine-id ") {
		t.Fatalf("new endpoint command should not pin a machine id: %s", profileCommands["review-required"])
	}
	commands := tokenBody["commands"].([]any)
	if len(commands) != 1 || !strings.Contains(commands[0].(string), "/install.sh") || !strings.Contains(commands[0].(string), "codex-bridge") {
		t.Fatalf("commands = %#v", commands)
	}
	setupCommand := commands[0].(string)
	if strings.Contains(setupCommand, "\n") {
		t.Fatalf("setup command should be one line: %q", setupCommand)
	}
	if out, err := exec.Command("sh", "-n", "-c", setupCommand).CombinedOutput(); err != nil {
		t.Fatalf("setup command shell syntax: %v\n%s\n%s", err, out, setupCommand)
	}
	if tokenBody["setupCommand"] != setupCommand {
		t.Fatalf("setupCommand mismatch: %#v != %#v", tokenBody["setupCommand"], setupCommand)
	}
	connectCommand := tokenBody["connectCommand"].(string)
	if strings.Contains(connectCommand, "\n") {
		t.Fatalf("connect command should be one line: %q", connectCommand)
	}
	for _, want := range []string{
		`CB_SERVICE_NAME="codex-bridge-${CB_HASH}.service"`,
		`systemctl --user stop "$CB_SERVICE_NAME"`,
		`systemctl --user daemon-reload && systemctl --user enable "$CB_SERVICE_NAME" && systemctl --user restart "$CB_SERVICE_NAME"`,
		`systemctl --user is-active --quiet "$CB_SERVICE_NAME"`,
		`${CB_HASH}.env`,
		`codex CLI not found in PATH`,
		`Claude Code CLI not found in PATH`,
		`CB_CODEX_PATH="$(command -v codex)"`,
		`CB_CLAUDE_PATH="$(command -v claude)"`,
		`printf 'PATH=%s\n'`,
		`BRIDGE_CODEX_PATH`,
		`BRIDGE_CLAUDE_PATH`,
		`HTTP_PROXY HTTPS_PROXY ALL_PROXY NO_PROXY http_proxy https_proxy all_proxy no_proxy`,
		`set -a; . "$CB_HOME/services/${CB_HASH}.env"; set +a`,
		`: > "$CB_LOG"`,
		`CB_WAIT=0`,
		`codex-bridge service started but Hub connection is not confirmed`,
		`codex-bridge connected: $CB_SERVICE_NAME log=$CB_LOG`,
		`codex-bridge user service did not stay active; falling back to nohup`,
		`loginctl enable-linger "$(id -un)"`,
		`ExecStart=%h/.codex-bridge/services/`,
		`nohup "$CB_START" > "$CB_LOG" 2>&1 &`,
		`codex-bridge started in background but Hub connection is not confirmed`,
		`--cwd "$CB_CWD"`,
		`--name "$CB_NAME"`,
		`--machine-id-file "$CB_HOME/machines/${CB_HASH}"`,
		`--runner codex-app-server --sandbox workspace-write --approval-policy untrusted`,
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
	if !strings.Contains(autoConnect, "--runner codex --sandbox danger-full-access --approval-policy never") {
		t.Fatalf("auto connect command missing full access flags: %s", autoConnect)
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
	for _, want := range []string{
		`CB_CWD=` + shellSingleQuote(tmp),
		`CB_NAME=` + shellSingleQuote("fake-bridge"),
		`--machine-id`,
		agent["machineId"].(string),
		`--runner codex-app-server --sandbox workspace-write --approval-policy untrusted`,
	} {
		if !strings.Contains(repairConnect, want) {
			t.Fatalf("repair connect command missing %q: %s", want, repairConnect)
		}
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
		if profile["id"] == "auto-execute" && strings.Contains(connect, "--runner codex --sandbox danger-full-access --approval-policy never") {
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

func fakeIsabelleCodexScript() string {
	return `#!/bin/sh
cat >/dev/null
printf '%s\n' '{"type":"item.started","item":{"id":"cmd_1","type":"command_execution","command":"mkdir -p isabelle_bridge_demo","status":"running"}}'
mkdir -p isabelle_bridge_demo
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
