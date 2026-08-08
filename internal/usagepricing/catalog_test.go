package usagepricing

import "testing"

func TestEstimateCodexDefaultUsesOfficialAnchor(t *testing.T) {
	quote, ok := Estimate("codex", "default", 23858, 1605, 0, 0)
	if !ok || quote.PricingModel != DefaultCodexModel || quote.CostUSD < 0.167439 || quote.CostUSD > 0.167441 {
		t.Fatalf("quote = %#v ok=%v", quote, ok)
	}
}

func TestEstimateOpenAICachedInputIsNotDoubleCharged(t *testing.T) {
	quote, ok := Estimate("codex", "gpt-5.6-sol", 100, 20, 40, 0)
	if !ok || quote.CostUSD < 0.000919 || quote.CostUSD > 0.000921 {
		t.Fatalf("quote = %#v ok=%v", quote, ok)
	}
}

func TestEstimateUnknownModelIsUnavailable(t *testing.T) {
	if quote, ok := Estimate("other", "default", 100, 20, 0, 0); ok {
		t.Fatalf("unexpected quote = %#v", quote)
	}
}
