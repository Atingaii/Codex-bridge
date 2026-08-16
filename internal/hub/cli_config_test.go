package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestReviewedModelCatalogUsesCLIAndVerifiedNativeLevels(t *testing.T) {
	tests := []struct{ cli, model, effort string }{
		{"codex", "gpt-5.6", "none"},
		{"codex", "gpt-5.6-terra", "xhigh"},
		{"claude", "claude-opus-4-6", "max"},
	}
	for _, tc := range tests {
		t.Run(tc.model, func(t *testing.T) {
			metadata := reviewedModelMetadata(tc.cli, tc.model)
			if metadata == nil || !metadata.Reviewed || !containsString(metadata.SupportedReasoningLevels, tc.effort) {
				t.Fatalf("catalog(%s, %q) = %#v", tc.cli, tc.model, metadata)
			}
		})
	}
}

func TestReviewedModelCatalogDoesNotInventCrossCLIOrVendorLevels(t *testing.T) {
	for _, tc := range []struct{ cli, model string }{
		{"claude", "gpt-5.6-terra"},
		{"codex", "claude-opus-4-6"},
		{"codex", "deepseek-v4-flash"},
		{"claude", "kimi-k3"},
		{"claude", "glm-5.2"},
		{"codex", "gemini-3.1-pro"},
		{"codex", "qwen3-max"},
	} {
		t.Run(tc.cli+"/"+tc.model, func(t *testing.T) {
			if metadata := reviewedModelMetadata(tc.cli, tc.model); metadata != nil {
				t.Fatalf("catalog(%s, %q) invented metadata: %#v", tc.cli, tc.model, metadata)
			}
		})
	}
}

func TestNormalizeReviewedPresetRefreshesLegacyReasoningLevels(t *testing.T) {
	preset := store.CLIConfigPreset{CLI: "codex", Model: "gpt-5.6-terra", ReasoningEffort: "high"}
	normalizeReviewedPreset(&preset)
	if !containsString(preset.ReasoningLevels, "none") || !containsString(preset.ReasoningLevels, "xhigh") || preset.ReasoningDefault != "medium" {
		t.Fatalf("legacy preset not refreshed: %#v", preset)
	}
}

func TestNormalizeReviewedPresetClearsLegacyInventedLevels(t *testing.T) {
	preset := store.CLIConfigPreset{
		CLI: "codex", Model: "glm-5.2", ReasoningEffort: "max",
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
			Version: 1, KeyID: "bridge-key", CLIs: []string{"codex", "claude"},
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
