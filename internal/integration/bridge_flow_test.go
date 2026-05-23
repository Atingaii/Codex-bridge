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
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tencent/codex-bridge/internal/bridge"
	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/hub"
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
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

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
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

func dialFakeBridge(t *testing.T, baseURL, token string) *fakeBridgeConn {
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
		Name:      "fake-bridge",
		MachineID: "fake-machine-" + store.NewID("test"),
		Hostname:  "test-host",
		Version:   "test",
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
