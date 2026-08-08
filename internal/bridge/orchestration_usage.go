package bridge

import (
	"encoding/json"
	"math"
	"strings"
)

// orchestrationUsage is deliberately compact because it is persisted as an
// orchestration event and becomes the source for the owner-scoped stats view.
// Native provider counts are preserved exactly. Missing usage stays unavailable
// instead of being approximated from visible character counts.
type orchestrationUsage struct {
	Model            string
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	EstimatedCostUSD float64
	Estimated        bool
	Native           bool
	CostKnown        bool
	CostSource       string
}

func estimateOrchestrationUsage(model, prompt, content string) orchestrationUsage {
	usage := orchestrationUsage{
		Model:      strings.TrimSpace(model),
		Estimated:  true,
		CostSource: "unavailable",
	}
	return usage
}

// Rates are USD per million tokens. They are only used when the provider
// returned native counts but did not include its own cost.
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
		return 1.25, 10, 0.125, 0
	case strings.Contains(model, "gpt-4o"):
		return 2.5, 10, 1.25, 0
	default:
		return 0, 0, 0, 0
	}
}

func (m *OrchestrationManager) recordNativeUsage(turnID, cli, model string, raw json.RawMessage) {
	usage, ok := normalizeNativeUsage(cli, model, raw)
	if !ok {
		return
	}
	m.mu.Lock()
	m.nativeUsage[turnID] = append(m.nativeUsage[turnID], usage)
	m.mu.Unlock()
}

func (m *OrchestrationManager) orchestrationUsageForTurn(turnID, cli, prompt, content string) orchestrationUsage {
	model := m.cfg.Bridge.Model
	if cli == "claude" {
		model = m.cfg.Bridge.ClaudeModel
	}
	m.mu.Lock()
	native, ok := m.nativeUsage[turnID]
	delete(m.nativeUsage, turnID)
	m.mu.Unlock()
	if ok {
		usage := orchestrationUsage{Model: strings.TrimSpace(model), Native: true, CostSource: "unavailable"}
		for _, item := range native {
			if usage.Model == "" {
				usage.Model = item.Model
			}
			usage.InputTokens += item.InputTokens
			usage.OutputTokens += item.OutputTokens
			usage.CacheReadTokens += item.CacheReadTokens
			usage.CacheWriteTokens += item.CacheWriteTokens
			usage.EstimatedCostUSD += item.EstimatedCostUSD
			usage.CostKnown = usage.CostKnown || item.CostKnown
			if item.CostSource != "" {
				usage.CostSource = item.CostSource
			}
		}
		return usage
	}
	return estimateOrchestrationUsage(model, prompt, content)
}

func normalizeNativeUsage(cli, model string, raw json.RawMessage) (orchestrationUsage, bool) {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return orchestrationUsage{}, false
	}
	outer := value
	if nested, ok := value["usage"].(map[string]any); ok {
		value = nested
	}
	if nested, ok := value["last"].(map[string]any); ok {
		value = nested
	}
	get := func(keys ...string) int64 {
		for _, key := range keys {
			if number, ok := value[key].(float64); ok && number >= 0 && number <= math.MaxInt64 {
				return int64(number)
			}
			if number, ok := value[key].(json.Number); ok {
				if n, err := number.Int64(); err == nil && n >= 0 {
					return n
				}
			}
		}
		return 0
	}
	usage := orchestrationUsage{Model: strings.TrimSpace(model), Native: true}
	usage.InputTokens = get("input_tokens", "inputTokens")
	usage.OutputTokens = get("output_tokens", "outputTokens")
	usage.CacheReadTokens = get("cache_read_input_tokens", "cachedInputTokens", "cacheReadTokens")
	usage.CacheWriteTokens = get("cache_creation_input_tokens", "cacheWriteInputTokens", "cacheWriteTokens")
	if usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.CacheReadTokens == 0 && usage.CacheWriteTokens == 0 {
		return orchestrationUsage{}, false
	}
	if model == "" {
		if nested, ok := outer["modelUsage"].(map[string]any); ok {
			for name := range nested {
				model = name
				usage.Model = name
				break
			}
		}
	}
	if nested, ok := outer["modelUsage"].(map[string]any); ok {
		for _, item := range nested {
			if entry, ok := item.(map[string]any); ok {
				if cost, ok := entry["costUSD"].(float64); ok {
					usage.EstimatedCostUSD = cost
					usage.CostKnown = true
					usage.CostSource = "provider"
					break
				}
			}
		}
	}
	if cost, ok := outer["costUSD"].(float64); ok {
		usage.EstimatedCostUSD = cost
		usage.CostKnown = true
		usage.CostSource = "provider"
	}
	if !usage.CostKnown {
		input, output, read, write := orchestrationModelRates(usage.Model)
		if input > 0 || output > 0 {
			usage.EstimatedCostUSD = float64(usage.InputTokens)*input/1e6 + float64(usage.OutputTokens)*output/1e6 + float64(usage.CacheReadTokens)*read/1e6 + float64(usage.CacheWriteTokens)*write/1e6
			usage.CostKnown = true
			usage.CostSource = "catalog"
		} else {
			usage.CostSource = "unavailable"
		}
	}
	return usage, true
}
