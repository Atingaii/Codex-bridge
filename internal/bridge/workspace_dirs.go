package bridge

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tencent/codex-bridge/internal/config"
)

const maxDiscoveredWorkingDirs = 101

func DiscoverWorkingDirs(cfg *config.Config) []string {
	base := "."
	if cfg != nil && cfg.Bridge.CWD != "" {
		base = cfg.Bridge.CWD
	}
	base = cleanWorkingDirPath(expandHome(base))
	if base == "" {
		base = "."
	}

	dirs := []string{base}
	entries, err := os.ReadDir(base)
	if err != nil {
		return dirs
	}
	children := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path := cleanWorkingDirPath(filepath.Join(base, entry.Name()))
		if path != "" {
			children = append(children, path)
		}
	}
	sort.Strings(children)
	for _, child := range children {
		if len(dirs) >= maxDiscoveredWorkingDirs {
			break
		}
		dirs = append(dirs, child)
	}
	return uniqueSortedWorkingDirs(dirs)
}

func cleanWorkingDirPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.Clean(path)
}

func uniqueSortedWorkingDirs(dirs []string) []string {
	if len(dirs) == 0 {
		return nil
	}
	base := dirs[0]
	seen := map[string]struct{}{base: {}}
	children := make([]string, 0, len(dirs)-1)
	for _, dir := range dirs[1:] {
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		children = append(children, dir)
	}
	sort.Strings(children)
	return append([]string{base}, children...)
}
