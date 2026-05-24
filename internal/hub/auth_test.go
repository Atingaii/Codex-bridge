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
	"github.com/tencent/codex-bridge/internal/store"
)

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
