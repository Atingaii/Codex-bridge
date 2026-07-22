package hub

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

func TestEmptyPromptCompleteFailsRun(t *testing.T) {
	t.Parallel()

	s, st := newAuthTestServer(t)
	ctx := context.Background()
	user, err := st.UserByUsername(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := st.UpsertAgentForUser(ctx, user.ID, "bridge", "machine-empty", "host", "inst", nil)
	if err != nil {
		t.Fatal(err)
	}
	session, err := st.CreateSession(ctx, user.ID, agent.ID, "Empty reply")
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.CreateRun(ctx, session.ID, "prompt-empty")
	if err != nil {
		t.Fatal(err)
	}

	s.handleBridgeEnvelope(ctx, agent.ID, protocol.MustEnvelope(protocol.TypePromptComplete, session.ID, protocol.PromptCompletePayload{
		RunID: run.ID, PromptID: run.PromptID,
	}))

	got, err := st.RunByID(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.RunFailed || got.Error != "runner completed without an assistant response" {
		t.Fatalf("run = %#v", got)
	}
	messages, err := st.ListMessages(ctx, session.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 0 {
		t.Fatalf("empty completion persisted messages: %#v", messages)
	}
}

func TestBridgeCannotWriteForeignOrDeletedSession(t *testing.T) {
	t.Parallel()

	s, st := newAuthTestServer(t)
	ctx := context.Background()
	user, err := st.UserByUsername(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := st.UpsertAgentForUser(ctx, user.ID, "owner", "machine-owner", "host", "inst", nil)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := st.UpsertAgentForUser(ctx, user.ID, "foreign", "machine-foreign", "host", "inst", nil)
	if err != nil {
		t.Fatal(err)
	}
	session, err := st.CreateSession(ctx, user.ID, owner.ID, "Owned")
	if err != nil {
		t.Fatal(err)
	}

	s.handleBridgeEnvelope(ctx, foreign.ID, protocol.MustEnvelope(protocol.TypeSessionUpdate, session.ID, protocol.SessionUpdatePayload{Delta: "foreign"}))
	if buffered := s.consumeAssistantBuffer(session.ID); buffered != "" {
		t.Fatalf("foreign bridge populated assistant buffer: %q", buffered)
	}
	s.handleBridgeEnvelope(ctx, owner.ID, protocol.MustEnvelope(protocol.TypeSessionUpdate, session.ID, protocol.SessionUpdatePayload{Delta: "owned"}))
	if buffered := s.consumeAssistantBuffer(session.ID); buffered != "owned" {
		t.Fatalf("owner output was not buffered: %q", buffered)
	}
	if err := st.DeleteSession(ctx, session.ID, user.ID); err != nil {
		t.Fatal(err)
	}
	s.forgetSessionOwner(session.ID)
	s.handleBridgeEnvelope(ctx, owner.ID, protocol.MustEnvelope(protocol.TypeSessionUpdate, session.ID, protocol.SessionUpdatePayload{Delta: "late"}))
	if buffered := s.consumeAssistantBuffer(session.ID); buffered != "" {
		t.Fatalf("deleted session accepted late output: %q", buffered)
	}
}

func TestDeleteSessionCascadesAndClosesBridgeSession(t *testing.T) {
	t.Parallel()

	s, st := newAuthTestServer(t)
	ctx := context.Background()
	user, err := st.UserByUsername(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := st.UpsertAgentForUser(ctx, user.ID, "bridge", "machine-delete-session", "host", "inst", nil)
	if err != nil {
		t.Fatal(err)
	}
	session, err := st.CreateSession(ctx, user.ID, agent.ID, "Delete me")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddMessage(ctx, session.ID, "user", "hello", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateRun(ctx, session.ID, "prompt-delete"); err != nil {
		t.Fatal(err)
	}
	conn := &BridgeConn{agentID: agent.ID, wsSender: wsSender{send: make(chan protocol.Envelope, 2), done: make(chan struct{})}}
	s.pool.RegisterAgent(conn)

	authedJSON(t, s, user.ID, http.MethodDelete, "/api/sessions/"+session.ID, nil, http.StatusOK)

	select {
	case env := <-conn.send:
		if env.Type != protocol.TypeCloseSession || env.Sid != session.ID {
			t.Fatalf("close envelope = %#v", env)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for close_session")
	}
	if _, err := st.SessionByID(ctx, session.ID, user.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted session lookup = %v", err)
	}
	if messages, err := st.ListMessages(ctx, session.ID, 10); err != nil || len(messages) != 0 {
		t.Fatalf("messages after deletion = %#v, %v", messages, err)
	}
	if runs, err := st.ListRuns(ctx, session.ID, 10); err != nil || len(runs) != 0 {
		t.Fatalf("runs after deletion = %#v, %v", runs, err)
	}
}
