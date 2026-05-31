package bridge

import (
	bridgeprofiles "github.com/tencent/codex-bridge/internal/bridge/profiles"
	"github.com/tencent/codex-bridge/internal/bridge/profiles/registry"
)

func recordCommandFingerprints(state *orchestrationSessionState, cwd string, tools []RunnerToolEvent) {
	if state == nil {
		return
	}
	if state.CommandFingerprints == nil {
		state.CommandFingerprints = map[string]bridgeprofiles.CommandFingerprint{}
	}
	registry.RecordCommandFingerprints(state.CommandFingerprints, cwd, profileCommands(tools))
}

func profileCommands(tools []RunnerToolEvent) []bridgeprofiles.Command {
	out := make([]bridgeprofiles.Command, 0, len(tools))
	for _, tool := range tools {
		out = append(out, bridgeprofiles.Command{
			ID:          tool.ID,
			Status:      tool.Status,
			Command:     tool.Command,
			Output:      tool.Output,
			ExitCode:    tool.ExitCode,
			StartedAt:   tool.StartedAt,
			CompletedAt: tool.CompletedAt,
		})
	}
	return out
}

func normalizeOrchestrationProfile(value string) string {
	return registry.Normalize(value)
}
