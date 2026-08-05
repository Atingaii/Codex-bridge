package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

type stubTurnstileVerifier struct {
	err      error
	requests []turnstileVerifyRequest
}

func (v *stubTurnstileVerifier) Verify(_ context.Context, req turnstileVerifyRequest) error {
	v.requests = append(v.requests, req)
	return v.err
}

func TestRegisterDisabledAndExistingUserLoginNormalizesUsername(t *testing.T) {
	t.Parallel()

	s, st := newAuthTestServer(t)
	body := register(t, s, map[string]string{"username": "new-user", "password": "abc1234567"}, http.StatusForbidden)
	if body["code"] != "REGISTRATION_DISABLED" {
		t.Fatalf("register error = %#v", body)
	}
	if _, err := st.UserByUsername(context.Background(), "new-user"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("disabled registration created user: %v", err)
	}
	if _, err := st.UpsertUser(context.Background(), "new-user", "abc1234567"); err != nil {
		t.Fatal(err)
	}
	login(t, s, map[string]string{"username": " new-user ", "password": "abc1234567"}, http.StatusOK)
}

func TestAuthRateLimit(t *testing.T) {
	t.Parallel()

	s, _ := newAuthTestServer(t)
	for i := 0; i < loginRateLimitMax; i++ {
		login(t, s, map[string]string{"username": "admin", "password": "wrong"}, http.StatusUnauthorized)
	}
	login(t, s, map[string]string{"username": "admin", "password": "wrong"}, http.StatusTooManyRequests)
}

func TestRegistrationRequiresTurnstileAndCreatesSignedInUser(t *testing.T) {
	t.Parallel()

	s, st := newAuthTestServer(t)
	s.cfg.Auth.Registration.Enabled = true
	s.cfg.Auth.Registration.TurnstileSiteKey = "site-public"
	s.cfg.Auth.Registration.TurnstileSecret = "server-secret"
	s.cfg.Auth.Registration.TurnstileHostname = "bridge.example"
	verifier := &stubTurnstileVerifier{}
	s.turnstile = verifier

	configReq := httptest.NewRequest(http.MethodGet, "/api/auth/config", nil)
	configRR := httptest.NewRecorder()
	s.httpSrv.Handler.ServeHTTP(configRR, configReq)
	if configRR.Code != http.StatusOK {
		t.Fatalf("auth config status = %d: %s", configRR.Code, configRR.Body.String())
	}
	if strings.Contains(configRR.Body.String(), "server-secret") || !strings.Contains(configRR.Body.String(), "site-public") {
		t.Fatalf("public auth config leaked secret or omitted sitekey: %s", configRR.Body.String())
	}

	register(t, s, map[string]string{"username": "member", "password": "long-password"}, http.StatusBadRequest)
	bodyBytes, err := json.Marshal(map[string]string{
		"username":       "member",
		"password":       "long-password",
		"turnstileToken": "browser-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/register", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("CF-Connecting-IP", "198.51.100.9")
	rr := httptest.NewRecorder()
	s.httpSrv.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if len(verifier.requests) != 1 {
		t.Fatalf("verification requests = %#v", verifier.requests)
	}
	got := verifier.requests[0]
	if got.Secret != "server-secret" || got.Token != "browser-token" || got.RemoteIP != "198.51.100.9" || got.ExpectedHostname != "bridge.example" || got.ExpectedAction != "register" {
		t.Fatalf("verification request = %#v", got)
	}
	if _, err := st.AuthenticateUser(context.Background(), "member", "long-password"); err != nil {
		t.Fatalf("registered user cannot authenticate: %v", err)
	}
	if len(rr.Result().Cookies()) == 0 {
		t.Fatal("registration did not sign the user in")
	}
}

func TestRegistrationFailsClosedAndRejectsPrivilegeAlias(t *testing.T) {
	t.Parallel()

	s, st := newAuthTestServer(t)
	s.cfg.Auth.Registration.Enabled = true
	s.cfg.Auth.Registration.TurnstileSiteKey = "site-public"
	s.cfg.Auth.Registration.TurnstileSecret = "server-secret"
	verifier := &stubTurnstileVerifier{err: errors.New("siteverify unavailable")}
	s.turnstile = verifier

	register(t, s, map[string]string{"username": "member", "password": "long-password", "turnstileToken": "bad"}, http.StatusBadRequest)
	if _, err := st.UserByUsername(context.Background(), "member"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("failed verification created user: %v", err)
	}
	register(t, s, map[string]string{"username": "Admin", "password": "long-password", "turnstileToken": "token"}, http.StatusBadRequest)
	if len(verifier.requests) != 1 {
		t.Fatalf("invalid username should be rejected before verification: %#v", verifier.requests)
	}
}

func TestCloudflareTurnstileVerifier(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		status    int
		response  string
		wantError string
	}{
		{name: "success", status: http.StatusOK, response: `{"success":true,"hostname":"bridge.example","action":"register"}`},
		{name: "rejected", status: http.StatusOK, response: `{"success":false,"error-codes":["invalid-input-response"]}`, wantError: "rejected"},
		{name: "action mismatch", status: http.StatusOK, response: `{"success":true,"hostname":"bridge.example","action":"login"}`, wantError: "action mismatch"},
		{name: "hostname mismatch", status: http.StatusOK, response: `{"success":true,"hostname":"other.example","action":"register"}`, wantError: "hostname mismatch"},
		{name: "upstream status", status: http.StatusBadGateway, response: `{}`, wantError: "status 502"},
		{name: "malformed response", status: http.StatusOK, response: `{`, wantError: "unexpected EOF"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var gotSecret, gotResponse, gotIP string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					t.Errorf("parse form: %v", err)
				}
				gotSecret = r.Form.Get("secret")
				gotResponse = r.Form.Get("response")
				gotIP = r.Form.Get("remoteip")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.response))
			}))
			defer upstream.Close()

			verifier := cloudflareTurnstileVerifier{client: upstream.Client(), url: upstream.URL}
			err := verifier.Verify(context.Background(), turnstileVerifyRequest{
				Secret:           "server-secret",
				Token:            "browser-token",
				RemoteIP:         "198.51.100.10",
				ExpectedHostname: "bridge.example",
				ExpectedAction:   "register",
			})
			if test.wantError == "" && err != nil {
				t.Fatalf("Verify() error = %v", err)
			}
			if test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError)) {
				t.Fatalf("Verify() error = %v, want substring %q", err, test.wantError)
			}
			if gotSecret != "server-secret" || gotResponse != "browser-token" || gotIP != "198.51.100.10" {
				t.Fatalf("siteverify form = secret %q response %q remoteip %q", gotSecret, gotResponse, gotIP)
			}
		})
	}
}

func TestRegistrationRateLimitIsIndependent(t *testing.T) {
	t.Parallel()

	s, _ := newAuthTestServer(t)
	s.cfg.Auth.Registration.Enabled = true
	s.cfg.Auth.Registration.TurnstileSiteKey = "site-public"
	s.cfg.Auth.Registration.TurnstileSecret = "server-secret"
	s.turnstile = &stubTurnstileVerifier{err: errors.New("rejected")}
	payload := map[string]string{"username": "limited-user", "password": "long-password", "turnstileToken": "bad"}
	for range registerRateLimitMax {
		register(t, s, payload, http.StatusBadRequest)
	}
	register(t, s, payload, http.StatusTooManyRequests)

	if _, err := s.store.UpsertUser(context.Background(), "limited-user", "long-password"); err != nil {
		t.Fatal(err)
	}
	login(t, s, map[string]string{"username": "limited-user", "password": "long-password"}, http.StatusOK)
}

func TestAuthenticatedChatRoutesAreUserIsolated(t *testing.T) {
	t.Parallel()

	s, st := newAuthTestServer(t)
	ctx := context.Background()
	userA, err := st.CreateUser(ctx, "user-a", "long-password")
	if err != nil {
		t.Fatal(err)
	}
	userB, err := st.CreateUser(ctx, "user-b", "long-password")
	if err != nil {
		t.Fatal(err)
	}
	agentA, err := st.UpsertAgentForUser(ctx, userA.ID, "a-cli", "machine-a", "host", "inst", nil)
	if err != nil {
		t.Fatal(err)
	}
	agentB, err := st.UpsertAgentForUser(ctx, userB.ID, "b-cli", "machine-b", "host", "inst", nil)
	if err != nil {
		t.Fatal(err)
	}
	sessionB, err := st.CreateSession(ctx, userB.ID, agentB.ID, "private-b")
	if err != nil {
		t.Fatal(err)
	}
	cookieA := loginCookie(t, s, map[string]string{"username": "user-a", "password": "long-password"})
	created := authJSONRequestWithCookie(t, s, http.MethodPost, "/api/sessions", cookieA, map[string]string{"agentId": agentA.ID, "title": "private-a"}, http.StatusCreated)
	if created["session"] == nil {
		t.Fatalf("normal user could not create owned chat session: %#v", created)
	}
	authRequestWithCookie(t, s, http.MethodGet, "/api/sessions/"+sessionB.ID+"/messages", cookieA, http.StatusNotFound)
	authJSONRequestWithCookie(t, s, http.MethodPost, "/api/sessions", cookieA, map[string]string{"agentId": agentB.ID, "title": "stolen"}, http.StatusBadRequest)
	wsReq := httptest.NewRequest(http.MethodGet, "/ws/chat?sid="+sessionB.ID, nil)
	wsReq.AddCookie(cookieA)
	wsRR := httptest.NewRecorder()
	s.httpSrv.Handler.ServeHTTP(wsRR, wsReq)
	if wsRR.Code != http.StatusNotFound {
		t.Fatalf("cross-user websocket preflight status = %d, body = %s", wsRR.Code, wsRR.Body.String())
	}
}

func TestOrchestrationAndShareRoutesAreUserIsolated(t *testing.T) {
	t.Parallel()

	s, st := newAuthTestServer(t)
	ctx := context.Background()
	owner, err := st.CreateUser(ctx, "run-owner", "long-password")
	if err != nil {
		t.Fatal(err)
	}
	intruder, err := st.CreateUser(ctx, "run-intruder", "long-password")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := st.UpsertAgentForUser(ctx, owner.ID, "owner-cli", "machine-owner", "host", "inst", nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.CreateOrchestrationRun(ctx, store.CreateOrchestrationRunParams{
		UserID: owner.ID, AgentID: agent.ID, Title: "private proof", Mode: "debate", Prompt: "prove it", MaxTurns: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	share, err := st.CreateOrUpdateConversationShare(ctx, owner.ID, store.ShareKindOrchestration, run.ID, run.Title)
	if err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, s, map[string]string{"username": intruder.Username, "password": "long-password"})

	list := authRequestWithCookie(t, s, http.MethodGet, "/api/orchestrations", cookie, http.StatusOK)
	encoded, _ := json.Marshal(list)
	if strings.Contains(string(encoded), run.ID) {
		t.Fatalf("cross-user orchestration leaked in list: %s", encoded)
	}
	for _, request := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/orchestrations/" + run.ID},
		{http.MethodGet, "/api/orchestrations/" + run.ID + "/events"},
		{http.MethodPost, "/api/orchestrations/" + run.ID + "/prompts"},
		{http.MethodPost, "/api/orchestrations/" + run.ID + "/cancel"},
		{http.MethodPost, "/api/orchestrations/" + run.ID + "/share"},
		{http.MethodDelete, "/api/shares/" + share.ID},
	} {
		authRequestWithCookie(t, s, request.method, request.path, cookie, http.StatusNotFound)
	}
	wsReq := httptest.NewRequest(http.MethodGet, "/ws/orchestrations?runId="+run.ID, nil)
	wsReq.AddCookie(cookie)
	wsRR := httptest.NewRecorder()
	s.httpSrv.Handler.ServeHTTP(wsRR, wsReq)
	if wsRR.Code != http.StatusNotFound {
		t.Fatalf("cross-user orchestration websocket status = %d, body = %s", wsRR.Code, wsRR.Body.String())
	}
}

func TestInstallScriptDefaultsToHubBinaryDownload(t *testing.T) {
	t.Parallel()

	s, _ := newAuthTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/install.sh", nil)
	req.Host = "sparkapi.test"
	req.Header.Set("X-Forwarded-Proto", "https")
	rr := httptest.NewRecorder()
	s.httpSrv.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("install HTTP status = %d, want %d, body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "https://sparkapi.test/downloads/codex-bridge-linux-amd64") {
		t.Fatalf("install script did not use hub binary download: %s", body)
	}
	for _, want := range []string{
		`TMP="${BIN}.tmp.$$"`,
		`curl -fL --retry 3 -o "$TMP" "$DOWNLOAD_URL"`,
		`wget -O "$TMP" "$DOWNLOAD_URL"`,
		`mv -f "$TMP" "$BIN"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("install script missing %q: %s", want, body)
		}
	}
}

func TestDeleteAgentHidesVisibleAgent(t *testing.T) {
	t.Parallel()

	s, st := newAuthTestServer(t)
	ctx := context.Background()
	user, err := st.UpsertUser(ctx, "worker", "abc1234567")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := st.UpsertAgentForUser(ctx, user.ID, "worker-cli", "machine-delete", "host", "inst", nil)
	if err != nil {
		t.Fatal(err)
	}
	otherUser, err := st.UpsertUser(ctx, "other", "abc1234567")
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.UpsertAgentForUser(ctx, otherUser.ID, "other-cli", "machine-other", "host", "inst", nil)
	if err != nil {
		t.Fatal(err)
	}

	cookie := loginCookie(t, s, map[string]string{"username": "worker", "password": "abc1234567"})
	authRequestWithCookie(t, s, http.MethodDelete, "/api/agents/"+other.ID, cookie, http.StatusNotFound)
	authRequestWithCookie(t, s, http.MethodDelete, "/api/agents/"+agent.ID, cookie, http.StatusOK)

	if _, err := st.AgentByID(ctx, agent.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected deleted agent to be hidden, got %v", err)
	}
	body := authRequestWithCookie(t, s, http.MethodGet, "/api/agents", cookie, http.StatusOK)
	agents := body["agents"].([]any)
	if len(agents) != 0 {
		t.Fatalf("deleted agent still visible: %#v", agents)
	}
}

func TestDeleteAgentRequestsOnlineBridgeShutdown(t *testing.T) {
	t.Parallel()

	s, st := newAuthTestServer(t)
	ctx := context.Background()
	user, err := st.UpsertUser(ctx, "worker", "abc1234567")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := st.UpsertAgentForUser(ctx, user.ID, "worker-cli", "machine-shutdown", "host", "inst", nil)
	if err != nil {
		t.Fatal(err)
	}
	conn := &BridgeConn{
		agentID: agent.ID,
		wsSender: wsSender{
			send: make(chan protocol.Envelope, 2),
			done: make(chan struct{}),
		},
	}
	s.pool.RegisterAgent(conn)

	cookie := loginCookie(t, s, map[string]string{"username": "worker", "password": "abc1234567"})
	authRequestWithCookie(t, s, http.MethodDelete, "/api/agents/"+agent.ID, cookie, http.StatusOK)

	select {
	case env := <-conn.send:
		if env.Type != protocol.TypeAgentShutdown {
			t.Fatalf("shutdown envelope type = %q", env.Type)
		}
		payload, err := protocol.Decode[protocol.AgentShutdownPayload](env)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(payload.Reason, "deleted") {
			t.Fatalf("shutdown reason = %#v", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for agent shutdown envelope")
	}
	if s.pool.AgentOnline(agent.ID) {
		t.Fatal("deleted agent still marked online")
	}
}

func newAuthTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()

	cfg := config.Default()
	cfg.Hub.DBPath = t.TempDir() + "/bridge.db"
	cfg.Auth.JWTSecret = "hub-auth-test-secret-32-byte-minimum"
	cfg.Auth.AccessTokenTTL.Duration = time.Hour

	st, err := store.Open(cfg.Hub.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertUser(context.Background(), "admin", "secret12345"); err != nil {
		t.Fatal(err)
	}
	return NewServer(&cfg, st, BuildInfo{Version: "test", BuildTime: "test"}), st
}

func register(t *testing.T, s *Server, payload map[string]string, wantStatus int) map[string]any {
	t.Helper()
	return authRequest(t, s, http.MethodPost, "/api/register", payload, wantStatus)
}

func login(t *testing.T, s *Server, payload map[string]string, wantStatus int) map[string]any {
	t.Helper()
	return authRequest(t, s, http.MethodPost, "/api/login", payload, wantStatus)
}

func loginCookie(t *testing.T, s *Server, payload map[string]string) *http.Cookie {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.httpSrv.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("login HTTP status = %d, want %d, body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}
	for _, cookie := range rr.Result().Cookies() {
		if cookie.Name == accessCookieName {
			return cookie
		}
	}
	t.Fatal("login did not return access cookie")
	return nil
}

func authRequestWithCookie(t *testing.T, s *Server, method, path string, cookie *http.Cookie, wantStatus int) map[string]any {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	s.httpSrv.Handler.ServeHTTP(rr, req)
	if rr.Code != wantStatus {
		t.Fatalf("%s HTTP status = %d, want %d, body = %s", path, rr.Code, wantStatus, rr.Body.String())
	}
	if strings.TrimSpace(rr.Body.String()) == "" {
		return nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode body: %v: %s", err, rr.Body.String())
	}
	return decoded
}

func authJSONRequestWithCookie(t *testing.T, s *Server, method, path string, cookie *http.Cookie, payload map[string]string, wantStatus int) map[string]any {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	s.httpSrv.Handler.ServeHTTP(rr, req)
	if rr.Code != wantStatus {
		t.Fatalf("%s HTTP status = %d, want %d, body = %s", path, rr.Code, wantStatus, rr.Body.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode body: %v: %s", err, rr.Body.String())
	}
	return decoded
}

func authRequest(t *testing.T, s *Server, method, path string, payload map[string]string, wantStatus int) map[string]any {
	t.Helper()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.0.2.50:1234"
	rr := httptest.NewRecorder()
	s.httpSrv.Handler.ServeHTTP(rr, req)
	if rr.Code != wantStatus {
		t.Fatalf("%s HTTP status = %d, want %d, body = %s", path, rr.Code, wantStatus, rr.Body.String())
	}
	if strings.TrimSpace(rr.Body.String()) == "" {
		return nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode auth body: %v: %s", err, rr.Body.String())
	}
	return decoded
}
