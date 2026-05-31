package registry

import (
	"github.com/tencent/codex-bridge/internal/bridge/profiles"
	"github.com/tencent/codex-bridge/internal/bridge/profiles/formalproof"
)

func Normalize(value string) string {
	return profiles.Normalize(value)
}

func UsesSpecialRules(value string) bool {
	return profiles.IsFormal(value)
}

func TimeoutBoundary(profile, userPrompt string) string {
	if !profiles.IsFormal(profile) {
		return ""
	}
	return formalproof.TimeoutBoundary(userPrompt)
}

func RelayGuidance(profile, userPrompt, mode, role string) string {
	if !profiles.IsFormal(profile) {
		return ""
	}
	return formalproof.RelayGuidance(userPrompt, mode, role)
}

func InitialStrategy(profile, mode, firstCLI, userPrompt string) string {
	if !profiles.IsFormal(profile) {
		return ""
	}
	return formalproof.InitialStrategy(mode, firstCLI, userPrompt)
}

func RecordCommandFingerprints(memory map[string]profiles.CommandFingerprint, cwd string, commands []profiles.Command) {
	formalproof.RecordCommandFingerprints(memory, cwd, commands)
}
