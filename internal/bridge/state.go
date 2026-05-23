package bridge

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/tencent/codex-bridge/internal/store"
)

func loadMachineID(path string) (string, error) {
	path = expandHome(path)
	if path == "" {
		path = expandHome("~/.codex-bridge/machine_id")
	}
	if data, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(data)); id != "" {
			return id, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	id := store.NewID("mac")
	if err := os.WriteFile(path, []byte(id+"\n"), 0o600); err != nil {
		return "", err
	}
	return id, nil
}

func loadToken(value, path string) (string, error) {
	if value != "" {
		return strings.TrimSpace(value), nil
	}
	if path != "" {
		data, err := os.ReadFile(expandHome(path))
		if err != nil {
			return "", err
		}
		value = strings.TrimSpace(string(data))
	}
	if value == "" {
		return "", errors.New("bridge token is required")
	}
	return value, nil
}

func expandHome(path string) string {
	if path == "" || path[0] != '~' {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}
