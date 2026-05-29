package bridge

import "path/filepath"

func tidyPath(path string) string {
	return filepath.Clean(path)
}
