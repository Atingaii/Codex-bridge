package usagepricing

import "strings"

const (
	SourceOfficialCatalog = "official-catalog"
	DefaultCodexModel     = "gpt-5.6-sol"
)

type Rates struct {
	Model                string
	InputPerMillion      float64
	CachedPerMillion     float64
	CacheWritePerMillion float64
	OutputPerMillion     float64
	CachedIncludedInput  bool
}

type Quote struct {
	PricingModel string
	CostUSD      float64
	Source       string
}

// Estimate applies the standard public API price for a known model. Codex
// "default" is anchored to the Bridge host's supported default model so usage
// events remain priceable without pretending the CLI reported a model name.
func Estimate(cli, model string, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int64) (Quote, bool) {
	rates, ok := Resolve(cli, model)
	if !ok || inputTokens+outputTokens+cacheReadTokens+cacheWriteTokens <= 0 {
		return Quote{}, false
	}
	if cacheWriteTokens > 0 && rates.CacheWritePerMillion == 0 {
		return Quote{}, false
	}
	billableInput := inputTokens
	if rates.CachedIncludedInput {
		billableInput -= cacheReadTokens
		if billableInput < 0 {
			billableInput = 0
		}
	}
	cost := float64(billableInput)*rates.InputPerMillion/1e6 +
		float64(cacheReadTokens)*rates.CachedPerMillion/1e6 +
		float64(cacheWriteTokens)*rates.CacheWritePerMillion/1e6 +
		float64(outputTokens)*rates.OutputPerMillion/1e6
	source := "catalog"
	if strings.HasPrefix(rates.Model, "gpt-") {
		source = SourceOfficialCatalog
	}
	return Quote{PricingModel: rates.Model, CostUSD: cost, Source: source}, true
}

func Resolve(cli, model string) (Rates, bool) {
	cli = strings.ToLower(strings.TrimSpace(cli))
	model = strings.ToLower(strings.TrimSpace(model))
	if cli == "codex" && (model == "" || model == "default" || model == "codex") {
		model = DefaultCodexModel
	}
	switch {
	case strings.Contains(model, "gpt-5.6-sol"):
		return openAI(model, "gpt-5.6-sol", 5, 0.5, 6.25, 30), true
	case strings.Contains(model, "gpt-5.6-terra"):
		return openAI(model, "gpt-5.6-terra", 2, 0.2, 2.5, 12), true
	case strings.Contains(model, "gpt-5.6-luna"):
		return openAI(model, "gpt-5.6-luna", 0.2, 0.02, 0.25, 1.2), true
	case strings.Contains(model, "gpt-5.5-pro"):
		return openAI(model, "gpt-5.5-pro", 30, 0, 0, 180), true
	case strings.Contains(model, "gpt-5.5"):
		return openAI(model, "gpt-5.5", 5, 0.5, 0, 30), true
	case strings.Contains(model, "gpt-5.4-mini"):
		return openAI(model, "gpt-5.4-mini", 0.75, 0.075, 0, 4.5), true
	case strings.Contains(model, "gpt-5.4-nano"):
		return openAI(model, "gpt-5.4-nano", 0.2, 0.02, 0, 1.25), true
	case strings.Contains(model, "gpt-5.4-pro"):
		return openAI(model, "gpt-5.4-pro", 30, 0, 0, 180), true
	case strings.Contains(model, "gpt-5.4"):
		return openAI(model, "gpt-5.4", 2.5, 0.25, 0, 15), true
	case strings.Contains(model, "gpt-5.3-codex"):
		return openAI(model, "gpt-5.3-codex", 1.75, 0.175, 0, 14), true
	case strings.Contains(model, "gpt-5.2-pro"):
		return openAI(model, "gpt-5.2-pro", 21, 0, 0, 168), true
	case strings.Contains(model, "gpt-5.2"):
		return openAI(model, "gpt-5.2", 1.75, 0.175, 0, 14), true
	case strings.Contains(model, "gpt-5-mini"):
		return openAI(model, "gpt-5-mini", 0.25, 0.025, 0, 2), true
	case strings.Contains(model, "gpt-5-nano"):
		return openAI(model, "gpt-5-nano", 0.05, 0.005, 0, 0.4), true
	case strings.Contains(model, "gpt-5-pro"):
		return openAI(model, "gpt-5-pro", 15, 0, 0, 120), true
	case strings.Contains(model, "gpt-5.1"):
		return openAI(model, "gpt-5.1", 1.25, 0.125, 0, 10), true
	case model == "gpt-5" || strings.HasPrefix(model, "gpt-5-202"):
		return openAI(model, "gpt-5", 1.25, 0.125, 0, 10), true
	case strings.Contains(model, "gpt-4o-mini"):
		return openAI(model, "gpt-4o-mini", 0.15, 0.075, 0, 0.6), true
	case model == "gpt-4o" || strings.HasPrefix(model, "gpt-4o-2024"):
		return openAI(model, "gpt-4o", 2.5, 1.25, 0, 10), true
	case strings.Contains(model, "claude-opus"):
		return Rates{Model: normalizedModel(model, "claude-opus"), InputPerMillion: 15, CachedPerMillion: 1.5, CacheWritePerMillion: 18.75, OutputPerMillion: 75}, true
	case strings.Contains(model, "claude-sonnet"):
		return Rates{Model: normalizedModel(model, "claude-sonnet"), InputPerMillion: 3, CachedPerMillion: 0.3, CacheWritePerMillion: 3.75, OutputPerMillion: 15}, true
	case strings.Contains(model, "claude-haiku"):
		return Rates{Model: normalizedModel(model, "claude-haiku"), InputPerMillion: 0.8, CachedPerMillion: 0.08, CacheWritePerMillion: 1, OutputPerMillion: 4}, true
	default:
		return Rates{}, false
	}
}

func openAI(actualModel, fallbackModel string, input, cached, cacheWrite, output float64) Rates {
	return Rates{
		Model:                normalizedModel(actualModel, fallbackModel),
		InputPerMillion:      input,
		CachedPerMillion:     cached,
		CacheWritePerMillion: cacheWrite,
		OutputPerMillion:     output,
		CachedIncludedInput:  true,
	}
}

func normalizedModel(model, fallback string) string {
	if strings.TrimSpace(model) == "" {
		return fallback
	}
	return model
}
