package bridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/tencent/codex-bridge/internal/protocol"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type orchestrationCodexSession struct {
	client   *appServerClient
	threadID string
	mode     string
	loaded   bool
}

type orchestrationMaintenanceResult struct {
	Content string
	Skipped bool
	Reason  string
	Err     error
}

type codexScanResult struct {
	Content       string
	Tools         []RunnerToolEvent
	ThreadID      string
	ThreadStarted bool
}

func (m *OrchestrationManager) runCodex(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID, role, prompt string) (string, []RunnerToolEvent, error) {
	content, tools, _, _, err := m.runCodexWithThread(ctx, payload, turnID, role, prompt, "")
	return content, tools, err
}

func (m *OrchestrationManager) runCodexInteractive(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID, role, workerSlot, prompt string, state *orchestrationSessionState) (string, []RunnerToolEvent, string, string, error) {
	if state == nil || state.NativeSession == nil {
		return m.runCodexWithThread(ctx, payload, turnID, role, prompt, "")
	}
	workerSlot = normalizeCodexWorkerSlot(workerSlot)
	session := state.NativeSession
	session.mu.Lock()
	defer session.mu.Unlock()
	codex, err := m.ensureCodexInteractiveSessionLocked(ctx, payload, workerSlot, state)
	if err != nil {
		return "", nil, state.codexThreadID(workerSlot), "codex-interactive-error", err
	}
	resumeMode := codex.mode
	req := RunnerRequest{
		Content:        prompt,
		RemoteThreadID: codex.threadID,
		RunID:          payload.RunID,
		PromptID:       turnID,
		CWD:            m.cwd(payload),
		Approvals: orchestrationApprovalRequester{
			manager: m,
			runID:   payload.RunID,
			turnID:  turnID,
			role:    role,
			cli:     "codex",
			cwd:     m.cwd(payload),
		},
	}
	var toolsMu sync.Mutex
	var tools []RunnerToolEvent
	toolStarts := make(map[string]time.Time)
	snapshotTools := func() []RunnerToolEvent {
		toolsMu.Lock()
		defer toolsMu.Unlock()
		return append([]RunnerToolEvent(nil), tools...)
	}
	done := make(chan appServerTurnResult, 1)
	runner := NewCodexAppServerRunner(m.cfg)
	scope := newAppServerTurnScope(codex.threadID)
	// The reader goroutine must not outlive this turn: without the turn-scoped
	// cancel, a failed turn/start would leave it emitting deltas for a turn
	// that already ended. The mutex covers tools/toolStarts, which the reader
	// callback mutates while the error paths below read them.
	turnCtx, cancelTurn := context.WithCancel(ctx)
	defer cancelTurn()
	go runner.readEvents(turnCtx, codex.client, req, scope, func(update RunnerUpdate) {
		if update.Delta != "" {
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{Kind: "turn.delta", TurnID: turnID, Role: role, CLI: "codex", Content: update.Delta})
		}
		if update.Content != "" {
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{Kind: "turn.delta", TurnID: turnID, Role: role, CLI: "codex", Content: update.Content})
		}
		if update.Tool != nil {
			toolsMu.Lock()
			stampToolTiming(update.Tool, toolStarts)
			tools = append(tools, *update.Tool)
			toolsMu.Unlock()
			m.emitTool(payload.RunID, turnID, role, "codex", update.Tool)
		}
	}, done)
	res, err := codex.client.request(ctx, "turn/start", runner.turnStartParams(codex.threadID, prompt, req))
	if err != nil {
		return "", snapshotTools(), codex.threadID, resumeMode, err
	}
	scope.setTurnID(appServerTurnIDFromResponse(res))
	select {
	case result := <-done:
		return strings.TrimSpace(result.result.Content), snapshotTools(), codex.threadID, resumeMode, result.err
	case <-ctx.Done():
		return "", snapshotTools(), codex.threadID, resumeMode, ctx.Err()
	}
}

func (m *OrchestrationManager) ensureCodexInteractiveSessionLocked(ctx context.Context, payload protocol.OrchestrationStartPayload, workerSlot string, state *orchestrationSessionState) (*orchestrationCodexSession, error) {
	session := state.NativeSession
	workerSlot = normalizeCodexWorkerSlot(workerSlot)
	if codex := session.codexSessionLocked(workerSlot); codex != nil && codex.client != nil && codex.threadID != "" {
		state.setCodexThreadID(workerSlot, codex.threadID)
		if codex.client.isClosed() {
			session.setCodexSessionLocked(workerSlot, nil)
			state.setCodexThreadID(workerSlot, codex.threadID)
			return m.ensureCodexInteractiveSessionLocked(ctx, payload, workerSlot, state)
		}
		if codex.loaded {
			return codex, nil
		}
		if _, err := codex.client.request(ctx, "thread/resume", NewCodexAppServerRunner(m.cfg).threadResumeParams(codex.threadID, RunnerRequest{
			RemoteThreadID: codex.threadID,
			RunID:          payload.RunID,
			CWD:            m.cwd(payload),
		})); err != nil {
			codex.client.close()
			session.setCodexSessionLocked(workerSlot, nil)
			return nil, err
		}
		codex.loaded = true
		codex.mode = "codex-interactive-resume"
		return codex, nil
	}
	runner := NewCodexAppServerRunner(m.cfg)
	req := RunnerRequest{
		RemoteThreadID: state.codexThreadID(workerSlot),
		RunID:          payload.RunID,
		CWD:            m.cwd(payload),
	}
	client, err := runner.start(context.Background(), req)
	if err != nil {
		return nil, err
	}
	if _, err := client.request(ctx, "initialize", map[string]any{
		"clientInfo": map[string]string{"name": "codex-bridge", "title": "Codex Bridge", "version": "dev"},
		"capabilities": map[string]any{
			"experimentalApi":    true,
			"requestAttestation": false,
		},
	}); err != nil {
		client.close()
		return nil, err
	}
	threadID := strings.TrimSpace(state.codexThreadID(workerSlot))
	mode := "codex-interactive-thread"
	if threadID == "" {
		res, err := client.request(ctx, "thread/start", runner.threadStartParams(req))
		if err != nil {
			client.close()
			return nil, err
		}
		threadID = nestedString(appServerResultMap(res), "thread", "id")
		if threadID == "" {
			client.close()
			return nil, errors.New("codex app-server did not return a thread id")
		}
		_, _ = client.request(ctx, "thread/name/set", map[string]any{
			"threadId": threadID,
			"name":     nativeSessionDisplayName(payload.RunID, workerSlot),
		})
	} else if _, err := client.request(ctx, "thread/resume", runner.threadResumeParams(threadID, req)); err != nil {
		client.close()
		return nil, err
	} else {
		mode = "codex-interactive-resume"
	}
	codex := &orchestrationCodexSession{client: client, threadID: threadID, mode: mode, loaded: true}
	session.setCodexSessionLocked(workerSlot, codex)
	state.setCodexThreadID(workerSlot, threadID)
	return codex, nil
}

func (m *OrchestrationManager) flushCodexInteractiveThread(session *orchestrationNativeSession, codex *orchestrationCodexSession) {
	if session == nil || codex == nil || codex.client == nil || strings.TrimSpace(codex.threadID) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), appServerThreadUnsubscribeTimeout)
	defer cancel()
	_ = codex.client.unsubscribeThread(ctx, codex.threadID)
	codex.loaded = false
	codex.mode = "codex-interactive-resume"
}

func (m *OrchestrationManager) compactCodexInteractiveThread(ctx context.Context, session *orchestrationNativeSession, codex *orchestrationCodexSession) orchestrationMaintenanceResult {
	if session == nil || codex == nil || codex.client == nil || strings.TrimSpace(codex.threadID) == "" {
		return orchestrationMaintenanceResult{Err: errors.New("codex native session is not available")}
	}
	maintCtx, cancel := context.WithTimeout(ctx, nativeContextCompactionTimeout)
	defer cancel()
	done := make(chan orchestrationMaintenanceResult, 1)
	go waitForCodexNativeCompaction(maintCtx, codex.client, codex.threadID, done)
	if _, err := codex.client.request(maintCtx, "thread/compact/start", map[string]any{"threadId": codex.threadID}); err != nil {
		return orchestrationMaintenanceResult{Err: err}
	}
	select {
	case result := <-done:
		return result
	case <-maintCtx.Done():
		return orchestrationMaintenanceResult{Err: maintCtx.Err()}
	}
}

func waitForCodexNativeCompaction(ctx context.Context, client *appServerClient, threadID string, done chan<- orchestrationMaintenanceResult) {
	for {
		select {
		case <-ctx.Done():
			done <- orchestrationMaintenanceResult{Err: ctx.Err()}
			return
		case msg, ok := <-client.events:
			if !ok {
				done <- orchestrationMaintenanceResult{Err: errors.New("codex app-server exited")}
				return
			}
			if msg.Method == "" {
				continue
			}
			if msgThreadID := appServerMessageThreadID(msg); msgThreadID != "" && msgThreadID != threadID {
				continue
			}
			switch msg.Method {
			case "thread/compacted":
				done <- orchestrationMaintenanceResult{}
				return
			case "item/completed":
				item, _ := appServerNestedMap(msg.Params, "item")
				if itemType, _ := item["type"].(string); itemType == "contextCompaction" {
					done <- orchestrationMaintenanceResult{}
					return
				}
			case "error":
				done <- orchestrationMaintenanceResult{Err: appServerEventError(msg, "")}
				return
			}
		}
	}
}

func (m *OrchestrationManager) runCodexWithThread(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID, role, prompt, threadID string) (string, []RunnerToolEvent, string, string, error) {
	if m.shouldRunCodexAppServer() {
		return m.runCodexAppServerWithThread(ctx, payload, turnID, role, prompt, threadID)
	}
	attempt := m.runCodexExecAttempt(ctx, payload, turnID, role, prompt, threadID)
	if threadID != "" {
		if attempt.threadID != "" && attempt.threadID != threadID {
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:    "turn.delta",
				TurnID:  turnID,
				Role:    role,
				CLI:     "codex",
				Status:  "warning",
				Content: "Codex resume did not return the expected thread; Bridge will continue with the new returned thread id.",
				Data: map[string]any{
					"expectedThreadId": threadID,
					"codexThreadId":    attempt.threadID,
					"resumeMode":       "codex-thread-miss-new",
					"relayOnly":        true,
				},
			})
			return attempt.content, attempt.tools, attempt.threadID, "codex-thread-miss-new", attempt.err
		}
		if shouldRetryCodexFreshAfterResume(threadID, attempt) && ctx.Err() == nil {
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:    "turn.delta",
				TurnID:  turnID,
				Role:    role,
				CLI:     "codex",
				Status:  "warning",
				Content: "Codex native resume did not produce a usable prior thread. Bridge will keep the compacted orchestration context and continue this turn in a fresh Codex thread.",
				Data: map[string]any{
					"expectedThreadId": threadID,
					"codexThreadId":    attempt.threadID,
					"resumeMode":       "codex-fresh-after-resume-miss",
					"relayOnly":        true,
				},
			})
			fresh := m.runCodexExecAttempt(ctx, payload, turnID, role, prompt, "")
			return fresh.content, fresh.tools, firstNonEmpty(fresh.threadID, attempt.threadID, threadID), "codex-fresh-after-resume-miss", fresh.err
		}
		if attempt.threadID == threadID {
			return attempt.content, attempt.tools, attempt.threadID, "codex-thread-returned", attempt.err
		}
	}
	mode := "codex-fresh"
	if threadID != "" {
		mode = "codex-thread-resume"
	}
	return attempt.content, attempt.tools, firstNonEmpty(attempt.threadID, threadID), mode, attempt.err
}

type codexExecAttempt struct {
	content       string
	tools         []RunnerToolEvent
	threadID      string
	threadStarted bool
	err           error
}

func (m *OrchestrationManager) runCodexExecAttempt(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID, role, prompt, threadID string) codexExecAttempt {
	prompt = sanitizePromptText(prompt)
	cmdCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	args := m.codexOrchestrationArgs(payload, threadID)
	cwd := m.cwd(payload)

	cmd := exec.CommandContext(cmdCtx, m.codexPath(), args...)
	configureManagedCommand(cmd)
	if cwd != "" {
		cmd.Dir = cwd
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return codexExecAttempt{threadID: threadID, err: err}
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return codexExecAttempt{threadID: threadID, err: err}
	}
	if err := cmd.Start(); err != nil {
		return codexExecAttempt{threadID: threadID, err: err}
	}
	_, _ = io.WriteString(stdin, prompt)
	_ = stdin.Close()

	scanResult, scanErr := m.scanCodexJSONLResult(stdout, payload.RunID, turnID, role)
	if scanErr != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	if err := ctx.Err(); err != nil {
		return codexExecAttempt{content: scanResult.Content, tools: scanResult.Tools, threadID: firstNonEmpty(scanResult.ThreadID, threadID), threadStarted: scanResult.ThreadStarted, err: err}
	}
	if scanErr != nil {
		return codexExecAttempt{content: scanResult.Content, tools: scanResult.Tools, threadID: firstNonEmpty(scanResult.ThreadID, threadID), threadStarted: scanResult.ThreadStarted, err: scanErr}
	}
	if waitErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = waitErr.Error()
		}
		return codexExecAttempt{content: scanResult.Content, tools: scanResult.Tools, threadID: firstNonEmpty(scanResult.ThreadID, threadID), threadStarted: scanResult.ThreadStarted, err: errors.New(msg)}
	}
	if scanResult.Content == "" {
		scanResult.Content = strings.TrimSpace(stderr.String())
	}
	return codexExecAttempt{content: scanResult.Content, tools: scanResult.Tools, threadID: firstNonEmpty(scanResult.ThreadID, threadID), threadStarted: scanResult.ThreadStarted}
}

func shouldRetryCodexFreshAfterResume(expectedThreadID string, attempt codexExecAttempt) bool {
	if expectedThreadID == "" {
		return false
	}
	if attempt.threadID != "" && attempt.threadID != expectedThreadID {
		return false
	}
	if attempt.err == nil {
		return false
	}
	return isCodexResumeMissingThreadError(attempt.err)
}

func isCodexResumeMissingThreadError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(stripANSI(err.Error()))
	if strings.Contains(msg, "missing required parameter") {
		return true
	}
	if strings.Contains(msg, "no such file") && (strings.Contains(msg, "rollout") || strings.Contains(msg, "session") || strings.Contains(msg, "thread")) {
		return true
	}
	if strings.Contains(msg, "not found") || strings.Contains(msg, "does not exist") || strings.Contains(msg, "missing") {
		return strings.Contains(msg, "resume") ||
			strings.Contains(msg, "rollout") ||
			strings.Contains(msg, "session") ||
			strings.Contains(msg, "thread") ||
			strings.Contains(msg, "conversation")
	}
	return false
}

func (m *OrchestrationManager) codexOrchestrationArgs(payload protocol.OrchestrationStartPayload, threadID string) []string {
	if threadID != "" {
		args := []string{"exec", "resume", "--json", "--skip-git-repo-check"}
		if m.cfg.Bridge.Model != "" {
			args = append(args, "--model", m.cfg.Bridge.Model)
		}
		if strings.EqualFold(m.cfg.Bridge.ApprovalPolicy, "never") && strings.EqualFold(m.cfg.Bridge.Sandbox, "danger-full-access") {
			args = append(args, "--dangerously-bypass-approvals-and-sandbox")
		} else {
			if m.cfg.Bridge.Sandbox != "" {
				args = append(args, "-c", "sandbox_mode="+quoteTomlString(m.cfg.Bridge.Sandbox))
			}
			if m.cfg.Bridge.ApprovalPolicy != "" {
				args = append(args, "-c", "approval_policy="+quoteTomlString(m.cfg.Bridge.ApprovalPolicy))
			}
		}
		return append(args, threadID, "-")
	}

	args := []string{"exec", "--json", "--color", "never", "--skip-git-repo-check"}
	if m.cfg.Bridge.Model != "" {
		args = append(args, "--model", m.cfg.Bridge.Model)
	}
	if strings.EqualFold(m.cfg.Bridge.ApprovalPolicy, "never") && strings.EqualFold(m.cfg.Bridge.Sandbox, "danger-full-access") {
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
	} else if m.cfg.Bridge.Sandbox != "" {
		args = append(args, "--sandbox", m.cfg.Bridge.Sandbox)
	}
	if m.cfg.Bridge.ApprovalPolicy != "" && !(strings.EqualFold(m.cfg.Bridge.ApprovalPolicy, "never") && strings.EqualFold(m.cfg.Bridge.Sandbox, "danger-full-access")) {
		args = append(args, "-c", "approval_policy="+quoteTomlString(m.cfg.Bridge.ApprovalPolicy))
	}
	cwd := m.cwd(payload)
	if cwd != "" {
		args = append(args, "--cd", cwd)
	}
	return append(args, "-")
}

func (m *OrchestrationManager) runCodexAppServerWithThread(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID, role, prompt, threadID string) (string, []RunnerToolEvent, string, string, error) {
	runner := NewCodexAppServerRunner(m.cfg)
	defer runner.Close()
	var tools []RunnerToolEvent
	toolStarts := make(map[string]time.Time)
	result, err := runner.Prompt(ctx, RunnerRequest{
		Content:        prompt,
		RemoteThreadID: threadID,
		RunID:          payload.RunID,
		PromptID:       turnID,
		CWD:            m.cwd(payload),
		Approvals: orchestrationApprovalRequester{
			manager: m,
			runID:   payload.RunID,
			turnID:  turnID,
			role:    role,
			cli:     "codex",
			cwd:     m.cwd(payload),
		},
	}, func(update RunnerUpdate) {
		if update.Delta != "" {
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{Kind: "turn.delta", TurnID: turnID, Role: role, CLI: "codex", Content: update.Delta})
		}
		if update.Content != "" {
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{Kind: "turn.delta", TurnID: turnID, Role: role, CLI: "codex", Content: update.Content})
		}
		if update.Tool != nil {
			stampToolTiming(update.Tool, toolStarts)
			tools = append(tools, *update.Tool)
			m.emitTool(payload.RunID, turnID, role, "codex", update.Tool)
		}
	})
	mode := "codex-fresh"
	if threadID != "" {
		mode = "codex-thread-resume"
	}
	return strings.TrimSpace(result.Content), tools, firstNonEmpty(result.RemoteThreadID, threadID), mode, err
}

func (m *OrchestrationManager) shouldRunCodexAppServer() bool {
	return !m.bypassApprovalsAndSandbox()
}

func (m *OrchestrationManager) scanCodexJSONLResult(stdout io.Reader, runID, turnID, role string) (codexScanResult, error) {
	reader := bufio.NewReaderSize(stdout, 64*1024)
	var content strings.Builder
	var eventErr string
	var tools []RunnerToolEvent
	var threadID string
	var threadStarted bool
	var pendingFailedTool *RunnerToolEvent
	toolStarts := make(map[string]time.Time)
	observer := m.longCommandObserverConfig()
	activeObservers := make(map[string]context.CancelFunc)
	defer func() {
		for _, cancel := range activeObservers {
			cancel()
		}
	}()
	emitCodexTool := func(tool *RunnerToolEvent) {
		if tool == nil {
			return
		}
		stampToolTiming(tool, toolStarts)
		if tool.ID != "" {
			if isRunningToolStatus(tool.Status) && m.longCommandObserverMatches(observer, "codex", tool.Command) {
				if activeObservers[tool.ID] == nil {
					activeObservers[tool.ID] = m.scheduleLongCommandObserver(runID, turnID, role, "codex", tool, claudeScanOptions{
						NudgeAfter:          observer.After,
						LongCommandObserver: observer,
					})
				}
			} else if !isRunningToolStatus(tool.Status) {
				if cancel := activeObservers[tool.ID]; cancel != nil {
					cancel()
					delete(activeObservers, tool.ID)
				}
			}
		}
		tools = append(tools, *tool)
		m.emitTool(runID, turnID, role, "codex", tool)
		if runnerToolEventFailed(*tool) {
			copy := *tool
			pendingFailedTool = &copy
		}
	}
	for {
		line, err := readJSONLLine(reader, 32*1024*1024)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return codexScanResult{Content: content.String(), Tools: tools, ThreadID: threadID, ThreadStarted: threadStarted}, err
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		typ, _ := msg["type"].(string)
		if isErrorEvent(typ) {
			if message := eventErrorMessage(msg); message != "" {
				eventErr = message
			}
		}
		switch typ {
		case "thread.started":
			threadStarted = true
			if id, _ := msg["thread_id"].(string); id != "" {
				threadID = id
			}
			if id, _ := msg["threadId"].(string); id != "" {
				threadID = id
			}
			if id := nestedString(msg, "thread", "id"); id != "" {
				threadID = id
			}
		case "item.agent_message.delta", "item.agentMessage.delta", "agent_message.delta", "agentMessage.delta", "response.output_text.delta":
			if delta := extractDelta(msg); delta != "" {
				content.WriteString(delta)
				pendingFailedTool = nil
				m.emit(runID, protocol.OrchestrationEventPayload{Kind: "turn.delta", TurnID: turnID, Role: role, CLI: "codex", Content: delta})
			}
		case "item.completed":
			item, _ := msg["item"].(map[string]any)
			itemType, _ := item["type"].(string)
			if itemType == "agent_message" || itemType == "agentMessage" {
				if text := agentMessageText(item); text != "" {
					if delta := appendAgentMessageContent(&content, text); delta != "" {
						pendingFailedTool = nil
						m.emit(runID, protocol.OrchestrationEventPayload{Kind: "turn.delta", TurnID: turnID, Role: role, CLI: "codex", Content: delta})
					}
				}
			}
			if itemType == "command_execution" || itemType == "commandExecution" {
				emitCodexTool(commandExecutionEvent(item))
			}
		case "item.started", "item.updated":
			item, _ := msg["item"].(map[string]any)
			itemType, _ := item["type"].(string)
			if itemType == "command_execution" || itemType == "commandExecution" {
				emitCodexTool(commandExecutionEvent(item))
			}
		}
	}
	if eventErr != "" && !codexTailErrorAfterContent(eventErr, content.String()) {
		return codexScanResult{Content: content.String(), Tools: tools, ThreadID: threadID, ThreadStarted: threadStarted}, errors.New(eventErr)
	}
	if pendingFailedTool != nil {
		return codexScanResult{Content: strings.TrimSpace(content.String()), Tools: tools, ThreadID: threadID, ThreadStarted: threadStarted}, failedToolWithoutFollowupError("codex", *pendingFailedTool)
	}
	return codexScanResult{Content: strings.TrimSpace(content.String()), Tools: tools, ThreadID: threadID, ThreadStarted: threadStarted}, nil
}
