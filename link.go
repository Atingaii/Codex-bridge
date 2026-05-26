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
	"runtime"
	"strings"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
)

const (
	linkProfileReviewRequired = "review-required"
	linkProfileAutoExecute    = "auto-execute"
	linkCCBRepoURL            = "https://github.com/SeemSeam/claude_codex_bridge.git"
	linkCCBArchiveURL         = "https://github.com/SeemSeam/claude_codex_bridge/archive/refs/heads/main.tar.gz"
	linkCCBInstallDirName     = "claude_codex_bridge"
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
	CCBPath    string

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
	ccbPath, err := lookPathWithLocalBin("ccb")
	if err != nil {
		fmt.Println("ccb not found; installing claude_codex_bridge...")
		if installErr := installCCB(context.Background(), opts.Home); installErr != nil {
			return installErr
		}
		ccbPath, err = lookPathWithLocalBin("ccb")
		if err != nil {
			return errors.New("ccb install finished but ccb was not found; ensure ~/.local/bin is on PATH and rerun link")
		}
	}
	opts.CodexPath = codexPath
	opts.ClaudePath = claudePath
	opts.CCBPath = ccbPath
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

func installCCB(ctx context.Context, home string) error {
	if runtime.GOOS == "windows" {
		return errors.New("automatic ccb install is only supported from Unix-like shells; install CCB manually and rerun link")
	}
	srcDir := filepath.Join(home, ".local", "share", linkCCBInstallDirName)
	if err := os.MkdirAll(filepath.Dir(srcDir), 0o755); err != nil {
		return fmt.Errorf("create ccb install parent: %w", err)
	}
	gitPath, gitErr := exec.LookPath("git")
	switch {
	case gitErr == nil:
		if _, err := os.Stat(filepath.Join(srcDir, ".git")); err == nil {
			if err := runLoggedCommand(ctx, srcDir, gitPath, "pull", "--ff-only"); err != nil {
				return fmt.Errorf("update ccb source: %w", err)
			}
		} else if _, err := os.Stat(filepath.Join(srcDir, "install.sh")); err == nil {
			fmt.Printf("using existing ccb source directory: %s\n", srcDir)
		} else {
			if _, err := os.Stat(srcDir); err == nil {
				return fmt.Errorf("%s already exists but is not a CCB checkout; move it aside or install ccb manually", srcDir)
			}
			if err := runLoggedCommand(ctx, "", gitPath, "clone", "--depth", "1", linkCCBRepoURL, srcDir); err != nil {
				return fmt.Errorf("clone ccb source: %w", err)
			}
		}
	default:
		if _, err := os.Stat(filepath.Join(srcDir, "install.sh")); err == nil {
			fmt.Printf("git not found; using existing ccb source directory: %s\n", srcDir)
		} else if err := downloadCCBArchive(ctx, srcDir); err != nil {
			return err
		}
	}
	env := append(os.Environ(),
		"CODEX_BIN_DIR="+filepath.Join(home, ".local", "bin"),
		"CODEX_INSTALL_PREFIX="+filepath.Join(home, ".local", "share", "codex-dual"),
		"CCB_INSTALL_ASSUME_YES=1",
		"CCB_DROID_AUTOINSTALL=0",
	)
	if os.Geteuid() == 0 {
		return installCCBRootWrapper(srcDir, filepath.Join(home, ".local", "bin"))
	}
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		return errors.New("bash is required to run CCB's installer; install bash or install CCB manually, then rerun link")
	}
	if err := runLoggedCommandEnv(ctx, srcDir, env, bashPath, "./install.sh", "install"); err != nil {
		return fmt.Errorf("install ccb: %w", err)
	}
	return nil
}

func installCCBRootWrapper(srcDir, binDir string) error {
	ccbSource := filepath.Join(srcDir, "ccb")
	if st, err := os.Stat(ccbSource); err != nil || st.IsDir() {
		if err != nil {
			return fmt.Errorf("install root ccb wrapper: %w", err)
		}
		return fmt.Errorf("install root ccb wrapper: %s is a directory", ccbSource)
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("create ccb bin dir: %w", err)
	}
	if err := os.Chmod(ccbSource, 0o755); err != nil {
		return fmt.Errorf("make ccb executable: %w", err)
	}
	target := filepath.Join(binDir, "ccb")
	if st, err := os.Lstat(target); err == nil {
		if st.IsDir() {
			return fmt.Errorf("%s exists as a directory; remove it or install ccb manually", target)
		}
		if err := os.Remove(target); err != nil {
			return fmt.Errorf("replace existing ccb entrypoint: %w", err)
		}
	}
	if err := os.Symlink(ccbSource, target); err == nil {
		fmt.Printf("installed root ccb wrapper: %s -> %s\n", target, ccbSource)
		return nil
	}
	wrapper := "#!/bin/sh\nexec " + shellEnvQuote(ccbSource) + " \"$@\"\n"
	if err := os.WriteFile(target, []byte(wrapper), 0o755); err != nil {
		return fmt.Errorf("write ccb wrapper: %w", err)
	}
	fmt.Printf("installed root ccb wrapper: %s\n", target)
	return nil
}

func downloadCCBArchive(ctx context.Context, srcDir string) error {
	tarPath, cleanup, err := fetchCCBArchive(ctx)
	if err != nil {
		return err
	}
	defer cleanup()

	tmpDir, err := os.MkdirTemp(filepath.Dir(srcDir), "ccb-src-*")
	if err != nil {
		return fmt.Errorf("create ccb temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := runLoggedCommand(ctx, "", "tar", "-xzf", tarPath, "-C", tmpDir, "--strip-components", "1"); err != nil {
		return fmt.Errorf("extract ccb archive: %w", err)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "install.sh")); err != nil {
		return fmt.Errorf("downloaded ccb archive is missing install.sh")
	}
	if _, err := os.Stat(srcDir); err == nil {
		return fmt.Errorf("%s already exists but is not a CCB checkout; move it aside or install ccb manually", srcDir)
	}
	if err := os.Rename(tmpDir, srcDir); err != nil {
		return fmt.Errorf("install ccb source archive: %w", err)
	}
	return nil
}

func fetchCCBArchive(ctx context.Context) (string, func(), error) {
	tmp, err := os.CreateTemp("", "ccb-main-*.tar.gz")
	if err != nil {
		return "", func() {}, fmt.Errorf("create ccb archive temp file: %w", err)
	}
	path := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(path)
		return "", func() {}, err
	}
	cleanup := func() { _ = os.Remove(path) }
	if curl, err := exec.LookPath("curl"); err == nil {
		if err := runLoggedCommand(ctx, "", curl, "-fL", "--retry", "3", "-o", path, linkCCBArchiveURL); err != nil {
			cleanup()
			return "", func() {}, fmt.Errorf("download ccb archive with curl: %w", err)
		}
		return path, cleanup, nil
	}
	if wget, err := exec.LookPath("wget"); err == nil {
		if err := runLoggedCommand(ctx, "", wget, "-O", path, linkCCBArchiveURL); err != nil {
			cleanup()
			return "", func() {}, fmt.Errorf("download ccb archive with wget: %w", err)
		}
		return path, cleanup, nil
	}
	cleanup()
	return "", func() {}, errors.New("git, curl, or wget is required to install ccb automatically; install one of them or install CCB manually, then rerun link")
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
	writeEnvAssignment(&b, "PATH", linkPathWithLocalBin(opts.Home))
	writeEnvAssignment(&b, "BRIDGE_CODEX_PATH", opts.CodexPath)
	writeEnvAssignment(&b, "BRIDGE_CLAUDE_PATH", opts.ClaudePath)
	writeEnvAssignment(&b, "BRIDGE_CCB_PATH", opts.CCBPath)
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"} {
		if value := os.Getenv(name); value != "" {
			writeEnvAssignment(&b, name, value)
		}
	}
	return b.String()
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
		"exec " + strings.Join(args, " "),
		"",
	}, "\n")
}

func linkProfileConnectArgs(profile string) []string {
	if profile == linkProfileAutoExecute {
		return []string{"--runner", "codex", "--orchestration-runner", "ccb", "--sandbox", "danger-full-access", "--approval-policy", "never"}
	}
	return []string{"--runner", "codex-app-server", "--orchestration-runner", "ccb", "--sandbox", "workspace-write", "--approval-policy", "untrusted"}
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
ExecStart=%s
Restart=always
RestartSec=5
StandardOutput=append:%s
StandardError=append:%s

[Install]
WantedBy=default.target
`, opts.CWD, opts.StartPath, opts.LogPath, opts.LogPath)
}

func startNohupBridge(ctx context.Context, opts linkOptions) error {
	cmd := exec.CommandContext(ctx, "nohup", opts.StartPath)
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
