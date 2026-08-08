package bridge

import (
	"strings"
	"unicode/utf8"
)

// orchestrationUsage is deliberately compact because it is persisted as an
// orchestration event and becomes the source for the owner-scoped stats view.
// CLI JSONL formats do not consistently expose usage across versions, so this
// records a clearly labelled character-based fallback until native usage is
// available for that runner.
type orchestrationUsage struct {
	Model            string
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	EstimatedCostUSD float64
	Estimated        bool
}

func estimateOrchestrationUsage(model, prompt, content string) orchestrationUsage {
	usage := orchestrationUsage{
		Model:        strings.TrimSpace(model),
		InputTokens:  estimateTextTokens(prompt),
		OutputTokens: estimateTextTokens(content),
		Estimated:    true,
	}
	inputRate, outputRate, cacheReadRate, cacheWriteRate := orchestrationModelRates(usage.Model)
	usage.EstimatedCostUSD = float64(usage.InputTokens)*inputRate/1_000_000 +
		float64(usage.OutputTokens)*outputRate/1_000_000 +
		float64(usage.CacheReadTokens)*cacheReadRate/1_000_000 +
		float64(usage.CacheWriteTokens)*cacheWriteRate/1_000_000
	return usage
}

func estimateTextTokens(text string) int64 {
	count := utf8.RuneCountInString(strings.TrimSpace(text))
	if count == 0 {
		return 0
	}
	// Four characters per token is intentionally conservative and matches the
	// standard approximation used when providers omit token accounting.
	return int64((count + 3) / 4)
}

// Rates are USD per million tokens. They are estimates, not billing records;
// the configured model remains visible in the UI so an operator can audit the
// chosen pricing tier. Unknown models use a conservative mixed-model fallback.
func orchestrationModelRates(model string) (input, output, cacheRead, cacheWrite float64) {
	model = strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.Contains(model, "claude-opus"):
		return 15, 75, 1.5, 18.75
	case strings.Contains(model, "claude-sonnet"):
		return 3, 15, 0.3, 3.75
	case strings.Contains(model, "claude-haiku"):
		return 0.8, 4, 0.08, 1
	case strings.Contains(model, "gpt-5") || strings.Contains(model, "codex"):
		return 2.5, 10, 0.25, 0
	case strings.Contains(model, "gpt-4o"):
		return 2.5, 10, 1.25, 0
	default:
		return 3, 12, 0.3, 0
	}
}

func (m *OrchestrationManager) orchestrationUsageForTurn(cli, prompt, content string) orchestrationUsage {
	model := m.cfg.Bridge.Model
	if cli == "claude" {
		model = m.cfg.Bridge.ClaudeModel
	}
	return estimateOrchestrationUsage(model, prompt, content)
}
