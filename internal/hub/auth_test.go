package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/store"
)

func TestRegisterValidatesCredentialsAndLoginNormalizesUsername(t *testing.T) {
	t.Parallel()

	s, st := newAuthTestServer(t)
	register(t, s, map[string]string{"username": "ab", "password": "abc1234567"}, http.StatusBadRequest)
	register(t, s, map[string]string{"username": "new user", "password": "abc1234567"}, http.StatusBadRequest)
	register(t, s, map[string]string{"username": "new-user", "password": "abcdefghij"}, http.StatusBadRequest)
	register(t, s, map[string]string{"username": " new-user ", "password": "abc1234567"}, http.StatusCreated)

	if _, err := st.UserByUsername(context.Background(), "new-user"); err != nil {
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
