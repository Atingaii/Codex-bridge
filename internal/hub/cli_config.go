package hub

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/serverutil"
	"github.com/tencent/codex-bridge/internal/store"
)

const (
	cliConfigRequestTimeout = 90 * time.Second
	cliConfigRateLimit      = 20
	cliConfigRateWindow     = time.Minute
	maxCLIConfigNameBytes   = 80
	maxCLIConfigURLBytes    = 2048
	maxCLIConfigModelBytes  = 160
	maxEncryptedFieldBytes  = 8192
)

type cliConfigPendingRequest struct {
	agentID string
	result  chan protocol.CLIConfigResult
}

type cliConfigInput struct {
	CLI              string                   `json:"cli"`
	PresetID         string                   `json:"presetId,omitempty"`
	Name             string                   `json:"name,omitempty"`
	BaseURL          string                   `json:"baseUrl"`
	Model            string                   `json:"model,omitempty"`
	ReasoningEffort  string                   `json:"reasoningEffort,omitempty"`
	ReasoningLevels  []string                 `json:"reasoningLevels,omitempty"`
	ReasoningDefault string                   `json:"reasoningDefault,omitempty"`
	Secret           protocol.EncryptedSecret `json:"secret"`
	KeyID            string                   `json:"keyId"`
}

type cliConfigResetInput struct {
	CLI string `json:"cli"`
}

func (s *Server) handleListCLIConfigPresets(w http.ResponseWriter, r *http.Request, uid string) {
	agentID := r.PathValue("agentID")
	if _, ok := s.requireCLIConfigAgent(w, r, uid, agentID, ""); !ok {
		return
	}
	presets, err := s.store.ListCLIConfigPresets(r.Context(), uid, agentID)
	if err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to list model presets")
		return
	}
	for i := range presets {
		normalizeReviewedPreset(&presets[i])
	}
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{"presets": presets})
}

func (s *Server) handleTestCLIConfig(w http.ResponseWriter, r *http.Request, uid string) {
	agentID := r.PathValue("agentID")
	if !s.allowAuthAttempt(r, "cli-config:"+agentID, uid, cliConfigRateLimit, cliConfigRateWindow) {
		serverutil.WriteError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many model configuration requests; try again shortly")
		return
	}
	var input cliConfigInput
	if err := serverutil.DecodeJSON(r, &input, 32<<10); err != nil {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid model configuration")
		return
	}
	capability, ok := s.requireCLIConfigAgent(w, r, uid, agentID, input.CLI)
	if !ok || !s.validateCLIConfigInput(w, input, capability, false, input.PresetID == "") {
		return
	}
	secret := input.Secret
	if input.PresetID != "" {
		preset, err := s.store.CLIConfigPresetByID(r.Context(), input.PresetID, uid, agentID)
		if errors.Is(err, store.ErrNotFound) {
			serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "model preset not found")
			return
		}
		if err != nil {
			serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to load model preset")
			return
		}
		if preset.CLI != input.CLI {
			serverutil.WriteError(w, http.StatusBadRequest, "BAD_CLI", "preset does not belong to the selected CLI")
			return
		}
		if !encryptedSecretPresent(secret) {
			if err := s.materializeCLIConfigPresetCredential(r.Context(), uid, agentID, &preset); err != nil {
				serverutil.WriteError(w, http.StatusConflict, "CREDENTIAL_UNAVAILABLE", err.Error())
				return
			}
			secret = preset.Secret
		}
	}
	request := protocol.CLIConfigRequest{
		CLI: input.CLI, BaseURL: strings.TrimSpace(input.BaseURL), Model: strings.TrimSpace(input.Model), ReasoningEffort: strings.TrimSpace(input.ReasoningEffort), ReasoningLevels: append([]string(nil), input.ReasoningLevels...), ReasoningDefault: strings.TrimSpace(input.ReasoningDefault), Secret: secret,
	}
	s.applyReviewedClaudeContext(&request)
	result, err := s.sendCLIConfigRequest(r.Context(), agentID, protocol.TypeCLIConfigTest, request)
	if err != nil {
		s.writeCLIConfigRelayError(w, err)
		return
	}
	if !result.OK {
		serverutil.WriteError(w, http.StatusBadGateway, "TEST_FAILED", result.Error)
		return
	}
	result.ModelMetadata = reviewedModelMetadata(input.CLI, input.Model)
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{"result": result})
}

// applyReviewedClaudeContext adds only Hub-reviewed context settings to a
// request. An unknown model intentionally leaves both values unset so Claude
// Code uses its own conservative defaults.
func (s *Server) applyReviewedClaudeContext(request *protocol.CLIConfigRequest) {
	if request == nil || request.CLI != "claude" {
		return
	}
	if profile, ok := reviewedClaudeContextForModel(request.Model); ok {
		request.ClaudeContextWindow = profile.Window
		request.ClaudeDisableUnknownModelWindowEnforcement = profile.DisableUnknownEnforcement
	}
}

func (s *Server) handleCreateCLIConfigPreset(w http.ResponseWriter, r *http.Request, uid string) {
	agentID := r.PathValue("agentID")
	var input cliConfigInput
	if err := serverutil.DecodeJSON(r, &input, 32<<10); err != nil {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid model preset")
		return
	}
	capability, ok := s.requireCLIConfigAgent(w, r, uid, agentID, input.CLI)
	if !ok || !s.validateCLIConfigInput(w, input, capability, true, true) {
		return
	}
	if capability.Version < 2 {
		serverutil.WriteError(w, http.StatusConflict, "BRIDGE_UPDATE_REQUIRED", "upgrade Bridge before saving a user-level model configuration")
		return
	}
	if !s.authorizeReviewedReasoning(w, r.Context(), agentID, &input, input.Secret) {
		return
	}
	var vaultSecret string
	if capability.Version >= 2 {
		var err error
		vaultSecret, err = s.captureCLIConfigVaultSecret(r.Context(), agentID, input.CLI, input.Secret)
		if err != nil {
			s.writeCLIConfigRelayError(w, err)
			return
		}
	}
	preset, err := s.store.CreateCLIConfigPreset(r.Context(), store.CLIConfigPreset{
		UserID: uid, AgentID: agentID, CLI: input.CLI, Name: strings.TrimSpace(input.Name),
		BaseURL: strings.TrimRight(strings.TrimSpace(input.BaseURL), "/"), Model: strings.TrimSpace(input.Model),
		ReasoningEffort: strings.TrimSpace(input.ReasoningEffort),
		ReasoningLevels: append([]string(nil), input.ReasoningLevels...), ReasoningDefault: strings.TrimSpace(input.ReasoningDefault),
		Secret: input.Secret, KeyHint: input.KeyID, VaultSecret: vaultSecret,
	})
	if err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to save model preset")
		return
	}
	serverutil.WriteJSON(w, http.StatusCreated, map[string]any{"preset": preset})
}

func (s *Server) handleUpdateCLIConfigPreset(w http.ResponseWriter, r *http.Request, uid string) {
	agentID := r.PathValue("agentID")
	var input cliConfigInput
	if err := serverutil.DecodeJSON(r, &input, 32<<10); err != nil {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid model preset")
		return
	}
	capability, ok := s.requireCLIConfigAgent(w, r, uid, agentID, input.CLI)
	if !ok || !s.validateCLIConfigInput(w, input, capability, true, false) {
		return
	}
	existing, err := s.store.CLIConfigPresetByID(r.Context(), r.PathValue("presetID"), uid, agentID)
	if errors.Is(err, store.ErrNotFound) {
		serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "model preset not found")
		return
	}
	if err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to load model preset")
		return
	}
	if existing.CLI != input.CLI {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_CLI", "preset CLI cannot be changed")
		return
	}
	secret, keyHint := existing.Secret, existing.KeyHint
	credentialChanged := encryptedSecretPresent(input.Secret)
	if credentialChanged && capability.Version < 2 {
		serverutil.WriteError(w, http.StatusConflict, "BRIDGE_UPDATE_REQUIRED", "upgrade Bridge before replacing a user-level model credential")
		return
	}
	if credentialChanged {
		if !validEncryptedSecret(w, input.Secret) {
			return
		}
		secret, keyHint = input.Secret, input.KeyID
	} else {
		if err := s.materializeCLIConfigPresetCredential(r.Context(), uid, agentID, &existing); err != nil {
			serverutil.WriteError(w, http.StatusConflict, "CREDENTIAL_UNAVAILABLE", err.Error())
			return
		}
		secret, keyHint = existing.Secret, existing.KeyHint
	}
	if !s.authorizeReviewedReasoning(w, r.Context(), agentID, &input, secret) {
		return
	}
	baseURL := strings.TrimRight(strings.TrimSpace(input.BaseURL), "/")
	model := strings.TrimSpace(input.Model)
	vaultSecret := existing.VaultSecret
	if credentialChanged && capability.Version >= 2 {
		vaultSecret, err = s.captureCLIConfigVaultSecret(r.Context(), agentID, input.CLI, secret)
		if err != nil {
			s.writeCLIConfigRelayError(w, err)
			return
		}
	}
	preset, err := s.store.UpdateCLIConfigPreset(r.Context(), store.CLIConfigPreset{
		ID: existing.ID, UserID: uid, AgentID: agentID, CLI: input.CLI,
		Name: strings.TrimSpace(input.Name), BaseURL: baseURL, Model: model,
		ReasoningEffort: strings.TrimSpace(input.ReasoningEffort),
		ReasoningLevels: append([]string(nil), input.ReasoningLevels...), ReasoningDefault: strings.TrimSpace(input.ReasoningDefault),
		Secret: secret, KeyHint: keyHint, VaultSecret: vaultSecret, ReplaceCredentials: credentialChanged,
	}, existing.BaseURL == baseURL && existing.Model == model && existing.ReasoningEffort == strings.TrimSpace(input.ReasoningEffort) && !credentialChanged)
	if errors.Is(err, store.ErrNotFound) {
		serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "model preset not found")
		return
	}
	if err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to update model preset")
		return
	}
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{"preset": preset})
}

func (s *Server) handleApplyCLIConfigPreset(w http.ResponseWriter, r *http.Request, uid string) {
	agentID := r.PathValue("agentID")
	if !s.allowAuthAttempt(r, "cli-config:"+agentID, uid, cliConfigRateLimit, cliConfigRateWindow) {
		serverutil.WriteError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many model configuration requests; try again shortly")
		return
	}
	_, ok := s.requireCLIConfigAgent(w, r, uid, agentID, "")
	if !ok {
		return
	}
	preset, err := s.store.CLIConfigPresetByID(r.Context(), r.PathValue("presetID"), uid, agentID)
	if errors.Is(err, store.ErrNotFound) {
		serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "model preset not found")
		return
	}
	if err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to load model preset")
		return
	}
	if err := s.materializeCLIConfigPresetCredential(r.Context(), uid, agentID, &preset); err != nil {
		serverutil.WriteError(w, http.StatusConflict, "CREDENTIAL_UNAVAILABLE", err.Error())
		return
	}
	normalizeReviewedPreset(&preset)
	request := protocol.CLIConfigRequest{
		CLI: preset.CLI, Name: preset.Name, BaseURL: preset.BaseURL, Model: preset.Model, ReasoningEffort: preset.ReasoningEffort, Secret: preset.Secret,
		ReasoningLevels: append([]string(nil), preset.ReasoningLevels...), ReasoningDefault: preset.ReasoningDefault,
	}
	s.applyReviewedClaudeContext(&request)
	result, err := s.sendCLIConfigRequest(r.Context(), agentID, protocol.TypeCLIConfigApply, request)
	if err != nil {
		s.writeCLIConfigRelayError(w, err)
		return
	}
	if !result.OK {
		serverutil.WriteError(w, http.StatusBadGateway, "APPLY_FAILED", result.Error)
		return
	}
	if err := s.store.ActivateCLIConfigPreset(r.Context(), preset.ID, uid, agentID, preset.CLI); err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "configuration applied but active preset could not be recorded")
		return
	}
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{"result": result})
}

func (s *Server) handleDeleteCLIConfigPreset(w http.ResponseWriter, r *http.Request, uid string) {
	agentID := r.PathValue("agentID")
	if _, ok := s.requireCLIConfigAgent(w, r, uid, agentID, ""); !ok {
		return
	}
	err := s.store.DeleteCLIConfigPreset(r.Context(), r.PathValue("presetID"), uid, agentID)
	if errors.Is(err, store.ErrNotFound) {
		serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "model preset not found")
		return
	}
	if err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to delete model preset")
		return
	}
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func (s *Server) handleResetCLIConfig(w http.ResponseWriter, r *http.Request, uid string) {
	agentID := r.PathValue("agentID")
	if !s.allowAuthAttempt(r, "cli-config:"+agentID, uid, cliConfigRateLimit, cliConfigRateWindow) {
		serverutil.WriteError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many model configuration requests; try again shortly")
		return
	}
	var input cliConfigResetInput
	if err := serverutil.DecodeJSON(r, &input, 4096); err != nil {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid official-login reset request")
		return
	}
	if _, ok := s.requireCLIConfigAgent(w, r, uid, agentID, input.CLI); !ok {
		return
	}
	result, err := s.sendCLIConfigRequest(r.Context(), agentID, protocol.TypeCLIConfigReset, protocol.CLIConfigRequest{CLI: input.CLI})
	if err != nil {
		s.writeCLIConfigRelayError(w, err)
		return
	}
	if !result.OK {
		serverutil.WriteError(w, http.StatusBadGateway, "RESET_FAILED", result.Error)
		return
	}
	if err := s.store.ClearActiveCLIConfigPreset(r.Context(), uid, agentID, input.CLI); err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "settings reset but active preset could not be cleared")
		return
	}
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{"result": result})
}

func (s *Server) requireCLIConfigAgent(w http.ResponseWriter, r *http.Request, uid, agentID, cli string) (*protocol.CLIConfigSwitcherCapability, bool) {
	if _, err := s.visibleAgentByID(r.Context(), uid, agentID); err != nil {
		serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "agent not found")
		return nil, false
	}
	connection, online := s.pool.AgentConnectionInfo(agentID)
	if !online {
		serverutil.WriteError(w, http.StatusConflict, "AGENT_OFFLINE", "Bridge must be online to manage model configuration")
		return nil, false
	}
	capability := connection.Capabilities
	if capability == nil || capability.ConfigSwitcher == nil || capability.ConfigSwitcher.Version < 1 {
		serverutil.WriteError(w, http.StatusConflict, "BRIDGE_UPDATE_REQUIRED", "upgrade Bridge before managing model configuration")
		return nil, false
	}
	switcher := capability.ConfigSwitcher
	if cli != "" && !containsString(switcher.CLIs, cli) {
		serverutil.WriteError(w, http.StatusBadRequest, "UNSUPPORTED_CLI", "selected CLI is not supported by this Bridge")
		return nil, false
	}
	return switcher, true
}

func (s *Server) validateCLIConfigInput(w http.ResponseWriter, input cliConfigInput, capability *protocol.CLIConfigSwitcherCapability, requireName, requireSecret bool) bool {
	name := strings.TrimSpace(input.Name)
	model := strings.TrimSpace(input.Model)
	baseURL := strings.TrimSpace(input.BaseURL)
	if input.CLI != "codex" && input.CLI != "claude" || !containsString(capability.CLIs, input.CLI) {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_CLI", "CLI must be codex or claude")
		return false
	}
	if requireName && (name == "" || len(name) > maxCLIConfigNameBytes) {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_NAME", "preset name is required and must be at most 80 bytes")
		return false
	}
	if len(model) > maxCLIConfigModelBytes || requireName && model == "" {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_MODEL", "model is required and must be at most 160 bytes")
		return false
	}
	if effort := strings.TrimSpace(input.ReasoningEffort); effort != "" {
		if len(input.ReasoningLevels) == 0 || !containsString(input.ReasoningLevels, effort) {
			serverutil.WriteError(w, http.StatusBadRequest, "BAD_REASONING_EFFORT", "selected reasoning effort is not supported by the reviewed model catalog")
			return false
		}
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || len(baseURL) > maxCLIConfigURLBytes || parsed.Host == "" || parsed.Scheme != "https" && parsed.Scheme != "http" {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_BASE_URL", "Base URL must be an http:// or https:// URL")
		return false
	}
	if input.KeyID != capability.KeyID {
		serverutil.WriteError(w, http.StatusConflict, "KEY_CHANGED", "Bridge encryption key changed; encrypt the API Key again")
		return false
	}
	if requireSecret || encryptedSecretPresent(input.Secret) {
		return validEncryptedSecret(w, input.Secret)
	}
	return true
}

// authorizeReviewedReasoning asks the private Bridge for the authoritative
// model metadata before a preset is persisted. Browser-supplied reasoning
// levels are never trusted as a catalog source.
func (s *Server) authorizeReviewedReasoning(w http.ResponseWriter, ctx context.Context, agentID string, input *cliConfigInput, secret protocol.EncryptedSecret) bool {
	result, err := s.sendCLIConfigRequest(ctx, agentID, protocol.TypeCLIConfigTest, protocol.CLIConfigRequest{
		CLI: input.CLI, BaseURL: strings.TrimSpace(input.BaseURL), Model: strings.TrimSpace(input.Model), Secret: secret,
	})
	if err != nil {
		s.writeCLIConfigRelayError(w, err)
		return false
	}
	if !result.OK {
		serverutil.WriteError(w, http.StatusBadGateway, "TEST_FAILED", result.Error)
		return false
	}
	if baseURL := strings.TrimRight(strings.TrimSpace(result.BaseURL), "/"); baseURL != "" {
		input.BaseURL = baseURL
	}
	// The Hub catalog is the approval authority. Bridge metadata remains useful
	// for runtime observability but is not trusted to approve browser input.
	metadata := reviewedModelMetadata(input.CLI, input.Model)
	// An unreviewed model may still be saved for ordinary use, but it cannot
	// carry caller-provided effort metadata or a selected effort.
	if metadata == nil || !metadata.Reviewed || len(metadata.SupportedReasoningLevels) == 0 {
		if strings.TrimSpace(input.ReasoningEffort) != "" {
			serverutil.WriteError(w, http.StatusBadRequest, "BAD_REASONING_EFFORT", "selected model has no reviewed reasoning levels")
			return false
		}
		input.ReasoningLevels = nil
		input.ReasoningDefault = ""
		return true
	}
	levels := append([]string(nil), metadata.SupportedReasoningLevels...)
	if effort := strings.TrimSpace(input.ReasoningEffort); effort != "" && !containsString(levels, effort) {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_REASONING_EFFORT", "selected reasoning effort is not supported by the reviewed model catalog")
		return false
	}
	input.ReasoningLevels = levels
	input.ReasoningDefault = strings.TrimSpace(metadata.DefaultReasoningLevel)
	return true
}

func encryptedSecretPresent(secret protocol.EncryptedSecret) bool {
	return secret.EphemeralPublicKey != "" || secret.Salt != "" || secret.IV != "" || secret.Ciphertext != ""
}

func validEncryptedSecret(w http.ResponseWriter, secret protocol.EncryptedSecret) bool {
	fields := []string{secret.EphemeralPublicKey, secret.Salt, secret.IV, secret.Ciphertext}
	for _, field := range fields {
		if field == "" || len(field) > maxEncryptedFieldBytes {
			serverutil.WriteError(w, http.StatusBadRequest, "BAD_SECRET", "encrypted API Key payload is invalid")
			return false
		}
	}
	return true
}

func (s *Server) sendCLIConfigRequest(ctx context.Context, agentID, frameType string, request protocol.CLIConfigRequest) (protocol.CLIConfigResult, error) {
	request.RequestID = store.NewID("ccr")
	pending := cliConfigPendingRequest{agentID: agentID, result: make(chan protocol.CLIConfigResult, 1)}
	s.cliConfigMu.Lock()
	s.cliConfigPending[request.RequestID] = pending
	s.cliConfigMu.Unlock()
	defer func() {
		s.cliConfigMu.Lock()
		delete(s.cliConfigPending, request.RequestID)
		s.cliConfigMu.Unlock()
	}()
	if err := s.pool.SendToAgent(agentID, protocol.MustEnvelope(frameType, "", request)); err != nil {
		return protocol.CLIConfigResult{}, err
	}
	timer := time.NewTimer(cliConfigRequestTimeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return protocol.CLIConfigResult{}, ctx.Err()
	case <-timer.C:
		return protocol.CLIConfigResult{}, context.DeadlineExceeded
	case result := <-pending.result:
		return result, nil
	}
}

func (s *Server) handleCLIConfigResult(agentID string, env protocol.Envelope) {
	result, err := protocol.Decode[protocol.CLIConfigResult](env)
	if err != nil || result.RequestID == "" {
		return
	}
	s.cliConfigMu.Lock()
	pending, ok := s.cliConfigPending[result.RequestID]
	if ok && pending.agentID == agentID {
		delete(s.cliConfigPending, result.RequestID)
	}
	s.cliConfigMu.Unlock()
	if ok && pending.agentID == agentID {
		select {
		case pending.result <- result:
		default:
		}
	}
}

func (s *Server) writeCLIConfigRelayError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrAgentOffline) {
		serverutil.WriteError(w, http.StatusConflict, "AGENT_OFFLINE", "Bridge disconnected before the request was sent")
		return
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		serverutil.WriteError(w, http.StatusGatewayTimeout, "BRIDGE_TIMEOUT", "Bridge did not complete the configuration request in time")
		return
	}
	serverutil.WriteError(w, http.StatusBadGateway, "BRIDGE_ERROR", "could not send the configuration request to Bridge")
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
