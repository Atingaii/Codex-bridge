package config

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestApplyEnvStrictWorkspace(t *testing.T) {
	t.Setenv("BRIDGE_STRICT_WORKSPACE", "true")
	want := []string{"/custom/coq", "/custom/isabelle"}
	t.Setenv("BRIDGE_STRICT_WORKSPACE_READ_ONLY", filepath.Join(want[0])+string(filepath.ListSeparator)+filepath.Join(want[1]))
	cfg := Default()

	applyEnv(&cfg)

	if !cfg.Bridge.StrictWorkspace {
		t.Fatal("BRIDGE_STRICT_WORKSPACE did not enable strict mode")
	}
	if !reflect.DeepEqual(cfg.Bridge.StrictWorkspaceReadOnly, want) {
		t.Fatalf("strict read-only paths = %#v, want %#v", cfg.Bridge.StrictWorkspaceReadOnly, want)
	}
}
