package bridge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tencent/codex-bridge/internal/protocol"
)

func TestParseCodexUsageLogCountsEveryLastUsage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-session-12345678.jsonl")
	content := `{"timestamp":"2026-08-09T01:00:00Z","type":"session_meta","payload":{"id":"session-12345678","model_provider":"openai"}}
{"timestamp":"2026-08-09T01:00:01Z","type":"turn_context","payload":{"model":"gpt-5.6-sol"}}
{"timestamp":"2026-08-09T01:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000,"cached_input_tokens":800,"cache_write_input_tokens":10,"output_tokens":50,"reasoning_output_tokens":20},"last_token_usage":{"input_tokens":1000,"cached_input_tokens":800,"cache_write_input_tokens":10,"output_tokens":50,"reasoning_output_tokens":20}}}}
{"timestamp":"2026-08-09T01:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000,"cached_input_tokens":800,"cache_write_input_tokens":10,"output_tokens":50,"reasoning_output_tokens":20},"last_token_usage":{"input_tokens":1000,"cached_input_tokens":800,"cache_write_input_tokens":10,"output_tokens":50,"reasoning_output_tokens":20}}}}
{"timestamp":"2026-08-09T01:00:03Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":2200,"cached_input_tokens":1700,"cache_write_input_tokens":10,"output_tokens":120,"reasoning_output_tokens":50},"last_token_usage":{"input_tokens":1200,"cached_input_tokens":900,"cache_write_input_tokens":0,"output_tokens":70,"reasoning_output_tokens":30}}}}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	session := protocol.OrchestrationUsageSession{CLI: "codex", WorkerSlot: "codex-a", SessionID: "session-12345678"}
	events, err := parseCodexUsageLog(path, session)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if events[0].InputTokens != 200 || events[0].CacheReadTokens != 800 || events[0].ReasoningTokens != 20 {
		t.Fatalf("first event = %+v", events[0])
	}
	if events[1].InputTokens != 300 || events[1].OutputTokens != 70 {
		t.Fatalf("second event = %+v", events[1])
	}
	if events[0].EventID == events[1].EventID {
		t.Fatal("event IDs must be distinct")
	}
}

func TestParseClaudeUsageLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session-12345678.jsonl")
	content := `{"timestamp":"2026-08-09T01:00:00Z","type":"assistant","message":{"id":"msg_1","model":"claude-sonnet-4","usage":{"input_tokens":40,"cache_read_input_tokens":100,"cache_creation_input_tokens":20,"output_tokens":30}}}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	events, err := parseClaudeUsageLog(path, protocol.OrchestrationUsageSession{CLI: "claude", SessionID: "session-12345678"})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].InputTokens != 40 || events[0].CacheReadTokens != 100 || events[0].CacheWriteTokens != 20 {
		t.Fatalf("events = %+v", events)
	}
}

func TestScanUsageSessionDoesNotFallBackForIsolatedWorker(t *testing.T) {
	_, err := scanUsageSession(protocol.OrchestrationUsageSession{
		CLI:       "claude",
		SessionID: "session-12345678",
		Isolated:  true,
	})
	if err == nil || err.Error() != "isolated worker runtime is no longer available" {
		t.Fatalf("error = %v", err)
	}
}
