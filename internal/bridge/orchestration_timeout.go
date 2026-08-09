package bridge

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tencent/codex-bridge/internal/protocol"
)

const explicitCommandTimeoutGrace = 15 * time.Second

var errExplicitCommandTimeout = errors.New("explicit command timeout exceeded")

type explicitTimeoutWatchdogs struct {
	manager   *OrchestrationManager
	runID     string
	turnID    string
	role      string
	cli       string
	grace     time.Duration
	interrupt func()

	mu      sync.Mutex
	active  map[string]contextCancel
	fired   bool
	command string
	limit   time.Duration
}

type contextCancel func()

func newExplicitTimeoutWatchdogs(manager *OrchestrationManager, runID, turnID, role, cli string, interrupt func()) *explicitTimeoutWatchdogs {
	return &explicitTimeoutWatchdogs{
		manager:   manager,
		runID:     runID,
		turnID:    turnID,
		role:      role,
		cli:       cli,
		grace:     explicitCommandTimeoutGrace,
		interrupt: interrupt,
		active:    make(map[string]contextCancel),
	}
}

func (w *explicitTimeoutWatchdogs) Observe(tool *RunnerToolEvent) {
	if w == nil || tool == nil || strings.TrimSpace(tool.ID) == "" {
		return
	}
	if !isRunningToolStatus(tool.Status) {
		w.cancel(tool.ID)
		return
	}
	if tool.Progress {
		return
	}
	limit, ok := explicitShellTimeout(tool.Command)
	if !ok {
		return
	}
	w.mu.Lock()
	if w.fired || w.active[tool.ID] != nil {
		w.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	var once sync.Once
	cancel := func() { once.Do(func() { close(stop) }) }
	w.active[tool.ID] = cancel
	grace := w.grace
	w.mu.Unlock()

	go func(command, toolID string, declared time.Duration) {
		timer := time.NewTimer(declared + grace)
		defer timer.Stop()
		select {
		case <-stop:
			return
		case <-timer.C:
		}

		w.mu.Lock()
		if w.fired || w.active[toolID] == nil {
			w.mu.Unlock()
			return
		}
		w.fired = true
		w.command = command
		w.limit = declared
		delete(w.active, toolID)
		w.mu.Unlock()

		w.manager.emit(w.runID, protocol.OrchestrationEventPayload{
			Kind:     "turn.delta",
			Source:   "bridge",
			Severity: "error",
			TurnID:   w.turnID,
			Role:     w.role,
			CLI:      w.cli,
			Content:  fmt.Sprintf("Bridge interrupted the current %s turn because its command exceeded the explicit timeout boundary (%s plus %s convergence grace). The command will not be replayed automatically.", w.cli, declared, grace),
			BridgeNoteData: &protocol.BridgeNoteData{
				Category:     "explicit-command-timeout",
				Command:      command,
				AfterSeconds: int((declared + grace).Seconds()),
			},
			Data: map[string]any{
				"category":        "explicit-command-timeout",
				"command":         command,
				"timeoutSeconds":  int(declared.Seconds()),
				"graceSeconds":    int(grace.Seconds()),
				"automaticReplay": false,
				"relayOnly":       true,
			},
		})
		if w.interrupt != nil {
			w.interrupt()
		}
	}(strings.TrimSpace(tool.Command), tool.ID, limit)
}

func (w *explicitTimeoutWatchdogs) cancel(toolID string) {
	w.mu.Lock()
	cancel := w.active[toolID]
	delete(w.active, toolID)
	w.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (w *explicitTimeoutWatchdogs) Close() {
	if w == nil {
		return
	}
	w.mu.Lock()
	cancels := make([]contextCancel, 0, len(w.active))
	for _, cancel := range w.active {
		cancels = append(cancels, cancel)
	}
	w.active = make(map[string]contextCancel)
	w.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (w *explicitTimeoutWatchdogs) Err() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.fired {
		return nil
	}
	return fmt.Errorf("%w after %s: %s", errExplicitCommandTimeout, w.limit, trimForPrompt(w.command, 1000))
}

func explicitShellTimeout(command string) (time.Duration, bool) {
	fields := strings.Fields(command)
	for index, raw := range fields {
		token := trimShellToken(raw)
		if token != "timeout" || !timeoutTokenAtCommandBoundary(fields, index) {
			continue
		}
		if duration, ok := parseTimeoutInvocation(fields[index+1:]); ok {
			return duration, true
		}
	}
	return 0, false
}

func timeoutTokenAtCommandBoundary(fields []string, index int) bool {
	if index == 0 {
		return true
	}
	previous := strings.TrimSpace(fields[index-1])
	trimmed := trimShellToken(previous)
	if trimmed == "-c" || trimmed == "-lc" || trimmed == "--command" {
		return true
	}
	return strings.HasSuffix(previous, ";") || strings.HasSuffix(previous, "&&") ||
		strings.HasSuffix(previous, "||") || strings.HasSuffix(previous, "|") ||
		strings.HasSuffix(previous, "(")
}

func parseTimeoutInvocation(fields []string) (time.Duration, bool) {
	var killAfter time.Duration
	for index := 0; index < len(fields); index++ {
		token := trimShellToken(fields[index])
		switch {
		case token == "--foreground" || token == "--preserve-status" || token == "--verbose":
			continue
		case strings.HasPrefix(token, "--kill-after="):
			value, ok := parseGNUTimeoutDuration(strings.TrimPrefix(token, "--kill-after="))
			if !ok {
				return 0, false
			}
			killAfter = value
			continue
		case token == "--kill-after" || token == "-k":
			index++
			if index >= len(fields) {
				return 0, false
			}
			value, ok := parseGNUTimeoutDuration(trimShellToken(fields[index]))
			if !ok {
				return 0, false
			}
			killAfter = value
			continue
		case strings.HasPrefix(token, "--signal="):
			continue
		case token == "--signal" || token == "-s":
			index++
			if index >= len(fields) {
				return 0, false
			}
			continue
		case strings.HasPrefix(token, "-"):
			return 0, false
		default:
			value, ok := parseGNUTimeoutDuration(token)
			if !ok || value <= 0 {
				return 0, false
			}
			return value + killAfter, true
		}
	}
	return 0, false
}

func parseGNUTimeoutDuration(value string) (time.Duration, bool) {
	value = strings.TrimSpace(strings.Trim(value, "\"'`;|&)"))
	if value == "" {
		return 0, false
	}
	if value[len(value)-1] >= '0' && value[len(value)-1] <= '9' {
		value += "s"
	}
	duration, err := time.ParseDuration(value)
	return duration, err == nil && duration > 0
}

func trimShellToken(value string) string {
	return strings.Trim(strings.TrimSpace(value), "\"'`()")
}
