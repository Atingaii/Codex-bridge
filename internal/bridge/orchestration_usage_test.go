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
	usage, ok := normalizeNativeUsage("codex", "gpt-5.6-sol", json.RawMessage(`{"last":{"inputTokens":100,"cachedInputTokens":40,"cacheWriteInputTokens":2,"outputTokens":20}}`))
	if !ok || !usage.Native || usage.InputTokens != 100 || usage.OutputTokens != 20 || usage.CacheReadTokens != 40 || usage.CacheWriteTokens != 2 || !usage.CostKnown || usage.PricingModel != "gpt-5.6-sol" || usage.CostSource != "official-catalog" {
		t.Fatalf("usage = %#v ok=%v", usage, ok)
	}
}

func TestNormalizeNativeUsageCodexDefaultUsesOfficialAnchor(t *testing.T) {
	usage, ok := normalizeNativeUsage("codex", "default", json.RawMessage(`{"last":{"inputTokens":23858,"outputTokens":1605}}`))
	if !ok || !usage.CostKnown || usage.PricingModel != "gpt-5.6-sol" || usage.EstimatedCostUSD < 0.167439 || usage.EstimatedCostUSD > 0.167441 {
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
	first := orchestrationTurn{TurnID: "t", Usage: orchestrationUsage{Model: "m", InputTokens: 3, OutputTokens: 4, EstimatedCostUSD: .1, Estimated: true, CostKnown: true, CostSource: "provider"}}
	next := orchestrationTurn{TurnID: "t", Usage: orchestrationUsage{InputTokens: 5, OutputTokens: 6, CacheReadTokens: 2, EstimatedCostUSD: .2, CostKnown: false, CostSource: "unavailable"}}
	got := mergeOrchestrationTurnAttempts(first, next)
	if got.Usage.InputTokens != 8 || got.Usage.OutputTokens != 10 || got.Usage.CacheReadTokens != 2 || got.Usage.EstimatedCostUSD < .299 || got.Usage.EstimatedCostUSD > .301 || got.Usage.CostKnown || got.Usage.CostSource != "mixed" {
		t.Fatalf("usage = %#v", got.Usage)
	}
}
