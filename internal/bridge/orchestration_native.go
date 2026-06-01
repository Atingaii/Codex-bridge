package bridge

import (
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
