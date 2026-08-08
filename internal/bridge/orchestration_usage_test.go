package bridge

import "testing"

func TestEstimateOrchestrationUsageUsesModelRates(t *testing.T) {
	usage := estimateOrchestrationUsage("claude-sonnet-4", "12345678", "12345678")
	if usage.InputTokens != 2 || usage.OutputTokens != 2 || !usage.Estimated || usage.EstimatedCostUSD <= 0 {
		t.Fatalf("usage = %#v", usage)
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
