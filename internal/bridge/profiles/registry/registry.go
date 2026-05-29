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

func TaskGuidance(profile, userPrompt, mode, role string) string {
	if !profiles.IsFormal(profile) {
		return ""
	}
	return formalproof.TaskGuidance(userPrompt, mode, role)
}

func VerifierGuidance(profile, userPrompt, mode string) string {
	if !profiles.IsFormal(profile) {
		return ""
	}
	return formalproof.VerifierGuidance(userPrompt, mode)
}

func AcceptanceFailure(userPrompt string, history []profiles.Turn) (string, bool) {
	return formalproof.AcceptanceFailure(userPrompt, history)
}

func AcceptanceBlockerSummary(userPrompt string, history []profiles.Turn) string {
	return formalproof.AcceptanceBlockerSummary(userPrompt, history)
}

func HasUnresolvedAcceptanceSignal(text, userPrompt string) bool {
	return formalproof.HasUnresolvedAcceptanceSignal(text, userPrompt)
}

func AssessmentGap(userPrompt string, history []profiles.Turn, changes profiles.WorkspaceChangeReport, memory map[string]profiles.CommandFingerprint) (string, bool) {
	return formalproof.AssessmentGap(userPrompt, history, changes, memory)
}

func ForegroundBuildError(userPrompt string, commands []profiles.Command, existing error) error {
	return formalproof.ForegroundBuildError(userPrompt, commands, existing)
}

func ManualBuildRequired(reason string, history []profiles.Turn, memory map[string]profiles.CommandFingerprint) bool {
	return formalproof.ManualBuildRequired(reason, history, memory)
}

func ManualBuildReason(userPrompt string, history []profiles.Turn, memory map[string]profiles.CommandFingerprint) string {
	return formalproof.ManualBuildReason(userPrompt, history, memory)
}

func ManualBuildVisibleSummary(userPrompt, contextSummary string, history []profiles.Turn, memory map[string]profiles.CommandFingerprint) string {
	return formalproof.ManualBuildVisibleSummary(userPrompt, contextSummary, history, memory)
}

func RecordCommandFingerprints(memory map[string]profiles.CommandFingerprint, cwd string, commands []profiles.Command) {
	formalproof.RecordCommandFingerprints(memory, cwd, commands)
}

func TextManualBuildSignal(text string) bool {
	return formalproof.ManualBuildSignal(text)
}

func LooksLikeSystemBTask(text string) bool {
	return formalproof.LooksLikeSystemBTask(text)
}

func LooksLikeSystemATask(text string) bool {
	return formalproof.LooksLikeSystemATask(text)
}

func PlaceholderScanEvidence(commandText, outputText, proseText string) bool {
	return formalproof.PlaceholderScanEvidence(commandText, outputText, proseText)
}

func PlaceholderScanEvidenceForHistory(history []profiles.Turn, proseText string) bool {
	return formalproof.PlaceholderScanEvidenceForHistory(history, proseText)
}

func CollectEvidence(history []profiles.Turn, changes profiles.WorkspaceChangeReport) formalproof.Evidence {
	return formalproof.CollectEvidence(history, changes)
}
