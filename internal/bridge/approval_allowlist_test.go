package bridge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProofCommandAutoApproval(t *testing.T) {
	root := t.TempDir()
	bin := t.TempDir()
	for _, name := range []string{"coqc", "coqtop"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	proofDir := filepath.Join(root, "proof files")
	if err := os.Mkdir(proofDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Main.v", "proof files/Check.v", "notes.txt"} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.WriteFile(path, []byte("Check nat.\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		command string
		want    bool
	}{
		{"cat file", "cat -n notes.txt", true},
		{"cat quoted file", `cat 'proof files/Check.v'`, true},
		{"coqc source", "coqc Main.v", true},
		{"coqc logical root", "coqc -R . Project Main.v", true},
		{"coqtop batch source", "coqtop -batch -l Main.v", true},
		{"coqtop interactive", "coqtop Main.v", false},
		{"cat stdin", "cat -", false},
		{"cat traversal", "cat " + outside, false},
		{"cat shell chain", "cat notes.txt; rm -rf .", false},
		{"coqc and chain", "coqc Main.v && touch owned", false},
		{"coqc substitution", "coqc $(echo Main.v)", false},
		{"coqtop redirect", "coqtop -quiet < Main.v", false},
		{"unsafe coqc option", "coqc -native-compiler yes Main.v", false},
		{"missing source", "coqc Missing.v", false},
		{"allowed name prefix", "coqc-helper Main.v", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isProofCommandAutoApprovable(tc.command, root); got != tc.want {
				t.Fatalf("isProofCommandAutoApprovable(%q) = %v, want %v", tc.command, got, tc.want)
			}
		})
	}
}

func TestProofCommandAutoApprovalRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked.txt")); err != nil {
		t.Fatal(err)
	}
	if isProofCommandAutoApprovable("cat linked.txt", root) {
		t.Fatal("symlink escaping the workspace was automatically approved")
	}
}
