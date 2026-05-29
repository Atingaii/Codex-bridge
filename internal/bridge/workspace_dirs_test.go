package bridge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tencent/codex-bridge/internal/config"
)

func TestDiscoverWorkingDirsIncludesBaseAndVisibleChildren(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"bridge", "proofs", ".cache"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Bridge.CWD = root
	got := DiscoverWorkingDirs(&cfg)
	want := []string{root, filepath.Join(root, "bridge"), filepath.Join(root, "proofs")}
	if len(got) != len(want) {
		t.Fatalf("dirs = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("dirs = %#v, want %#v", got, want)
		}
	}
}

func TestExistingUniqueSortedWorkingDirsDropsDeletedPaths(t *testing.T) {
	root := t.TempDir()
	active := filepath.Join(root, "active")
	deleted := filepath.Join(root, "deleted")
	if err := os.Mkdir(active, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(deleted, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(deleted); err != nil {
		t.Fatal(err)
	}

	got := existingUniqueSortedWorkingDirs([]string{root, deleted, active, active})
	want := []string{root, active}
	if len(got) != len(want) {
		t.Fatalf("dirs = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("dirs = %#v, want %#v", got, want)
		}
	}
}

func TestDiscoverWorkingDirsDropsDeletedBase(t *testing.T) {
	root := filepath.Join(t.TempDir(), "gone")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Bridge.CWD = root
	if got := DiscoverWorkingDirs(&cfg); len(got) != 0 {
		t.Fatalf("dirs = %#v, want none", got)
	}
}
