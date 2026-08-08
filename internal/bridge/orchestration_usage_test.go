package bridge

import (
	"encoding/json"
	"testing"
)

func TestEstimateOrchestrationUsageMarksMissingNativeData(t *testing.T) {
	usage := estimateOrchestrationUsage("claude-sonnet-4", "12345678", "12345678")
	if usage.InputTokens != 0 || usage.OutputTokens != 0 || !usage.Estimated || usage.CostKnown || usage.CostSource != "unavailable" {
		t.Fatalf("usage = %#v", usage)
	}
}

func TestNormalizeNativeUsageCodex(t *testing.T) {
	usage, ok := normalizeNativeUsage("codex", "gpt-5-codex", json.RawMessage(`{"last":{"inputTokens":100,"cachedInputTokens":40,"cacheWriteInputTokens":2,"outputTokens":20}}`))
	if !ok || !usage.Native || usage.InputTokens != 100 || usage.OutputTokens != 20 || usage.CacheReadTokens != 40 || usage.CacheWriteTokens != 2 || !usage.CostKnown {
		t.Fatalf("usage = %#v ok=%v", usage, ok)
	}
}

func TestNormalizeNativeUsageClaudeProviderCost(t *testing.T) {
	usage, ok := normalizeNativeUsage("claude", "", json.RawMessage(`{"usage":{"input_tokens":100,"output_tokens":20},"modelUsage":{"claude-sonnet-4":{"costUSD":0.123}}}`))
	if !ok || usage.Model != "claude-sonnet-4" || usage.EstimatedCostUSD != 0.123 || usage.CostSource != "provider" {
		t.Fatalf("usage = %#v ok=%v", usage, ok)
	}
}

func TestMergeOrchestrationTurnAttemptsAggregatesUsage(t *testing.T) {
	first := orchestrationTurn{TurnID: "t", Usage: orchestrationUsage{Model: "m", InputTokens: 3, OutputTokens: 4, EstimatedCostUSD: .1, Estimated: true}}
	next := orchestrationTurn{TurnID: "t", Usage: orchestrationUsage{InputTokens: 5, OutputTokens: 6, CacheReadTokens: 2, EstimatedCostUSD: .2}}
	got := mergeOrchestrationTurnAttempts(first, next)
	if got.Usage.InputTokens != 8 || got.Usage.OutputTokens != 10 || got.Usage.CacheReadTokens != 2 || got.Usage.EstimatedCostUSD < .299 || got.Usage.EstimatedCostUSD > .301 {
		t.Fatalf("usage = %#v", got.Usage)
	}
}
