package bridge

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
)

func TestExplicitShellTimeoutParsesSupportedForms(t *testing.T) {
	cases := []struct {
		command string
		want    time.Duration
	}{
		{command: `timeout 120s make check`, want: 120 * time.Second},
		{command: `/bin/bash -lc "timeout --kill-after=10s 120s make check"`, want: 130 * time.Second},
		{command: `printf 'timeout 1s'`, want: 0},
		{command: `echo timeout 1s`, want: 0},
	}
	for _, tc := range cases {
		got, ok := explicitShellTimeout(tc.command)
		if tc.want == 0 {
			if ok {
				t.Errorf("explicitShellTimeout(%q) = %s, want no match", tc.command, got)
			}
			continue
		}
		if !ok || got != tc.want {
			t.Errorf("explicitShellTimeout(%q) = %s, %v; want %s, true", tc.command, got, ok, tc.want)
		}
	}
}

func TestExplicitTimeoutWatchdogInterruptsOnlyAfterBoundary(t *testing.T) {
	manager := NewOrchestrationManager(&config.Config{})
	out := make(chan protocol.Envelope, 4)
	manager.AttachOut(out)
	interrupted := make(chan struct{})
	watchdog := newExplicitTimeoutWatchdogs(manager, "run-timeout", "turn-timeout", "reviewer", "codex", func() {
		close(interrupted)
	})
	watchdog.grace = 5 * time.Millisecond
	watchdog.Observe(&RunnerToolEvent{ID: "command-1", Status: "running", Command: "timeout 10ms make check"})
	select {
	case <-interrupted:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("explicit timeout watchdog did not interrupt")
	}
	if watchdog.Err() == nil {
		t.Fatal("watchdog did not retain timeout error")
	}
	watchdog.Close()
}

func TestExplicitTimeoutWatchdogCancelsOnTerminalEvent(t *testing.T) {
	interrupted := make(chan struct{})
	watchdog := newExplicitTimeoutWatchdogs(NewOrchestrationManager(&config.Config{}), "", "", "", "", func() {
		close(interrupted)
	})
	watchdog.grace = 5 * time.Millisecond
	watchdog.Observe(&RunnerToolEvent{ID: "command-1", Status: "running", Command: "timeout 10ms make check"})
	watchdog.Observe(&RunnerToolEvent{ID: "command-1", Status: "completed", Command: "timeout 10ms make check"})
	time.Sleep(30 * time.Millisecond)
	select {
	case <-interrupted:
		t.Fatal("terminal command event did not cancel watchdog")
	default:
	}
	if watchdog.Err() != nil {
		t.Fatal("terminal command left timeout error")
	}
	watchdog.Close()
}

func TestCommandProgressEmitsUpdateKind(t *testing.T) {
	manager := NewOrchestrationManager(&config.Config{})
	out := make(chan protocol.Envelope, 2)
	manager.AttachOut(out)
	manager.emitTool("run-progress", "turn-progress", "reviewer", "codex", &RunnerToolEvent{
		ID: "cmd-1", Status: "running", Command: "make check", Progress: true, Output: "partial",
	})
	var env protocol.Envelope
	select {
	case env = <-out:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for coalesced command progress")
	}
	var event protocol.OrchestrationEventPayload
	if err := json.Unmarshal(env.Payload, &event); err != nil {
		t.Fatal(err)
	}
	if event.Kind != "command.update" {
		t.Fatalf("event kind = %q, want command.update", event.Kind)
	}
	if !strings.Contains(event.CommandData.Output, "partial") {
		t.Fatalf("event output = %q", event.CommandData.Output)
	}
}

func TestCommandProgressCoalescesAndFlushesBeforeEnd(t *testing.T) {
	manager := NewOrchestrationManager(&config.Config{})
	out := make(chan protocol.Envelope, 4)
	manager.AttachOut(out)
	for _, output := range []string{"first\n", "second\n", "third\n"} {
		manager.emitTool("run-progress", "turn-progress", "reviewer", "codex", &RunnerToolEvent{
			ID: "cmd-1", Status: "running", Command: "make check", Progress: true, Output: output,
		})
	}
	exitCode := 0
	manager.emitTool("run-progress", "turn-progress", "reviewer", "codex", &RunnerToolEvent{
		ID: "cmd-1", Status: "completed", Command: "make check", ExitCode: &exitCode,
	})

	progress := decodeOrchestrationEvent(t, <-out)
	terminal := decodeOrchestrationEvent(t, <-out)
	if progress.Kind != "command.update" || terminal.Kind != "command.end" {
		t.Fatalf("event order = %q, %q", progress.Kind, terminal.Kind)
	}
	if got, want := progress.CommandData.Output, "first\nsecond\nthird\n"; got != want {
		t.Fatalf("coalesced output = %q, want %q", got, want)
	}
	if got, _ := progress.Data["output"].(string); got != progress.CommandData.Output {
		t.Fatalf("legacy output = %q, commandData output = %q", got, progress.CommandData.Output)
	}
	select {
	case unexpected := <-out:
		t.Fatalf("unexpected extra event: %s", unexpected.Type)
	default:
	}
}

func TestCommandProgressKeepsCommandsSeparate(t *testing.T) {
	manager := NewOrchestrationManager(&config.Config{})
	out := make(chan protocol.Envelope, 4)
	manager.AttachOut(out)
	manager.emitTool("run-progress", "turn-progress", "reviewer", "codex", &RunnerToolEvent{
		ID: "cmd-1", Status: "running", Progress: true, Output: "one",
	})
	manager.emitTool("run-progress", "turn-progress", "reviewer", "codex", &RunnerToolEvent{
		ID: "cmd-2", Status: "running", Progress: true, Output: "two",
	})
	manager.flushAllCommandProgress()

	first := decodeOrchestrationEvent(t, <-out)
	second := decodeOrchestrationEvent(t, <-out)
	if first.CommandData.ID != "cmd-1" || first.CommandData.Output != "one" {
		t.Fatalf("first command progress = %#v", first.CommandData)
	}
	if second.CommandData.ID != "cmd-2" || second.CommandData.Output != "two" {
		t.Fatalf("second command progress = %#v", second.CommandData)
	}
}

func TestCommandStartAndEndRemainImmediate(t *testing.T) {
	manager := NewOrchestrationManager(&config.Config{})
	out := make(chan protocol.Envelope, 4)
	manager.AttachOut(out)
	manager.emitTool("run-lifecycle", "turn-lifecycle", "reviewer", "codex", &RunnerToolEvent{
		ID: "cmd-1", Status: "running", Command: "make check",
	})
	exitCode := 0
	manager.emitTool("run-lifecycle", "turn-lifecycle", "reviewer", "codex", &RunnerToolEvent{
		ID: "cmd-1", Status: "completed", Command: "make check", ExitCode: &exitCode,
	})

	if event := decodeOrchestrationEvent(t, <-out); event.Kind != "command.start" {
		t.Fatalf("first event kind = %q, want command.start", event.Kind)
	}
	if event := decodeOrchestrationEvent(t, <-out); event.Kind != "command.end" {
		t.Fatalf("second event kind = %q, want command.end", event.Kind)
	}
}

func decodeOrchestrationEvent(t *testing.T, env protocol.Envelope) protocol.OrchestrationEventPayload {
	t.Helper()
	var event protocol.OrchestrationEventPayload
	if err := json.Unmarshal(env.Payload, &event); err != nil {
		t.Fatal(err)
	}
	return event
}

func TestExplicitTimeoutWatchdogDoesNotInterruptUnboundedCommand(t *testing.T) {
	interrupted := make(chan struct{})
	watchdog := newExplicitTimeoutWatchdogs(NewOrchestrationManager(&config.Config{}), "", "", "", "", func() {
		close(interrupted)
	})
	watchdog.grace = time.Millisecond
	watchdog.Observe(&RunnerToolEvent{ID: "command-1", Status: "running", Command: "make check"})
	time.Sleep(20 * time.Millisecond)
	select {
	case <-interrupted:
		t.Fatal("command without an explicit timeout was interrupted")
	default:
	}
	if watchdog.Err() != nil {
		t.Fatal("unbounded command left a timeout error")
	}
	watchdog.Close()
}
