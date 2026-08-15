package bridge

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tencent/codex-bridge/internal/protocol"
)

const maxUsageLogLineBytes = 16 << 20

func scanOrchestrationUsage(request protocol.OrchestrationUsageSyncRequest) protocol.OrchestrationUsageSyncResult {
	result := protocol.OrchestrationUsageSyncResult{RunID: request.RunID, ScannedAt: time.Now().Unix()}
	complete := 0
	for _, session := range request.Sessions {
		events, err := scanUsageSession(session)
		item := protocol.OrchestrationUsageSessionResult{
			CLI: session.CLI, WorkerSlot: session.WorkerSlot, SessionID: session.SessionID,
			Status: "complete", EventCount: len(events),
		}
		if err != nil {
			item.Status = "unavailable"
			item.Error = sanitizeUsageScanError(err)
		} else {
			complete++
			result.Events = append(result.Events, events...)
		}
		result.Sessions = append(result.Sessions, item)
	}
	switch {
	case len(request.Sessions) == 0 || complete == 0:
		result.Status = "unavailable"
	case complete < len(request.Sessions):
		result.Status = "partial"
	default:
		result.Status = "complete"
	}
	return result
}

func scanUsageSession(session protocol.OrchestrationUsageSession) ([]protocol.OrchestrationUsageEvent, error) {
	if !validNativeSessionID(session.SessionID) {
		return nil, errors.New("invalid native session id")
	}
	if session.Isolated {
		return nil, errors.New("isolated worker runtime is no longer available")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, errors.New("home directory unavailable")
	}
	switch strings.ToLower(strings.TrimSpace(session.CLI)) {
	case "codex":
		path, err := findNativeSessionLog(filepath.Join(home, ".codex", "sessions"), session.SessionID)
		if err != nil {
			return nil, err
		}
		return parseCodexUsageLog(path, session)
	case "claude":
		path, err := findNativeSessionLog(filepath.Join(home, ".claude", "projects"), session.SessionID)
		if err != nil {
			return nil, err
		}
		return parseClaudeUsageLog(path, session)
	default:
		return nil, errors.New("unsupported CLI")
	}
}

func validNativeSessionID(value string) bool {
	if len(value) < 8 || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' {
			continue
		}
		return false
	}
	return true
}

func findNativeSessionLog(root, sessionID string) (string, error) {
	var found string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if name == ".git" || name == "node_modules" || name == "cache" || name == "backups" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".jsonl") && strings.Contains(entry.Name(), sessionID) {
			found = path
			return io.EOF
		}
		return nil
	})
	if err != nil && !errors.Is(err, io.EOF) {
		return "", errors.New("native session log scan failed")
	}
	if found == "" {
		return "", errors.New("native session log not found")
	}
	return found, nil
}

type nativeTokenUsage struct {
	Input      int64 `json:"input_tokens"`
	Cached     int64 `json:"cached_input_tokens"`
	CacheWrite int64 `json:"cache_write_input_tokens"`
	Output     int64 `json:"output_tokens"`
	Reasoning  int64 `json:"reasoning_output_tokens"`
}

func parseCodexUsageLog(path string, session protocol.OrchestrationUsageSession) ([]protocol.OrchestrationUsageEvent, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("native session log unavailable")
	}
	defer file.Close()
	provider, model := "", ""
	var events []protocol.OrchestrationUsageEvent
	seenTotals := map[string]bool{}
	err = scanJSONLines(file, func(sequence int, raw []byte) {
		var record struct {
			Timestamp string `json:"timestamp"`
			Type      string `json:"type"`
			Payload   struct {
				ID            string `json:"id"`
				ModelProvider string `json:"model_provider"`
				Model         string `json:"model"`
				Type          string `json:"type"`
				Info          struct {
					Total nativeTokenUsage `json:"total_token_usage"`
					Last  nativeTokenUsage `json:"last_token_usage"`
				} `json:"info"`
				ThreadSettings struct {
					Model           string `json:"model"`
					ModelProviderID string `json:"model_provider_id"`
				} `json:"thread_settings"`
			} `json:"payload"`
		}
		if json.Unmarshal(raw, &record) != nil {
			return
		}
		switch record.Type {
		case "session_meta":
			provider = strings.TrimSpace(record.Payload.ModelProvider)
		case "turn_context":
			if record.Payload.Model != "" {
				model = record.Payload.Model
			}
		case "event_msg":
			if record.Payload.Type == "thread_settings_applied" {
				if record.Payload.ThreadSettings.Model != "" {
					model = record.Payload.ThreadSettings.Model
				}
				if record.Payload.ThreadSettings.ModelProviderID != "" {
					provider = record.Payload.ThreadSettings.ModelProviderID
				}
				return
			}
			if record.Payload.Type != "token_count" {
				return
			}
			usage := record.Payload.Info.Last
			if usage.Input+usage.Cached+usage.CacheWrite+usage.Output == 0 {
				return
			}
			total := record.Payload.Info.Total
			if total.Input+total.Cached+total.CacheWrite+total.Output > 0 {
				totalKey := fmt.Sprintf("%d:%d:%d:%d:%d", total.Input, total.Cached, total.CacheWrite, total.Output, total.Reasoning)
				if seenTotals[totalKey] {
					return
				}
				seenTotals[totalKey] = true
			}
			input := usage.Input - usage.Cached
			if input < 0 {
				input = 0
			}
			events = append(events, usageEvent(session, sequence, record.Timestamp, provider, model, input, usage.Cached, usage.CacheWrite, usage.Output, usage.Reasoning))
		}
	})
	return events, err
}

func parseClaudeUsageLog(path string, session protocol.OrchestrationUsageSession) ([]protocol.OrchestrationUsageEvent, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("native session log unavailable")
	}
	defer file.Close()
	var events []protocol.OrchestrationUsageEvent
	err = scanJSONLines(file, func(sequence int, raw []byte) {
		var record struct {
			Timestamp string `json:"timestamp"`
			Type      string `json:"type"`
			Message   struct {
				ID    string `json:"id"`
				Model string `json:"model"`
				Usage struct {
					Input      int64 `json:"input_tokens"`
					CacheRead  int64 `json:"cache_read_input_tokens"`
					CacheWrite int64 `json:"cache_creation_input_tokens"`
					Output     int64 `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(raw, &record) != nil || record.Type != "assistant" {
			return
		}
		usage := record.Message.Usage
		if usage.Input+usage.CacheRead+usage.CacheWrite+usage.Output == 0 {
			return
		}
		events = append(events, usageEvent(session, sequence, record.Timestamp, "anthropic", record.Message.Model, usage.Input, usage.CacheRead, usage.CacheWrite, usage.Output, 0))
	})
	return events, err
}

func scanJSONLines(reader io.Reader, visit func(int, []byte)) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), maxUsageLogLineBytes)
	sequence := 0
	for scanner.Scan() {
		sequence++
		visit(sequence, scanner.Bytes())
	}
	if err := scanner.Err(); err != nil {
		return errors.New("native session log parse failed")
	}
	return nil
}

func usageEvent(session protocol.OrchestrationUsageSession, sequence int, timestamp, provider, model string, input, cacheRead, cacheWrite, output, reasoning int64) protocol.OrchestrationUsageEvent {
	identity := fmt.Sprintf("%s\x00%s\x00%d\x00%d\x00%d\x00%d\x00%d", session.CLI, session.SessionID, sequence, input, cacheRead, cacheWrite, output)
	digest := sha256.Sum256([]byte(identity))
	occurredAt := int64(0)
	if parsed, err := time.Parse(time.RFC3339Nano, timestamp); err == nil {
		occurredAt = parsed.Unix()
	}
	return protocol.OrchestrationUsageEvent{
		EventID: hex.EncodeToString(digest[:]), CLI: strings.ToLower(session.CLI), WorkerSlot: session.WorkerSlot,
		SessionID: session.SessionID, OccurredAt: occurredAt, Provider: provider, Model: model,
		InputTokens: input, CacheReadTokens: cacheRead, CacheWriteTokens: cacheWrite,
		OutputTokens: output, ReasoningTokens: reasoning,
	}
}

func sanitizeUsageScanError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if strings.Contains(message, string(filepath.Separator)) {
		return "native session log scan failed"
	}
	return message
}
