package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/protocol"
	"golang.org/x/crypto/bcrypt"
)

func TestStoreUserAgentSessionMessageFlow(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	user, err := st.UpsertUser(ctx, "admin", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AuthenticateUser(ctx, "admin", "bad"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected unauthorized, got %v", err)
	}
	if _, err := st.AuthenticateUser(ctx, "admin", "secret"); err != nil {
		t.Fatal(err)
	}
	var hash string
	if err := st.db.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE username = ?`, "admin").Scan(&hash); err != nil {
		t.Fatal(err)
	}
	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil {
		t.Fatal(err)
	}
	if cost < passwordHashCost {
		t.Fatalf("bcrypt cost = %d, want at least %d", cost, passwordHashCost)
	}

	agent, err := st.UpsertAgent(ctx, "bridge", "machine-1", "host", "inst-1", []string{"/work", "/work/project"})
	if err != nil {
		t.Fatal(err)
	}
	if agent.Instance != "inst-1" {
		t.Fatalf("agent instance = %q", agent.Instance)
	}
	if len(agent.WorkingDirs) != 2 || agent.WorkingDirs[0] != "/work" || agent.WorkingDirs[1] != "/work/project" {
		t.Fatalf("agent working dirs = %#v", agent.WorkingDirs)
	}
	agent, err = st.UpsertAgent(ctx, "bridge", "machine-1", "host", "inst-2", []string{"/next", "/next", " "})
	if err != nil {
		t.Fatal(err)
	}
	if agent.Instance != "inst-2" {
		t.Fatalf("updated agent instance = %q", agent.Instance)
	}
	if len(agent.WorkingDirs) != 1 || agent.WorkingDirs[0] != "/next" {
		t.Fatalf("updated agent working dirs = %#v", agent.WorkingDirs)
	}
	if err := st.TouchAgentWorkingDirs(ctx, agent.ID, []string{"/next", "/fresh", "/fresh", " "}); err != nil {
		t.Fatal(err)
	}
	agent, err = st.AgentByID(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(agent.WorkingDirs) != 2 || agent.WorkingDirs[0] != "/next" || agent.WorkingDirs[1] != "/fresh" {
		t.Fatalf("heartbeat-refreshed working dirs = %#v", agent.WorkingDirs)
	}
	session, err := st.CreateSession(ctx, user.ID, agent.ID, "chat")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateSessionRemoteThread(ctx, session.ID, user.ID, "thread-1"); err != nil {
		t.Fatal(err)
	}
	renamed, err := st.UpdateSessionTitle(ctx, session.ID, user.ID, "  renamed chat  ")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Title != "renamed chat" {
		t.Fatalf("renamed session title = %q", renamed.Title)
	}
	if _, err := st.AddMessage(ctx, session.ID, "user", "hello", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddMessage(ctx, session.ID, "assistant", "world", `{"output_tokens":1}`); err != nil {
		t.Fatal(err)
	}
	messages, err := st.ListMessages(ctx, session.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0].Role != "user" || messages[1].UsageJSON == "" {
		t.Fatalf("unexpected messages: %#v", messages)
	}

	run, err := st.CreateRun(ctx, session.ID, "prompt-1")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != RunRunning || run.PromptID != "prompt-1" {
		t.Fatalf("unexpected run: %#v", run)
	}
	if _, err := st.CreateRun(ctx, session.ID, "prompt-1"); err == nil {
		t.Fatal("expected duplicate prompt id to fail")
	} else if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	active, err := st.ActiveRunBySession(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if active.ID != run.ID {
		t.Fatalf("active run = %#v, want %#v", active, run)
	}
	if err := st.UpdateRunStatus(ctx, run.ID, RunSucceeded, "", `{"output_tokens":1}`); err != nil {
		t.Fatal(err)
	}
	updated, err := st.RunByID(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != RunSucceeded || updated.FinishedAt == 0 || updated.UsageJSON == "" {
		t.Fatalf("updated run = %#v", updated)
	}
	if _, err := st.ActiveRunBySession(ctx, session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected no active run, got %v", err)
	}

	run2, err := st.CreateRun(ctx, session.ID, "prompt-2")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := st.MarkUnfinishedRunsFailed(ctx, "restart"); err != nil {
		t.Fatal(err)
	} else if n != 1 {
		t.Fatalf("marked unfinished runs = %d, want 1", n)
	}
	restarted, err := st.RunByID(ctx, run2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.Status != RunFailed || restarted.Error != "restart" || restarted.FinishedAt == 0 {
		t.Fatalf("restart-marked run = %#v", restarted)
	}

	run3, err := st.CreateRun(ctx, session.ID, "prompt-3")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := st.MarkActiveRunsForAgentFailed(ctx, agent.ID, "offline"); err != nil {
		t.Fatal(err)
	} else if n != 1 {
		t.Fatalf("marked agent runs = %d, want 1", n)
	}
	offline, err := st.RunByID(ctx, run3.ID)
	if err != nil {
		t.Fatal(err)
	}
	if offline.Status != RunFailed || offline.Error != "offline" {
		t.Fatalf("offline-marked run = %#v", offline)
	}
	orchestration, err := st.CreateOrchestrationRun(ctx, CreateOrchestrationRunParams{
		UserID:   user.ID,
		AgentID:  agent.ID,
		Title:    "active orchestration",
		Mode:     "collaboration",
		FirstCLI: "codex",
		Prompt:   "prove it",
		MaxTurns: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if orchestration.FirstCLI != "codex" {
		t.Fatalf("orchestration first cli = %q, want codex", orchestration.FirstCLI)
	}
	if err := st.UpdateOrchestrationRunStatus(ctx, orchestration.ID, OrchestrationRunning, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddOrchestrationEvent(ctx, OrchestrationEvent{
		RunID:   orchestration.ID,
		Kind:    "turn.delta",
		TurnID:  "turn-1",
		Role:    "implementer",
		CLI:     "claude",
		Content: "已创建可见项目目录并定位 ROOT 的 HWQ-U 布局。",
	}); err != nil {
		t.Fatal(err)
	}
	markedOrchestrations, err := st.MarkActiveOrchestrationRunsForAgentFailed(ctx, agent.ID, "offline orchestration")
	if err != nil {
		t.Fatal(err)
	}
	if len(markedOrchestrations) != 1 || markedOrchestrations[0].ID != orchestration.ID {
		t.Fatalf("marked orchestrations = %#v, want %s", markedOrchestrations, orchestration.ID)
	}
	failedOrchestration, err := st.OrchestrationRunByID(ctx, orchestration.ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failedOrchestration.Status != OrchestrationFailed || failedOrchestration.Error != "offline orchestration" || failedOrchestration.FinishedAt == 0 {
		t.Fatalf("offline-marked orchestration = %#v", failedOrchestration)
	}
	orchestrationEvents, err := st.ListOrchestrationEvents(ctx, orchestration.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(orchestrationEvents) != 3 ||
		orchestrationEvents[1].Kind != "turn.end" ||
		orchestrationEvents[1].TurnID != "turn-1" ||
		orchestrationEvents[1].Status != "error" ||
		orchestrationEvents[1].Severity != "error" ||
		orchestrationEvents[1].Error != "offline orchestration" ||
		orchestrationEvents[2].Kind != "run.error" ||
		orchestrationEvents[2].Error != "offline orchestration" {
		t.Fatalf("offline orchestration events = %#v", orchestrationEvents)
	}
	if !strings.Contains(orchestrationEvents[1].Content, "本轮因 Bridge 连接") {
		t.Fatalf("offline orchestration turn.end content missing disconnect context: %q", orchestrationEvents[1].Content)
	}
	if !strings.Contains(orchestrationEvents[2].Content, "Bridge 连接") ||
		!strings.Contains(orchestrationEvents[2].Content, "不是证明任务已经通过或失败的验收结论") ||
		!strings.Contains(orchestrationEvents[2].Content, "已创建可见项目目录") {
		t.Fatalf("offline orchestration run.error content missing diagnostic context: %q", orchestrationEvents[2].Content)
	}
	if err := st.DeleteAgent(ctx, agent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AgentByID(ctx, agent.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected deleted agent to be hidden, got %v", err)
	}
	if _, err := st.AgentByMachineID(ctx, "machine-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected deleted machine to be hidden, got %v", err)
	}
	agents, err := st.ListAgents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range agents {
		if item.ID == agent.ID {
			t.Fatalf("deleted agent listed: %#v", agents)
		}
	}
	if _, err := st.SessionByID(ctx, session.ID, user.ID); err != nil {
		t.Fatalf("session history should remain after deleting agent: %v", err)
	}
	if _, err := st.UpsertAgent(ctx, "bridge", "machine-1", "host", "inst-3", nil); err != nil {
		t.Fatalf("re-enroll deleted machine: %v", err)
	}
	if err := st.DeleteSession(ctx, session.ID, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SessionByID(ctx, session.ID, user.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected deleted session to be missing, got %v", err)
	}
	messages, err = st.ListMessages(ctx, session.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 0 {
		t.Fatalf("expected cascade-deleted messages, got %#v", messages)
	}
}

func TestStoreRejectsQuotedEmptyPasswords(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	for _, password := range []string{`""`, `''`, ` "" `} {
		if _, err := st.UpsertUser(ctx, "admin", password); err == nil {
			t.Fatalf("UpsertUser accepted quoted empty password %q", password)
		}
		if _, err := st.CreateUser(ctx, "member", password); err == nil {
			t.Fatalf("CreateUser accepted quoted empty password %q", password)
		}
	}
}

func TestStoreOrchestrationRunEventFlow(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	user, err := st.UpsertUser(ctx, "admin", "secret")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := st.UpsertAgent(ctx, "bridge", "machine-orc", "host", "inst", nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.CreateOrchestrationRun(ctx, CreateOrchestrationRunParams{
		UserID:   user.ID,
		AgentID:  agent.ID,
		Title:    "Debate",
		Mode:     "debate",
		FirstCLI: "codex",
		Profile:  "formal-proof",
		Prompt:   "prove a theorem",
		MaxTurns: 2,
		Files:    []OrchestrationFile{{Name: "A.v", MimeType: "text/plain", Size: 10}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != OrchestrationQueued || run.Mode != "debate" || run.FirstCLI != "codex" || run.Profile != "formal-proof" || len(run.Files) != 1 {
		t.Fatalf("unexpected run: %+v", run)
	}
	if _, err := st.AddOrchestrationEvent(ctx, OrchestrationEvent{
		RunID:  run.ID,
		Kind:   "turn.start",
		Role:   "proposer",
		CLI:    "claude",
		Source: "bridge",
		TurnStartData: &protocol.TurnStartData{
			CLI:        "claude",
			Turn:       1,
			MaxTurns:   2,
			PromptText: "secret prompt",
			Profile:    "formal-proof",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddOrchestrationEvent(ctx, OrchestrationEvent{
		RunID: run.ID,
		Kind:  "command.end",
		Role:  "proposer",
		CLI:   "claude",
		CommandData: &protocol.CommandData{
			ID:       "cmd_1",
			Command:  "go test ./...",
			Status:   "completed",
			Output:   "ok",
			ExitCode: intPtr(0),
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddOrchestrationEvent(ctx, OrchestrationEvent{RunID: run.ID, Kind: "turn.end", Role: "proposer", CLI: "claude", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	events, err := st.ListOrchestrationEvents(ctx, run.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].Seq != 1 || events[1].Seq != 2 || events[2].Seq != 3 {
		t.Fatalf("unexpected events: %+v", events)
	}
	if events[0].Source != "bridge" || events[0].TurnStartData == nil || events[0].TurnStartData.PromptText != "secret prompt" {
		t.Fatalf("turn typed data did not round-trip: %+v", events[0])
	}
	if events[1].CommandData == nil || events[1].CommandData.Command != "go test ./..." || events[1].CommandData.ExitCode == nil || *events[1].CommandData.ExitCode != 0 {
		t.Fatalf("command typed data did not round-trip: %+v", events[1])
	}
	if err := st.UpdateOrchestrationRunStatus(ctx, run.ID, OrchestrationCompleted, ""); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.OrchestrationRunByID(ctx, run.ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != OrchestrationCompleted || loaded.FinishedAt == 0 {
		t.Fatalf("unexpected loaded run: %+v", loaded)
	}
	if err := st.UpdateOrchestrationRunSettings(ctx, run.ID, agent.ID, run.Mode, run.WorkerPair, "claude", run.Profile, run.CWD, "after-turn", run.MaxTurns, run.Files); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateOrchestrationRunStatus(ctx, run.ID, OrchestrationRunning, ""); err != nil {
		t.Fatal(err)
	}
	loaded, err = st.OrchestrationRunByID(ctx, run.ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != OrchestrationRunning || loaded.FinishedAt != 0 {
		t.Fatalf("resumed run kept terminal state: %+v", loaded)
	}
	if loaded.FirstCLI != "claude" || loaded.Profile != "formal-proof" || loaded.NativeContextCompaction != "after-turn" {
		t.Fatalf("resumed settings = %+v", loaded)
	}
	if err := st.UpdateOrchestrationRunSession(ctx, run.ID, "thread_1", true, "/abs/repo"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateOrchestrationRunSession(ctx, run.ID, "", false, "/abs/other"); err != nil {
		t.Fatal(err)
	}
	loaded, err = st.OrchestrationRunByID(ctx, run.ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CodexThreadID != "thread_1" || !loaded.ClaudeStarted || loaded.RunCWD != "/abs/repo" {
		t.Fatalf("session state not preserved: %+v", loaded)
	}
	if loaded.CodexThreadIDs["codex"] != "thread_1" {
		t.Fatalf("legacy codex thread map not preserved: %+v", loaded.CodexThreadIDs)
	}
	if err := st.UpdateOrchestrationRunSessionState(ctx, run.ID, "", map[string]string{"codex-a": "thread_a", "codex-b": "thread_b"}, false, ""); err != nil {
		t.Fatal(err)
	}
	loaded, err = st.OrchestrationRunByID(ctx, run.ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CodexThreadIDs["codex"] != "thread_1" || loaded.CodexThreadIDs["codex-a"] != "thread_a" || loaded.CodexThreadIDs["codex-b"] != "thread_b" {
		t.Fatalf("codex thread map not merged: %+v", loaded.CodexThreadIDs)
	}
}

func TestListOrchestrationEventsReturnsLatestWindowInAscendingOrder(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	user, err := st.UpsertUser(ctx, "admin", "secret")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := st.UpsertAgent(ctx, "bridge", "machine-window", "host", "inst", nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.CreateOrchestrationRun(ctx, CreateOrchestrationRunParams{
		UserID:   user.ID,
		AgentID:  agent.ID,
		Title:    "Window",
		Mode:     "collaboration",
		Prompt:   "check latest",
		MaxTurns: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := st.AddOrchestrationEvent(ctx, OrchestrationEvent{RunID: run.ID, Kind: "turn.delta", Content: "event"}); err != nil {
			t.Fatal(err)
		}
	}
	events, err := st.ListOrchestrationEvents(ctx, run.ID, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].Seq != 3 || events[1].Seq != 4 || events[2].Seq != 5 {
		t.Fatalf("unexpected latest window: %+v", events)
	}
	events, err = st.ListOrchestrationEventsAfter(ctx, run.ID, 3, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Seq != 4 || events[1].Seq != 5 {
		t.Fatalf("unexpected after-seq window: %+v", events)
	}
	events, err = st.ListOrchestrationEventsAfter(ctx, run.ID, 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Seq != 4 {
		t.Fatalf("unexpected limited after-seq window: %+v", events)
	}
	events, err = st.ListOrchestrationEventsBefore(ctx, run.ID, 4, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].Seq != 1 || events[1].Seq != 2 || events[2].Seq != 3 {
		t.Fatalf("unexpected before-seq window: %+v", events)
	}
	events, err = st.ListOrchestrationEventsBefore(ctx, run.ID, 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Seq != 2 || events[1].Seq != 3 {
		t.Fatalf("unexpected limited before-seq window: %+v", events)
	}
}

func TestListOrchestrationRunsByAgentFiltersAndLimits(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	user, err := st.UpsertUser(ctx, "admin", "secret")
	if err != nil {
		t.Fatal(err)
	}
	agentA, err := st.UpsertAgent(ctx, "bridge-a", "machine-runs-a", "host", "inst-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	agentB, err := st.UpsertAgent(ctx, "bridge-b", "machine-runs-b", "host", "inst-b", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateOrchestrationRun(ctx, CreateOrchestrationRunParams{
		UserID:   user.ID,
		AgentID:  agentA.ID,
		Title:    "A1",
		Mode:     "collaboration",
		Prompt:   "agent a first",
		MaxTurns: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateOrchestrationRun(ctx, CreateOrchestrationRunParams{
		UserID:   user.ID,
		AgentID:  agentA.ID,
		Title:    "A2",
		Mode:     "collaboration",
		Prompt:   "agent a second",
		MaxTurns: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateOrchestrationRun(ctx, CreateOrchestrationRunParams{
		UserID:   user.ID,
		AgentID:  agentB.ID,
		Title:    "B1",
		Mode:     "collaboration",
		Prompt:   "agent b only",
		MaxTurns: 2,
	}); err != nil {
		t.Fatal(err)
	}

	runs, err := st.ListOrchestrationRunsByAgent(ctx, user.ID, agentA.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("agent A runs = %+v", runs)
	}
	for _, run := range runs {
		if run.AgentID != agentA.ID {
			t.Fatalf("run from wrong agent returned: %+v", run)
		}
	}
	limited, err := st.ListOrchestrationRunsByAgent(ctx, user.ID, agentA.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 || limited[0].AgentID != agentA.ID {
		t.Fatalf("limited agent A runs = %+v", limited)
	}
}

func TestStoreConversationShareFlow(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	user, err := st.UpsertUser(ctx, "admin", "secret")
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.UpsertUser(ctx, "other", "secret")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := st.UpsertAgent(ctx, "bridge", "machine-share", "host", "inst", nil)
	if err != nil {
		t.Fatal(err)
	}
	session, err := st.CreateSession(ctx, user.ID, agent.ID, "chat")
	if err != nil {
		t.Fatal(err)
	}
	share, err := st.CreateOrUpdateConversationShare(ctx, user.ID, ShareKindChat, session.ID, "  Chat title  ")
	if err != nil {
		t.Fatal(err)
	}
	if share.Kind != ShareKindChat || share.TargetID != session.ID || share.Title != "Chat title" || share.CreatedAt == 0 || share.UpdatedAt == 0 {
		t.Fatalf("unexpected chat share: %+v", share)
	}
	loaded, err := st.ActiveConversationShareByID(ctx, share.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != share.ID || loaded.UserID != user.ID {
		t.Fatalf("loaded share = %+v, want %+v", loaded, share)
	}
	reused, err := st.CreateOrUpdateConversationShare(ctx, user.ID, ShareKindChat, session.ID, "Renamed")
	if err != nil {
		t.Fatal(err)
	}
	if reused.ID != share.ID || reused.Title != "Renamed" {
		t.Fatalf("share was not reused with updated title: %+v", reused)
	}
	if err := st.RevokeConversationShare(ctx, share.ID, other.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected wrong-owner revoke to be not found, got %v", err)
	}
	if _, err := st.ActiveConversationShareByID(ctx, share.ID); err != nil {
		t.Fatalf("wrong-owner revoke hid share: %v", err)
	}
	if err := st.RevokeConversationShare(ctx, share.ID, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ActiveConversationShareByID(ctx, share.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected revoked share to be hidden, got %v", err)
	}
	recreated, err := st.CreateOrUpdateConversationShare(ctx, user.ID, ShareKindChat, session.ID, "Chat title")
	if err != nil {
		t.Fatal(err)
	}
	if recreated.ID == share.ID {
		t.Fatalf("revoked share was reused: old=%s new=%s", share.ID, recreated.ID)
	}

	run, err := st.CreateOrchestrationRun(ctx, CreateOrchestrationRunParams{
		UserID:   user.ID,
		AgentID:  agent.ID,
		Title:    "Orchestration",
		Mode:     "collaboration",
		Prompt:   "prove it",
		MaxTurns: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	orchestrationShare, err := st.CreateOrUpdateConversationShare(ctx, user.ID, ShareKindOrchestration, run.ID, run.Title)
	if err != nil {
		t.Fatal(err)
	}
	if orchestrationShare.Kind != ShareKindOrchestration || orchestrationShare.TargetID != run.ID {
		t.Fatalf("unexpected orchestration share: %+v", orchestrationShare)
	}
	if _, err := st.CreateOrUpdateConversationShare(ctx, user.ID, "bad", run.ID, run.Title); err == nil {
		t.Fatal("expected invalid share kind to fail")
	}

	loadedSession, err := st.SessionByIDAnyUser(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loadedSession.ID != session.ID || loadedSession.UserID != user.ID {
		t.Fatalf("session any-user lookup = %+v", loadedSession)
	}
}

func TestConsumeEnrollToken(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	expires := time.Now().Add(time.Hour)
	if err := st.CreateEnrollToken(ctx, "token-1", &expires); err != nil {
		t.Fatal(err)
	}
	if err := st.ConsumeEnrollToken(ctx, "token-1", "machine-1"); err != nil {
		t.Fatal(err)
	}
	if err := st.ConsumeEnrollToken(ctx, "token-1", "machine-1"); err != nil {
		t.Fatal(err)
	}
	if err := st.ConsumeEnrollToken(ctx, "token-1", "machine-2"); !errors.Is(err, ErrTokenConsumed) {
		t.Fatalf("expected consumed, got %v", err)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return st
}

func intPtr(value int) *int {
	return &value
}

func createTestOrchestrationRun(t *testing.T, st *Store, userID, agentID, title string) OrchestrationRun {
	t.Helper()
	run, err := st.CreateOrchestrationRun(context.Background(), CreateOrchestrationRunParams{
		UserID:   userID,
		AgentID:  agentID,
		Title:    title,
		Mode:     "collaboration",
		FirstCLI: "codex",
		Prompt:   "task",
		MaxTurns: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestMarkUnfinishedOrchestrationRunsFailedAtBoot(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	user, err := st.UpsertUser(ctx, "admin", "secret")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := st.UpsertAgent(ctx, "bridge", "machine-boot", "host", "inst", nil)
	if err != nil {
		t.Fatal(err)
	}

	running := createTestOrchestrationRun(t, st, user.ID, agent.ID, "running")
	if err := st.UpdateOrchestrationRunStatus(ctx, running.ID, OrchestrationRunning, ""); err != nil {
		t.Fatal(err)
	}
	canceling := createTestOrchestrationRun(t, st, user.ID, agent.ID, "canceling")
	if err := st.UpdateOrchestrationRunStatus(ctx, canceling.ID, OrchestrationCanceling, ""); err != nil {
		t.Fatal(err)
	}
	completed := createTestOrchestrationRun(t, st, user.ID, agent.ID, "completed")
	if err := st.UpdateOrchestrationRunStatus(ctx, completed.ID, OrchestrationCompleted, ""); err != nil {
		t.Fatal(err)
	}

	marked, err := st.MarkUnfinishedOrchestrationRunsFailed(ctx, "hub restarted")
	if err != nil {
		t.Fatal(err)
	}
	if len(marked) != 1 || marked[0].ID != running.ID {
		t.Fatalf("marked runs = %#v, want only %s", marked, running.ID)
	}

	swept, err := st.OrchestrationRunByID(ctx, running.ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if swept.Status != OrchestrationFailed || swept.Error != "hub restarted" || swept.FinishedAt == 0 {
		t.Fatalf("boot-swept running run = %#v", swept)
	}
	events, err := st.ListOrchestrationEvents(ctx, running.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	sawRunError := false
	for _, event := range events {
		if event.Kind == "run.error" && event.RunConclusion != nil {
			sawRunError = true
		}
	}
	if !sawRunError {
		t.Fatalf("boot sweep left no run.error conclusion event: %#v", events)
	}

	settled, err := st.OrchestrationRunByID(ctx, canceling.ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if settled.Status != OrchestrationCanceled || settled.FinishedAt == 0 {
		t.Fatalf("boot-swept canceling run = %#v", settled)
	}

	untouched, err := st.OrchestrationRunByID(ctx, completed.ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if untouched.Status != OrchestrationCompleted {
		t.Fatalf("completed run changed by boot sweep: %#v", untouched)
	}
}

func TestClaimOrchestrationRunForContinue(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	user, err := st.UpsertUser(ctx, "admin", "secret")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := st.UpsertAgent(ctx, "bridge", "machine-claim", "host", "inst", nil)
	if err != nil {
		t.Fatal(err)
	}
	run := createTestOrchestrationRun(t, st, user.ID, agent.ID, "claim")
	if err := st.UpdateOrchestrationRunStatus(ctx, run.ID, OrchestrationCompleted, ""); err != nil {
		t.Fatal(err)
	}

	claimed, err := st.ClaimOrchestrationRunForContinue(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !claimed {
		t.Fatal("expected first claim of a terminal run to succeed")
	}
	claimedRun, err := st.OrchestrationRunByID(ctx, run.ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if claimedRun.Status != OrchestrationRunning {
		t.Fatalf("claimed run status = %q, want running", claimedRun.Status)
	}

	again, err := st.ClaimOrchestrationRunForContinue(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again {
		t.Fatal("expected second claim of an active run to fail")
	}
}

func TestAddOrchestrationEventKeepsTurnEndStatus(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	user, err := st.UpsertUser(ctx, "admin", "secret")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := st.UpsertAgent(ctx, "bridge", "machine-status", "host", "inst", nil)
	if err != nil {
		t.Fatal(err)
	}
	run := createTestOrchestrationRun(t, st, user.ID, agent.ID, "status")

	turnEnd, err := st.AddOrchestrationEvent(ctx, OrchestrationEvent{
		RunID:  run.ID,
		Kind:   "turn.end",
		TurnID: "turn-1",
		CLI:    "claude",
		Status: "error",
		Error:  "claude exited",
	})
	if err != nil {
		t.Fatal(err)
	}
	if turnEnd.Status != "error" || turnEnd.Severity != "error" {
		t.Fatalf("turn.end migration = status %q severity %q, want both error", turnEnd.Status, turnEnd.Severity)
	}

	note, err := st.AddOrchestrationEvent(ctx, OrchestrationEvent{
		RunID:   run.ID,
		Kind:    "turn.delta",
		TurnID:  "turn-1",
		CLI:     "claude",
		Status:  "warning",
		Content: "legacy log row",
	})
	if err != nil {
		t.Fatal(err)
	}
	if note.Status != "" || note.Severity != "warning" {
		t.Fatalf("legacy migration = status %q severity %q, want empty/warning", note.Status, note.Severity)
	}
}
