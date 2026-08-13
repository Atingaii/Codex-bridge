package bridge

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/tencent/codex-bridge/internal/config"
)

var strictCodexSeedEntries = []string{"config.toml", "auth.json", "skills", "rules"}
var strictClaudeSeedEntries = []string{"settings.json", ".credentials.json", "skills", "commands", "agents", "plugins"}

var strictCodexExternalFiles = map[string]string{
	"model_instructions_file": "model-instructions.md",
	"model_catalog_json":      "model-catalog.json",
}

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
	paths := strictWorkspaceNonHomeRootPaths()
	paths = append(paths, cfg.Bridge.StrictWorkspaceReadOnly...)
	paths = append(paths, strictWorkspaceHiddenHomePaths()...)
	paths = append(paths, strictWorkspaceToolEnvironmentPaths()...)
	paths = append(paths, strictWorkspaceKnownToolHomePaths()...)
	paths = append(paths, strictWorkspaceExecutableSearchPaths()...)
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

// strictWorkspaceNonHomeRootPaths makes the policy about the endpoint owner's
// home rather than about a hard-coded Linux distribution. Root-level branches
// outside HOME are read-only. If HOME is below a custom prefix such as
// /data/home/user, sibling branches below /data are also read-only, while the
// user-home container itself stays closed.
func strictWorkspaceNonHomeRootPaths() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	home, err = filepath.Abs(home)
	if err != nil {
		return nil
	}
	parts := strings.Split(strings.TrimPrefix(filepath.Clean(home), string(filepath.Separator)), string(filepath.Separator))
	if len(parts) == 0 {
		return nil
	}
	var paths []string
	parent := string(filepath.Separator)
	for index, homePart := range parts {
		entries, readErr := os.ReadDir(parent)
		if readErr != nil {
			break
		}
		for _, entry := range entries {
			reservedHomeRoot := parent == string(filepath.Separator) && (entry.Name() == "home" || entry.Name() == "root")
			if entry.Name() == homePart || reservedHomeRoot || (parent == string(filepath.Separator) && entry.Name() == "tmp") {
				continue
			}
			paths = append(paths, filepath.Join(parent, entry.Name()))
		}
		if index == len(parts)-2 {
			break
		}
		nextParent := filepath.Join(parent, homePart)
		if nextParent == "/home" || nextParent == "/root" || nextParent == "/tmp" {
			break
		}
		parent = nextParent
	}
	return paths
}

func strictWorkspaceExecutableSearchPaths() []string {
	var paths []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		dir = strings.TrimSpace(dir)
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		paths = append(paths, dir, discoverManagedInstallRoot(dir), discoverHomePathComponentRoot(dir))
	}
	return paths
}

// discoverHomePathComponentRoot treats an ordinary home directory as a public
// component only when its bin/sbin directory was explicitly exported in PATH.
func discoverHomePathComponentRoot(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil || !strictPathWithin(home, abs) {
		return ""
	}
	base := filepath.Base(abs)
	if base != "bin" && base != "sbin" {
		return ""
	}
	root := filepath.Dir(abs)
	if root == home {
		return ""
	}
	return root
}

// strictWorkspaceToolEnvironmentPaths covers toolchains installed in ordinary
// home directories when their standard environment variables identify them.
// It never derives a parent directory from an arbitrary path.
func strictWorkspaceToolEnvironmentPaths() []string {
	var paths []string
	for _, name := range []string{
		"GOROOT", "GOPATH", "JAVA_HOME", "JDK_HOME", "M2_HOME", "GRADLE_HOME",
		"CARGO_HOME", "RUSTUP_HOME", "OPAMROOT", "COQPATH", "COQLIB",
		"ISABELLE_HOME", "ISABELLE_HOME_USER", "LEAN_PATH", "ELAN_HOME",
		"NVM_DIR", "VOLTA_HOME", "PYENV_ROOT", "CONDA_PREFIX", "CONDA_ROOT",
		"SDKMAN_DIR", "ASDF_DIR", "MISE_DATA_DIR",
	} {
		for _, value := range filepath.SplitList(os.Getenv(name)) {
			value = strings.TrimSpace(value)
			if value == "" || !filepath.IsAbs(value) {
				continue
			}
			paths = append(paths, value)
		}
	}
	return paths
}

// strictWorkspaceKnownToolHomePaths recognizes conventional user-level
// component roots that use non-hidden names. Only existing top-level entries
// with well-known names are selected; unrelated ordinary directories remain
// outside the Landlock allowlist.
func strictWorkspaceKnownToolHomePaths() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		return nil
	}
	exact := map[string]struct{}{
		"go":        {},
		"miniconda": {}, "miniconda3": {}, "anaconda": {}, "anaconda3": {},
		"linuxbrew": {}, "homebrew": {},
		"coqplatform": {}, "proofgeneral": {},
	}
	prefixes := []string{"isabelle", "coq", "lean"}
	var paths []string
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		name := strings.ToLower(entry.Name())
		_, known := exact[name]
		if !known {
			for _, prefix := range prefixes {
				if strictWorkspaceVersionedToolName(name, prefix) {
					known = true
					break
				}
			}
		}
		if known {
			paths = append(paths, filepath.Join(home, entry.Name()))
		}
	}
	return paths
}

func strictWorkspaceVersionedToolName(name, prefix string) bool {
	if name == prefix {
		return true
	}
	if !strings.HasPrefix(name, prefix) || len(name) == len(prefix) {
		return false
	}
	next := name[len(prefix)]
	return (next >= '0' && next <= '9') || next == '-' || next == '_'
}

// strictWorkspaceHiddenHomePaths keeps user-installed CLI tooling compatible
// without exposing ordinary sibling projects. The endpoint owner explicitly
// opts into this read-only exception by selecting strict-workspace.
func strictWorkspaceHiddenHomePaths() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		return nil
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, ".") || name == "." || name == ".." {
			continue
		}
		paths = append(paths, filepath.Join(home, name))
	}
	return paths
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
	if err := localizeStrictCodexExternalFiles(
		filepath.Join(codexSource, "config.toml"),
		filepath.Join(sandboxHome, ".codex", "config.toml"),
	); err != nil {
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

func localizeStrictCodexExternalFiles(sourceConfig, privateConfig string) error {
	raw, err := os.ReadFile(sourceConfig)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read Codex configuration for strict workspace: %w", err)
	}
	var document map[string]any
	if err := toml.Unmarshal(raw, &document); err != nil {
		return fmt.Errorf("parse Codex configuration for strict workspace: %w", err)
	}

	privateExternalDir := filepath.Join(filepath.Dir(privateConfig), "external-config")
	if err := localizeStrictCodexDocument(document, nil, filepath.Dir(sourceConfig), privateExternalDir); err != nil {
		return err
	}
	if profiles, ok := document["profiles"].(map[string]any); ok {
		for name, value := range profiles {
			profile, ok := value.(map[string]any)
			if !ok {
				continue
			}
			if err := localizeStrictCodexDocument(profile, []string{"profiles", name}, filepath.Dir(sourceConfig), privateExternalDir); err != nil {
				return err
			}
		}
	}

	localized, err := toml.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode private Codex configuration for strict workspace: %w", err)
	}
	if err := atomicWrite(privateConfig, localized, 0o600); err != nil {
		return fmt.Errorf("write private Codex configuration for strict workspace: %w", err)
	}
	return nil
}

func localizeStrictCodexDocument(document map[string]any, keyPath []string, sourceDir, privateDir string) error {
	for key, filename := range strictCodexExternalFiles {
		value, exists := document[key]
		if !exists {
			continue
		}
		currentPath := append(append([]string(nil), keyPath...), key)
		reference, ok := value.(string)
		fieldPath := strings.Join(currentPath, ".")
		if !ok || strings.TrimSpace(reference) == "" {
			return fmt.Errorf("Codex %s must be a non-empty file path in strict workspace mode", fieldPath)
		}
		sourcePath := expandHome(strings.TrimSpace(reference))
		if !filepath.IsAbs(sourcePath) {
			sourcePath = filepath.Join(sourceDir, sourcePath)
		}
		sourcePath = filepath.Clean(sourcePath)
		info, err := os.Lstat(sourcePath)
		if err != nil {
			return fmt.Errorf("read Codex %s source %s for strict workspace: %w", fieldPath, sourcePath, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Codex %s source %s is a symbolic link; strict workspace mode requires a regular file", fieldPath, sourcePath)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("Codex %s source %s is not a regular file", fieldPath, sourcePath)
		}
		contents, err := os.ReadFile(sourcePath)
		if err != nil {
			return fmt.Errorf("read Codex %s source %s for strict workspace: %w", fieldPath, sourcePath, err)
		}
		if len(keyPath) > 0 {
			sum := sha256.Sum256([]byte(fieldPath))
			filename = hex.EncodeToString(sum[:])[:12] + "-" + filename
		}
		privatePath := filepath.Join(privateDir, filename)
		if err := atomicWrite(privatePath, contents, 0o600); err != nil {
			return fmt.Errorf("copy Codex %s into strict workspace: %w", fieldPath, err)
		}
		document[key] = privatePath
	}
	return nil
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
