package hub

import (
	"context"
	"encoding/json"
	"net/http"
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
		"cli": "codex", "name": "renamed", "baseUrl": preset.BaseURL, "model": preset.Model, "keyId": "bridge-key",
	}, http.StatusOK)
	if strings.Contains(toJSON(rename), "private-ciphertext") || strings.Contains(toJSON(rename), "secret") {
		t.Fatalf("update response exposed encrypted secret: %#v", rename)
	}
	loaded, err := st.CLIConfigPresetByID(ctx, preset.ID, user.ID, agent.ID)
	if err != nil || loaded.Name != "renamed" || !loaded.Active || loaded.Secret.Ciphertext != "private-ciphertext" {
		t.Fatalf("rename update = %#v, err=%v", loaded, err)
	}

	authJSONRequestWithCookie(t, s, http.MethodPut, path, cookie, map[string]string{
		"cli": "codex", "name": "renamed", "baseUrl": preset.BaseURL, "model": "model-b", "keyId": "bridge-key",
	}, http.StatusOK)
	loaded, err = st.CLIConfigPresetByID(ctx, preset.ID, user.ID, agent.ID)
	if err != nil || loaded.Model != "model-b" || loaded.Active || loaded.Secret.Ciphertext != "private-ciphertext" {
		t.Fatalf("model update = %#v, err=%v", loaded, err)
	}
}

func toJSON(value any) string {
	raw, _ := json.Marshal(value)
	return strings.TrimSpace(string(raw))
}
