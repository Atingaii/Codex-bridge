package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tencent/codex-bridge/internal/protocol"
)

const nativeContextCompactionCommand = "/compact"
const nativeContextCompactionTimeout = 90 * time.Second
const claudeTranscriptStableWait = 2 * time.Second
const claudeTranscriptStablePoll = 100 * time.Millisecond

func (m *OrchestrationManager) runNativeContextCompaction(ctx context.Context, runID, turnID, role, cli string, enabled bool, session *orchestrationNativeSession, native any) {
	if !enabled || session == nil {
		return
	}
	m.emitNativeContextCompactionNote(runID, turnID, role, cli, "info", fmt.Sprintf("Bridge is compacting %s native context.", nativeDisplayName(cli)), "", "")
	var result orchestrationMaintenanceResult
	switch cli {
	case "codex":
		codex, _ := native.(*orchestrationCodexSession)
		result = m.compactCodexInteractiveThread(ctx, session, codex)
	case "claude":
		claude, _ := native.(*orchestrationClaudeSession)
		result = m.compactClaudeInteractiveSession(ctx, runID, turnID, role, claude)
	default:
		result.Err = fmt.Errorf("unsupported native compaction cli %q", cli)
	}
	if result.Skipped {
		m.emitNativeContextCompactionNote(runID, turnID, role, cli, "info", fmt.Sprintf("Bridge skipped %s native context compaction.", nativeDisplayName(cli)), "", result.Reason)
		return
	}
	if result.Err != nil {
		m.emitNativeContextCompactionNote(runID, turnID, role, cli, "warning", fmt.Sprintf("Bridge could not compact %s native context.", nativeDisplayName(cli)), visibleCLIError(result.Err), "")
		return
	}
	m.emitNativeContextCompactionNote(runID, turnID, role, cli, "info", fmt.Sprintf("Bridge compacted %s native context.", nativeDisplayName(cli)), "", "")
}

func (m *OrchestrationManager) emitNativeContextCompactionNote(runID, turnID, role, cli, severity, content, errText, detail string) {
	data := map[string]any{
		"relayOnly": true,
		"category":  "native-context-compaction",
		"command":   nativeContextCompactionCommand,
	}
	if detail != "" {
		data["detail"] = detail
	}
	m.emit(runID, protocol.OrchestrationEventPayload{
		Kind:     "turn.delta",
		Source:   "bridge",
		Severity: severity,
		TurnID:   turnID,
		Role:     role,
		CLI:      cli,
		Content:  content,
		Error:    errText,
		BridgeNoteData: &protocol.BridgeNoteData{
			Category: "native-context-compaction",
			Command:  nativeContextCompactionCommand,
		},
		Data: data,
	})
}

func nativeDisplayName(cli string) string {
	switch cli {
	case "codex":
		return "Codex"
	case "claude":
		return "Claude Code"
	default:
		return cli
	}
}

func codexNativeResumeInfo(threadID, cwd string) *protocol.NativeResumeInfo {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	return &protocol.NativeResumeInfo{
		CLI:              "codex",
		ID:               threadID,
		Command:          "codex resume " + threadID,
		CWD:              cwd,
		Visible:          true,
		VisibilityReason: "Codex app-server returned a persisted native thread id.",
	}
}

func (m *OrchestrationManager) claudeNativeResumeInfo(sessionID, cwd string) *protocol.NativeResumeInfo {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	info := &protocol.NativeResumeInfo{
		CLI:     "claude",
		ID:      sessionID,
		Command: "claude --resume " + sessionID,
		CWD:     cwd,
	}
	path := claudeSessionFilePath(cwd, sessionID)
	info.TranscriptPath = path
	if ok, reason := verifyClaudeTranscript(path, sessionID); ok {
		info.Visible = true
		info.VisibilityReason = reason
	} else {
		info.Visible = false
		info.VisibilityReason = reason
	}
	return info
}

func runEndDataWithNativeResume(data *protocol.RunEndData, cwd string) *protocol.RunEndData {
	if data == nil {
		return nil
	}
	if data.CodexThreadID != "" && data.CodexNativeResume == nil {
		data.CodexNativeResume = codexNativeResumeInfo(data.CodexThreadID, cwd)
	}
	if data.CodexNativeResume != nil {
		data.NativeResume = appendNativeResumeInfo(data.NativeResume, *data.CodexNativeResume)
	}
	if data.ClaudeNativeResume != nil {
		data.NativeResume = appendNativeResumeInfo(data.NativeResume, *data.ClaudeNativeResume)
	}
	return data
}

func appendNativeResumeInfo(values []protocol.NativeResumeInfo, value protocol.NativeResumeInfo) []protocol.NativeResumeInfo {
	if strings.TrimSpace(value.CLI) == "" || strings.TrimSpace(value.ID) == "" {
		return values
	}
	for i := range values {
		if values[i].CLI == value.CLI {
			values[i] = value
			return values
		}
	}
	return append(values, value)
}

func (m *OrchestrationManager) registerClaudeNativeResume(session *orchestrationNativeSession, claude *orchestrationClaudeSession, runID, cwd string) *protocol.NativeResumeInfo {
	if claude == nil {
		return nil
	}
	if strings.TrimSpace(cwd) == "" && session != nil {
		cwd = session.cwd
	}
	info := m.claudeNativeResumeInfo(claude.sessionID, cwd)
	if info == nil {
		return nil
	}
	if err := updateClaudeProjectLastSession(cwd, claude.sessionID); err != nil && info.VisibilityReason == "" {
		info.VisibilityReason = "Claude transcript was checked, but Bridge could not update Claude project metadata: " + err.Error()
	}
	if err := materializeClaudePickerVisibility(cwd, claude.sessionID, runID); err != nil {
		info.Visible = false
		info.VisibilityReason = "Claude transcript exists, but Bridge could not materialize it for Claude Code /resume picker visibility: " + err.Error()
	} else if info.Visible {
		if refreshed := m.claudeNativeResumeInfo(claude.sessionID, cwd); refreshed != nil {
			info = refreshed
		}
		info.Visible = true
		info.VisibilityReason = "Claude wrote a project transcript for this session, and Bridge materialized it for the Claude Code /resume picker."
	}
	if err := writeClaudeSessionHint(claude, runID, cwd, info); err != nil && info.VisibilityReason == "" {
		info.VisibilityReason = "Claude transcript was checked, but Bridge could not write the compatibility session hint: " + err.Error()
	}
	return info
}

func cwdForNativeSession(session *orchestrationNativeSession, fallback string) string {
	if session == nil {
		return fallback
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if strings.TrimSpace(session.cwd) != "" {
		return session.cwd
	}
	return fallback
}

func claudeSessionFilePath(cwd, sessionID string) string {
	dir := claudeProjectDir(cwd)
	if dir == "" || strings.TrimSpace(sessionID) == "" {
		return ""
	}
	return filepath.Join(dir, sessionID+".jsonl")
}

func verifyClaudeTranscript(path, sessionID string) (bool, string) {
	if strings.TrimSpace(path) == "" {
		return false, "Claude transcript path could not be resolved."
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, "Claude transcript has not been written yet; use the direct resume command after the turn is flushed."
		}
		return false, "Claude transcript could not be read: " + err.Error()
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return false, "Claude transcript exists but is empty."
	}
	if strings.TrimSpace(sessionID) != "" && !strings.Contains(string(data), `"sessionId":"`+sessionID+`"`) {
		return false, "Claude transcript exists but does not contain the expected session id."
	}
	if strings.Contains(string(data), `"entrypoint":"cli"`) {
		home, err := os.UserHomeDir()
		if err == nil && claudeHistoryContainsSession(filepath.Join(home, ".claude", "history.jsonl"), sessionID) {
			return true, "Claude wrote a project transcript for this session, and Bridge materialized it for the Claude Code /resume picker."
		}
	}
	return true, "Claude wrote a project transcript for this session; direct native resume is available."
}

func updateClaudeProjectLastSession(cwd, sessionID string) error {
	cwd = strings.TrimSpace(cwd)
	sessionID = strings.TrimSpace(sessionID)
	if cwd == "" || sessionID == "" {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path := filepath.Join(home, ".claude.json")
	root := map[string]any{}
	if data, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(data))) > 0 {
		if err := json.Unmarshal(data, &root); err != nil {
			return err
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	projects, _ := root["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
		root["projects"] = projects
	}
	project, _ := projects[cwd].(map[string]any)
	if project == nil {
		project = map[string]any{}
		projects[cwd] = project
	}
	project["lastSessionId"] = sessionID
	project["lastGracefulShutdown"] = true
	project["lastVersionBase"] = "bridge"
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o600)
}

func materializeClaudePickerVisibility(cwd, sessionID, runID string) error {
	cwd = strings.TrimSpace(cwd)
	sessionID = strings.TrimSpace(sessionID)
	if cwd == "" || sessionID == "" {
		return nil
	}
	transcriptPath := claudeSessionFilePath(cwd, sessionID)
	if strings.TrimSpace(transcriptPath) == "" {
		return errors.New("Claude transcript path could not be resolved")
	}
	if err := waitForStableClaudeTranscript(transcriptPath, claudeTranscriptStableWait); err != nil {
		return err
	}
	data, err := os.ReadFile(transcriptPath)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return errors.New("Claude transcript is empty")
	}
	materialized, err := claudePickerVisibleTranscript(data)
	if err != nil {
		return err
	}
	if !strings.Contains(string(materialized), `"entrypoint":"cli"`) {
		return errors.New("materialized transcript does not contain a cli entrypoint")
	}
	if err := os.WriteFile(transcriptPath, materialized, 0o600); err != nil {
		return err
	}
	return appendClaudeHistoryIndex(cwd, sessionID, runID, materialized)
}

func waitForStableClaudeTranscript(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastSize int64 = -1
	var lastMod time.Time
	for {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if info.Size() == lastSize && info.ModTime().Equal(lastMod) {
			return nil
		}
		lastSize = info.Size()
		lastMod = info.ModTime()
		if time.Now().After(deadline) {
			return nil
		}
		time.Sleep(claudeTranscriptStablePoll)
	}
}

func claudePickerVisibleTranscript(data []byte) ([]byte, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var out strings.Builder
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return nil, err
		}
		normalizeClaudePickerRecord(record)
		encoded, err := json.Marshal(record)
		if err != nil {
			return nil, err
		}
		out.Write(encoded)
		out.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if out.Len() == 0 {
		return nil, errors.New("Claude transcript has no JSONL records")
	}
	return []byte(out.String()), nil
}

func normalizeClaudePickerRecord(record map[string]any) {
	if entrypoint, _ := record["entrypoint"].(string); entrypoint == "sdk-cli" {
		record["entrypoint"] = "cli"
	}
	if record["userType"] == nil {
		record["userType"] = "external"
	}
	if record["isSidechain"] == nil {
		record["isSidechain"] = false
	}
	if record["version"] == nil {
		record["version"] = "bridge"
	}
	if record["gitBranch"] == nil {
		record["gitBranch"] = "HEAD"
	}
}

func appendClaudeHistoryIndex(cwd, sessionID, runID string, transcript []byte) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path := filepath.Join(home, ".claude", "history.jsonl")
	if claudeHistoryContainsSession(path, sessionID) {
		return nil
	}
	display := claudeHistoryDisplay(runID, transcript)
	entry := map[string]any{
		"display":        display,
		"pastedContents": map[string]any{},
		"timestamp":      time.Now().UnixMilli(),
		"project":        cwd,
		"sessionId":      sessionID,
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(encoded, '\n')); err != nil {
		return err
	}
	return nil
}

func claudeHistoryContainsSession(path, sessionID string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), `"sessionId":"`+sessionID+`"`)
}

func claudeHistoryDisplay(runID string, transcript []byte) string {
	title := strings.TrimSpace(claudeTranscriptTitle(transcript))
	if title != "" {
		return title
	}
	if runID = strings.TrimSpace(runID); runID != "" {
		return nativeSessionDisplayName(runID, "claude")
	}
	return "Codex Bridge Claude session"
}

func claudeTranscriptTitle(transcript []byte) string {
	scanner := bufio.NewScanner(strings.NewReader(string(transcript)))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var firstUserTitle string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}
		if value, _ := record["customTitle"].(string); strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
		if value, _ := record["aiTitle"].(string); strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
		if value, _ := record["agentName"].(string); strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
		if typ, _ := record["type"].(string); typ != "user" {
			continue
		}
		if msg, _ := record["message"].(map[string]any); msg != nil {
			if firstUserTitle == "" {
				if content := claudeMessageContentText(msg["content"]); content != "" {
					firstUserTitle = trimForPrompt(content, 80)
				}
			}
		}
	}
	return firstUserTitle
}

func claudeMessageContentText(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []any:
		var parts []string
		for _, item := range typed {
			obj, _ := item.(map[string]any)
			if obj == nil {
				continue
			}
			if text, _ := obj["text"].(string); strings.TrimSpace(text) != "" {
				parts = append(parts, strings.TrimSpace(text))
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	default:
		return ""
	}
}

func writeClaudeSessionHint(claude *orchestrationClaudeSession, runID, cwd string, info *protocol.NativeResumeInfo) error {
	if claude == nil || strings.TrimSpace(claude.sessionID) == "" {
		return nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	sessionsDir := filepath.Join(homeDir, ".claude", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		return err
	}
	sessionFile := filepath.Join(sessionsDir, claude.sessionID+".json")
	status := "idle"
	if claude.cmd != nil && claude.cmd.Process != nil {
		status = "running"
	}
	sessionData := map[string]any{
		"sessionId":             claude.sessionID,
		"cwd":                   cwd,
		"status":                status,
		"updatedAt":             time.Now().UnixMilli(),
		"kind":                  "interactive",
		"entrypoint":            "sdk-cli",
		"nativeResumeCommand":   info.Command,
		"nativeTranscriptPath":  info.TranscriptPath,
		"nativeResumeAvailable": info.Visible,
	}
	if claude.cmd != nil && claude.cmd.Process != nil {
		sessionData["pid"] = claude.cmd.Process.Pid
	}
	if runID != "" {
		sessionData["name"] = nativeSessionDisplayName(runID, "claude")
	}
	data, err := json.Marshal(sessionData)
	if err != nil {
		return err
	}
	return os.WriteFile(sessionFile, data, 0o600)
}
