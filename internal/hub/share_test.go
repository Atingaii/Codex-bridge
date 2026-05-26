package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tencent/codex-bridge/internal/store"
)

func TestPublicChatShareReadOnlyAndSanitized(t *testing.T) {
	t.Parallel()

	s, st := newAuthTestServer(t)
	ctx := context.Background()
	user, err := st.UserByUsername(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := st.UpsertAgent(ctx, "bridge", "machine-share-chat", "host", "inst", nil)
	if err != nil {
		t.Fatal(err)
	}
	session, err := st.CreateSession(ctx, user.ID, agent.ID, "Shared chat")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateSessionRemoteThread(ctx, session.ID, user.ID, "remote-thread-secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddMessage(ctx, session.ID, "user", "hello", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddMessage(ctx, session.ID, "assistant", "world", `{"tokens":1}`); err != nil {
		t.Fatal(err)
	}

	body := authedJSON(t, s, user.ID, http.MethodPost, "/api/sessions/"+session.ID+"/share", nil, http.StatusCreated)
	share := body["share"].(map[string]any)
	shareID := share["id"].(string)
	if !strings.Contains(share["url"].(string), "/share/"+shareID) {
		t.Fatalf("share url = %#v", share)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/public/shares/"+shareID, nil)
	rr := httptest.NewRecorder()
	s.httpSrv.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("public share status = %d, want %d, body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}
	raw := rr.Body.String()
	for _, forbidden := range []string{"userId", "agentId", "remoteThreadId", "usageJson", "remote-thread-secret"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("public chat share leaked %q: %s", forbidden, raw)
		}
	}
	var decoded map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	messages := decoded["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("messages = %#v", messages)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/sessions/"+session.ID+"/messages", nil)
	rr = httptest.NewRecorder()
	s.httpSrv.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("private messages without auth status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}

	authedJSON(t, s, user.ID, http.MethodDelete, "/api/shares/"+shareID, nil, http.StatusOK)
	req = httptest.NewRequest(http.MethodGet, "/api/public/shares/"+shareID, nil)
	rr = httptest.NewRecorder()
	s.httpSrv.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("revoked public share status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestShareCreationRejectsOtherUserTargets(t *testing.T) {
	t.Parallel()

	s, st := newAuthTestServer(t)
	ctx := context.Background()
	admin, err := st.UserByUsername(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := st.UpsertUser(ctx, "owner", "abc1234567")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := st.UpsertAgentForUser(ctx, owner.ID, "owner-cli", "machine-owner-share", "host", "inst", nil)
	if err != nil {
		t.Fatal(err)
	}
	session, err := st.CreateSession(ctx, owner.ID, agent.ID, "Owner chat")
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.CreateOrchestrationRun(ctx, store.CreateOrchestrationRunParams{
		UserID:   owner.ID,
		AgentID:  agent.ID,
		Title:    "Owner run",
		Mode:     "collaboration",
		Prompt:   "prove it",
		MaxTurns: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	authedJSON(t, s, admin.ID, http.MethodPost, "/api/sessions/"+session.ID+"/share", nil, http.StatusNotFound)
	authedJSON(t, s, admin.ID, http.MethodPost, "/api/orchestrations/"+run.ID+"/share", nil, http.StatusNotFound)
}

func TestPublicOrchestrationShareSanitizesRunAndEventData(t *testing.T) {
	t.Parallel()

	s, st, userID, agentID := newOrchestrationTestServer(t)
	ctx := context.Background()
	run := createOrchestrationRun(t, st, userID, agentID)
	if _, err := st.AddOrchestrationEvent(ctx, store.OrchestrationEvent{
		RunID:   run.ID,
		Kind:    "command.end",
		CLI:     "codex",
		Status:  "completed",
		Content: "done",
		Data: map[string]any{
			"command": "go test ./...",
			"output":  "ok",
			"secret":  "do-not-share",
			"files":   []any{map[string]any{"name": "Model.thy", "size": 12, "data": "file-body-secret"}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	body := authedJSON(t, s, userID, http.MethodPost, "/api/orchestrations/"+run.ID+"/share", nil, http.StatusCreated)
	shareID := body["share"].(map[string]any)["id"].(string)
	req := httptest.NewRequest(http.MethodGet, "/api/public/shares/"+shareID, nil)
	rr := httptest.NewRecorder()
	s.httpSrv.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("public orchestration status = %d, want %d, body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}
	raw := rr.Body.String()
	for _, forbidden := range []string{"userId", "agentId", "secret", "do-not-share", "file-body-secret"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("public orchestration share leaked %q: %s", forbidden, raw)
		}
	}
	for _, want := range []string{"go test ./...", "Model.thy", `"events"`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("public orchestration share missing %q: %s", want, raw)
		}
	}
}

func authedJSON(t *testing.T, s *Server, userID, method, path string, payload any, wantStatus int) map[string]any {
	t.Helper()

	token, _, err := s.signer.Sign(userID)
	if err != nil {
		t.Fatal(err)
	}
	var body *bytes.Reader
	if payload == nil {
		body = bytes.NewReader(nil)
	} else {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(encoded)
	}
	req := httptest.NewRequest(method, path, body)
	req.AddCookie(&http.Cookie{Name: accessCookieName, Value: token})
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	s.httpSrv.Handler.ServeHTTP(rr, req)
	if rr.Code != wantStatus {
		t.Fatalf("%s %s status = %d, want %d, body = %s", method, path, rr.Code, wantStatus, rr.Body.String())
	}
	if strings.TrimSpace(rr.Body.String()) == "" {
		return nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v: %s", err, rr.Body.String())
	}
	return decoded
}
