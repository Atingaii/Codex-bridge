package hub

import (
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

func TestAdminUsageOverviewCombinesUsersWithoutContent(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC).Unix()
	snapshot := store.AdminUsageSnapshot{
		Users: []store.AdminUserUsage{
			{UserID: "admin", Username: "admin", CreatedAt: now - 100, LastActiveAt: now - 30, AgentIDs: []string{"agent-online"}, ChatSessions: 2},
			{UserID: "user", Username: "member", CreatedAt: now - 900000, LastActiveAt: now - 3*86400, AgentIDs: []string{"agent-offline"}, OrchestrationRuns: 1, RunningRuns: 1},
		},
		UsageEvents: []store.AdminUsageEvent{
			{UserID: "admin", OccurredAt: now - 60, CLI: "codex", Model: "gpt-5.6-sol", InputTokens: 20, CacheReadTokens: 80, OutputTokens: 5, Normalized: true},
			{UserID: "user", OccurredAt: now - 86400, CLI: "unknown", Model: "private-model", InputTokens: 7, OutputTokens: 3, Normalized: true},
		},
		ActivityEvents: []store.AdminActivityEvent{{UserID: "admin", OccurredAt: now - 60, Kind: "chat_message", Count: 1}, {UserID: "user", OccurredAt: now - 86400, Kind: "orchestration_run", Count: 1}},
	}
	overview := buildAdminUsageOverview(snapshot, 7, -480, now, func(agentID string) bool { return agentID == "agent-online" })
	if overview.Users != 2 || overview.OnlineUsers != 1 || overview.OnlineAgents != 1 || overview.TotalAgents != 2 {
		t.Fatalf("unexpected user/agent totals: %#v", overview)
	}
	if overview.InputTokens != 27 || overview.CacheTokens != 80 || overview.OutputTokens != 8 || overview.TotalTokens != 115 || overview.CallCount != 2 {
		t.Fatalf("unexpected usage totals: %#v", overview.adminUsageTotals)
	}
	if overview.CostKnown {
		t.Fatal("mixed known and unknown pricing must remain incomplete")
	}
	if len(overview.Trend) != 7 || overview.Items[0].ActivityStatus != "online" || overview.Items[1].ActivityStatus != "idle" {
		t.Fatalf("unexpected trend or statuses: %#v", overview)
	}
}

func TestAdminUsageRouteRequiresAdministrator(t *testing.T) {
	ctx := context.Background()
	dbPath := t.TempDir() + "/hub.db"
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	admin, err := st.CreateUser(ctx, "admin", "admin-password")
	if err != nil {
		t.Fatal(err)
	}
	member, err := st.CreateUser(ctx, "member", "member-password")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Auth.BootstrapUsername = "admin"
	cfg.Auth.JWTSecret = "01234567890123456789012345678901"
	s := NewServer(&cfg, st, BuildInfo{})

	handler := s.withAdmin(s.handleAdminUsage)
	unauthenticated := httptest.NewRecorder()
	handler(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/admin/usage?days=7", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", unauthenticated.Code)
	}
	request := func(userID string) *httptest.ResponseRecorder {
		token, _, signErr := s.signer.Sign(userID)
		if signErr != nil {
			t.Fatal(signErr)
		}
		r := httptest.NewRequest(http.MethodGet, "/api/admin/usage?days=7", nil)
		r.AddCookie(&http.Cookie{Name: accessCookieName, Value: token})
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}
	if got := request(member.ID); got.Code != http.StatusForbidden {
		t.Fatalf("member status = %d, want 403", got.Code)
	} else {
		var denied map[string]any
		if err := json.Unmarshal(got.Body.Bytes(), &denied); err != nil {
			t.Fatal(err)
		}
		if denied["code"] != "ADMIN_ONLY" || denied["overview"] != nil {
			t.Fatalf("member received unexpected response: %s", got.Body.String())
		}
	}
	got := request(admin.ID)
	if got.Code != http.StatusOK {
		t.Fatalf("admin status = %d body=%s", got.Code, got.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(got.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["overview"] == nil {
		t.Fatalf("missing overview: %s", got.Body.String())
	}
}

func TestAdminConversationContentRequiresAdministratorAndChecksOwnership(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir() + "/hub.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	admin, err := st.CreateUser(ctx, "admin", "admin-password")
	if err != nil {
		t.Fatal(err)
	}
	member, err := st.CreateUser(ctx, "member", "member-password")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := st.UpsertAgentForUser(ctx, member.ID, "member-laptop", "member-machine", "host", "instance", nil)
	if err != nil {
		t.Fatal(err)
	}
	session, err := st.CreateSession(ctx, member.ID, agent.ID, "Visible chat title")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddMessage(ctx, session.ID, "user", "SECRET_MESSAGE_BODY", ""); err != nil {
		t.Fatal(err)
	}
	run, err := st.CreateRun(ctx, session.ID, "prompt")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateRunStatus(ctx, run.ID, store.RunSucceeded, "", `{"model":"gpt-5.6-sol","input_tokens":100,"cached_input_tokens":80,"output_tokens":5}`); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateSessionRemoteThread(ctx, session.ID, member.ID, "SECRET_REMOTE_THREAD"); err != nil {
		t.Fatal(err)
	}
	orchestration, err := st.CreateOrchestrationRun(ctx, store.CreateOrchestrationRunParams{
		UserID: member.ID, AgentID: agent.ID, Title: "Visible proof title", Mode: "collaboration",
		Prompt: "SECRET_PROMPT", CWD: "/SECRET/WORKSPACE/PATH", MaxTurns: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddOrchestrationEvent(ctx, store.OrchestrationEvent{
		RunID: orchestration.ID, Kind: "turn.delta", Role: "worker", Source: "cli", Content: "SECRET_ORCHESTRATION_MESSAGE",
	}); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Auth.BootstrapUsername = "admin"
	cfg.Auth.JWTSecret = "01234567890123456789012345678901"
	s := NewServer(&cfg, st, BuildInfo{})
	usageHandler := s.withAdmin(s.handleAdminUserUsage)
	requestUsage := func(userID, targetID string) *httptest.ResponseRecorder {
		token, _, signErr := s.signer.Sign(userID)
		if signErr != nil {
			t.Fatal(signErr)
		}
		r := httptest.NewRequest(http.MethodGet, "/api/admin/users/"+targetID+"/usage?days=0", nil)
		r.SetPathValue("userID", targetID)
		r.AddCookie(&http.Cookie{Name: accessCookieName, Value: token})
		w := httptest.NewRecorder()
		usageHandler(w, r)
		return w
	}
	if got := requestUsage(member.ID, member.ID); got.Code != http.StatusForbidden || !strings.Contains(got.Body.String(), "ADMIN_ONLY") {
		t.Fatalf("member detail status/body = %d %s, want 403 ADMIN_ONLY", got.Code, got.Body.String())
	}
	if got := requestUsage(admin.ID, "usr_missing"); got.Code != http.StatusNotFound {
		t.Fatalf("missing detail status = %d, want 404", got.Code)
	}
	got := requestUsage(admin.ID, member.ID)
	if got.Code != http.StatusOK {
		t.Fatalf("admin detail status = %d body=%s", got.Code, got.Body.String())
	}
	body := got.Body.String()
	for _, forbidden := range []string{"SECRET_MESSAGE_BODY", "SECRET_REMOTE_THREAD", "SECRET_PROMPT", "SECRET/WORKSPACE/PATH", "usageJson", "remoteThreadId"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("admin detail leaked %q: %s", forbidden, body)
		}
	}
	for _, expected := range []string{"Visible chat title", "Visible proof title", "member-laptop", `"totalTokens":105`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("admin detail missing %q: %s", expected, body)
		}
	}

	contentHandler := s.withAdmin(s.handleAdminConversationContent)
	requestContent := func(actorID, targetID, kind, conversationID string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/api/admin/users/"+targetID+"/conversations/"+kind+"/"+conversationID, nil)
		r.SetPathValue("userID", targetID)
		r.SetPathValue("kind", kind)
		r.SetPathValue("conversationID", conversationID)
		if actorID != "" {
			token, _, signErr := s.signer.Sign(actorID)
			if signErr != nil {
				t.Fatal(signErr)
			}
			r.AddCookie(&http.Cookie{Name: accessCookieName, Value: token})
		}
		w := httptest.NewRecorder()
		contentHandler(w, r)
		return w
	}
	if got := requestContent("", member.ID, "chat", session.ID); got.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated content status = %d, want 401", got.Code)
	}
	if got := requestContent(member.ID, member.ID, "chat", session.ID); got.Code != http.StatusForbidden || !strings.Contains(got.Body.String(), "ADMIN_ONLY") {
		t.Fatalf("member content status/body = %d %s, want 403 ADMIN_ONLY", got.Code, got.Body.String())
	}
	if got := requestContent(admin.ID, admin.ID, "chat", session.ID); got.Code != http.StatusNotFound {
		t.Fatalf("wrong-owner content status = %d, want 404", got.Code)
	}
	chat := requestContent(admin.ID, member.ID, "chat", session.ID)
	if chat.Code != http.StatusOK || !strings.Contains(chat.Body.String(), "SECRET_MESSAGE_BODY") {
		t.Fatalf("admin chat content status/body = %d %s", chat.Code, chat.Body.String())
	}
	orchestrationContent := requestContent(admin.ID, member.ID, "orchestration", orchestration.ID)
	if orchestrationContent.Code != http.StatusOK {
		t.Fatalf("admin orchestration content status/body = %d %s", orchestrationContent.Code, orchestrationContent.Body.String())
	}
	for _, expected := range []string{"SECRET_PROMPT", "SECRET_ORCHESTRATION_MESSAGE"} {
		if !strings.Contains(orchestrationContent.Body.String(), expected) {
			t.Fatalf("admin orchestration content missing %q: %s", expected, orchestrationContent.Body.String())
		}
	}
	for _, forbidden := range []string{"SECRET/WORKSPACE/PATH", "SECRET_REMOTE_THREAD", "remoteThreadId", "codexThreadId", "runCwd"} {
		if strings.Contains(chat.Body.String(), forbidden) || strings.Contains(orchestrationContent.Body.String(), forbidden) {
			t.Fatalf("admin conversation content leaked %q", forbidden)
		}
	}
}

func TestNormalizeAdminUsageEventChatCodexSeparatesCachedInput(t *testing.T) {
	usage, ok := normalizeAdminUsageEvent(store.AdminUsageEvent{CLI: "codex", UsageJSON: `{"input_tokens":100,"cached_input_tokens":80,"output_tokens":5}`})
	if !ok || usage.InputTokens != 20 || usage.CacheTokens != 80 || usage.OutputTokens != 5 || usage.TotalTokens != 105 || !usage.CostKnown {
		t.Fatalf("unexpected normalized usage: %#v ok=%v", usage, ok)
	}
}

func TestAddAdminUsageIgnoresUsersWithoutCalls(t *testing.T) {
	total := adminUsageTotals{}
	addAdminUsage(&total, adminUsageTotals{InputTokens: 5, TotalTokens: 5, CallCount: 1, CostKnown: true})
	addAdminUsage(&total, adminUsageTotals{CostKnown: false})
	if total.CallCount != 1 || total.TotalTokens != 5 || !total.CostKnown {
		t.Fatalf("zero-call row changed aggregate: %#v", total)
	}
}
