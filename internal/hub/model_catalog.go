package hub

import (
	"strings"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

// reviewedModelFamilies is deliberately explicit. Provider model listings are
// transport data, not a policy source. Each listed family receives the native
// effort values supported by the selected CLI.
type reviewedModelFamily struct {
	Prefixes []string
	Exact    []string
	Levels   []string
	Default  string
}

// reviewedClaudeContextProfile is intentionally separate from reasoning
// effort. A provider may expose a model through Claude-compatible transport
// without sharing either capability. The Hub is the policy authority; Bridge
// receives only the resolved values for the selected preset.
type reviewedClaudeContextProfile struct {
	Window                    int
	DisableUnknownEnforcement bool
}

var reviewedClaudeContextProfiles = map[string]reviewedClaudeContextProfile{
	"claude-fable-5":         {Window: 1_000_000},
	"claude-opus-5":          {Window: 1_000_000},
	"claude-sonnet-5":        {Window: 1_000_000},
	"claude-opus-4-6":        {Window: 1_000_000},
	"claude-sonnet-4-6":      {Window: 1_000_000},
	"claude-opus-4-5":        {Window: 200_000},
	"claude-sonnet-4-5":      {Window: 200_000},
	"claude-3-7-sonnet":      {Window: 200_000},
	"claude-3-5-sonnet":      {Window: 200_000},
	"claude-3-5-haiku":       {Window: 200_000},
	"claude-haiku-4-5":       {Window: 200_000},
	"claude-3-opus":          {Window: 200_000},
	"claude-3-sonnet":        {Window: 200_000},
	"claude-3-haiku":         {Window: 200_000},
	"gpt-5.6":                {Window: 1_050_000, DisableUnknownEnforcement: true},
	"gpt-5.6-sol":            {Window: 1_050_000, DisableUnknownEnforcement: true},
	"gpt-5.6-terra":          {Window: 1_050_000, DisableUnknownEnforcement: true},
	"gpt-5.6-luna":           {Window: 1_050_000, DisableUnknownEnforcement: true},
	"gpt-4.1":                {Window: 1_047_576, DisableUnknownEnforcement: true},
	"gpt-4.1-mini":           {Window: 1_047_576, DisableUnknownEnforcement: true},
	"gpt-4.1-nano":           {Window: 1_047_576, DisableUnknownEnforcement: true},
	"gpt-4o":                 {Window: 128_000, DisableUnknownEnforcement: true},
	"gpt-4o-mini":            {Window: 128_000, DisableUnknownEnforcement: true},
	"deepseek-v4-flash":      {Window: 1_000_000, DisableUnknownEnforcement: true},
	"deepseek-chat":          {Window: 128_000, DisableUnknownEnforcement: true},
	"deepseek-reasoner":      {Window: 128_000, DisableUnknownEnforcement: true},
	"deepseek-v3":            {Window: 128_000, DisableUnknownEnforcement: true},
	"deepseek-r1":            {Window: 128_000, DisableUnknownEnforcement: true},
	"kimi-k2.5":              {Window: 262_144, DisableUnknownEnforcement: true},
	"kimi-k3":                {Window: 1_000_000, DisableUnknownEnforcement: true},
	"gemini-2.5-pro":         {Window: 1_048_576, DisableUnknownEnforcement: true},
	"gemini-2.5-flash":       {Window: 1_048_576, DisableUnknownEnforcement: true},
	"gemini-2.5-flash-lite":  {Window: 1_048_576, DisableUnknownEnforcement: true},
	"gemini-2.0-flash":       {Window: 1_048_576, DisableUnknownEnforcement: true},
	"llama-3.3-70b-instruct": {Window: 128_000, DisableUnknownEnforcement: true},
	"mistral-large-latest":   {Window: 128_000, DisableUnknownEnforcement: true},
}

var reviewedModelFamilies = []reviewedModelFamily{
	{Exact: []string{"claude-opus-5", "claude-fable-5", "claude-sonnet-5", "claude-opus-4.8", "claude-opus-4-8"}, Levels: []string{"low", "medium", "high", "xhigh", "max"}, Default: "high"},
	{Exact: []string{"claude-opus-4.7", "claude-opus-4-7", "claude-sonnet-4.7", "claude-sonnet-4-7", "claude-sonnet-4.8", "claude-sonnet-4-8"}, Levels: []string{"low", "medium", "high", "xhigh", "max"}, Default: "high"},
	{Exact: []string{"claude-opus-4.6", "claude-opus-4-6", "claude-sonnet-4.6", "claude-sonnet-4-6"}, Levels: []string{"low", "medium", "high", "max"}, Default: "high"},
	{Exact: []string{"gpt-5.6", "gpt-5.6-terra"}, Levels: []string{"none", "low", "medium", "high", "xhigh", "max"}, Default: "medium"},
	{Exact: []string{"gpt-5.6-sol", "gpt-5.6-luna"}, Levels: []string{"low", "medium", "high", "xhigh", "max"}, Default: "medium"},
	{Exact: []string{"gpt-5.5", "grok-4.6", "grok-4-6"}, Levels: []string{"low", "medium", "high", "xhigh"}, Default: "medium"},
	{Exact: []string{"gemini-3.7-flash"}, Levels: []string{"low", "medium", "high"}, Default: "medium"},
	{Exact: []string{"glm-5.2"}, Levels: []string{"high", "max"}, Default: "high"},
	{Exact: []string{"deepseek-v4-pro", "deepseek-v4-flash"}, Levels: []string{"low", "high", "max"}, Default: "high"},
	{Exact: []string{"kimi-k3"}, Levels: []string{"max"}, Default: "max"},
	{Exact: []string{"qwen3.8-max", "muse-spark-1.2"}, Levels: []string{"xhigh"}, Default: "xhigh"},
	{Exact: []string{"gemini-3.6-flash", "gemini-3.5-flash"}, Levels: []string{"high"}, Default: "high"},
}

func reviewedModelMetadata(cli, model string) *protocol.CLIModelMetadata {
	cli = strings.ToLower(strings.TrimSpace(cli))
	if cli != "codex" && cli != "claude" {
		return nil
	}
	name := normalizeCatalogModel(model)
	if name == "" {
		return nil
	}
	for _, profile := range reviewedModelFamilies {
		matched := false
		for _, exact := range profile.Exact {
			matched = name == exact
			if matched {
				break
			}
		}
		if !matched {
			for _, prefix := range profile.Prefixes {
				if strings.HasPrefix(name, prefix) {
					matched = true
					break
				}
			}
		}
		if matched {
			return &protocol.CLIModelMetadata{Model: model, Reviewed: true, SupportedReasoningLevels: append([]string(nil), profile.Levels...), DefaultReasoningLevel: profile.Default}
		}
	}
	return nil
}

func normalizeCatalogModel(model string) string {
	name := strings.ToLower(strings.TrimSpace(model))
	name = strings.TrimSuffix(name, "[1m]")
	name = strings.TrimPrefix(name, "models/")
	for _, prefix := range []string{"openai/", "anthropic/", "deepseek/", "moonshot/", "kimi/", "zhipu/", "glm/", "google/", "gemini/", "xai/", "qwen/", "minimax/", "bytedance/"} {
		name = strings.TrimPrefix(name, prefix)
	}
	return name
}

func reviewedClaudeContextForModel(model string) (reviewedClaudeContextProfile, bool) {
	profile, ok := reviewedClaudeContextProfiles[normalizeCatalogModel(model)]
	return profile, ok
}

func normalizeReviewedPreset(p *store.CLIConfigPreset) {
	if p == nil {
		return
	}
	metadata := reviewedModelMetadata(p.CLI, p.Model)
	if metadata == nil {
		// Older experimental builds persisted browser-visible generic effort
		// levels for third-party aliases. The reviewed Hub catalog is the only
		// authority now, so stale capability data must not survive a read/bind.
		p.ReasoningLevels = nil
		p.ReasoningDefault = ""
		p.ReasoningEffort = ""
		return
	}
	p.ReasoningLevels = append([]string(nil), metadata.SupportedReasoningLevels...)
	p.ReasoningDefault = metadata.DefaultReasoningLevel
	if p.ReasoningEffort != "" && !containsString(p.ReasoningLevels, p.ReasoningEffort) {
		p.ReasoningEffort = ""
	}
}
