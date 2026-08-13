package bridge

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tencent/codex-bridge/internal/config"
)

var strictCodexSeedEntries = []string{"config.toml", "auth.json", "skills", "rules"}
var strictClaudeSeedEntries = []string{"settings.json", ".credentials.json", "skills", "commands", "agents", "plugins"}

func strictWorkspaceRuntimeBase() string {
	if value := strings.TrimSpace(os.Getenv("CODEX_BRIDGE_RUNTIME_DIR")); value != "" {
		return expandHome(value)
	}
	if value := strings.TrimSpace(os.Getenv("XDG_RUNTIME_DIR")); value != "" {
		return filepath.Join(value, "codex-bridge")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex-bridge", "runtime")
}

func strictWorkspaceID(workspace string) string {
	sum := sha256.Sum256([]byte(workspace))
	return hex.EncodeToString(sum[:])[:16]
}

func strictWorkspaceReadOnlyPaths(cfg *config.Config, target string) []string {
	paths := []string{
		"/bin", "/sbin", "/usr/bin", "/usr/sbin", "/usr/lib", "/usr/lib64",
		"/usr/include", "/usr/libexec", "/usr/local/bin", "/usr/local/sbin", "/usr/local/include", "/usr/local/lib", "/usr/local/lib64", "/usr/local/share",
		"/lib", "/lib64", "/nix/store", "/gnu/store", "/snap", "/var/lib/snapd",
		"/etc/alternatives", "/etc/ssl", "/etc/pki",
		"/etc/ca-certificates", "/etc/ld.so.cache", "/etc/ld.so.conf", "/etc/ld.so.conf.d",
		"/usr/share", "/etc/resolv.conf", "/etc/hosts", "/etc/nsswitch.conf", "/etc/passwd", "/etc/group",
	}
	paths = append(paths, cfg.Bridge.StrictWorkspaceReadOnly...)
	paths = append(paths, resolvedExecutableRoots(target)...)
	for _, name := range []string{
		"codex", "claude",
		"bash", "sh", "node", "python", "python3", "java", "perl", "ruby",
		"git", "make", "cmake", "ninja", "gcc", "g++", "clang", "clang++", "go", "rustc", "cargo",
		"coqc", "coqtop", "dune", "opam", "isabelle", "lean", "lake",
	} {
		if path, err := exec.LookPath(name); err == nil {
			paths = append(paths, resolvedExecutableRoots(path)...)
			paths = append(paths, discoverManagedInstallRoot(path), discoverOPAMRoot(path))
			if name == "isabelle" {
				paths = append(paths, discoverIsabelleRoot(path))
			}
		}
	}
	return existingUniquePaths(paths)
}

func discoverOPAMRoot(path string) string {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		real = path
	}
	parts := strings.Split(filepath.Clean(real), string(filepath.Separator))
	for index, part := range parts {
		if part != ".opam" || index+2 >= len(parts) || parts[index+2] != "bin" {
			continue
		}
		root := string(filepath.Separator) + filepath.Join(parts[1:index+2]...)
		if _, err := os.Stat(filepath.Join(root, ".opam-switch")); err == nil {
			return root
		}
	}
	return ""
}

func resolvedExecutableRoots(path string) []string {
	if resolved, err := exec.LookPath(path); err == nil {
		path = resolved
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		real = abs
	}
	return []string{real, discoverManagedInstallRoot(abs), discoverManagedInstallRoot(real)}
}

// discoverManagedInstallRoot recognizes the version-scoped layouts used by
// common user-level runtime managers. It deliberately returns one selected
// version, never the manager root or the user's home directory.
func discoverManagedInstallRoot(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	clean := filepath.Clean(abs)
	parts := strings.Split(strings.TrimPrefix(clean, string(filepath.Separator)), string(filepath.Separator))
	type layout struct {
		marker []string
		tail   int
	}
	layouts := []layout{
		{marker: []string{".nvm", "versions", "node"}, tail: 1},
		{marker: []string{".volta", "tools", "image"}, tail: 2},
		{marker: []string{".fnm", "node-versions"}, tail: 2},
		{marker: []string{".local", "share", "fnm", "node-versions"}, tail: 2},
		{marker: []string{".pyenv", "versions"}, tail: 1},
		{marker: []string{".conda", "envs"}, tail: 1},
		{marker: []string{"miniconda3"}, tail: 0},
		{marker: []string{"anaconda3"}, tail: 0},
		{marker: []string{".rbenv", "versions"}, tail: 1},
		{marker: []string{".sdkman", "candidates"}, tail: 2},
		{marker: []string{".asdf", "installs"}, tail: 2},
		{marker: []string{".local", "share", "mise", "installs"}, tail: 2},
		{marker: []string{".local", "share", "claude", "versions"}, tail: 1},
		{marker: []string{".codex", "packages", "standalone", "releases"}, tail: 1},
		{marker: []string{".local", "lib", "node_modules"}, tail: 1},
		{marker: []string{".rustup", "toolchains"}, tail: 1},
		{marker: []string{".elan", "toolchains"}, tail: 1},
		{marker: []string{".linuxbrew", "Cellar"}, tail: 2},
		{marker: []string{"mise", "installs"}, tail: 2},
	}
	for _, candidate := range layouts {
		for index := 0; index+len(candidate.marker)+candidate.tail <= len(parts); index++ {
			matched := true
			for offset, marker := range candidate.marker {
				if parts[index+offset] != marker {
					matched = false
					break
				}
			}
			if matched {
				end := index + len(candidate.marker) + candidate.tail
				return string(filepath.Separator) + filepath.Join(parts[:end]...)
			}
		}
	}
	for index, part := range parts {
		if part != "node_modules" || index+1 >= len(parts) {
			continue
		}
		tail := 1
		if strings.HasPrefix(parts[index+1], "@") && index+2 < len(parts) {
			tail = 2
		}
		return string(filepath.Separator) + filepath.Join(parts[:index+1+tail]...)
	}
	if len(parts) >= 3 && parts[0] == "opt" {
		// /opt/<package> is the conventional self-contained installation root.
		return string(filepath.Separator) + filepath.Join(parts[:2]...)
	}
	return ""
}

func discoverIsabelleRoot(path string) string {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		real = path
	}
	dir := filepath.Dir(real)
	for i := 0; i < 6 && dir != filepath.Dir(dir); i++ {
		if info, err := os.Stat(filepath.Join(dir, "etc", "settings")); err == nil && !info.IsDir() {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	return ""
}

func strictWorkspaceDevicePaths() []string {
	return existingUniquePaths([]string{"/dev/null", "/dev/zero", "/dev/random", "/dev/urandom", "/dev/tty"})
}

func prepareStrictWorkspaceHome(runtimeDir string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home for strict workspace: %w", err)
	}
	sandboxHome := filepath.Join(runtimeDir, "home")
	for _, path := range []string{
		sandboxHome,
		filepath.Join(runtimeDir, "tmp"),
		filepath.Join(sandboxHome, ".codex"),
		filepath.Join(sandboxHome, ".claude"),
		filepath.Join(sandboxHome, ".config"),
		filepath.Join(sandboxHome, ".local", "share"),
		filepath.Join(sandboxHome, ".local", "state"),
		filepath.Join(sandboxHome, ".cache"),
	} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return "", fmt.Errorf("create strict workspace state %s: %w", path, err)
		}
	}
	codexSource := firstNonEmptyEnv("CODEX_HOME", "CODEX_CONFIG_HOME")
	if codexSource == "" {
		codexSource = filepath.Join(home, ".codex")
	}
	claudeSource := firstNonEmptyEnv("CLAUDE_CONFIG_DIR", "CLAUDE_HOME")
	if claudeSource == "" {
		claudeSource = filepath.Join(home, ".claude")
	}
	if err := seedStrictCLIState(codexSource, filepath.Join(sandboxHome, ".codex"), strictCodexSeedEntries); err != nil {
		return "", err
	}
	if err := seedStrictCLIState(claudeSource, filepath.Join(sandboxHome, ".claude"), strictClaudeSeedEntries); err != nil {
		return "", err
	}
	for _, name := range []string{".gitconfig"} {
		if err := syncStrictConfigFile(filepath.Join(home, name), filepath.Join(sandboxHome, name)); err != nil {
			return "", err
		}
	}
	return sandboxHome, nil
}

func firstNonEmptyEnv(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return expandHome(value)
		}
	}
	return ""
}

func seedStrictCLIState(source, destination string, entries []string) error {
	for _, name := range entries {
		src := filepath.Join(source, name)
		dst := filepath.Join(destination, name)
		info, err := os.Stat(src)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("inspect strict workspace CLI state %s: %w", src, err)
		}
		if info.IsDir() {
			if _, err := os.Stat(dst); os.IsNotExist(err) {
				if err := copyStrictStateTree(src, dst); err != nil {
					return err
				}
			}
			continue
		}
		if err := syncStrictConfigFile(src, dst); err != nil {
			return err
		}
	}
	return nil
}

func syncStrictConfigFile(source, destination string) error {
	info, err := os.Stat(source)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	return os.WriteFile(destination, raw, info.Mode().Perm()&0o700)
}

func copyStrictStateTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return err
		}
		return syncStrictConfigFile(path, target)
	})
}

func existingUniquePaths(paths []string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = expandHome(strings.TrimSpace(path))
		if path == "" {
			continue
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			continue
		}
		if real, err := filepath.EvalSymlinks(abs); err == nil {
			abs = real
		}
		if _, err := os.Stat(abs); err != nil {
			continue
		}
		abs = filepath.Clean(abs)
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		out = append(out, abs)
	}
	return out
}
