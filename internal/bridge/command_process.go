package bridge

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
)

func configureManagedCommand(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = managedCommandSysProcAttr()
	cmd.WaitDelay = 5 * time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return terminateProcessGroup(cmd.Process.Pid)
	}
}

func configureStrictWorkspaceCommand(cmd *exec.Cmd, cfg *config.Config, cwd string) error {
	if cmd == nil || cfg == nil || !cfg.Bridge.StrictWorkspace {
		return nil
	}
	workspace, err := canonicalDirectory(cfg.Bridge.CWD)
	if err != nil {
		return fmt.Errorf("strict workspace root: %w", err)
	}
	commandCWD := strings.TrimSpace(cwd)
	if commandCWD == "" {
		commandCWD = cmd.Dir
	}
	if commandCWD == "" {
		commandCWD = workspace
	}
	commandCWD, err = canonicalDirectory(commandCWD)
	if err != nil {
		return fmt.Errorf("strict workspace command directory: %w", err)
	}
	if !strictPathWithin(workspace, commandCWD) {
		return fmt.Errorf("strict workspace rejected command directory %q outside bound workspace %q", commandCWD, workspace)
	}
	runtimeDir := filepath.Join(strictWorkspaceRuntimeBase(), strictWorkspaceID(workspace))
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		return fmt.Errorf("create strict workspace runtime: %w", err)
	}
	privateStateHome, err := prepareStrictWorkspaceHome(runtimeDir)
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate Bridge sandbox launcher: %w", err)
	}
	target := cmd.Path
	if target == "" && len(cmd.Args) > 0 {
		target = cmd.Args[0]
	}
	if target == "" {
		return errors.New("strict workspace target command is empty")
	}
	if resolved, resolveErr := exec.LookPath(target); resolveErr == nil {
		target = resolved
	}
	if resolved, resolveErr := filepath.EvalSymlinks(target); resolveErr == nil {
		target = resolved
	}
	args := []string{exe, "sandbox-exec", "--workspace", workspace, "--runtime", runtimeDir}
	for _, path := range strictWorkspaceReadOnlyPaths(cfg, target) {
		args = append(args, "--read-only", path)
	}
	for _, path := range strictWorkspaceDevicePaths() {
		args = append(args, "--state", path)
	}
	args = append(args, "--", target)
	if len(cmd.Args) > 1 {
		args = append(args, cmd.Args[1:]...)
	}
	cmd.Path = exe
	cmd.Args = args
	replaceCommandEnv(cmd,
		"CODEX_HOME="+filepath.Join(privateStateHome, ".codex"),
		"CLAUDE_CONFIG_DIR="+filepath.Join(privateStateHome, ".claude"),
		"XDG_STATE_HOME="+filepath.Join(privateStateHome, ".local", "state"),
		"XDG_CACHE_HOME="+filepath.Join(privateStateHome, ".cache"),
		"TMPDIR="+filepath.Join(runtimeDir, "tmp"), "TMP="+filepath.Join(runtimeDir, "tmp"), "TEMP="+filepath.Join(runtimeDir, "tmp"),
	)
	return nil
}

func canonicalDirectory(path string) (string, error) {
	path = expandHome(strings.TrimSpace(path))
	if path == "" {
		path = "."
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", real)
	}
	return filepath.Clean(real), nil
}

func strictPathWithin(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func appendCommandEnv(cmd *exec.Cmd, values ...string) {
	if cmd == nil {
		return
	}
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	cmd.Env = append(cmd.Env, values...)
}

// replaceCommandEnv is intentionally strict-mode-only. Existing runners use
// appendCommandEnv and retain their historical environment ordering.
func replaceCommandEnv(cmd *exec.Cmd, values ...string) {
	if cmd == nil {
		return
	}
	env := cmd.Env
	if env == nil {
		env = os.Environ()
	}
	overrides := make(map[string]string, len(values))
	order := make([]string, 0, len(values))
	for _, value := range values {
		key, _, ok := strings.Cut(value, "=")
		if !ok || key == "" {
			continue
		}
		if _, exists := overrides[key]; !exists {
			order = append(order, key)
		}
		overrides[key] = value
	}
	merged := make([]string, 0, len(env)+len(overrides))
	for _, value := range env {
		key, _, ok := strings.Cut(value, "=")
		if ok {
			if _, replaced := overrides[key]; replaced {
				continue
			}
		}
		merged = append(merged, value)
	}
	for _, key := range order {
		merged = append(merged, overrides[key])
	}
	cmd.Env = merged
}

func terminateProcessGroup(pid int) error {
	if pid <= 0 {
		return nil
	}
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		pgid = pid
	}
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	time.Sleep(250 * time.Millisecond)
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}
