package web

import (
	"io/fs"
	"strings"
	"testing"
)

func TestStaticUIContracts(t *testing.T) {
	indexBytes, err := StaticFS.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	index := string(indexBytes)

	requiredIndex := []string{
		"Codex Bridge",
		`<div id="root"></div>`,
		`type="module"`,
		`/assets/`,
		`/manifest.webmanifest`,
		`/icon.svg`,
	}
	for _, needle := range requiredIndex {
		if !strings.Contains(index, needle) {
			t.Fatalf("index.html missing frontend contract %q", needle)
		}
	}

	requiredStaticFiles := []string{
		"static/icon.svg",
		"static/manifest.webmanifest",
		"static/sw.js",
	}
	for _, name := range requiredStaticFiles {
		if _, err := StaticFS.ReadFile(name); err != nil {
			t.Fatalf("missing embedded static file %q: %v", name, err)
		}
	}

	assetFiles, err := fs.Glob(StaticFS, "static/assets/*")
	if err != nil {
		t.Fatal(err)
	}
	var hasJS, hasCSS bool
	for _, name := range assetFiles {
		hasJS = hasJS || strings.HasSuffix(name, ".js")
		hasCSS = hasCSS || strings.HasSuffix(name, ".css")
	}
	if !hasJS || !hasCSS {
		t.Fatalf("missing built Vite assets: js=%v css=%v files=%v", hasJS, hasCSS, assetFiles)
	}
}
