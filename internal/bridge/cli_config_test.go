package bridge

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
	"golang.org/x/crypto/hkdf"
)

func TestProviderCandidatesNormalizeCommonEndpoints(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"https://api.example.com", []string{"https://api.example.com", "https://api.example.com/v1"}},
		{"https://api.example.com/v1/models", []string{"https://api.example.com/v1", "https://api.example.com"}},
		{"https://api.example.com/openai/v1/responses?ignored=1", []string{"https://api.example.com/openai/v1", "https://api.example.com/openai"}},
	}
	for _, tc := range tests {
		got, err := providerCandidates(tc.input)
		if err != nil {
			t.Fatalf("providerCandidates(%q): %v", tc.input, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("providerCandidates(%q) = %#v, want %#v", tc.input, got, tc.want)
		}
	}
	if _, err := providerCandidates("file:///tmp/provider"); err == nil {
		t.Fatal("expected non-http URL to be rejected")
	}
}

func TestClaudeBaseURLRemovesSDKVersionSuffix(t *testing.T) {
	for input, want := range map[string]string{
		"https://cpa.example/v1":           "https://cpa.example",
		"https://cpa.example/v1/":          "https://cpa.example",
		"https://api.example/anthropic":    "https://api.example/anthropic",
		"https://api.example/anthropic/v1": "https://api.example/anthropic",
	} {
		if got := claudeBaseURL(input); got != want {
			t.Fatalf("claudeBaseURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCLIConfigDecryptRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m, err := newCLIConfigManager(&config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	secret := encryptCLIConfigSecretForTest(t, m.private.PublicKey(), []byte("test-secret"))
	got, err := m.decrypt(secret)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "test-secret" {
		t.Fatalf("decrypted secret = %q", got)
	}
}

func TestCLIConfigApplyAndResetPreserveUnrelatedSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	codexOriginal := "sandbox_mode = \"workspace-write\"\n\"model\" = \"old\"\n'model_provider' = \"old-provider\"\n\n[mcp_servers.keep]\ncommand = \"keep-me\"\n"
	if err := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte(codexOriginal), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "auth.json"), []byte("{\"refresh_token\":\"keep\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	claudeOriginal := `{"permissions":{"allow":["Read"]},"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"agentguard"}]}]},"modelOverrides":{"claude-opus-4-6":"keep-opus","claude-sonnet-4-6":"keep-sonnet"},"env":{"KEEP":"yes","ANTHROPIC_REASONING_MODEL":"old-model","ANTHROPIC_DEFAULT_OPUS_MODEL":"old-model","ANTHROPIC_DEFAULT_SONNET_MODEL":"old-model","ANTHROPIC_DEFAULT_HAIKU_MODEL":"old-model"}}`
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(claudeOriginal), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	m, err := newCLIConfigManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.apply("codex", "https://provider.example/v1", "codex-model", "codex-key", []string{"codex-model", "codex-model-fast"}); err != nil {
		t.Fatal(err)
	}
	if err := m.apply("claude", "https://claude.example/v1", "claude-model", "claude-key", []string{"claude-model"}); err != nil {
		t.Fatal(err)
	}

	codexApplied := readTestFile(t, filepath.Join(home, ".codex", "config.toml"))
	for _, wanted := range []string{"sandbox_mode = \"workspace-write\"", "[mcp_servers.keep]", "command = \"keep-me\"", "[model_providers.custom]", "model = \"codex-model\"", "model_catalog_json = "} {
		if !strings.Contains(codexApplied, wanted) {
			t.Fatalf("Codex config missing %q:\n%s", wanted, codexApplied)
		}
	}
	if strings.Contains(codexApplied, "old-provider") || strings.Contains(codexApplied, "model\" = \"old") {
		t.Fatalf("Codex config retained an old quoted provider key:\n%s", codexApplied)
	}
	var auth map[string]any
	if err := json.Unmarshal([]byte(readTestFile(t, filepath.Join(home, ".codex", "auth.json"))), &auth); err != nil {
		t.Fatal(err)
	}
	if auth["refresh_token"] != "keep" || auth["OPENAI_API_KEY"] != "codex-key" {
		t.Fatalf("unexpected Codex auth fields: %#v", auth)
	}
	var catalog codexModelCatalog
	if err := json.Unmarshal([]byte(readTestFile(t, filepath.Join(m.root, "codex-model-catalog.json"))), &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) != 2 || catalog.Models[0].Slug != "codex-model" || catalog.Models[1].Slug != "codex-model-fast" || catalog.Models[0].ContextWindow != unknownContextWindow {
		t.Fatalf("unexpected Codex model catalog: %#v", catalog)
	}
	var claude map[string]any
	if err := json.Unmarshal([]byte(readTestFile(t, filepath.Join(home, ".claude", "settings.json"))), &claude); err != nil {
		t.Fatal(err)
	}
	if claude["permissions"] == nil || claude["hooks"] == nil || claude["model"] != "claude-model" {
		t.Fatalf("unexpected Claude settings: %#v", claude)
	}
	claudeEnv, _ := claude["env"].(map[string]any)
	if claudeEnv["KEEP"] != "yes" || claudeEnv["ANTHROPIC_BASE_URL"] != "https://claude.example" || claudeEnv["ANTHROPIC_AUTH_TOKEN"] != "claude-key" {
		t.Fatalf("unexpected Claude environment: %#v", claudeEnv)
	}
	for _, key := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_FABLE_MODEL", "CLAUDE_CODE_SUBAGENT_MODEL", "ANTHROPIC_CUSTOM_MODEL_OPTION", "ANTHROPIC_CUSTOM_MODEL_OPTION_NAME"} {
		if claudeEnv[key] != "claude-model" {
			t.Fatalf("Claude model field %q = %#v, want claude-model", key, claudeEnv[key])
		}
	}
	if _, exists := claudeEnv["CLAUDE_CODE_MAX_CONTEXT_TOKENS"]; exists {
		t.Fatalf("unknown Claude model inherited a context profile: %#v", claudeEnv)
	}
	if _, exists := claudeEnv["ANTHROPIC_REASONING_MODEL"]; exists {
		t.Fatalf("stale Claude reasoning override was not removed: %#v", claudeEnv)
	}
	claudeOverrides, _ := claude["modelOverrides"].(map[string]any)
	if claudeOverrides["claude-sonnet-4-6"] != "keep-sonnet" || claudeOverrides["claude-opus-4-6"] != "keep-opus" {
		t.Fatalf("unexpected Claude model overrides: %#v", claudeOverrides)
	}
	restartedCfg := &config.Config{}
	restarted, err := newCLIConfigManager(restartedCfg)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.state.ClaudeModel != "claude-model" || claudeBridgeModel(restartedCfg) != "claude-model" {
		t.Fatalf("restarted Bridge lost Claude model mapping state: %#v", restarted.state)
	}
	if err := m.reset("codex"); err != nil {
		t.Fatal(err)
	}
	if err := m.reset("claude"); err != nil {
		t.Fatal(err)
	}
	codexReset := readTestFile(t, filepath.Join(home, ".codex", "config.toml"))
	if strings.Contains(codexReset, "model_providers.custom") || strings.Contains(codexReset, "model_catalog_json") || !strings.Contains(codexReset, "forced_login_method = \"chatgpt\"") || !strings.Contains(codexReset, "model_provider = \"openai\"") || !strings.Contains(codexReset, "[mcp_servers.keep]") {
		t.Fatalf("unexpected reset Codex config:\n%s", codexReset)
	}
	auth = nil
	if err := json.Unmarshal([]byte(readTestFile(t, filepath.Join(home, ".codex", "auth.json"))), &auth); err != nil {
		t.Fatal(err)
	}
	if _, exists := auth["OPENAI_API_KEY"]; exists || auth["refresh_token"] != "keep" {
		t.Fatalf("unexpected reset Codex auth: %#v", auth)
	}
	claude = nil
	if err := json.Unmarshal([]byte(readTestFile(t, filepath.Join(home, ".claude", "settings.json"))), &claude); err != nil {
		t.Fatal(err)
	}
	if claude["permissions"] == nil || claude["hooks"] == nil || claude["model"] != nil || claude["env"].(map[string]any)["KEEP"] != "yes" {
		t.Fatalf("unexpected reset Claude settings: %#v", claude)
	}
	claudeOverrides, _ = claude["modelOverrides"].(map[string]any)
	if claudeOverrides["claude-sonnet-4-6"] != "keep-sonnet" || claudeOverrides["claude-opus-4-6"] != "keep-opus" {
		t.Fatalf("Claude model overrides were not restored: %#v", claudeOverrides)
	}
	if got := claudeBridgeModel(cfg); got != "" {
		t.Fatalf("Claude model after reset = %q, want empty", got)
	}
	restartedCfg = &config.Config{}
	restarted, err = newCLIConfigManager(restartedCfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := claudeBridgeModel(restartedCfg); got != "" {
		t.Fatalf("restarted Bridge model after reset = %q, want empty", got)
	}
}

func TestCLIConfigApplyMigratesLegacyClaudeOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	settings := `{"model":"claude-sonnet-4-6","modelOverrides":{"claude-sonnet-4-6":"old-provider-model","keep":"untouched"},"env":{"KEEP":"yes"}}`
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, ".codex-bridge", "config-switcher")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	state := `{"claudeModel":"old-provider-model","claudeOverrideKey":"claude-sonnet-4-6","claudeOverridePrevious":"original-sonnet","claudeOverrideHadPrevious":true,"claudeConfigManaged":true}`
	if err := os.WriteFile(filepath.Join(root, "state.json"), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	manager, err := newCLIConfigManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(readTestFile(t, filepath.Join(home, ".claude", "settings.json"))), &got); err != nil {
		t.Fatal(err)
	}
	if got["model"] != "old-provider-model" {
		t.Fatalf("migrated Claude model = %#v", got["model"])
	}
	env, _ := got["env"].(map[string]any)
	for _, key := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_FABLE_MODEL", "CLAUDE_CODE_SUBAGENT_MODEL", "ANTHROPIC_CUSTOM_MODEL_OPTION"} {
		if env[key] != "old-provider-model" {
			t.Fatalf("migrated Claude field %q = %#v", key, env[key])
		}
	}
	overrides, _ := got["modelOverrides"].(map[string]any)
	if overrides["claude-sonnet-4-6"] != "original-sonnet" || overrides["keep"] != "untouched" {
		t.Fatalf("legacy override was not restored: %#v", overrides)
	}
	if manager.state.ClaudeOverrideKey != "" || manager.state.ClaudeModel != "old-provider-model" {
		t.Fatalf("legacy state was not migrated: %#v", manager.state)
	}
}

func TestClaudeContextProfileFollowsSelectedModelAndRestoresUserSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	settings := `{"env":{"CLAUDE_CODE_MAX_CONTEXT_TOKENS":"333333","CLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT":"0","KEEP":"yes"}}`
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := newCLIConfigManager(&config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.apply("claude", "https://claude.example/v1", "claude-sonnet-5", "key", nil); err != nil {
		t.Fatal(err)
	}
	claude := readClaudeSettings(t, home)
	env := claude["env"].(map[string]any)
	if env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] != "1000000" {
		t.Fatalf("Sonnet 5 context = %#v, want 1000000", env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"])
	}
	if _, exists := env["CLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT"]; exists {
		t.Fatalf("recognized Anthropic model should not need unknown-model override: %#v", env)
	}
	if err := m.apply("claude", "https://claude.example/v1", "deepseek-v4-flash", "key", nil); err != nil {
		t.Fatal(err)
	}
	claude = readClaudeSettings(t, home)
	env = claude["env"].(map[string]any)
	if env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] != "1000000" || env["CLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT"] != "1" {
		t.Fatalf("DeepSeek context profile = %#v", env)
	}
	if err := m.reset("claude"); err != nil {
		t.Fatal(err)
	}
	claude = readClaudeSettings(t, home)
	env = claude["env"].(map[string]any)
	if env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] != "333333" || env["CLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT"] != "0" || env["KEEP"] != "yes" {
		t.Fatalf("switching known models lost original user context settings: %#v", env)
	}
	if err := m.apply("claude", "https://claude.example/v1", "deepseek-v4-flash", "key", nil); err != nil {
		t.Fatal(err)
	}
	if err := m.apply("claude", "https://claude.example/v1", "provider-unlisted-model", "key", nil); err != nil {
		t.Fatal(err)
	}
	claude = readClaudeSettings(t, home)
	env = claude["env"].(map[string]any)
	if env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] != "333333" || env["CLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT"] != "0" || env["KEEP"] != "yes" {
		t.Fatalf("unknown model did not restore user context settings: %#v", env)
	}
	if err := m.apply("claude", "https://claude.example/v1", "claude-sonnet-5", "key", nil); err != nil {
		t.Fatal(err)
	}
	if err := m.reset("claude"); err != nil {
		t.Fatal(err)
	}
	claude = readClaudeSettings(t, home)
	env = claude["env"].(map[string]any)
	if env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] != "333333" || env["CLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT"] != "0" || env["KEEP"] != "yes" {
		t.Fatalf("official reset did not restore user context settings: %#v", env)
	}
}

func TestClaudeContextProfilesCoverPopularModelAliases(t *testing.T) {
	tests := []struct {
		model   string
		window  int
		disable bool
	}{
		{model: "claude-sonnet-5", window: 1_000_000},
		{model: "anthropic/claude-sonnet-5[1m]", window: 1_000_000},
		{model: "claude-3-7-sonnet", window: 200_000},
		{model: "gpt-5.6-sol", window: 1_050_000, disable: true},
		{model: "models/gpt-4.1-mini", window: 1_047_576, disable: true},
		{model: "gpt-4o", window: 128_000, disable: true},
		{model: "deepseek-v4-flash", window: 1_000_000, disable: true},
		{model: "deepseek/deepseek-chat", window: 128_000, disable: true},
		{model: "kimi-k3", window: 1_000_000, disable: true},
		{model: "gemini-2.5-pro", window: 1_048_576, disable: true},
		{model: "gemini-2.0-flash", window: 1_048_576, disable: true},
		{model: "llama-3.3-70b-instruct", window: 128_000, disable: true},
		{model: "mistral-large-latest", window: 128_000, disable: true},
	}
	for _, tc := range tests {
		t.Run(tc.model, func(t *testing.T) {
			profile, ok := claudeContextProfileForModel(tc.model)
			if !ok || profile.Window != tc.window || profile.DisableUnknownEnforcement != tc.disable {
				t.Fatalf("profile(%q) = %#v, %v; want window=%d disable=%v", tc.model, profile, ok, tc.window, tc.disable)
			}
		})
	}
	if _, ok := claudeContextProfileForModel("provider-invented-alias"); ok {
		t.Fatal("unverified provider alias must retain conservative native behavior")
	}
}

func TestCLIConfigStartupAppliesContextProfileForExistingManagedPreset(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	settings := `{"model":"deepseek-v4-flash","env":{"ANTHROPIC_BASE_URL":"https://cpa.example","KEEP":"yes"}}`
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, ".codex-bridge", "config-switcher")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	state := `{"claudeModel":"deepseek-v4-flash","claudeConfigManaged":true}`
	if err := os.WriteFile(filepath.Join(root, "state.json"), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newCLIConfigManager(&config.Config{}); err != nil {
		t.Fatal(err)
	}
	claude := readClaudeSettings(t, home)
	env := claude["env"].(map[string]any)
	if env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] != "1000000" || env["CLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT"] != "1" || env["KEEP"] != "yes" {
		t.Fatalf("existing managed profile was not migrated: %#v", env)
	}
}

func readClaudeSettings(t *testing.T, home string) map[string]any {
	t.Helper()
	var settings map[string]any
	if err := json.Unmarshal([]byte(readTestFile(t, filepath.Join(home, ".claude", "settings.json"))), &settings); err != nil {
		t.Fatal(err)
	}
	return settings
}

func TestCLIConfigStartupNormalizesManagedClaudeBaseURL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	settings := `{"model":"deepseek-v4-flash","env":{"ANTHROPIC_BASE_URL":"https://cpa.example/v1","ANTHROPIC_AUTH_TOKEN":"keep-key","KEEP":"yes"},"permissions":{"defaultMode":"bypassPermissions"}}`
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, ".codex-bridge", "config-switcher")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	state := `{"claudeModel":"deepseek-v4-flash","claudeConfigManaged":true}`
	if err := os.WriteFile(filepath.Join(root, "state.json"), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newCLIConfigManager(&config.Config{}); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(readTestFile(t, filepath.Join(home, ".claude", "settings.json"))), &got); err != nil {
		t.Fatal(err)
	}
	env, _ := got["env"].(map[string]any)
	if env["ANTHROPIC_BASE_URL"] != "https://cpa.example" || env["ANTHROPIC_AUTH_TOKEN"] != "keep-key" || env["KEEP"] != "yes" || got["permissions"] == nil {
		t.Fatalf("unexpected normalized Claude settings: %#v", got)
	}
}

func encryptCLIConfigSecretForTest(t *testing.T, recipient *ecdh.PublicKey, plaintext []byte) protocol.EncryptedSecret {
	t.Helper()
	ephemeral, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := ephemeral.ECDH(recipient)
	if err != nil {
		t.Fatal(err)
	}
	salt := make([]byte, 16)
	iv := make([]byte, 12)
	if _, err := rand.Read(salt); err != nil {
		t.Fatal(err)
	}
	if _, err := rand.Read(iv); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, shared, salt, []byte(cliConfigHKDFInfo)), key); err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	encode := base64.RawStdEncoding.EncodeToString
	return protocol.EncryptedSecret{
		EphemeralPublicKey: encode(ephemeral.PublicKey().Bytes()),
		Salt:               encode(salt), IV: encode(iv), Ciphertext: encode(gcm.Seal(nil, iv, plaintext, nil)),
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestCLIConfigProbeRespectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, _, err := probeProvider(ctx, "codex", "https://127.0.0.1:1", "model", "secret"); err == nil {
		t.Fatal("expected canceled probe to fail")
	}
}

func TestCLIConfigProbeBudgetsAllowSlowInference(t *testing.T) {
	if cliConfigCallLimit < 20*time.Second {
		t.Fatalf("per-call probe budget = %s, want at least 20s for slow providers", cliConfigCallLimit)
	}
	if cliConfigProbeLimit <= cliConfigCallLimit {
		t.Fatalf("total probe budget = %s, want greater than per-call budget %s", cliConfigProbeLimit, cliConfigCallLimit)
	}
}
