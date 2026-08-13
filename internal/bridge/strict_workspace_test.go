//go:build linux

package bridge

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/tencent/codex-bridge/internal/config"
)

func TestStrictWorkspaceCommandUsesCanonicalBoundRoot(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "workspace-link")
	if err := os.Symlink(workspace, alias); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CWD = alias
	cfg.Bridge.StrictWorkspace = true
	cmd := exec.Command("/bin/true")
	cmd.Dir = workspace
	if err := configureStrictWorkspaceCommand(cmd, &cfg, workspace); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "--workspace "+workspace) || !strings.Contains(joined, "sandbox-exec") {
		t.Fatalf("strict wrapper args = %q", joined)
	}

	outsideCmd := exec.Command("/bin/true")
	outsideCmd.Dir = outside
	if err := configureStrictWorkspaceCommand(outsideCmd, &cfg, outside); err == nil || !strings.Contains(err.Error(), "outside bound workspace") {
		t.Fatalf("outside cwd error = %v", err)
	}
}

func TestStrictWorkspaceCommandDisabledLeavesExistingRunnersUntouched(t *testing.T) {
	cfg := config.Default()
	cfg.Bridge.StrictWorkspace = false
	cmd := exec.Command("/bin/true", "legacy-arg")
	cmd.Dir = "/legacy/workspace"
	cmd.Env = []string{"HOME=/legacy/home", "TMPDIR=/tmp"}
	wantPath := cmd.Path
	wantArgs := append([]string(nil), cmd.Args...)
	wantEnv := append([]string(nil), cmd.Env...)

	if err := configureStrictWorkspaceCommand(cmd, &cfg, "/outside/does-not-need-to-exist"); err != nil {
		t.Fatal(err)
	}
	if cmd.Path != wantPath || !reflect.DeepEqual(cmd.Args, wantArgs) || !reflect.DeepEqual(cmd.Env, wantEnv) || cmd.Dir != "/legacy/workspace" {
		t.Fatalf("disabled strict mode mutated legacy command: path=%q args=%#v env=%#v dir=%q", cmd.Path, cmd.Args, cmd.Env, cmd.Dir)
	}
}

func TestDiscoverManagedInstallRootKeepsVersionBoundary(t *testing.T) {
	tests := map[string]string{
		"/home/u/.nvm/versions/node/v22.5.0/bin/node":                          "/home/u/.nvm/versions/node/v22.5.0",
		"/home/u/.volta/tools/image/node/22.5.0/bin/node":                      "/home/u/.volta/tools/image/node/22.5.0",
		"/home/u/.local/share/fnm/node-versions/v22.5.0/installation/bin/node": "/home/u/.local/share/fnm/node-versions/v22.5.0/installation",
		"/home/u/.pyenv/versions/3.12.4/bin/python":                            "/home/u/.pyenv/versions/3.12.4",
		"/home/u/.conda/envs/proof/bin/python":                                 "/home/u/.conda/envs/proof",
		"/home/u/miniconda3/bin/python":                                        "/home/u/miniconda3",
		"/home/u/.rbenv/versions/3.3.4/bin/ruby":                               "/home/u/.rbenv/versions/3.3.4",
		"/home/u/.sdkman/candidates/java/21.0.3/bin/java":                      "/home/u/.sdkman/candidates/java/21.0.3",
		"/home/u/.asdf/installs/nodejs/22.5.0/bin/node":                        "/home/u/.asdf/installs/nodejs/22.5.0",
		"/home/u/.local/share/mise/installs/node/22.5.0/bin/node":              "/home/u/.local/share/mise/installs/node/22.5.0",
		"/home/u/.local/share/claude/versions/2.1.227/claude":                  "/home/u/.local/share/claude/versions/2.1.227",
		"/home/u/.codex/packages/standalone/releases/0.147/codex":              "/home/u/.codex/packages/standalone/releases/0.147",
		"/home/u/.nvm/lib/node_modules/@anthropic-ai/claude-code/cli.js":       "/home/u/.nvm/lib/node_modules/@anthropic-ai/claude-code",
		"/home/u/.rustup/toolchains/stable-x86_64/bin/rustc":                   "/home/u/.rustup/toolchains/stable-x86_64",
		"/home/u/.elan/toolchains/leanprover--lean4---v4.19.0/bin/lean":        "/home/u/.elan/toolchains/leanprover--lean4---v4.19.0",
		"/home/linuxbrew/.linuxbrew/Cellar/node/22.5.0/bin/node":               "/home/linuxbrew/.linuxbrew/Cellar/node/22.5.0",
		"/opt/isabelle2025/bin/isabelle":                                       "/opt/isabelle2025",
		"/home/u/projects/private/tool":                                        "",
	}
	for input, want := range tests {
		if got := discoverManagedInstallRoot(input); got != want {
			t.Errorf("discoverManagedInstallRoot(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestAppendCommandEnvReplacesSensitiveRoots(t *testing.T) {
	cmd := exec.Command("/bin/true")
	cmd.Env = []string{"HOME=/home/real", "PATH=/bin", "TMPDIR=/tmp/shared", "HOME=/home/stale"}
	replaceCommandEnv(cmd, "HOME=/private/home", "TMPDIR=/private/tmp")
	joined := "\n" + strings.Join(cmd.Env, "\n") + "\n"
	if strings.Count(joined, "\nHOME=") != 1 || !strings.Contains(joined, "\nHOME=/private/home\n") {
		t.Fatalf("HOME was not replaced exactly once: %#v", cmd.Env)
	}
	if strings.Count(joined, "\nTMPDIR=") != 1 || !strings.Contains(joined, "\nTMPDIR=/private/tmp\n") {
		t.Fatalf("TMPDIR was not replaced exactly once: %#v", cmd.Env)
	}
}

func TestPrepareStrictWorkspaceHomeLocalizesCodexExternalFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CODEX_CONFIG_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CLAUDE_HOME", "")
	codexDir := filepath.Join(home, ".codex")
	switcherDir := filepath.Join(home, ".any-provider-switcher")
	bridgeStateDir := filepath.Join(home, ".codex-bridge", "model-config")
	for _, path := range []string{codexDir, switcherDir, bridgeStateDir} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	instructions := filepath.Join(switcherDir, "instructions.md")
	catalog := filepath.Join(bridgeStateDir, "catalog.json")
	if err := os.WriteFile(instructions, []byte("first instructions"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalog, []byte(`{"models":[{"slug":"custom"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	originalConfig := "model = \"custom\"\nmodel_instructions_file = " + tomlTestQuote(instructions) + "\nmodel_catalog_json = " + tomlTestQuote(catalog) + "\n\n[mcp_servers.keep]\ncommand = \"keep\"\n"
	configPath := filepath.Join(codexDir, "config.toml")
	if err := os.WriteFile(configPath, []byte(originalConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	sandboxHome, err := prepareStrictWorkspaceHome(runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	privateConfigPath := filepath.Join(sandboxHome, ".codex", "config.toml")
	privateDocument := readTOMLDocument(t, privateConfigPath)
	privateInstructions := privateDocument["model_instructions_file"].(string)
	privateCatalog := privateDocument["model_catalog_json"].(string)
	for field, privatePath := range map[string]string{
		"model_instructions_file": privateInstructions,
		"model_catalog_json":      privateCatalog,
	} {
		if !strictPathWithin(filepath.Join(sandboxHome, ".codex", "external-config"), privatePath) {
			t.Fatalf("private %s path escaped external-config: %s", field, privatePath)
		}
		if strings.Contains(privatePath, home) {
			t.Fatalf("private %s retained real home path: %s", field, privatePath)
		}
	}
	if got := string(mustReadFile(t, privateInstructions)); got != "first instructions" {
		t.Fatalf("private instructions = %q", got)
	}
	if got := string(mustReadFile(t, privateCatalog)); !strings.Contains(got, `"slug":"custom"`) {
		t.Fatalf("private catalog = %q", got)
	}
	if privateDocument["model"] != "custom" {
		t.Fatalf("private model = %#v", privateDocument["model"])
	}
	if mcp, _ := privateDocument["mcp_servers"].(map[string]any); mcp == nil {
		t.Fatalf("unrelated Codex configuration was lost: %#v", privateDocument)
	}
	if got := string(mustReadFile(t, configPath)); got != originalConfig {
		t.Fatalf("source configuration was modified:\n%s", got)
	}

	if err := os.WriteFile(instructions, []byte("updated instructions"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareStrictWorkspaceHome(runtimeDir); err != nil {
		t.Fatal(err)
	}
	if got := string(mustReadFile(t, privateInstructions)); got != "updated instructions" {
		t.Fatalf("private instructions were not refreshed: %q", got)
	}
}

func TestStrictCodexExternalFilesSupportRelativeProfileReferences(t *testing.T) {
	root := t.TempDir()
	sourceDir := filepath.Join(root, "source")
	privateDir := filepath.Join(root, "private")
	if err := os.MkdirAll(sourceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "profile.md"), []byte("profile instructions"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(sourceDir, "config.toml")
	privateConfig := filepath.Join(privateDir, "config.toml")
	config := "[profiles.switched]\nmodel = \"custom\"\nmodel_instructions_file = \"profile.md\"\n"
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := localizeStrictCodexExternalFiles(configPath, privateConfig); err != nil {
		t.Fatal(err)
	}
	document := readTOMLDocument(t, privateConfig)
	profiles := document["profiles"].(map[string]any)
	profile := profiles["switched"].(map[string]any)
	localized := profile["model_instructions_file"].(string)
	if got := string(mustReadFile(t, localized)); got != "profile instructions" {
		t.Fatalf("localized profile instructions = %q", got)
	}
	if !strictPathWithin(filepath.Join(privateDir, "external-config"), localized) {
		t.Fatalf("localized profile path escaped private config: %s", localized)
	}
}

func TestStrictCodexExternalFilesFailClosed(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "source", "config.toml")
	privateConfig := filepath.Join(root, "private", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}

	t.Run("missing", func(t *testing.T) {
		missing := filepath.Join(root, "missing.md")
		if err := os.WriteFile(configPath, []byte("model_instructions_file = "+tomlTestQuote(missing)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := localizeStrictCodexExternalFiles(configPath, privateConfig)
		if err == nil || !strings.Contains(err.Error(), "model_instructions_file") || !strings.Contains(err.Error(), "no such file") {
			t.Fatalf("missing reference error = %v", err)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		target := filepath.Join(root, "target.md")
		link := filepath.Join(root, "instructions-link.md")
		if err := os.WriteFile(target, []byte("instructions"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(configPath, []byte("model_instructions_file = "+tomlTestQuote(link)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := localizeStrictCodexExternalFiles(configPath, privateConfig)
		if err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Fatalf("symlink reference error = %v", err)
		}
	})
}

func TestLandlockStrictWorkspaceReadsLocalizedCodexFilesOnly(t *testing.T) {
	if err := ValidateStrictWorkspaceSupport(); err != nil {
		t.Skip(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CODEX_CONFIG_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CLAUDE_HOME", "")
	workspace := filepath.Join(t.TempDir(), "workspace")
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	switcherDir := filepath.Join(home, ".provider-switcher")
	for _, path := range []string{workspace, filepath.Join(home, ".codex"), switcherDir} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	instructions := filepath.Join(switcherDir, "instructions.md")
	siblingSecret := filepath.Join(switcherDir, "credentials.json")
	if err := os.WriteFile(instructions, []byte("localized-ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(siblingSecret, []byte("must-not-be-readable"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.WriteFile(configPath, []byte("model_instructions_file = "+tomlTestQuote(instructions)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sandboxHome, err := prepareStrictWorkspaceHome(runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	privateDocument := readTOMLDocument(t, filepath.Join(sandboxHome, ".codex", "config.toml"))
	privateInstructions := privateDocument["model_instructions_file"].(string)

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := "test \"$(cat " + shellTestQuote(privateInstructions) + ")\" = localized-ok" +
		"; ! cat " + shellTestQuote(instructions) +
		"; ! cat " + shellTestQuote(siblingSecret)
	args := []string{"-test.run=TestStrictWorkspaceSandboxHelper", "--", "--workspace", workspace, "--runtime", runtimeDir,
		"--read-only", "/bin", "--read-only", "/usr/bin", "--read-only", "/usr/lib", "--read-only", "/lib",
		"--state", "/dev/null", "--", "/bin/sh", "-c", script}
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), "CODEX_BRIDGE_STRICT_HELPER=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("strict localized configuration helper failed: %v\n%s", err, output)
	}
}

func TestStrictRuntimeIntrospectionPathsAreFilesNotBroadTrees(t *testing.T) {
	for _, path := range strictRuntimeIntrospectionPaths() {
		if path == "/proc" || path == "/proc/self" || path == "/sys" || path == "/sys/kernel" {
			t.Fatalf("strict runtime introspection path is too broad: %s", path)
		}
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.IsDir() {
			t.Fatalf("strict runtime introspection path must be a file: %s", path)
		}
	}
}

func TestLandlockStrictWorkspaceEnforcesReadAndWriteBoundary(t *testing.T) {
	if err := ValidateStrictWorkspaceSupport(); err != nil {
		t.Skip(err)
	}
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	outside := filepath.Join(root, "outside")
	runtimeDir := filepath.Join(root, "runtime")
	for _, path := range []string{workspace, outside, runtimeDir} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	outsideFile := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("must-not-be-readable"), 0o600); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	workspaceFile := filepath.Join(workspace, "ok.txt")
	script := "printf allowed > " + shellTestQuote(workspaceFile) +
		"; test \"$(cat " + shellTestQuote(workspaceFile) + ")\" = allowed" +
		"; ! cat " + shellTestQuote(outsideFile)
	args := []string{"-test.run=TestStrictWorkspaceSandboxHelper", "--", "--workspace", workspace, "--runtime", runtimeDir,
		"--read-only", "/bin", "--read-only", "/usr/bin", "--read-only", "/usr/lib", "--read-only", "/lib",
		"--state", "/dev/null", "--", "/bin/sh", "-c", script}
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), "CODEX_BRIDGE_STRICT_HELPER=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("strict helper failed: %v\n%s", err, output)
	}
	if raw, err := os.ReadFile(workspaceFile); err != nil || string(raw) != "allowed" {
		t.Fatalf("workspace write = %q, %v", raw, err)
	}
}

func TestStrictWorkspaceAllowsDetectedManagedRuntimeOnly(t *testing.T) {
	if err := ValidateStrictWorkspaceSupport(); err != nil {
		t.Skip(err)
	}
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	runtimeDir := filepath.Join(root, "runtime")
	managedRoot := filepath.Join(root, "home", "u", ".volta", "tools", "image", "node", "22.5.0")
	binDir := filepath.Join(managedRoot, "bin")
	privateDir := filepath.Join(root, "home", "u", "private-project")
	for _, path := range []string{workspace, runtimeDir, binDir, privateDir} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	runtimeData := filepath.Join(managedRoot, "runtime.txt")
	privateData := filepath.Join(privateDir, "secret.txt")
	if err := os.WriteFile(runtimeData, []byte("runtime-ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(privateData, []byte("must-not-be-readable"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(binDir, "managed-tool")
	script := "#!/bin/sh\ntest \"$(cat " + shellTestQuote(runtimeData) + ")\" = runtime-ok && ! cat " + shellTestQuote(privateData) + "\n"
	if err := os.WriteFile(target, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"-test.run=TestStrictWorkspaceSandboxHelper", "--", "--workspace", workspace, "--runtime", runtimeDir,
		"--read-only", "/bin", "--read-only", "/usr/bin", "--read-only", "/usr/lib", "--read-only", "/lib",
		"--read-only", discoverManagedInstallRoot(target), "--state", "/dev/null", "--", target}
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), "CODEX_BRIDGE_STRICT_HELPER=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("managed runtime command failed: %v\n%s", err, output)
	}
}

func TestStrictWorkspaceSandboxHelper(t *testing.T) {
	if os.Getenv("CODEX_BRIDGE_STRICT_HELPER") != "1" {
		return
	}
	separator := -1
	for index, arg := range os.Args {
		if arg == "--" {
			separator = index
			break
		}
	}
	if separator < 0 {
		os.Exit(2)
	}
	if err := RunStrictWorkspaceSandbox(os.Args[separator+1:]); err != nil {
		_, _ = os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
}

func shellTestQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func tomlTestQuote(value string) string {
	encoded, _ := toml.Marshal(map[string]string{"value": value})
	return strings.TrimSpace(strings.TrimPrefix(string(encoded), "value = "))
}

func readTOMLDocument(t *testing.T, path string) map[string]any {
	t.Helper()
	var document map[string]any
	if err := toml.Unmarshal(mustReadFile(t, path), &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
