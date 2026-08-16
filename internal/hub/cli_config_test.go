package hub

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

func TestCLIConfigRelayBudgetExceedsBridgeProbeBudget(t *testing.T) {
	if cliConfigRequestTimeout < 90*time.Second {
		t.Fatalf("relay budget = %s, want at least 90s for slow provider probes", cliConfigRequestTimeout)
	}
}

func TestListUserCLIConfigPresetsDoesNotRequireBridge(t *testing.T) {
	s, st := newAuthTestServer(t)
	ctx := context.Background()
	owner, err := st.UpsertUser(ctx, "library-owner", "abc1234567")
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.UpsertUser(ctx, "library-other", "abc1234567")
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.CreateCLIConfigPreset(ctx, store.CLIConfigPreset{
		UserID: owner.ID, CLI: "codex", Name: "shared provider", BaseURL: "https://provider.example/v1", Model: "gpt-5.6-terra",
		VaultSecret: "vault-ciphertext-must-not-leak",
	})
	if err != nil {
		t.Fatal(err)
	}

	ownerCookie := loginCookie(t, s, map[string]string{"username": "library-owner", "password": "abc1234567"})
	request := httptest.NewRequest(http.MethodGet, "/api/cli-config/presets", nil)
	request.AddCookie(ownerCookie)
	recorder := httptest.NewRecorder()
	s.httpSrv.Handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "vault-ciphertext-must-not-leak") || strings.Contains(recorder.Body.String(), "secret") {
		t.Fatalf("account library response exposed credential material: %s", recorder.Body.String())
	}
	var response struct {
		Presets []store.CLIConfigPreset `json:"presets"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Presets) != 1 || response.Presets[0].Name != "shared provider" || response.Presets[0].Model != "gpt-5.6-terra" {
		t.Fatalf("account library response = %#v", response.Presets)
	}

	otherCookie := loginCookie(t, s, map[string]string{"username": "library-other", "password": "abc1234567"})
	otherRequest := httptest.NewRequest(http.MethodGet, "/api/cli-config/presets", nil)
	otherRequest.AddCookie(otherCookie)
	otherRecorder := httptest.NewRecorder()
	s.httpSrv.Handler.ServeHTTP(otherRecorder, otherRequest)
	if otherRecorder.Code != http.StatusOK {
		t.Fatalf("other-user list status = %d, body = %s", otherRecorder.Code, otherRecorder.Body.String())
	}
	var otherResponse struct {
		Presets []store.CLIConfigPreset `json:"presets"`
	}
	if err := json.Unmarshal(otherRecorder.Body.Bytes(), &otherResponse); err != nil {
		t.Fatal(err)
	}
	if len(otherResponse.Presets) != 0 {
		t.Fatalf("other user received presets: %#v", otherResponse.Presets)
	}
}

func TestUserCLIConfigTestDoesNotRequireBridge(t *testing.T) {
	s, st := newAuthTestServer(t)
	ctx := context.Background()
	_, err := st.UpsertUser(ctx, "library-probe-owner", "abc1234567")
	if err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, s, map[string]string{"username": "library-probe-owner", "password": "abc1234567"})
	request := authJSONRequestWithCookie(t, s, http.MethodPost, "/api/cli-config/test", cookie, map[string]string{
		"cli": "codex", "baseUrl": "https://127.0.0.1/v1", "apiKey": "must-not-be-stored-or-relayed",
	}, http.StatusBadGateway)
	if strings.Contains(toJSON(request), "AGENT_OFFLINE") || strings.Contains(toJSON(request), "BRIDGE") {
		t.Fatalf("account-level probe required a Bridge: %#v", request)
	}
}

func TestUserVaultMaterializesPresetForAnotherBridge(t *testing.T) {
	s, st := newAuthTestServer(t)
	ctx := context.Background()
	user, err := st.UpsertUser(ctx, "vault-owner", "abc1234567")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := st.UpsertAgentForUser(ctx, user.ID, "vault-target", "vault-machine", "host", "instance", nil)
	if err != nil {
		t.Fatal(err)
	}
	targetPrivate, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicKey := base64.RawStdEncoding.EncodeToString(targetPrivate.PublicKey().Bytes())
	connection := &BridgeConn{
		agentID: agent.ID,
		capabilities: &protocol.BridgeCapabilities{ConfigSwitcher: &protocol.CLIConfigSwitcherCapability{
			Version: 2, PublicKey: publicKey, KeyID: "target-key", CLIs: []string{"codex"},
		}},
		wsSender: wsSender{send: make(chan protocol.Envelope, 1), done: make(chan struct{})},
	}
	s.pool.RegisterAgent(connection)
	t.Cleanup(func() { connection.Close() })
	vaultSecret, err := s.sealCLIConfigVaultSecret([]byte("user-level-api-key"))
	if err != nil {
		t.Fatal(err)
	}
	preset, err := st.CreateCLIConfigPreset(ctx, store.CLIConfigPreset{
		UserID: user.ID, CLI: "codex", Name: "shared", BaseURL: "https://provider.example/v1", Model: "gpt-5.6-terra", VaultSecret: vaultSecret,
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := st.CLIConfigPresetByID(ctx, preset.ID, user.ID, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.materializeCLIConfigPresetCredential(ctx, user.ID, agent.ID, &loaded); err != nil {
		t.Fatal(err)
	}
	plaintext, err := decryptCLIConfigEnvelope(targetPrivate, loaded.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if string(plaintext) != "user-level-api-key" || loaded.KeyHint != "target-key" {
		t.Fatalf("materialized preset = %#v, plaintext = %q", loaded, plaintext)
	}
	serialized, err := json.Marshal(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serialized), vaultSecret) || strings.Contains(string(serialized), "user-level-api-key") {
		t.Fatalf("preset response exposed vault material: %s", serialized)
	}
}

func TestReviewedModelCatalogUsesCLIAndVerifiedNativeLevels(t *testing.T) {
	tests := []struct {
		model         string
		levels        []string
		defaultEffort string
	}{
		{"claude-opus-5", []string{"low", "medium", "high", "xhigh", "max"}, "high"},
		{"claude-fable-5", []string{"low", "medium", "high", "xhigh", "max"}, "high"},
		{"claude-opus-4.6", []string{"low", "medium", "high", "max"}, "high"},
		{"gpt-5.6-sol", []string{"low", "medium", "high", "xhigh", "max"}, "medium"},
		{"gpt-5.6-terra", []string{"none", "low", "medium", "high", "xhigh", "max"}, "medium"},
		{"gpt-5.6-luna", []string{"low", "medium", "high", "xhigh", "max"}, "medium"},
		{"gpt-5.5", []string{"low", "medium", "high", "xhigh"}, "medium"},
		{"grok-4.6", []string{"low", "medium", "high", "xhigh"}, "medium"},
		{"gemini-3.7-flash", []string{"low", "medium", "high"}, "medium"},
		{"deepseek-v4-pro", []string{"low", "high", "max"}, "high"},
		{"qwen3.8-max", []string{"xhigh"}, "xhigh"},
		{"muse-spark-1.2", []string{"xhigh"}, "xhigh"},
		{"claude-opus-4.8", []string{"low", "medium", "high", "xhigh", "max"}, "high"},
		{"claude-sonnet-5", []string{"low", "medium", "high", "xhigh", "max"}, "high"},
		{"deepseek-v4-flash", []string{"low", "high", "max"}, "high"},
		{"gemini-3.6-flash", []string{"high"}, "high"},
		{"glm-5.2", []string{"high", "max"}, "high"},
		{"gemini-3.5-flash", []string{"high"}, "high"},
	}
	for _, tc := range tests {
		for _, cli := range []string{"codex", "claude"} {
			t.Run(cli+"/"+tc.model, func(t *testing.T) {
				metadata := reviewedModelMetadata(cli, tc.model)
				if metadata == nil || !metadata.Reviewed || !slices.Equal(metadata.SupportedReasoningLevels, tc.levels) || metadata.DefaultReasoningLevel != tc.defaultEffort {
					t.Fatalf("catalog(%s, %q) = %#v", cli, tc.model, metadata)
				}
			})
		}
	}
}

func TestReviewedModelCatalogDoesNotInventUnmeasuredLevels(t *testing.T) {
	for _, tc := range []struct{ cli, model string }{
		{"codex", "gpt-5.6-cyber"},
		{"codex", "gemini-3.1-pro"},
		{"claude", "qwen3.7-plus"},
	} {
		t.Run(tc.cli+"/"+tc.model, func(t *testing.T) {
			if metadata := reviewedModelMetadata(tc.cli, tc.model); metadata != nil {
				t.Fatalf("catalog(%s, %q) invented metadata: %#v", tc.cli, tc.model, metadata)
			}
		})
	}
}

func TestReviewedModelCatalogNormalizesProviderAndOfficialAliases(t *testing.T) {
	for _, model := range []string{"openai/gpt-5.6-sol", "models/gemini-3.7-flash", "xai/grok-4-6", "deepseek/DeepSeek-V4-Pro", "anthropic/claude-opus-4-8"} {
		if metadata := reviewedModelMetadata("claude", model); metadata == nil {
			t.Fatalf("catalog alias %q was not normalized", model)
		}
	}
}

func TestNormalizeReviewedPresetRefreshesLegacyReasoningLevels(t *testing.T) {
	preset := store.CLIConfigPreset{CLI: "codex", Model: "gpt-5.6-terra", ReasoningEffort: "high"}
	normalizeReviewedPreset(&preset)
	if !containsString(preset.ReasoningLevels, "low") || !containsString(preset.ReasoningLevels, "xhigh") || preset.ReasoningDefault != "medium" {
		t.Fatalf("legacy preset not refreshed: %#v", preset)
	}
}

func TestNormalizeReviewedPresetClearsLegacyInventedLevels(t *testing.T) {
	preset := store.CLIConfigPreset{
		CLI: "codex", Model: "qwen3.7-plus", ReasoningEffort: "max",
		ReasoningLevels: []string{"low", "medium", "high", "xhigh", "max"}, ReasoningDefault: "medium",
	}
	normalizeReviewedPreset(&preset)
	if preset.ReasoningEffort != "" || preset.ReasoningDefault != "" || len(preset.ReasoningLevels) != 0 {
		t.Fatalf("legacy invented levels survived normalization: %#v", preset)
	}
}

func TestUpdateCLIConfigPresetKeepsSecretPrivateAndTracksActiveState(t *testing.T) {
	t.Parallel()

	s, st := newAuthTestServer(t)
	ctx := context.Background()
	user, err := st.UpsertUser(ctx, "preset-editor", "abc1234567")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := st.UpsertAgentForUser(ctx, user.ID, "preset-cli", "preset-machine", "host", "instance", nil)
	if err != nil {
		t.Fatal(err)
	}
	conn := &BridgeConn{
		agentID: agent.ID,
		capabilities: &protocol.BridgeCapabilities{ConfigSwitcher: &protocol.CLIConfigSwitcherCapability{
			Version: 2, KeyID: "bridge-key", CLIs: []string{"codex", "claude"},
		}},
		wsSender: wsSender{send: make(chan protocol.Envelope, 1), done: make(chan struct{})},
	}
	s.pool.RegisterAgent(conn)
	t.Cleanup(func() { conn.Close() })
	go func() {
		for {
			select {
			case <-conn.wsSender.done:
				return
			case env := <-conn.wsSender.send:
				request, err := protocol.Decode[protocol.CLIConfigRequest](env)
				if err != nil {
					return
				}
				if env.Type == protocol.TypeCLIConfigExport {
					secret, err := encryptCLIConfigForPublicKey(request.RecipientPublicKey, []byte("test-provider-key"))
					if err != nil {
						return
					}
					s.handleCLIConfigResult(agent.ID, protocol.MustEnvelope(protocol.TypeCLIConfigResult, "", protocol.CLIConfigResult{
						RequestID: request.RequestID, CLI: request.CLI, OK: true, Secret: secret,
					}))
					continue
				}
				s.handleCLIConfigResult(agent.ID, protocol.MustEnvelope(protocol.TypeCLIConfigResult, "", protocol.CLIConfigResult{
					RequestID: request.RequestID, CLI: request.CLI, OK: true, BaseURL: "https://provider.example/v1",
				}))
			}
		}
	}()

	preset, err := st.CreateCLIConfigPreset(ctx, store.CLIConfigPreset{
		UserID: user.ID, AgentID: agent.ID, CLI: "codex", Name: "primary",
		BaseURL: "https://provider.example/v1", Model: "model-a", KeyHint: "bridge-key",
		Secret: protocol.EncryptedSecret{EphemeralPublicKey: "pub", Salt: "salt", IV: "iv", Ciphertext: "private-ciphertext"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ActivateCLIConfigPreset(ctx, preset.ID, user.ID, agent.ID, "codex"); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, s, map[string]string{"username": "preset-editor", "password": "abc1234567"})
	path := "/api/agents/" + agent.ID + "/cli-config/presets/" + preset.ID

	rename := authJSONRequestWithCookie(t, s, http.MethodPut, path, cookie, map[string]string{
		"cli": "codex", "name": "renamed", "baseUrl": "https://provider.example", "model": preset.Model, "keyId": "bridge-key",
	}, http.StatusOK)
	if strings.Contains(toJSON(rename), "private-ciphertext") || strings.Contains(toJSON(rename), "secret") {
		t.Fatalf("update response exposed encrypted secret: %#v", rename)
	}
	loaded, err := st.CLIConfigPresetByID(ctx, preset.ID, user.ID, agent.ID)
	if err != nil || loaded.Name != "renamed" || loaded.BaseURL != "https://provider.example/v1" || !loaded.Active || loaded.Secret.Ciphertext != "private-ciphertext" {
		t.Fatalf("rename update = %#v, err=%v", loaded, err)
	}

	authJSONRequestWithCookie(t, s, http.MethodPut, path, cookie, map[string]string{
		"cli": "codex", "name": "renamed", "baseUrl": "https://provider.example", "model": "model-b", "keyId": "bridge-key",
	}, http.StatusOK)
	loaded, err = st.CLIConfigPresetByID(ctx, preset.ID, user.ID, agent.ID)
	if err != nil || loaded.Model != "model-b" || loaded.Active || loaded.Secret.Ciphertext != "private-ciphertext" {
		t.Fatalf("model update = %#v, err=%v", loaded, err)
	}

	createBody, err := json.Marshal(map[string]any{
		"cli": "codex", "name": "secondary", "baseUrl": "https://provider.example", "model": "model-c", "keyId": "bridge-key",
		"secret": map[string]string{"ephemeralPublicKey": "pub", "salt": "salt", "iv": "iv", "ciphertext": "secondary-ciphertext"},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/agents/"+agent.ID+"/cli-config/presets", bytes.NewReader(createBody))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	s.httpSrv.Handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create preset status = %d, want %d, body = %s", recorder.Code, http.StatusCreated, recorder.Body.String())
	}
	presets, err := st.ListCLIConfigPresets(ctx, user.ID, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range presets {
		if candidate.Name == "secondary" {
			if candidate.BaseURL != "https://provider.example/v1" {
				t.Fatalf("created preset BaseURL = %q, want probed canonical URL", candidate.BaseURL)
			}
			return
		}
	}
	t.Fatal("created preset not found")
}

func toJSON(value any) string {
	raw, _ := json.Marshal(value)
	return strings.TrimSpace(string(raw))
}
