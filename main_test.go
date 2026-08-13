package main

import (
	"reflect"
	"testing"
)

func TestIsQuotedEmptyPassword(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "double quoted empty", value: `""`, want: true},
		{name: "single quoted empty", value: `''`, want: true},
		{name: "space padded double quoted empty", value: `  ""  `, want: true},
		{name: "real password", value: `change-me-123`, want: false},
		{name: "empty password", value: ``, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isQuotedEmptyPassword(tt.value); got != tt.want {
				t.Fatalf("isQuotedEmptyPassword(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestNormalizeConnectArgsHandlesStrictWorkspaceBoolean(t *testing.T) {
	args := []string{
		"--hub", "https://hub.example",
		"--runner", "codex",
		"--sandbox", "workspace-write",
		"--approval-policy", "never",
		"--strict-workspace",
		"--cwd", "/repo",
		"--name", "strict-agent",
		"enr_test",
	}

	token, flagArgs, err := normalizeConnectArgs(args)
	if err != nil {
		t.Fatal(err)
	}
	if token != "enr_test" {
		t.Fatalf("token = %q, want enr_test", token)
	}
	wantFlags := args[:len(args)-1]
	if !reflect.DeepEqual(flagArgs, wantFlags) {
		t.Fatalf("flag args = %#v, want %#v", flagArgs, wantFlags)
	}
}
