package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

func TestPrepareLinkOptionsDefaults(t *testing.T) {
	tmp := t.TempDir()
	opts, err := prepareLinkOptions("", "tok_test", "", tmp, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if opts.HubURL != "https://sparkapi.tech" {
		t.Fatalf("hub url = %q", opts.HubURL)
	}
	if opts.Profile != linkProfileReviewRequired {
		t.Fatalf("profile = %q", opts.Profile)
	}
	if opts.Token != "tok_test" {
		t.Fatalf("token = %q", opts.Token)
	}
	if opts.Hash == "" || strings.Contains(opts.Service, "machine_id") || !strings.HasPrefix(opts.Service, "codex-bridge-") {
		t.Fatalf("bad service naming: hash=%q service=%q", opts.Hash, opts.Service)
	}
	if opts.MIDPath != filepath.Join(opts.Home, ".codex-bridge", "machines", opts.Hash) {
		t.Fatalf("machine id path = %q", opts.MIDPath)
	}
	if opts.PIDPath != filepath.Join(opts.Home, ".codex-bridge", "services", opts.Hash+".pid") {
		t.Fatalf("pid path = %q", opts.PIDPath)
	}
}

func TestLinkStartScriptUsesProfileAndPinnedMachineID(t *testing.T) {
	opts, err := prepareLinkOptions("https://hub.example/", "tok 'quoted'", linkProfileAutoExecute, "/repo", "agent name", "mid-123")
	if err != nil {
		t.Fatal(err)
	}
	script := linkStartScript(opts)
	for _, want := range []string{
		"connect --hub 'https://hub.example'",
		"--runner codex --sandbox danger-full-access --approval-policy never",
		"--cwd \"$CB_CWD\"",
		"--name \"$CB_NAME\"",
		"--machine-id-file '" + opts.MIDPath + "'",
		"--machine-id 'mid-123'",
		"cd \"$CB_CWD\"",
		"'tok '\\''quoted'",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("start script missing %q:\n%s", want, script)
		}
	}
}

func TestStrictWorkspaceLinkProfileIsExplicitAndIsolated(t *testing.T) {
	if got := normalizeLinkProfile(linkProfileStrictWorkspace); got != linkProfileStrictWorkspace {
		t.Fatalf("strict profile = %q", got)
	}
	args := strings.Join(linkProfileConnectArgs(linkProfileStrictWorkspace), " ")
	for _, want := range []string{"--runner codex", "--sandbox workspace-write", "--approval-policy never", "--strict-workspace"} {
		if !strings.Contains(args, want) {
			t.Fatalf("strict profile args missing %q: %s", want, args)
		}
	}
	if strings.Contains(args, "danger-full-access") {
		t.Fatalf("strict profile must not request danger-full-access: %s", args)
	}
}

func TestLinkProfileConnectArgsKeepPermissionModesIsolated(t *testing.T) {
	tests := []struct {
		name    string
		profile string
		want    []string
	}{
		{
			name:    "review-required",
			profile: linkProfileReviewRequired,
			want:    []string{"--runner", "codex-app-server", "--sandbox", "workspace-write", "--approval-policy", "untrusted"},
		},
		{
			name:    "auto-execute",
			profile: linkProfileAutoExecute,
			want:    []string{"--runner", "codex", "--sandbox", "danger-full-access", "--approval-policy", "never"},
		},
		{
			name:    "strict-workspace",
			profile: linkProfileStrictWorkspace,
			want:    []string{"--runner", "codex", "--sandbox", "workspace-write", "--approval-policy", "never", "--strict-workspace"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := linkProfileConnectArgs(tt.profile); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("profile args = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestStrictWorkspaceLinkPreservesRuntimeOverrides(t *testing.T) {
	names := strings.Join(linkPreservedEnvNames(), "\n")
	for _, want := range []string{"CODEX_BRIDGE_RUNTIME_DIR", "BRIDGE_STRICT_WORKSPACE_READ_ONLY"} {
		if !strings.Contains("\n"+names+"\n", "\n"+want+"\n") {
			t.Fatalf("link environment does not preserve %s", want)
		}
	}
}

func TestResolveLinkCLIsResolvesCodexAndClaude(t *testing.T) {
	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"codex", "claude"} {
		path := filepath.Join(binDir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", tmp)

	opts := linkOptions{Home: tmp}
	if err := resolveLinkCLIs(&opts); err != nil {
		t.Fatal(err)
	}
	if opts.CodexPath == "" || opts.ClaudePath == "" {
		t.Fatalf("resolved CLI paths = codex:%q claude:%q", opts.CodexPath, opts.ClaudePath)
	}
}

func TestWriteLinkFilesWritesDetectedPathsAndProxyEnv(t *testing.T) {
	tmp := t.TempDir()
	codexHome := filepath.Join(tmp, "codex-home")
	claudeConfig := filepath.Join(tmp, "claude-config")
	t.Setenv("HTTP_PROXY", "http://proxy.example")
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "oauth-test")
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("CLAUDE_CONFIG_DIR", claudeConfig)
	opts := linkOptions{
		HubURL:     "https://hub.example",
		Token:      "tok",
		Profile:    linkProfileReviewRequired,
		CWD:        tmp,
		Name:       "agent",
		MachineID:  "mid",
		Home:       tmp,
		CodexPath:  "/usr/bin/codex",
		ClaudePath: "/usr/bin/claude",
		Hash:       "abc123",
		ServiceDir: filepath.Join(tmp, ".codex-bridge", "services"),
		LogDir:     filepath.Join(tmp, ".codex-bridge", "logs"),
		MachineDir: filepath.Join(tmp, ".codex-bridge", "machines"),
		Service:    "codex-bridge-abc123.service",
	}
	opts.StartPath = filepath.Join(opts.ServiceDir, opts.Hash+".sh")
	opts.LogPath = filepath.Join(opts.LogDir, opts.Hash+".log")
	opts.EnvPath = filepath.Join(opts.ServiceDir, opts.Hash+".env")
	opts.CWDPath = filepath.Join(opts.ServiceDir, opts.Hash+".cwd")
	opts.NamePath = filepath.Join(opts.ServiceDir, opts.Hash+".name")
	opts.MIDPath = filepath.Join(opts.MachineDir, opts.Hash)
	opts.PIDPath = filepath.Join(opts.ServiceDir, opts.Hash+".pid")

	if err := writeLinkFiles(opts); err != nil {
		t.Fatal(err)
	}
	envBytes, err := os.ReadFile(opts.EnvPath)
	if err != nil {
		t.Fatal(err)
	}
	env := string(envBytes)
	for _, want := range []string{
		"HOME='" + tmp + "'",
		"BRIDGE_CODEX_PATH='/usr/bin/codex'",
		"BRIDGE_CLAUDE_PATH='/usr/bin/claude'",
		"CODEX_HOME='" + codexHome + "'",
		"CLAUDE_CONFIG_DIR='" + claudeConfig + "'",
		"HTTP_PROXY='http://proxy.example'",
		"OPENAI_API_KEY='sk-test'",
		"CLAUDE_CODE_OAUTH_TOKEN='oauth-test'",
	} {
		if !strings.Contains(env, want) {
			t.Fatalf("env missing %q:\n%s", want, env)
		}
	}
	mid, err := os.ReadFile(opts.MIDPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(mid)) != "mid" {
		t.Fatalf("machine id = %q", mid)
	}
}

func TestLinkSystemdUnitKeepsBridgeAliveOnChildOOM(t *testing.T) {
	opts := linkOptions{
		CWD:       "/repo",
		Home:      "/home/user",
		StartPath: "/home/user/.codex-bridge/services/abc123.sh",
		LogPath:   "/home/user/.codex-bridge/logs/abc123.log",
	}
	unit := linkSystemdUnit(opts)
	for _, want := range []string{
		"Restart=always",
		"OOMPolicy=continue",
		"WorkingDirectory=/repo",
		"Environment=HOME=/home/user",
		"ExecStart=/home/user/.codex-bridge/services/abc123.sh",
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("systemd unit missing %q:\n%s", want, unit)
		}
	}
}

func TestStopManagedNohupBridgeRejectsUnownedPID(t *testing.T) {
	tmp := t.TempDir()
	services := filepath.Join(tmp, ".codex-bridge", "services")
	if err := os.MkdirAll(services, 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	pidPath := filepath.Join(services, "abc123.pid")
	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", cmd.Process.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	stopManagedNohupBridge(linkOptions{Home: tmp, ServiceDir: services, Hash: "abc123", PIDPath: pidPath})
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("unowned process was terminated: %v", err)
	}
	if _, err := os.Stat(pidPath); err != nil {
		t.Fatalf("unowned PID record should be retained for diagnosis: %v", err)
	}
}
