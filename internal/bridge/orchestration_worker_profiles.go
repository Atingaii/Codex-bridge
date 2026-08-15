package bridge

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tencent/codex-bridge/internal/protocol"
)

// orchestrationWorkerRuntime is private Bridge state. Its directory is only
// passed to the child process, never emitted as orchestration event data.
type orchestrationWorkerRuntime struct {
	fingerprint     string
	dir             string
	env             []string
	model           string
	presetName      string
	reasoningEffort string
}

func workerProfileContains(values []string, want string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}

func (r orchestrationWorkerRuntime) claudeConfigDir() string {
	for _, value := range r.env {
		const prefix = "CLAUDE_CONFIG_DIR="
		if strings.HasPrefix(value, prefix) {
			return strings.TrimPrefix(value, prefix)
		}
	}
	return ""
}

func (r orchestrationWorkerRuntime) remove() {
	if r.dir != "" {
		_ = os.RemoveAll(r.dir)
	}
}

// retainResumeMetadata leaves native session records available to the operator
// without retaining the worker's encrypted provider credential after a run.
func (r orchestrationWorkerRuntime) retainResumeMetadata() {
	if r.dir == "" {
		return
	}
	_ = os.Remove(filepath.Join(r.dir, "codex", "auth.json"))
	_ = os.Remove(filepath.Join(r.dir, "claude", "settings.json"))
}

func (r orchestrationWorkerRuntime) codexHome() string {
	for _, value := range r.env {
		if strings.HasPrefix(value, "CODEX_HOME=") {
			return strings.TrimPrefix(value, "CODEX_HOME=")
		}
	}
	return ""
}

func workerProfileBinding(payload protocol.OrchestrationStartPayload, slot, cli string) (protocol.WorkerProfileBinding, bool) {
	binding, ok := payload.WorkerProfiles[slot]
	if !ok || binding.CLI != cli || strings.TrimSpace(binding.PresetID) == "" {
		return protocol.WorkerProfileBinding{}, false
	}
	return binding, true
}

func workerProfileFingerprint(binding protocol.WorkerProfileBinding) string {
	return strings.Join([]string{binding.PresetID, binding.CLI, binding.BaseURL, binding.Model, binding.ReasoningEffort, fmt.Sprint(binding.ClaudeContextWindow), fmt.Sprint(binding.ClaudeDisableUnknownModelWindowEnforcement), binding.Secret.Ciphertext}, "\x00")
}

func (m *OrchestrationManager) workerRuntime(payload protocol.OrchestrationStartPayload, slot, cli string, session *orchestrationNativeSession) (orchestrationWorkerRuntime, error) {
	binding, bound := workerProfileBinding(payload, slot, cli)
	if !bound {
		return orchestrationWorkerRuntime{}, nil
	}
	if m.cliConfig == nil {
		return orchestrationWorkerRuntime{}, fmt.Errorf("encrypted worker profile requires the Bridge configuration switcher")
	}
	// Hub authorizes model capabilities. Bridge checks only that its received
	// snapshot is self-consistent; it does not consult a local model catalog.
	if effort := strings.TrimSpace(binding.ReasoningEffort); effort != "" && !workerProfileContains(binding.ReasoningLevels, effort) {
		return orchestrationWorkerRuntime{}, fmt.Errorf("%s worker profile selects unsupported reasoning effort %q for reviewed model %q", slot, effort, binding.Model)
	}
	fingerprint := workerProfileFingerprint(binding)
	session.mu.Lock()
	if cached, ok := session.profileRuntime[slot]; ok && cached.fingerprint == fingerprint {
		session.mu.Unlock()
		return cached, nil
	}
	old := session.profileRuntime[slot]
	session.mu.Unlock()
	old.remove()

	key, err := m.cliConfig.decrypt(binding.Secret)
	if err != nil {
		return orchestrationWorkerRuntime{}, fmt.Errorf("decrypt %s worker profile: %w", slot, err)
	}
	defer clear(key)
	home, err := os.UserHomeDir()
	if err != nil {
		return orchestrationWorkerRuntime{}, err
	}
	dir := filepath.Join(home, ".codex-bridge", "orchestration-profiles", safeOrchestrationRuntimeName(payload.RunID), safeOrchestrationRuntimeName(slot))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return orchestrationWorkerRuntime{}, err
	}
	runtime := orchestrationWorkerRuntime{fingerprint: fingerprint, dir: dir, model: binding.Model, presetName: binding.Name, reasoningEffort: binding.ReasoningEffort}
	if cli == "codex" {
		codexHome := filepath.Join(dir, "codex")
		if err := os.MkdirAll(codexHome, 0o700); err != nil {
			runtime.remove()
			return orchestrationWorkerRuntime{}, err
		}
		configText := updateCodexConfig("", binding.BaseURL, binding.Model, binding.ReasoningEffort, false)
		if err := atomicWrite(filepath.Join(codexHome, "config.toml"), []byte(configText), 0o600); err != nil {
			runtime.remove()
			return orchestrationWorkerRuntime{}, err
		}
		auth, _ := json.Marshal(map[string]string{"OPENAI_API_KEY": string(key)})
		if err := atomicWrite(filepath.Join(codexHome, "auth.json"), append(auth, '\n'), 0o600); err != nil {
			runtime.remove()
			return orchestrationWorkerRuntime{}, err
		}
		runtime.env = []string{"CODEX_HOME=" + codexHome}
	} else {
		claudeHome := filepath.Join(dir, "claude")
		if err := os.MkdirAll(claudeHome, 0o700); err != nil {
			runtime.remove()
			return orchestrationWorkerRuntime{}, err
		}
		settings := map[string]any{"env": map[string]any{"ANTHROPIC_BASE_URL": claudeBaseURL(binding.BaseURL), "ANTHROPIC_AUTH_TOKEN": string(key)}}
		setClaudeModelFields(settings, binding.Model)
		applyClaudeContextSettings(settings, binding.ClaudeContextWindow, binding.ClaudeDisableUnknownModelWindowEnforcement)
		if effort := strings.TrimSpace(binding.ReasoningEffort); effort != "" {
			settings["effortLevel"] = effort
		}
		raw, err := json.MarshalIndent(settings, "", "  ")
		if err != nil {
			runtime.remove()
			return orchestrationWorkerRuntime{}, err
		}
		if err := atomicWrite(filepath.Join(claudeHome, "settings.json"), append(raw, '\n'), 0o600); err != nil {
			runtime.remove()
			return orchestrationWorkerRuntime{}, err
		}
		runtime.env = []string{"CLAUDE_CONFIG_DIR=" + claudeHome, "ANTHROPIC_BASE_URL=" + claudeBaseURL(binding.BaseURL), "ANTHROPIC_AUTH_TOKEN=" + string(key), "ANTHROPIC_MODEL=" + binding.Model}
	}
	session.mu.Lock()
	if session.profileRuntime == nil {
		session.profileRuntime = map[string]orchestrationWorkerRuntime{}
	}
	session.profileRuntime[slot] = runtime
	session.mu.Unlock()
	return runtime, nil
}

func (m *OrchestrationManager) codexRuntime(payload protocol.OrchestrationStartPayload, slot string) (orchestrationWorkerRuntime, error) {
	if session := m.nativeSession(payload.RunID, m.cwd(payload)); session != nil {
		return m.workerRuntime(payload, normalizeCodexWorkerSlot(slot), "codex", session)
	}
	return orchestrationWorkerRuntime{}, nil
}

func (m *OrchestrationManager) codexModelForPayload(payload protocol.OrchestrationStartPayload, slot string) string {
	if binding, ok := workerProfileBinding(payload, normalizeCodexWorkerSlot(slot), "codex"); ok {
		return binding.Model
	}
	return codexBridgeModel(m.cfg)
}

func (m *OrchestrationManager) workerModelForPayload(payload protocol.OrchestrationStartPayload, slot, cli string) string {
	if binding, ok := workerProfileBinding(payload, slot, cli); ok {
		return binding.Model
	}
	if cli == "claude" {
		return claudeBridgeModel(m.cfg)
	}
	return codexBridgeModel(m.cfg)
}

func (m *OrchestrationManager) claudeModelForPayload(payload protocol.OrchestrationStartPayload) string {
	return m.claudeModelForPayloadSlot(payload, orchestrationClaudeDefaultSlot)
}

func (m *OrchestrationManager) claudeModelForPayloadSlot(payload protocol.OrchestrationStartPayload, slot string) string {
	if binding, ok := workerProfileBinding(payload, normalizeClaudeWorkerSlot(slot), "claude"); ok {
		return binding.Model
	}
	return claudeBridgeModel(m.cfg)
}

func (m *OrchestrationManager) claudeEffortForPayloadSlot(payload protocol.OrchestrationStartPayload, slot string) string {
	if binding, ok := workerProfileBinding(payload, normalizeClaudeWorkerSlot(slot), "claude"); ok {
		return binding.ReasoningEffort
	}
	return m.cfg.Bridge.ClaudeEffort
}

func safeOrchestrationRuntimeName(value string) string {
	return safeOrchestrationFileName.ReplaceAllString(strings.TrimSpace(value), "_")
}

func applyWorkerRuntime(cmd *exec.Cmd, runtime orchestrationWorkerRuntime) {
	if len(runtime.env) > 0 {
		replaceCommandEnv(cmd, runtime.env...)
	}
}
