package main

import (
	"os"
	"path/filepath"
	"strings"
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
}

func TestLinkStartScriptUsesProfileAndPinnedMachineID(t *testing.T) {
	opts, err := prepareLinkOptions("https://hub.example/", "tok 'quoted'", linkProfileAutoExecute, "/repo", "agent name", "mid-123")
	if err != nil {
		t.Fatal(err)
	}
	script := linkStartScript(opts)
	for _, want := range []string{
		"connect --hub 'https://hub.example'",
		"--runner codex --orchestration-runner ccb --sandbox danger-full-access --approval-policy never",
		"--cwd \"$CB_CWD\"",
		"--name \"$CB_NAME\"",
		"--machine-id-file '" + opts.MIDPath + "'",
		"--machine-id 'mid-123'",
		"'tok '\\''quoted'",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("start script missing %q:\n%s", want, script)
		}
	}
}

func TestWriteLinkFilesWritesDetectedPathsAndProxyEnv(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HTTP_PROXY", "http://proxy.example")
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
		CCBPath:    "/usr/bin/ccb",
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

	if err := writeLinkFiles(opts); err != nil {
		t.Fatal(err)
	}
	envBytes, err := os.ReadFile(opts.EnvPath)
	if err != nil {
		t.Fatal(err)
	}
	env := string(envBytes)
	for _, want := range []string{
		"BRIDGE_CODEX_PATH='/usr/bin/codex'",
		"BRIDGE_CLAUDE_PATH='/usr/bin/claude'",
		"BRIDGE_CCB_PATH='/usr/bin/ccb'",
		"HTTP_PROXY='http://proxy.example'",
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
		StartPath: "/home/user/.codex-bridge/services/abc123.sh",
		LogPath:   "/home/user/.codex-bridge/logs/abc123.log",
	}
	unit := linkSystemdUnit(opts)
	for _, want := range []string{
		"Restart=always",
		"OOMPolicy=continue",
		"ExecStart=/home/user/.codex-bridge/services/abc123.sh",
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("systemd unit missing %q:\n%s", want, unit)
		}
	}
}

func TestInstallCCBRootWrapperLinksSourceEntrypoint(t *testing.T) {
	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "src")
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(srcDir, "ccb")
	if err := os.WriteFile(source, []byte("#!/usr/bin/env python3\nprint('ok')\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := installCCBRootWrapper(srcDir, binDir); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(binDir, "ccb")
	st, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode()&0o111 == 0 {
		t.Fatalf("installed ccb is not executable: %s", st.Mode())
	}
	sourceInfo, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	if sourceInfo.Mode()&0o111 == 0 {
		t.Fatalf("source ccb is not executable: %s", sourceInfo.Mode())
	}
}
