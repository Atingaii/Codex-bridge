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
	if claude["permissions"] == nil || claude["hooks"] == nil || claude["model"] != claudeModelSlot {
		t.Fatalf("unexpected Claude settings: %#v", claude)
	}
	claudeEnv, _ := claude["env"].(map[string]any)
	if _, exists := claudeEnv["ANTHROPIC_MODEL"]; exists || claudeEnv["KEEP"] != "yes" {
		t.Fatalf("unexpected Claude environment: %#v", claudeEnv)
	}
	for _, key := range []string{"ANTHROPIC_REASONING_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL"} {
		if _, exists := claudeEnv[key]; exists {
			t.Fatalf("Claude alias override %q was not removed: %#v", key, claudeEnv)
		}
	}
	claudeOverrides, _ := claude["modelOverrides"].(map[string]any)
	if claudeOverrides[claudeModelSlot] != "claude-model" || claudeOverrides["claude-opus-4-6"] != "keep-opus" {
		t.Fatalf("unexpected Claude model overrides: %#v", claudeOverrides)
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
	if claudeOverrides[claudeModelSlot] != "keep-sonnet" || claudeOverrides["claude-opus-4-6"] != "keep-opus" {
		t.Fatalf("Claude model overrides were not restored: %#v", claudeOverrides)
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
