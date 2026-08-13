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
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("CODEX_BRIDGE_RUNTIME_DIR", filepath.Join(root, "runtime"))
	for _, name := range []string{"HOME", "CODEX_BRIDGE_RUNTIME_DIR"} {
		if err := os.MkdirAll(os.Getenv(name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
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

func TestStrictWorkspaceCommandPreservesRealHomeAndXDGConfig(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	workspace := filepath.Join(root, "workspace")
	runtimeDir := filepath.Join(root, "runtime")
	for _, path := range []string{home, workspace, runtimeDir} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config-custom"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".data-custom"))
	t.Setenv("CODEX_BRIDGE_RUNTIME_DIR", runtimeDir)
	cfg := config.Default()
	cfg.Bridge.CWD = workspace
	cfg.Bridge.StrictWorkspace = true
	cmd := exec.Command("/bin/true")
	cmd.Dir = workspace
	if err := configureStrictWorkspaceCommand(cmd, &cfg, workspace); err != nil {
		t.Fatal(err)
	}
	env := "\n" + strings.Join(cmd.Env, "\n") + "\n"
	for _, want := range []string{"HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config-custom"), "XDG_DATA_HOME=" + filepath.Join(home, ".data-custom")} {
		if !strings.Contains(env, "\n"+want+"\n") {
			t.Fatalf("strict command did not preserve %s: %#v", want, cmd.Env)
		}
	}
	for _, private := range []string{"CODEX_HOME=", "CLAUDE_CONFIG_DIR=", "TMPDIR="} {
		if !strings.Contains(env, "\n"+private) {
			t.Fatalf("strict command did not isolate %s: %#v", private, cmd.Env)
		}
	}
}

func TestStrictWorkspaceHiddenHomePathsSelectTopLevelHiddenEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, path := range []string{
		filepath.Join(home, ".provider-switcher"),
		filepath.Join(home, ".ssh"),
		filepath.Join(home, ".git"),
		filepath.Join(home, "projects"),
	} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, ".toolrc"), []byte("config"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := strictWorkspaceHiddenHomePaths()
	for _, want := range []string{
		filepath.Join(home, ".provider-switcher"),
		filepath.Join(home, ".ssh"),
		filepath.Join(home, ".git"),
		filepath.Join(home, ".toolrc"),
	} {
		if !containsString(got, want) {
			t.Errorf("hidden home paths %#v do not contain %s", got, want)
		}
	}
	for _, disallowed := range []string{filepath.Join(home, "projects")} {
		if containsString(got, disallowed) {
			t.Errorf("hidden home paths unexpectedly contain %s: %#v", disallowed, got)
		}
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

func TestLandlockStrictWorkspaceReadsHiddenHomeEntriesReadOnly(t *testing.T) {
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
	normalProject := filepath.Join(home, "other-project")
	for _, path := range []string{workspace, filepath.Join(home, ".codex"), switcherDir, normalProject} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	instructions := filepath.Join(switcherDir, "instructions.md")
	siblingSecret := filepath.Join(switcherDir, "credentials.json")
	normalProjectFile := filepath.Join(normalProject, "private.txt")
	if err := os.WriteFile(instructions, []byte("localized-ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(siblingSecret, []byte("must-not-be-readable"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(normalProjectFile, []byte("outside-project"), 0o600); err != nil {
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
		"; test \"$(cat " + shellTestQuote(instructions) + ")\" = localized-ok" +
		"; test \"$(cat " + shellTestQuote(siblingSecret) + ")\" = must-not-be-readable" +
		"; ! printf changed > " + shellTestQuote(siblingSecret) +
		"; test \"$(cat " + shellTestQuote(siblingSecret) + ")\" = must-not-be-readable" +
		"; ! cat " + shellTestQuote(normalProjectFile)
	args := []string{"-test.run=TestStrictWorkspaceSandboxHelper", "--", "--workspace", workspace, "--runtime", runtimeDir,
		"--read-only", "/bin", "--read-only", "/usr/bin", "--read-only", "/usr/lib", "--read-only", "/lib",
		"--read-only", switcherDir,
		"--state", "/dev/null", "--", "/bin/sh", "-c", script}
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), "CODEX_BRIDGE_STRICT_HELPER=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("strict localized configuration helper failed: %v\n%s", err, output)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestStrictWorkspaceReadOnlyPathsIncludeBroadSystemRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := config.Default()
	paths := strictWorkspaceReadOnlyPaths(&cfg, "/bin/sh")
	for _, want := range []string{"/dev", "/etc", "/opt", "/proc", "/run", "/sys", "/usr", "/var"} {
		if _, err := os.Stat(want); err == nil && !containsString(paths, want) {
			t.Errorf("strict read-only paths do not contain system root %s: %#v", want, paths)
		}
	}
}

func TestStrictWorkspaceNonHomeRootPathsExcludeHomeContainerAndSharedTmp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	paths := strictWorkspaceNonHomeRootPaths()
	for _, denied := range []string{"/tmp", "/home", "/root"} {
		if containsString(paths, denied) {
			t.Fatalf("non-home root paths unexpectedly expose private root %s: %#v", denied, paths)
		}
	}
	if containsString(paths, filepath.Dir(home)) || containsString(paths, home) {
		t.Fatalf("non-home root paths unexpectedly expose home container: %#v", paths)
	}
	if _, err := os.Stat("/usr"); err == nil && !containsString(paths, "/usr") {
		t.Fatalf("non-home root paths do not contain /usr: %#v", paths)
	}
}

func TestStrictWorkspaceNonHomeRootPathsDoNotScanMultiUserContainers(t *testing.T) {
	for _, home := range []string{"/home/users/endpoint", "/root/nested", "/tmp/runtime-home/endpoint"} {
		t.Run(home, func(t *testing.T) {
			t.Setenv("HOME", home)
			for _, path := range strictWorkspaceNonHomeRootPaths() {
				if strictPathWithin("/home", path) || strictPathWithin("/root", path) || strictPathWithin("/tmp", path) {
					t.Fatalf("home %s unexpectedly exposed private container path %s", home, path)
				}
			}
		})
	}
}

func TestStrictWorkspaceExecutableSearchPathsAllowOnlyConfiguredBins(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	toolBin := filepath.Join(home, "public-tools", "bin")
	privateDir := filepath.Join(home, "private-notes")
	for _, path := range []string{toolBin, privateDir} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", toolBin+string(os.PathListSeparator)+"relative-bin")
	paths := strictWorkspaceExecutableSearchPaths()
	if !containsString(paths, toolBin) {
		t.Fatalf("executable search paths %#v do not contain %s", paths, toolBin)
	}
	if !containsString(paths, filepath.Dir(toolBin)) {
		t.Fatalf("executable search paths %#v do not contain PATH component root %s", paths, filepath.Dir(toolBin))
	}
	if containsString(paths, home) || containsString(paths, privateDir) {
		t.Fatalf("executable search paths widened outside configured bin: %#v", paths)
	}
}

func TestDiscoverHomePathComponentRootRequiresExplicitBin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	component := filepath.Join(home, "component", "bin")
	if got := discoverHomePathComponentRoot(component); got != filepath.Dir(component) {
		t.Fatalf("component root = %q, want %q", got, filepath.Dir(component))
	}
	for _, path := range []string{filepath.Join(home, "private"), filepath.Join(home, "bin"), "/usr/bin"} {
		if got := discoverHomePathComponentRoot(path); got != "" {
			t.Errorf("component root for %q = %q, want empty", path, got)
		}
	}
}

func TestStrictWorkspaceExecutableSearchPathsResolveExportedSymlinks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	exportedBin := filepath.Join(home, ".local", "bin")
	componentBin := filepath.Join(home, "shared-component", "bin")
	privateDir := filepath.Join(home, "private-project")
	for _, path := range []string{exportedBin, componentBin, privateDir} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(componentBin, "tool"), []byte("tool"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(componentBin, "tool"), filepath.Join(exportedBin, "tool")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", exportedBin)
	paths := strictWorkspaceExecutableSearchPaths()
	if !containsString(paths, filepath.Dir(componentBin)) {
		t.Fatalf("PATH symlink components %#v do not contain %s", paths, filepath.Dir(componentBin))
	}
	if !containsString(paths, filepath.Join(componentBin, "tool")) {
		t.Fatalf("PATH symlink targets %#v do not contain tool target", paths)
	}
	if containsString(paths, privateDir) || containsString(paths, home) {
		t.Fatalf("PATH symlink components widened to unrelated directory: %#v", paths)
	}
}

func TestStrictWorkspacePATHSymlinkAllowsOnlyOrdinaryTargetFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	exportedBin := filepath.Join(home, ".local", "bin")
	privateProject := filepath.Join(home, "private-project")
	for _, path := range []string{exportedBin, privateProject} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(privateProject, "explicit-tool")
	if err := os.WriteFile(target, []byte("tool"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(exportedBin, "explicit-tool")); err != nil {
		t.Fatal(err)
	}

	paths := strictWorkspacePATHSymlinkComponents(exportedBin)
	if !containsString(paths, target) {
		t.Fatalf("PATH symlink paths %#v do not contain explicit target %s", paths, target)
	}
	if containsString(paths, privateProject) {
		t.Fatalf("PATH symlink unexpectedly exposed target parent %s: %#v", privateProject, paths)
	}
}

func TestStrictWorkspaceReadOnlyPathsDoNotGuessOrdinaryToolNames(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin")
	for _, name := range []string{"CoqPlatform", "Isabelle2025", "miniconda3", "private-project"} {
		if err := os.MkdirAll(filepath.Join(home, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	paths := strictWorkspaceReadOnlyPaths(&cfg, "/bin/sh")
	for _, name := range []string{"CoqPlatform", "Isabelle2025", "miniconda3", "private-project"} {
		if containsString(paths, filepath.Join(home, name)) {
			t.Fatalf("strict paths guessed ordinary directory %s: %#v", name, paths)
		}
	}
}

func TestLandlockStrictWorkspaceAllowsSystemRuntimeTrees(t *testing.T) {
	if err := ValidateStrictWorkspaceSupport(); err != nil {
		t.Skip(err)
	}
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	runtimeDir := filepath.Join(root, "runtime")
	for _, path := range []string{workspace, runtimeDir} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := "cat /proc/sys/kernel/overflowuid >/dev/null" +
		"; cat /proc/sys/kernel/overflowgid >/dev/null" +
		"; cat /sys/devices/system/cpu/online >/dev/null" +
		"; test -r /etc/os-release"
	args := []string{"-test.run=TestStrictWorkspaceSandboxHelper", "--", "--workspace", workspace, "--runtime", runtimeDir,
		"--read-only", "/bin", "--read-only", "/dev", "--read-only", "/etc", "--read-only", "/proc",
		"--read-only", "/sys", "--read-only", "/usr", "--read-only", "/lib", "--state", "/dev/null",
		"--", "/bin/sh", "-c", script}
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), "CODEX_BRIDGE_STRICT_HELPER=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("strict system runtime helper failed: %v\n%s", err, output)
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

func TestStrictWorkspaceAllowsPATHComponentWithoutSiblingProject(t *testing.T) {
	if err := ValidateStrictWorkspaceSupport(); err != nil {
		t.Skip(err)
	}
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	runtimeDir := filepath.Join(root, "runtime")
	home := filepath.Join(root, "home", "u")
	t.Setenv("HOME", home)
	otherProject := filepath.Join(home, "other-project")
	managedRoot := filepath.Join(otherProject, "_opam")
	binDir := filepath.Join(managedRoot, "bin")
	privateDir := filepath.Join(otherProject, "src")
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
		"--read-only", discoverHomePathComponentRoot(binDir), "--state", "/dev/null", "--", "/bin/sh", target}
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), "CODEX_BRIDGE_STRICT_HELPER=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("managed runtime command failed: %v\n%s", err, output)
	}
}

func TestConfiguredStrictWorkspaceRunsPATHToolWithoutExposingSiblingProject(t *testing.T) {
	if err := ValidateStrictWorkspaceSupport(); err != nil {
		t.Skip(err)
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	workspace := filepath.Join(root, "workspace")
	runtimeDir := filepath.Join(root, "runtime")
	toolRoot := filepath.Join(home, "shared-tool")
	toolBin := filepath.Join(toolRoot, "bin")
	privateProject := filepath.Join(home, "private-project")
	for _, path := range []string{home, workspace, runtimeDir, toolBin, privateProject} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	toolData := filepath.Join(toolRoot, "runtime.txt")
	privateData := filepath.Join(privateProject, "secret.txt")
	if err := os.WriteFile(toolData, []byte("runtime-ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(privateData, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(toolBin, "first-run-tool")
	toolScript := "#!/bin/sh\ntest \"$(cat " + shellTestQuote(toolData) + ")\" = runtime-ok && ! cat " + shellTestQuote(privateData) + "\n"
	if err := os.WriteFile(tool, []byte(toolScript), 0o700); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", home)
	t.Setenv("PATH", toolBin+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("CODEX_BRIDGE_RUNTIME_DIR", runtimeDir)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CODEX_CONFIG_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CLAUDE_HOME", "")
	cfg := config.Default()
	cfg.Bridge.CWD = workspace
	cfg.Bridge.StrictWorkspace = true
	cmd := exec.Command("/bin/sh", "-c", "first-run-tool")
	cmd.Dir = workspace
	if err := configureStrictWorkspaceCommand(cmd, &cfg, workspace); err != nil {
		t.Fatal(err)
	}
	helperArgs := append([]string{"-test.run=TestStrictWorkspaceSandboxHelper", "--"}, cmd.Args[2:]...)
	helper := exec.Command(cmd.Path, helperArgs...)
	helper.Dir = cmd.Dir
	helper.Env = append(cmd.Env, "CODEX_BRIDGE_STRICT_HELPER=1")
	if output, err := helper.CombinedOutput(); err != nil {
		t.Fatalf("configured strict PATH tool failed: %v\n%s", err, output)
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
