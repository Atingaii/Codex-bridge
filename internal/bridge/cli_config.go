package bridge

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
	"golang.org/x/crypto/hkdf"
)

const cliConfigHKDFInfo = "codex-bridge-cli-config-v1"

const (
	maxDiscoveredModels  = 500
	maxModelIDBytes      = 256
	unknownContextWindow = 200_000
	cliConfigProbeLimit  = 75 * time.Second
	cliConfigCallLimit   = 30 * time.Second
)

var bridgeModelsMu sync.RWMutex

type cliConfigManager struct {
	cfg     *config.Config
	mu      sync.Mutex
	private *ecdh.PrivateKey
	root    string
	state   cliConfigState
}

type cliConfigState struct {
	CodexModel                string                     `json:"codexModel,omitempty"`
	ClaudeModel               string                     `json:"claudeModel,omitempty"`
	ClaudeOverrideKey         string                     `json:"claudeOverrideKey,omitempty"`
	ClaudeOverridePrevious    string                     `json:"claudeOverridePrevious,omitempty"`
	ClaudeOverrideHadPrevious bool                       `json:"claudeOverrideHadPrevious,omitempty"`
	ClaudeConfigManaged       bool                       `json:"claudeConfigManaged,omitempty"`
	ClaudeContextManaged      bool                       `json:"claudeContextManaged,omitempty"`
	ClaudeContextPrevious     map[string]managedEnvValue `json:"claudeContextPrevious,omitempty"`
}

// managedEnvValue preserves a user setting when the Bridge temporarily owns it.
type managedEnvValue struct {
	Value  string `json:"value"`
	Exists bool   `json:"exists"`
}

func newCLIConfigManager(cfg *config.Config) (*cliConfigManager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	root := filepath.Join(home, ".codex-bridge", "config-switcher")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	m := &cliConfigManager{cfg: cfg, root: root}
	if err := m.loadKey(); err != nil {
		return nil, err
	}
	var state cliConfigState
	if raw, err := os.ReadFile(filepath.Join(root, "state.json")); err == nil && json.Unmarshal(raw, &state) == nil {
		m.state = state
		setBridgeModels(cfg, state.CodexModel, state.ClaudeModel)
		if state.ClaudeOverrideKey != "" && state.ClaudeModel != "" {
			if err := m.migrateLegacyClaudeConfig(home); err != nil {
				return nil, err
			}
		} else if state.ClaudeConfigManaged && state.ClaudeModel != "" {
			if err := m.normalizeManagedClaudeBaseURL(home); err != nil {
				return nil, err
			}
			if err := m.reconcileManagedClaudeContext(home); err != nil {
				return nil, err
			}
			if err := m.writeState(); err != nil {
				return nil, err
			}
		}
	}
	return m, nil
}

func (m *cliConfigManager) loadKey() error {
	path := filepath.Join(m.root, "ecdh.key")
	if raw, err := os.ReadFile(path); err == nil {
		decoded, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(raw)))
		if err != nil {
			return err
		}
		m.private, err = ecdh.P256().NewPrivateKey(decoded)
		return err
	}
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := atomicWrite(path, []byte(base64.RawStdEncoding.EncodeToString(key.Bytes())+"\n"), 0o600); err != nil {
		return err
	}
	m.private = key
	return nil
}

func (m *cliConfigManager) capability() *protocol.CLIConfigSwitcherCapability {
	pub := m.private.PublicKey().Bytes()
	sum := sha256.Sum256(pub)
	return &protocol.CLIConfigSwitcherCapability{
		Version:   1,
		PublicKey: base64.RawStdEncoding.EncodeToString(pub),
		KeyID:     base64.RawURLEncoding.EncodeToString(sum[:9]),
		CLIs:      []string{"codex", "claude"},
	}
}

func (m *cliConfigManager) handle(ctx context.Context, typ string, req protocol.CLIConfigRequest) protocol.CLIConfigResult {
	res := protocol.CLIConfigResult{RequestID: req.RequestID, CLI: req.CLI}
	if req.CLI != "codex" && req.CLI != "claude" {
		res.Error = "unsupported CLI"
		return res
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if typ == protocol.TypeCLIConfigReset {
		err := m.reset(req.CLI)
		res.OK = err == nil
		res.ConfigChanged = res.OK
		if err != nil {
			res.Error = "could not restore official-login settings"
		} else {
			res.Message = "custom provider overrides removed; complete official login locally"
		}
		return res
	}

	key, err := m.decrypt(req.Secret)
	if err != nil {
		res.Error = "could not decrypt API key"
		return res
	}
	defer clear(key)
	base, wireProtocol, models, listed, err := probeProvider(ctx, req.CLI, req.BaseURL, req.Model, string(key))
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.OK = true
	res.BaseURL = base
	res.Protocol = wireProtocol
	res.Models = models
	res.ModelsListed = listed

	if typ == protocol.TypeCLIConfigApply {
		if strings.TrimSpace(req.Model) == "" {
			res.OK = false
			res.Error = "model is required"
			return res
		}
		if err := m.apply(req.CLI, base, req.Model, string(key), models); err != nil {
			res.OK = false
			res.Error = "could not update native CLI configuration"
			return res
		}
		res.ConfigChanged = true
		res.AppliedModel = req.Model
		res.Message = "configuration applied; new CLI processes will use it"
	}
	return res
}

func (m *cliConfigManager) decrypt(secret protocol.EncryptedSecret) ([]byte, error) {
	decode := func(value string) ([]byte, error) {
		return base64.RawStdEncoding.DecodeString(value)
	}
	pubRaw, err := decode(secret.EphemeralPublicKey)
	if err != nil {
		return nil, err
	}
	pub, err := ecdh.P256().NewPublicKey(pubRaw)
	if err != nil {
		return nil, err
	}
	shared, err := m.private.ECDH(pub)
	if err != nil {
		return nil, err
	}
	salt, err := decode(secret.Salt)
	if err != nil {
		return nil, err
	}
	iv, err := decode(secret.IV)
	if err != nil {
		return nil, err
	}
	enc, err := decode(secret.Ciphertext)
	if err != nil {
		return nil, err
	}
	h := hkdf.New(sha256.New, shared, salt, []byte(cliConfigHKDFInfo))
	aesKey := make([]byte, 32)
	if _, err := io.ReadFull(h, aesKey); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(aesKey)
	clear(aesKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, iv, enc, nil)
}

func probeProvider(parent context.Context, cli, rawBase, model, key string) (string, string, []string, bool, error) {
	candidates, err := providerCandidates(rawBase)
	if err != nil {
		return "", "", nil, false, err
	}
	probeCtx, stop := context.WithTimeout(parent, cliConfigProbeLimit)
	defer stop()
	client := &http.Client{Timeout: cliConfigCallLimit}
	var authFailed bool
	var messages []string
	for _, base := range candidates {
		ctx, cancel := context.WithTimeout(probeCtx, cliConfigCallLimit)
		models, status, fetchErr := fetchModels(ctx, client, base, key, cli)
		cancel()
		if fetchErr == nil && status >= 200 && status < 300 {
			if strings.TrimSpace(model) == "" {
				return base, protocolForCLI(cli), models, true, nil
			}
			ctx, cancel = context.WithTimeout(probeCtx, cliConfigCallLimit)
			inferenceStatus, inferenceErr := probeInference(ctx, client, base, model, key, cli)
			cancel()
			if inferenceErr == nil && inferenceStatus >= 200 && inferenceStatus < 300 {
				return base, protocolForCLI(cli), models, true, nil
			}
			if inferenceStatus == http.StatusUnauthorized || inferenceStatus == http.StatusForbidden {
				authFailed = true
			}
			if inferenceErr != nil {
				messages = append(messages, boundedProbeError(inferenceErr))
			} else {
				messages = append(messages, fmt.Sprintf("%s inference returned HTTP %d", base, inferenceStatus))
			}
			continue
		}
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			authFailed = true
		}
		if strings.TrimSpace(model) != "" && status != http.StatusUnauthorized && status != http.StatusForbidden {
			ctx, cancel = context.WithTimeout(probeCtx, cliConfigCallLimit)
			inferenceStatus, inferenceErr := probeInference(ctx, client, base, model, key, cli)
			cancel()
			if inferenceErr == nil && inferenceStatus >= 200 && inferenceStatus < 300 {
				return base, protocolForCLI(cli), nil, false, nil
			}
			if inferenceStatus == http.StatusUnauthorized || inferenceStatus == http.StatusForbidden {
				authFailed = true
			}
		}
		if fetchErr != nil {
			messages = append(messages, boundedProbeError(fetchErr))
		} else {
			messages = append(messages, fmt.Sprintf("%s returned HTTP %d", base, status))
		}
	}
	if authFailed {
		return "", "", nil, false, errors.New("API Key authentication failed")
	}
	return "", "", nil, false, fmt.Errorf("connection test failed: %s", strings.Join(messages, "; "))
}

func providerCandidates(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("Base URL must start with http:// or https://")
	}
	path := strings.TrimRight(u.Path, "/")
	for _, suffix := range []string{"/chat/completions", "/responses", "/messages", "/models"} {
		if strings.HasSuffix(path, suffix) {
			path = strings.TrimSuffix(path, suffix)
		}
	}
	u.Path = path
	u.RawQuery = ""
	u.Fragment = ""
	base := strings.TrimRight(u.String(), "/")
	root := base
	if strings.HasSuffix(root, "/v1") {
		root = strings.TrimSuffix(root, "/v1")
	}
	values := []string{base, root + "/v1", root}
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimRight(value, "/")
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out, nil
}

func fetchModels(ctx context.Context, client *http.Client, base, key, cli string) ([]string, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/models", nil)
	if err != nil {
		return nil, 0, err
	}
	setProviderHeaders(req, key, cli)
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, resp.StatusCode, nil
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&body); err != nil {
		return nil, resp.StatusCode, err
	}
	seen := map[string]bool{}
	models := make([]string, 0, len(body.Data)+len(body.Models))
	for _, item := range append(body.Data, body.Models...) {
		id := strings.TrimSpace(item.ID)
		if id != "" && len(id) <= maxModelIDBytes && !seen[id] {
			seen[id] = true
			models = append(models, id)
			if len(models) == maxDiscoveredModels {
				break
			}
		}
	}
	sort.Strings(models)
	return models, resp.StatusCode, nil
}

func probeInference(ctx context.Context, client *http.Client, base, model, key, cli string) (int, error) {
	var endpoint string
	var payload []byte
	if cli == "claude" {
		endpoint = strings.TrimRight(base, "/") + "/messages"
		payload, _ = json.Marshal(map[string]any{
			"model": model, "max_tokens": 1,
			"messages": []map[string]string{{"role": "user", "content": "ping"}},
		})
	} else {
		endpoint = strings.TrimRight(base, "/") + "/responses"
		payload, _ = json.Marshal(map[string]any{"model": model, "input": "ping", "max_output_tokens": 16})
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	setProviderHeaders(req, key, cli)
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode, nil
}

func setProviderHeaders(req *http.Request, key, cli string) {
	req.Header.Set("Authorization", "Bearer "+key)
	if cli == "claude" {
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
	}
}

func boundedProbeError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "request timed out"
	}
	message := strings.TrimSpace(err.Error())
	if len(message) > 180 {
		message = message[:180] + "..."
	}
	return message
}

func protocolForCLI(cli string) string {
	if cli == "claude" {
		return "anthropic-compatible"
	}
	return "openai-compatible"
}

func (m *cliConfigManager) apply(cli, base, model, key string, models []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if cli == "codex" {
		dir := filepath.Join(home, ".codex")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		configPath := filepath.Join(dir, "config.toml")
		raw, err := readOptional(configPath)
		if err != nil {
			return err
		}
		catalogPath := filepath.Join(m.root, "codex-model-catalog.json")
		if err := writeCodexModelCatalog(catalogPath, model, models); err != nil {
			return err
		}
		if err := backupAndWrite(configPath, []byte(updateCodexConfig(string(raw), base, model, catalogPath, false)), 0o600); err != nil {
			return err
		}
		authPath := filepath.Join(dir, "auth.json")
		auth, err := readJSONObject(authPath)
		if err != nil {
			return fmt.Errorf("parse Codex auth: %w", err)
		}
		auth["OPENAI_API_KEY"] = key
		encoded, err := json.MarshalIndent(auth, "", "  ")
		if err != nil {
			return err
		}
		if err := backupAndWrite(authPath, append(encoded, '\n'), 0o600); err != nil {
			return err
		}
		_, claudeModel := bridgeModels(m.cfg)
		setBridgeModels(m.cfg, model, claudeModel)
	} else {
		path := filepath.Join(home, ".claude", "settings.json")
		settings, err := readJSONObject(path)
		if err != nil {
			return fmt.Errorf("parse Claude settings: %w", err)
		}
		if err := m.restoreClaudeModelOverride(settings); err != nil {
			return err
		}
		m.state.ClaudeOverrideKey = ""
		m.state.ClaudeOverridePrevious = ""
		m.state.ClaudeOverrideHadPrevious = false
		env, _ := settings["env"].(map[string]any)
		if env == nil {
			env = map[string]any{}
		}
		env["ANTHROPIC_BASE_URL"] = claudeBaseURL(base)
		env["ANTHROPIC_AUTH_TOKEN"] = key
		delete(env, "ANTHROPIC_API_KEY")
		settings["env"] = env
		setClaudeModelFields(settings, model)
		if err := m.configureClaudeContext(settings, model); err != nil {
			return err
		}
		encoded, err := json.MarshalIndent(settings, "", "  ")
		if err != nil {
			return err
		}
		if err := backupAndWrite(path, append(encoded, '\n'), 0o600); err != nil {
			return err
		}
		codexModel, _ := bridgeModels(m.cfg)
		setBridgeModels(m.cfg, codexModel, model)
		m.state.ClaudeConfigManaged = true
	}
	return m.writeState()
}

func (m *cliConfigManager) reset(cli string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if cli == "codex" {
		configPath := filepath.Join(home, ".codex", "config.toml")
		raw, err := readOptional(configPath)
		if err != nil {
			return err
		}
		if err := backupAndWrite(configPath, []byte(updateCodexConfig(string(raw), "", "", "", true)), 0o600); err != nil {
			return err
		}
		authPath := filepath.Join(home, ".codex", "auth.json")
		auth, err := readJSONObject(authPath)
		if err != nil {
			return err
		}
		delete(auth, "OPENAI_API_KEY")
		encoded, err := json.MarshalIndent(auth, "", "  ")
		if err != nil {
			return err
		}
		if err := backupAndWrite(authPath, append(encoded, '\n'), 0o600); err != nil {
			return err
		}
		_, claudeModel := bridgeModels(m.cfg)
		setBridgeModels(m.cfg, "", claudeModel)
	} else {
		path := filepath.Join(home, ".claude", "settings.json")
		settings, err := readJSONObject(path)
		if err != nil {
			return err
		}
		if err := m.restoreClaudeContextSettings(settings); err != nil {
			return err
		}
		delete(settings, "model")
		if env, ok := settings["env"].(map[string]any); ok {
			for _, key := range []string{
				"ANTHROPIC_BASE_URL",
				"ANTHROPIC_AUTH_TOKEN",
				"ANTHROPIC_API_KEY",
				"ANTHROPIC_MODEL",
				"ANTHROPIC_REASONING_MODEL",
				"ANTHROPIC_DEFAULT_OPUS_MODEL",
				"ANTHROPIC_DEFAULT_SONNET_MODEL",
				"ANTHROPIC_DEFAULT_HAIKU_MODEL",
				"ANTHROPIC_DEFAULT_FABLE_MODEL",
				"CLAUDE_CODE_SUBAGENT_MODEL",
				"ANTHROPIC_CUSTOM_MODEL_OPTION",
				"ANTHROPIC_CUSTOM_MODEL_OPTION_NAME",
				"ANTHROPIC_CUSTOM_MODEL_OPTION_DESCRIPTION",
				"ANTHROPIC_CUSTOM_MODEL_OPTION_SUPPORTED_CAPABILITIES",
			} {
				delete(env, key)
			}
			if len(env) == 0 {
				delete(settings, "env")
			} else {
				settings["env"] = env
			}
		}
		if err := m.restoreClaudeModelOverride(settings); err != nil {
			return err
		}
		encoded, err := json.MarshalIndent(settings, "", "  ")
		if err != nil {
			return err
		}
		if err := backupAndWrite(path, append(encoded, '\n'), 0o600); err != nil {
			return err
		}
		m.state.ClaudeOverrideKey = ""
		m.state.ClaudeOverridePrevious = ""
		m.state.ClaudeOverrideHadPrevious = false
		m.state.ClaudeConfigManaged = true
		codexModel, _ := bridgeModels(m.cfg)
		setBridgeModels(m.cfg, codexModel, "")
	}
	return m.writeState()
}

func (m *cliConfigManager) writeState() error {
	codexModel, claudeModel := bridgeModels(m.cfg)
	m.state.CodexModel = codexModel
	m.state.ClaudeModel = claudeModel
	encoded, err := json.Marshal(m.state)
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(m.root, "state.json"), append(encoded, '\n'), 0o600)
}

func (m *cliConfigManager) migrateLegacyClaudeConfig(home string) error {
	path := filepath.Join(home, ".claude", "settings.json")
	settings, err := readJSONObject(path)
	if err != nil {
		return fmt.Errorf("parse Claude settings for migration: %w", err)
	}
	if err := m.restoreClaudeModelOverride(settings); err != nil {
		return err
	}
	setClaudeModelFields(settings, m.state.ClaudeModel)
	encoded, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := backupAndWrite(path, append(encoded, '\n'), 0o600); err != nil {
		return err
	}
	m.state.ClaudeOverrideKey = ""
	m.state.ClaudeOverridePrevious = ""
	m.state.ClaudeOverrideHadPrevious = false
	m.state.ClaudeConfigManaged = true
	return m.writeState()
}

func (m *cliConfigManager) normalizeManagedClaudeBaseURL(home string) error {
	path := filepath.Join(home, ".claude", "settings.json")
	settings, err := readJSONObject(path)
	if err != nil {
		return fmt.Errorf("parse Claude settings for Base URL migration: %w", err)
	}
	env, _ := settings["env"].(map[string]any)
	base, _ := env["ANTHROPIC_BASE_URL"].(string)
	normalized := claudeBaseURL(base)
	if normalized == "" || normalized == base {
		return nil
	}
	env["ANTHROPIC_BASE_URL"] = normalized
	settings["env"] = env
	encoded, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return backupAndWrite(path, append(encoded, '\n'), 0o600)
}

func (m *cliConfigManager) reconcileManagedClaudeContext(home string) error {
	path := filepath.Join(home, ".claude", "settings.json")
	settings, err := readJSONObject(path)
	if err != nil {
		return fmt.Errorf("parse Claude settings for context migration: %w", err)
	}
	before, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	if err := m.configureClaudeContext(settings, m.state.ClaudeModel); err != nil {
		return err
	}
	after, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	if bytes.Equal(before, after) {
		return nil
	}
	encoded, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return backupAndWrite(path, append(encoded, '\n'), 0o600)
}

func claudeBaseURL(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	return strings.TrimSuffix(base, "/v1")
}

func setClaudeModelFields(settings map[string]any, model string) {
	env, _ := settings["env"].(map[string]any)
	if env == nil {
		env = map[string]any{}
	}
	for _, name := range []string{
		"ANTHROPIC_MODEL",
		"ANTHROPIC_DEFAULT_OPUS_MODEL",
		"ANTHROPIC_DEFAULT_SONNET_MODEL",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL",
		"ANTHROPIC_DEFAULT_FABLE_MODEL",
		"CLAUDE_CODE_SUBAGENT_MODEL",
		"ANTHROPIC_CUSTOM_MODEL_OPTION",
	} {
		env[name] = model
	}
	env["ANTHROPIC_CUSTOM_MODEL_OPTION_NAME"] = model
	env["ANTHROPIC_CUSTOM_MODEL_OPTION_DESCRIPTION"] = "Custom provider model managed by ProofBridge Hub"
	delete(env, "ANTHROPIC_REASONING_MODEL")
	settings["env"] = env
	settings["model"] = model
}

type claudeContextProfile struct {
	Window                    int
	DisableUnknownEnforcement bool
}

// claudeContextProfileForModel uses a reviewed catalog of widely deployed
// models. Providers' /models endpoints do not reliably expose a context-window
// field, so an unknown provider model must retain Claude Code's conservative
// behavior instead of inheriting an unrelated model's profile.
func claudeContextProfileForModel(model string) (claudeContextProfile, bool) {
	name := normalizedContextModelName(model)
	if profile, ok := claudeContextProfiles[name]; ok {
		return profile, true
	}
	return claudeContextProfile{}, false
}

var claudeContextProfiles = map[string]claudeContextProfile{
	// Anthropic. These names are recognized by current Claude Code releases.
	"claude-fable-5":    {Window: 1_000_000},
	"claude-opus-5":     {Window: 1_000_000},
	"claude-sonnet-5":   {Window: 1_000_000},
	"claude-opus-4-6":   {Window: 1_000_000},
	"claude-sonnet-4-6": {Window: 1_000_000},
	"claude-opus-4-5":   {Window: 200_000},
	"claude-sonnet-4-5": {Window: 200_000},
	"claude-3-7-sonnet": {Window: 200_000},
	"claude-3-5-sonnet": {Window: 200_000},
	"claude-3-5-haiku":  {Window: 200_000},
	"claude-haiku-4-5":  {Window: 200_000},
	"claude-3-opus":     {Window: 200_000},
	"claude-3-sonnet":   {Window: 200_000},
	"claude-3-haiku":    {Window: 200_000},

	// OpenAI/Codex models commonly exposed through Anthropic-compatible proxies.
	"gpt-5.6":       {Window: 1_050_000, DisableUnknownEnforcement: true},
	"gpt-5.6-sol":   {Window: 1_050_000, DisableUnknownEnforcement: true},
	"gpt-5.6-terra": {Window: 1_050_000, DisableUnknownEnforcement: true},
	"gpt-5.6-luna":  {Window: 1_050_000, DisableUnknownEnforcement: true},
	"gpt-4.1":       {Window: 1_047_576, DisableUnknownEnforcement: true},
	"gpt-4.1-mini":  {Window: 1_047_576, DisableUnknownEnforcement: true},
	"gpt-4.1-nano":  {Window: 1_047_576, DisableUnknownEnforcement: true},
	"gpt-4o":        {Window: 128_000, DisableUnknownEnforcement: true},
	"gpt-4o-mini":   {Window: 128_000, DisableUnknownEnforcement: true},

	// Frequently selected third-party models. Their exact names are intentional:
	// an aggregator alias is not assumed to have the original model's limits.
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

func normalizedContextModelName(model string) string {
	name := strings.ToLower(strings.TrimSpace(model))
	name = strings.TrimSuffix(name, "[1m]")
	name = strings.TrimPrefix(name, "models/")
	for _, prefix := range []string{"anthropic/", "openai/", "deepseek/", "moonshot/", "google/", "meta/", "mistral/"} {
		name = strings.TrimPrefix(name, prefix)
	}
	return name
}

var claudeContextEnvironmentKeys = []string{
	"CLAUDE_CODE_MAX_CONTEXT_TOKENS",
	"CLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT",
}

func (m *cliConfigManager) configureClaudeContext(settings map[string]any, model string) error {
	env, _ := settings["env"].(map[string]any)
	if env == nil {
		env = map[string]any{}
	}
	profile, known := claudeContextProfileForModel(model)
	if !known {
		return m.restoreClaudeContextSettings(settings)
	}
	if !m.state.ClaudeContextManaged {
		m.state.ClaudeContextPrevious = make(map[string]managedEnvValue, len(claudeContextEnvironmentKeys))
		for _, key := range claudeContextEnvironmentKeys {
			value, exists := env[key]
			text, _ := value.(string)
			m.state.ClaudeContextPrevious[key] = managedEnvValue{Value: text, Exists: exists}
		}
		m.state.ClaudeContextManaged = true
	}
	env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] = strconv.Itoa(profile.Window)
	if profile.DisableUnknownEnforcement {
		env["CLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT"] = "1"
	} else {
		delete(env, "CLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT")
	}
	settings["env"] = env
	return nil
}

func (m *cliConfigManager) restoreClaudeContextSettings(settings map[string]any) error {
	if !m.state.ClaudeContextManaged {
		return nil
	}
	env, _ := settings["env"].(map[string]any)
	if env == nil {
		env = map[string]any{}
	}
	for _, key := range claudeContextEnvironmentKeys {
		previous := m.state.ClaudeContextPrevious[key]
		if previous.Exists {
			env[key] = previous.Value
		} else {
			delete(env, key)
		}
	}
	if len(env) == 0 {
		delete(settings, "env")
	} else {
		settings["env"] = env
	}
	m.state.ClaudeContextManaged = false
	m.state.ClaudeContextPrevious = nil
	return nil
}

func (m *cliConfigManager) restoreClaudeModelOverride(settings map[string]any) error {
	if m.state.ClaudeOverrideKey == "" {
		return nil
	}
	overrides, err := claudeModelOverrides(settings)
	if err != nil {
		return err
	}
	if m.state.ClaudeOverrideHadPrevious {
		overrides[m.state.ClaudeOverrideKey] = m.state.ClaudeOverridePrevious
	} else {
		delete(overrides, m.state.ClaudeOverrideKey)
	}
	if len(overrides) == 0 {
		delete(settings, "modelOverrides")
	} else {
		settings["modelOverrides"] = overrides
	}
	return nil
}

func claudeModelOverrides(settings map[string]any) (map[string]any, error) {
	raw, exists := settings["modelOverrides"]
	if !exists {
		return map[string]any{}, nil
	}
	overrides, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New("Claude modelOverrides must be an object")
	}
	return overrides, nil
}

func bridgeModels(cfg *config.Config) (string, string) {
	bridgeModelsMu.RLock()
	defer bridgeModelsMu.RUnlock()
	return cfg.Bridge.Model, cfg.Bridge.ClaudeModel
}

func codexBridgeModel(cfg *config.Config) string {
	codexModel, _ := bridgeModels(cfg)
	return codexModel
}

func claudeBridgeModel(cfg *config.Config) string {
	codexModel, claudeModel := bridgeModels(cfg)
	if claudeModel != "" {
		return claudeModel
	}
	return codexModel
}

func setBridgeModels(cfg *config.Config, codexModel, claudeModel string) {
	bridgeModelsMu.Lock()
	cfg.Bridge.Model = codexModel
	cfg.Bridge.ClaudeModel = claudeModel
	bridgeModelsMu.Unlock()
}

func updateCodexConfig(text, base, model, catalogPath string, official bool) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines)+8)
	section := ""
	skipManagedSection := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "["), "]"))
			skipManagedSection = section == "model_providers.custom" || section == "model_providers.\"custom\""
			if skipManagedSection {
				continue
			}
		}
		if skipManagedSection {
			continue
		}
		if section == "" {
			key := strings.Trim(strings.TrimSpace(strings.SplitN(trimmed, "=", 2)[0]), "\"'")
			if key == "model" || key == "model_provider" || key == "model_catalog_json" || key == "forced_login_method" || key == "openai_base_url" || key == "chatgpt_base_url" {
				continue
			}
		}
		out = append(out, line)
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	if official {
		out = append([]string{"model_provider = \"openai\"", "forced_login_method = \"chatgpt\""}, out...)
	} else {
		out = append([]string{"model = " + strconv.Quote(model), "model_provider = \"custom\"", "model_catalog_json = " + strconv.Quote(catalogPath)}, out...)
		out = append(out, "", "[model_providers.custom]", "name = \"custom\"", "base_url = "+strconv.Quote(strings.TrimRight(base, "/")), "wire_api = \"responses\"", "requires_openai_auth = true")
	}
	return strings.Join(out, "\n") + "\n"
}

type codexModelCatalog struct {
	Models []codexModelCatalogEntry `json:"models"`
}

type codexModelCatalogEntry struct {
	Slug                       string                `json:"slug"`
	DisplayName                string                `json:"display_name"`
	Description                string                `json:"description"`
	DefaultReasoningLevel      string                `json:"default_reasoning_level"`
	SupportedReasoningLevels   []codexReasoningLevel `json:"supported_reasoning_levels"`
	ShellType                  string                `json:"shell_type"`
	Visibility                 string                `json:"visibility"`
	SupportedInAPI             bool                  `json:"supported_in_api"`
	Priority                   int                   `json:"priority"`
	SupportVerbosity           bool                  `json:"support_verbosity"`
	TruncationPolicy           map[string]any        `json:"truncation_policy"`
	SupportsParallelToolCalls  bool                  `json:"supports_parallel_tool_calls"`
	ExperimentalSupportedTools []string              `json:"experimental_supported_tools"`
	ContextWindow              int                   `json:"context_window"`
	BaseInstructions           string                `json:"base_instructions"`
}

type codexReasoningLevel struct {
	Effort      string `json:"effort"`
	Description string `json:"description"`
}

func writeCodexModelCatalog(path, selected string, discovered []string) error {
	models := make([]string, 0, len(discovered)+1)
	seen := map[string]bool{}
	for _, model := range append([]string{selected}, discovered...) {
		model = strings.TrimSpace(model)
		if model == "" || len(model) > maxModelIDBytes || seen[model] {
			continue
		}
		seen[model] = true
		models = append(models, model)
	}
	entries := make([]codexModelCatalogEntry, 0, len(models))
	for priority, model := range models {
		entries = append(entries, codexModelCatalogEntry{
			Slug: model, DisplayName: model,
			Description:                "Custom provider model managed by ProofBridge Hub.",
			DefaultReasoningLevel:      "medium",
			SupportedReasoningLevels:   []codexReasoningLevel{{Effort: "low", Description: "Faster response"}, {Effort: "medium", Description: "Balanced reasoning"}, {Effort: "high", Description: "Deeper reasoning"}},
			ShellType:                  "shell_command",
			Visibility:                 "list",
			SupportedInAPI:             true,
			Priority:                   priority + 1,
			SupportVerbosity:           false,
			TruncationPolicy:           map[string]any{"mode": "tokens", "limit": 10_000},
			SupportsParallelToolCalls:  true,
			ExperimentalSupportedTools: []string{},
			ContextWindow:              unknownContextWindow,
			BaseInstructions:           "You are a precise coding agent. Work directly in the user's workspace, follow repository instructions, make focused changes, and verify your work.",
		})
	}
	encoded, err := json.MarshalIndent(codexModelCatalog{Models: entries}, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, append(encoded, '\n'), 0o600)
}

func readOptional(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return raw, err
}

func readJSONObject(path string) (map[string]any, error) {
	raw, err := readOptional(path)
	if err != nil {
		return nil, err
	}
	value := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
	}
	return value, nil
}

func backupAndWrite(path string, data []byte, mode os.FileMode) error {
	if old, err := os.ReadFile(path); err == nil {
		if err := atomicWrite(path+".codex-bridge.bak", old, mode); err != nil {
			return err
		}
	}
	return atomicWrite(path, data, mode)
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".codex-bridge-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(mode); err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}
