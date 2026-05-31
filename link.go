package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
)

const (
	linkProfileReviewRequired = "review-required"
	linkProfileAutoExecute    = "auto-execute"
)

type linkOptions struct {
	HubURL    string
	Token     string
	Profile   string
	CWD       string
	Name      string
	MachineID string
	Home      string

	CodexPath  string
	ClaudePath string

	Hash       string
	ServiceDir string
	LogDir     string
	MachineDir string
	Service    string
	StartPath  string
	LogPath    string
	EnvPath    string
	CWDPath    string
	NamePath   string
	MIDPath    string
}

func runLink(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("link", flag.ContinueOnError)
	hubURL := fs.String("hub", cfg.Bridge.HubURL, "hub URL")
	name := fs.String("name", "", "CLI endpoint name")
	cwd := fs.String("cwd", "", "workspace directory")
	machineID := fs.String("machine-id", "", "existing machine id to write before connecting")
	profile := fs.String("profile", linkProfileReviewRequired, "permission profile: review-required or auto-execute")

	token, flagArgs, err := normalizeConnectArgs(args)
	if err != nil {
		return err
	}
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		if token != "" || fs.NArg() > 1 {
			return errors.New("link requires exactly one enroll token")
		}
		token = fs.Arg(0)
	}
	if token == "" {
		return errors.New("link requires exactly one enroll token")
	}

	opts, err := prepareLinkOptions(*hubURL, token, *profile, *cwd, *name, *machineID)
	if err != nil {
		return err
	}
	if err := resolveLinkCLIs(&opts); err != nil {
		return err
	}
	if err := writeLinkFiles(opts); err != nil {
		return err
	}
	if err := startLinkedBridge(context.Background(), opts); err != nil {
		return err
	}
	return nil
}

func prepareLinkOptions(hubURL, token, profile, cwd, name, machineID string) (linkOptions, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return linkOptions{}, fmt.Errorf("resolve home directory: %w", err)
	}
	hubURL = strings.TrimRight(strings.TrimSpace(hubURL), "/")
	if hubURL == "" {
		hubURL = "https://sparkapi.tech"
	}
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		cwd, err = os.Getwd()
		if err != nil {
			cwd = "."
		}
	}
	cwd = expandUserPath(cwd)
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	profile = normalizeLinkProfile(profile)
	if profile == "" {
		return linkOptions{}, errors.New("link --profile must be review-required or auto-execute")
	}

	hash := shortLinkHash(cwd)
	name = strings.TrimSpace(name)
	if name == "" {
		name = defaultLinkName(cwd, hash)
	}

	base := filepath.Join(home, ".codex-bridge")
	opts := linkOptions{
		HubURL:     hubURL,
		Token:      token,
		Profile:    profile,
		CWD:        cwd,
		Name:       name,
		MachineID:  strings.TrimSpace(machineID),
		Home:       home,
		Hash:       hash,
		ServiceDir: filepath.Join(base, "services"),
		LogDir:     filepath.Join(base, "logs"),
		MachineDir: filepath.Join(base, "machines"),
		Service:    "codex-bridge-" + hash + ".service",
	}
	opts.StartPath = filepath.Join(opts.ServiceDir, hash+".sh")
	opts.LogPath = filepath.Join(opts.LogDir, hash+".log")
	opts.EnvPath = filepath.Join(opts.ServiceDir, hash+".env")
	opts.CWDPath = filepath.Join(opts.ServiceDir, hash+".cwd")
	opts.NamePath = filepath.Join(opts.ServiceDir, hash+".name")
	opts.MIDPath = filepath.Join(opts.MachineDir, hash)
	return opts, nil
}

func normalizeLinkProfile(profile string) string {
	switch strings.TrimSpace(strings.ToLower(profile)) {
	case "", linkProfileReviewRequired:
		return linkProfileReviewRequired
	case linkProfileAutoExecute:
		return linkProfileAutoExecute
	default:
		return ""
	}
}

func defaultLinkName(cwd, hash string) string {
	host, _ := os.Hostname()
	base := filepath.Base(cwd)
	if base == "" || base == "." || base == string(filepath.Separator) {
		base = "workspace"
	}
	if host == "" {
		host = "cli"
	}
	return host + "-" + base + "-" + hash
}

func shortLinkHash(cwd string) string {
	sum := sha256.Sum256([]byte(cwd))
	return hex.EncodeToString(sum[:])[:12]
}

func resolveLinkCLIs(opts *linkOptions) error {
	codexPath, err := requireCLI("codex", "Codex CLI")
	if err != nil {
		return err
	}
	claudePath, err := requireCLI("claude", "Claude Code CLI")
	if err != nil {
		return err
	}
	opts.CodexPath = codexPath
	opts.ClaudePath = claudePath
	return nil
}

func requireCLI(name, label string) (string, error) {
	path, err := lookPathWithLocalBin(name)
	if err == nil {
		return path, nil
	}
	return "", fmt.Errorf("%s not found. Install it or run link from a shell where `command -v %s` works. PATH=%s", label, name, os.Getenv("PATH"))
}

func lookPathWithLocalBin(name string) (string, error) {
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err == nil {
		candidate := filepath.Join(home, ".local", "bin", name)
		if st, statErr := os.Stat(candidate); statErr == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", exec.ErrNotFound
}

func writeLinkFiles(opts linkOptions) error {
	if err := os.MkdirAll(opts.ServiceDir, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(opts.LogDir, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(opts.MachineDir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(opts.CWDPath, []byte(opts.CWD+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(opts.NamePath, []byte(opts.Name+"\n"), 0o600); err != nil {
		return err
	}
	if opts.MachineID != "" {
		if err := os.WriteFile(opts.MIDPath, []byte(opts.MachineID+"\n"), 0o600); err != nil {
			return err
		}
	}
	if err := os.WriteFile(opts.EnvPath, []byte(linkEnvFile(opts)), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(opts.StartPath, []byte(linkStartScript(opts)), 0o700); err != nil {
		return err
	}
	if _, err := os.OpenFile(opts.LogPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600); err != nil {
		return err
	}
	return nil
}

func linkEnvFile(opts linkOptions) string {
	var b strings.Builder
	writeEnvAssignment(&b, "HOME", opts.Home)
	writeEnvAssignment(&b, "PATH", linkPathWithLocalBin(opts.Home))
	writeEnvAssignment(&b, "BRIDGE_CODEX_PATH", opts.CodexPath)
	writeEnvAssignment(&b, "BRIDGE_CLAUDE_PATH", opts.ClaudePath)
	for _, name := range linkPreservedEnvNames() {
		if value := os.Getenv(name); value != "" {
			writeEnvAssignment(&b, name, value)
		}
	}
	return b.String()
}

func linkPreservedEnvNames() []string {
	return []string{
		"CODEX_HOME",
		"CODEX_CONFIG_HOME",
		"CLAUDE_CONFIG_DIR",
		"CLAUDE_HOME",
		"XDG_CONFIG_HOME",
		"XDG_DATA_HOME",
		"XDG_STATE_HOME",
		"OPENAI_API_KEY",
		"OPENAI_BASE_URL",
		"OPENAI_ORG_ID",
		"OPENAI_PROJECT",
		"ANTHROPIC_API_KEY",
		"CLAUDE_API_KEY",
		"CLAUDE_CODE_OAUTH_TOKEN",
		"CODEX_API_KEY",
		"AZURE_OPENAI_API_KEY",
		"AZURE_OPENAI_ENDPOINT",
		"AZURE_OPENAI_API_VERSION",
		"GEMINI_API_KEY",
		"GOOGLE_API_KEY",
		"OPENROUTER_API_KEY",
		"HTTP_PROXY",
		"HTTPS_PROXY",
		"ALL_PROXY",
		"NO_PROXY",
		"http_proxy",
		"https_proxy",
		"all_proxy",
		"no_proxy",
	}
}

func linkPathWithLocalBin(home string) string {
	path := os.Getenv("PATH")
	localBin := filepath.Join(home, ".local", "bin")
	for _, part := range filepath.SplitList(path) {
		if part == localBin {
			return path
		}
	}
	if path == "" {
		return localBin
	}
	return localBin + string(os.PathListSeparator) + path
}

func writeEnvAssignment(b *strings.Builder, name, value string) {
	b.WriteString(name)
	b.WriteByte('=')
	b.WriteString(shellEnvQuote(value))
	b.WriteByte('\n')
}

func shellEnvQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func linkStartScript(opts linkOptions) string {
	args := []string{
		shellEnvQuote(filepath.Join(opts.Home, ".local", "bin", "codex-bridge")),
		"connect",
		"--hub", shellEnvQuote(opts.HubURL),
	}
	args = append(args, linkProfileConnectArgs(opts.Profile)...)
	args = append(args,
		"--cwd", `"$CB_CWD"`,
		"--name", `"$CB_NAME"`,
		"--machine-id-file", shellEnvQuote(opts.MIDPath),
	)
	if opts.MachineID != "" {
		args = append(args, "--machine-id", shellEnvQuote(opts.MachineID))
	}
	args = append(args, shellEnvQuote(opts.Token))

	return strings.Join([]string{
		"#!/bin/sh",
		"set -eu",
		fmt.Sprintf("CB_ENV=%s", shellEnvQuote(opts.EnvPath)),
		`if [ -f "$CB_ENV" ]; then set -a; . "$CB_ENV"; set +a; fi`,
		fmt.Sprintf("CB_CWD=$(cat %s)", shellEnvQuote(opts.CWDPath)),
		fmt.Sprintf("CB_NAME=$(cat %s)", shellEnvQuote(opts.NamePath)),
		`cd "$CB_CWD"`,
		"exec " + strings.Join(args, " "),
		"",
	}, "\n")
}

func linkProfileConnectArgs(profile string) []string {
	if profile == linkProfileAutoExecute {
		return []string{"--runner", "codex", "--sandbox", "danger-full-access", "--approval-policy", "never"}
	}
	return []string{"--runner", "codex-app-server", "--sandbox", "workspace-write", "--approval-policy", "untrusted"}
}

func startLinkedBridge(ctx context.Context, opts linkOptions) error {
	if systemdUserAvailable(ctx) {
		started, err := startSystemdBridge(ctx, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "codex-bridge user service failed; falling back to nohup: %v\n", err)
		} else if started {
			return nil
		}
	}
	return startNohupBridge(ctx, opts)
}

func systemdUserAvailable(ctx context.Context) bool {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return exec.CommandContext(checkCtx, "systemctl", "--user", "show-environment").Run() == nil
}

func startSystemdBridge(ctx context.Context, opts linkOptions) (bool, error) {
	serviceDir := filepath.Join(opts.Home, ".config", "systemd", "user")
	if err := os.MkdirAll(serviceDir, 0o700); err != nil {
		return false, err
	}
	unitPath := filepath.Join(serviceDir, opts.Service)
	if err := os.WriteFile(unitPath, []byte(linkSystemdUnit(opts)), 0o600); err != nil {
		return false, err
	}
	_ = runQuietCommand(ctx, "", "systemctl", "--user", "stop", opts.Service)
	for _, args := range [][]string{
		{"--user", "daemon-reload"},
		{"--user", "enable", opts.Service},
		{"--user", "restart", opts.Service},
	} {
		if err := runLoggedCommand(ctx, "", "systemctl", args...); err != nil {
			return false, err
		}
	}
	time.Sleep(2 * time.Second)
	if err := runQuietCommand(ctx, "", "systemctl", "--user", "is-active", "--quiet", opts.Service); err != nil {
		_ = runLoggedCommand(ctx, "", "systemctl", "--user", "status", opts.Service, "--no-pager")
		if _, lookErr := exec.LookPath("journalctl"); lookErr == nil {
			_ = runLoggedCommand(ctx, "", "journalctl", "--user", "-u", opts.Service, "-n", "30", "--no-pager")
		}
		_ = runQuietCommand(ctx, "", "systemctl", "--user", "stop", opts.Service)
		return false, err
	}
	if _, err := exec.LookPath("loginctl"); err == nil {
		_ = runQuietCommand(ctx, "", "loginctl", "enable-linger", currentUsername())
	}
	confirmed := waitForBridgeConnected(opts.LogPath, 10*time.Second)
	if confirmed {
		fmt.Printf("codex-bridge connected: %s log=%s\n", opts.Service, opts.LogPath)
	} else {
		fmt.Fprintf(os.Stderr, "codex-bridge service started but Hub connection is not confirmed. log=%s\n", opts.LogPath)
		printTail(opts.LogPath, 40)
	}
	return true, nil
}

func linkSystemdUnit(opts linkOptions) string {
	return fmt.Sprintf(`[Unit]
Description=Codex Bridge endpoint for %s
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=%s
Environment=HOME=%s
ExecStart=%s
Restart=always
RestartSec=5
OOMPolicy=continue
StandardOutput=append:%s
StandardError=append:%s

[Install]
WantedBy=default.target
`, opts.CWD, systemdEscape(opts.CWD), systemdEscape(opts.Home), systemdEscape(opts.StartPath), systemdEscape(opts.LogPath), systemdEscape(opts.LogPath))
}

func systemdEscape(value string) string {
	return strings.ReplaceAll(value, "%", "%%")
}

func startNohupBridge(ctx context.Context, opts linkOptions) error {
	cmd := exec.CommandContext(ctx, "nohup", opts.StartPath)
	cmd.Dir = opts.CWD
	logFile, err := os.OpenFile(opts.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start background bridge: %w", err)
	}
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("release background bridge process: %w", err)
	}
	confirmed := waitForBridgeConnected(opts.LogPath, 10*time.Second)
	if confirmed {
		fmt.Printf("codex-bridge connected in background: pid=%d log=%s\n", cmd.Process.Pid, opts.LogPath)
	} else {
		fmt.Fprintf(os.Stderr, "codex-bridge started in background but Hub connection is not confirmed: pid=%d log=%s\n", cmd.Process.Pid, opts.LogPath)
		printTail(opts.LogPath, 40)
	}
	return nil
}

func waitForBridgeConnected(logPath string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		data, _ := os.ReadFile(logPath)
		if bytes.Contains(data, []byte("[bridge] connected")) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Second)
	}
}

func printTail(path string, maxLines int) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	for _, line := range lines {
		fmt.Fprintln(os.Stderr, line)
	}
}

func currentUsername() string {
	if user := os.Getenv("USER"); strings.TrimSpace(user) != "" {
		return user
	}
	if user := os.Getenv("LOGNAME"); strings.TrimSpace(user) != "" {
		return user
	}
	return ""
}

func runLoggedCommand(ctx context.Context, dir, name string, args ...string) error {
	return runLoggedCommandEnv(ctx, dir, os.Environ(), name, args...)
}

func runLoggedCommandEnv(ctx context.Context, dir string, env []string, name string, args ...string) error {
	cmdCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runQuietCommand(ctx context.Context, dir, name string, args ...string) error {
	cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, name, args...)
	cmd.Dir = dir
	return cmd.Run()
}
