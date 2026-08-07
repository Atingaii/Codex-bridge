package bridge

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
)

type scriptedChatRunner struct {
	attempts []func(RunnerRequest, func(RunnerUpdate)) (RunnerResult, error)
	requests []RunnerRequest
}

type scriptedSessionRunner struct {
	scriptedChatRunner
	openCalls  int
	openErrors []error
	prompts    []PromptSessionRequest
}

func (r *scriptedSessionRunner) OpenSession(_ context.Context, req OpenSessionRequest) (SessionHandle, error) {
	r.openCalls++
	if index := r.openCalls - 1; index < len(r.openErrors) && r.openErrors[index] != nil {
		return SessionHandle{}, r.openErrors[index]
	}
	return SessionHandle{ACPSessionID: "acp-thread", NativeResumeID: "native-thread"}, nil
}

func (r *scriptedSessionRunner) Resume(_ context.Context, req ResumeRequest) (SessionHandle, error) {
	return r.OpenSession(context.Background(), OpenSessionRequest{SID: req.SID, CWD: req.CWD, RemoteThreadID: req.RemoteThreadID, Approvals: req.Approvals})
}

func (r *scriptedSessionRunner) PromptSession(_ context.Context, req PromptSessionRequest, onUpdate func(RunnerUpdate)) (RunnerResult, error) {
	r.prompts = append(r.prompts, req)
	index := len(r.prompts) - 1
	return r.attempts[index](RunnerRequest{SID: req.SID, Content: req.Content, RunID: req.RunID, PromptID: req.PromptID}, onUpdate)
}

func (r *scriptedSessionRunner) CloseSession(string) {}

func (r *scriptedChatRunner) Name() string { return "scripted" }
func (r *scriptedChatRunner) Close()       {}

func (r *scriptedChatRunner) Prompt(_ context.Context, req RunnerRequest, onUpdate func(RunnerUpdate)) (RunnerResult, error) {
	r.requests = append(r.requests, req)
	index := len(r.requests) - 1
	if index >= len(r.attempts) {
		return RunnerResult{}, errors.New("unexpected prompt attempt")
	}
	return r.attempts[index](req, onUpdate)
}

func TestSessionPromptCompletesFromStreamedTextWithoutRetry(t *testing.T) {
	runner := &scriptedChatRunner{attempts: []func(RunnerRequest, func(RunnerUpdate)) (RunnerResult, error){
		func(_ RunnerRequest, onUpdate func(RunnerUpdate)) (RunnerResult, error) {
			onUpdate(RunnerUpdate{Delta: "streamed final"})
			return RunnerResult{RemoteThreadID: "thread-1"}, nil
		},
	}}
	envelopes := runScriptedChatPrompt(t, runner, "original request")

	if len(runner.requests) != 1 {
		t.Fatalf("prompt attempts = %d, want 1", len(runner.requests))
	}
	complete := findPromptComplete(t, envelopes)
	if complete.Content != "streamed final" || complete.RemoteThreadID != "thread-1" {
		t.Fatalf("prompt complete = %#v", complete)
	}
}

func TestSessionPromptContinuesEmptyResponseOnSameThreadOnce(t *testing.T) {
	runner := &scriptedChatRunner{attempts: []func(RunnerRequest, func(RunnerUpdate)) (RunnerResult, error){
		func(_ RunnerRequest, _ func(RunnerUpdate)) (RunnerResult, error) {
			return RunnerResult{RemoteThreadID: "thread-1"}, nil
		},
		func(_ RunnerRequest, onUpdate func(RunnerUpdate)) (RunnerResult, error) {
			onUpdate(RunnerUpdate{Delta: "recovered final"})
			return RunnerResult{Content: "recovered final", RemoteThreadID: "thread-1"}, nil
		},
	}}
	envelopes := runScriptedChatPrompt(t, runner, "original unique request")

	if len(runner.requests) != 2 {
		t.Fatalf("prompt attempts = %d, want 2", len(runner.requests))
	}
	if runner.requests[1].RemoteThreadID != "thread-1" {
		t.Fatalf("continuation thread = %q", runner.requests[1].RemoteThreadID)
	}
	if strings.Contains(runner.requests[1].Content, "original unique request") {
		t.Fatalf("continuation replayed original prompt: %q", runner.requests[1].Content)
	}
	if !strings.Contains(runner.requests[1].Content, "do not repeat completed work") {
		t.Fatalf("continuation lacks side-effect guard: %q", runner.requests[1].Content)
	}
	if got := findPromptComplete(t, envelopes).Content; got != "recovered final" {
		t.Fatalf("completed content = %q", got)
	}
}

func TestSessionPromptRecoveryPreservesVisibleTextAndToolEvidence(t *testing.T) {
	runner := &scriptedChatRunner{attempts: []func(RunnerRequest, func(RunnerUpdate)) (RunnerResult, error){
		func(_ RunnerRequest, onUpdate func(RunnerUpdate)) (RunnerResult, error) {
			onUpdate(RunnerUpdate{Delta: "Work completed."})
			onUpdate(RunnerUpdate{Tool: &RunnerToolEvent{ID: "tool-1", Status: "completed", Command: "make verify", Output: "ok"}})
			return RunnerResult{RemoteThreadID: "thread-tools"}, errors.New("connection closed")
		},
		func(_ RunnerRequest, onUpdate func(RunnerUpdate)) (RunnerResult, error) {
			onUpdate(RunnerUpdate{Delta: " Final summary."})
			return RunnerResult{Content: "Final summary.", RemoteThreadID: "thread-tools"}, nil
		},
	}}
	envelopes := runScriptedChatPrompt(t, runner, "perform changes")

	continuation := runner.requests[1].Content
	for _, want := range []string{"Work completed.", "make verify", "do not blindly repeat"} {
		if !strings.Contains(continuation, want) {
			t.Fatalf("continuation missing %q: %q", want, continuation)
		}
	}
	if got := findPromptComplete(t, envelopes).Content; got != "Work completed. Final summary." {
		t.Fatalf("completed content = %q", got)
	}
}

func TestSessionPromptRecoveryStopsAfterOneContinuation(t *testing.T) {
	runner := &scriptedChatRunner{attempts: []func(RunnerRequest, func(RunnerUpdate)) (RunnerResult, error){
		func(_ RunnerRequest, _ func(RunnerUpdate)) (RunnerResult, error) {
			return RunnerResult{RemoteThreadID: "thread-empty"}, nil
		},
		func(_ RunnerRequest, _ func(RunnerUpdate)) (RunnerResult, error) {
			return RunnerResult{RemoteThreadID: "thread-empty"}, nil
		},
	}}
	envelopes := runScriptedChatPrompt(t, runner, "remain empty")

	if len(runner.requests) != 2 {
		t.Fatalf("prompt attempts = %d, want 2", len(runner.requests))
	}
	errPayload := findSessionError(t, envelopes)
	if errPayload.Code != "EMPTY_RESPONSE" {
		t.Fatalf("error = %#v", errPayload)
	}
}

func TestSessionPromptRecoveryReusesResidentSession(t *testing.T) {
	runner := &scriptedSessionRunner{scriptedChatRunner: scriptedChatRunner{attempts: []func(RunnerRequest, func(RunnerUpdate)) (RunnerResult, error){
		func(_ RunnerRequest, _ func(RunnerUpdate)) (RunnerResult, error) {
			return RunnerResult{}, errors.New("adapter response ended early")
		},
		func(_ RunnerRequest, _ func(RunnerUpdate)) (RunnerResult, error) {
			return RunnerResult{Content: "resident recovery"}, nil
		},
	}}}
	envelopes := runScriptedChatPrompt(t, runner, "resident request")

	if runner.openCalls != 1 || len(runner.prompts) != 2 {
		t.Fatalf("open calls = %d, prompt calls = %d", runner.openCalls, len(runner.prompts))
	}
	complete := findPromptComplete(t, envelopes)
	if complete.Content != "resident recovery" || complete.RemoteThreadID != "acp-thread" || complete.NativeResumeID != "native-thread" {
		t.Fatalf("prompt complete = %#v", complete)
	}
}

func TestSessionPromptRetriesResidentSessionOpenBeforePrompt(t *testing.T) {
	runner := &scriptedSessionRunner{
		scriptedChatRunner: scriptedChatRunner{attempts: []func(RunnerRequest, func(RunnerUpdate)) (RunnerResult, error){
			func(_ RunnerRequest, _ func(RunnerUpdate)) (RunnerResult, error) {
				return RunnerResult{Content: "opened safely"}, nil
			},
		}},
		openErrors: []error{errors.New("adapter exited during initialize")},
	}
	envelopes := runScriptedChatPrompt(t, runner, "open resident session")

	if runner.openCalls != 2 || len(runner.prompts) != 1 {
		t.Fatalf("open calls = %d, prompt calls = %d", runner.openCalls, len(runner.prompts))
	}
	if got := findPromptComplete(t, envelopes).Content; got != "opened safely" {
		t.Fatalf("completed content = %q", got)
	}
}

func TestChatPromptEvidenceBoundsContentAndTools(t *testing.T) {
	const contentLimit = 257
	evidence := newChatPromptEvidence(contentLimit)
	evidence.observe(RunnerUpdate{Delta: strings.Repeat("界", contentLimit)})
	if len(evidence.content) > contentLimit || strings.ToValidUTF8(evidence.contentText(), "") != evidence.contentText() {
		t.Fatalf("content evidence is not bounded valid UTF-8: %d bytes", len(evidence.content))
	}
	for index := 0; index < chatEvidenceToolLimit+3; index++ {
		evidence.observe(RunnerUpdate{Tool: &RunnerToolEvent{ID: string(rune('a' + index)), Command: strings.Repeat("x", chatEvidenceToolFieldLimit+50)}})
	}
	if len(evidence.tools) != chatEvidenceToolLimit {
		t.Fatalf("tool evidence count = %d", len(evidence.tools))
	}
	for _, tool := range evidence.tools {
		if len(tool.Command) > chatEvidenceToolFieldLimit {
			t.Fatalf("tool command was not bounded: %d bytes", len(tool.Command))
		}
	}
}

func TestSessionPromptPreservesTerminalContentToConfiguredLimit(t *testing.T) {
	terminal := strings.Repeat("a", 300*1024)
	runner := &scriptedChatRunner{attempts: []func(RunnerRequest, func(RunnerUpdate)) (RunnerResult, error){
		func(_ RunnerRequest, _ func(RunnerUpdate)) (RunnerResult, error) {
			return RunnerResult{Content: terminal, RemoteThreadID: "thread-long"}, nil
		},
	}}
	envelopes := runScriptedChatPrompt(t, runner, "long reply")
	if got := findPromptComplete(t, envelopes).Content; got != terminal {
		t.Fatalf("terminal content length = %d, want %d", len(got), len(terminal))
	}
}

func TestSessionPromptDoesNotRetryCancellation(t *testing.T) {
	runner := &scriptedChatRunner{attempts: []func(RunnerRequest, func(RunnerUpdate)) (RunnerResult, error){
		func(_ RunnerRequest, _ func(RunnerUpdate)) (RunnerResult, error) {
			return RunnerResult{RemoteThreadID: "thread-canceled"}, context.Canceled
		},
	}}
	envelopes := runScriptedChatPrompt(t, runner, "cancel me")

	if len(runner.requests) != 1 {
		t.Fatalf("prompt attempts = %d, want 1", len(runner.requests))
	}
	errPayload := findSessionError(t, envelopes)
	if errPayload.Code != "CANCELED" {
		t.Fatalf("error = %#v", errPayload)
	}
}

func runScriptedChatPrompt(t *testing.T, runner Runner, content string) []protocol.Envelope {
	t.Helper()
	cfg := config.Default()
	manager := NewSessionManager(&cfg)
	out := make(chan protocol.Envelope, 32)
	manager.sessions["session-1"] = &Session{
		sid:       "session-1",
		runner:    runner,
		out:       out,
		approvals: make(map[string]chan protocol.ApprovalResponsePayload),
	}
	manager.Prompt(context.Background(), "session-1", protocol.PromptPayload{
		Content:  content,
		RunID:    "run-1",
		PromptID: "prompt-1",
	}, out)

	var envelopes []protocol.Envelope
	for {
		select {
		case env := <-out:
			envelopes = append(envelopes, env)
		default:
			return envelopes
		}
	}
}

func findPromptComplete(t *testing.T, envelopes []protocol.Envelope) protocol.PromptCompletePayload {
	t.Helper()
	for _, env := range envelopes {
		if env.Type != protocol.TypePromptComplete {
			continue
		}
		payload, err := protocol.Decode[protocol.PromptCompletePayload](env)
		if err != nil {
			t.Fatal(err)
		}
		return payload
	}
	t.Fatalf("prompt_complete not found in %#v", envelopes)
	return protocol.PromptCompletePayload{}
}

func findSessionError(t *testing.T, envelopes []protocol.Envelope) protocol.ErrorPayload {
	t.Helper()
	for _, env := range envelopes {
		if env.Type != protocol.TypeError {
			continue
		}
		payload, err := protocol.Decode[protocol.ErrorPayload](env)
		if err != nil {
			t.Fatal(err)
		}
		return payload
	}
	t.Fatalf("error not found in %#v", envelopes)
	return protocol.ErrorPayload{}
}
