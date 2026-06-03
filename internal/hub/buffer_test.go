package hub

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/web"
)

func TestAssistantBufferDeltaAndContentSemantics(t *testing.T) {
	s := &Server{buffers: make(map[string]string)}

	s.appendAssistantDelta("sid", "hel", "")
	s.appendAssistantDelta("sid", "lo", "")
	if got := s.consumeAssistantBuffer("sid"); got != "hello" {
		t.Fatalf("delta buffer = %q", got)
	}

	s.appendAssistantDelta("sid", "draft", "")
	s.appendAssistantDelta("sid", "", "final")
	if got := s.consumeAssistantBuffer("sid"); got != "final" {
		t.Fatalf("content should replace delta buffer, got %q", got)
	}

	s.appendAssistantDelta("sid", "", "first")
	s.appendAssistantDelta("sid", "", "second")
	if got := s.consumeAssistantBuffer("sid"); got != "second" {
		t.Fatalf("latest content should win, got %q", got)
	}
}

func TestCheckOriginExactHost(t *testing.T) {
	cfg := config.Default()
	cfg.Hub.AllowedOrigins = []string{"https://sparkapi.tech"}
	s := &Server{cfg: &cfg}

	allowed := httptest.NewRequest(http.MethodGet, "https://sparkapi.tech/ws/chat", nil)
	allowed.Host = "sparkapi.tech"
	allowed.Header.Set("Origin", "https://sparkapi.tech")
	if !s.checkOrigin(allowed) {
		t.Fatal("same host origin should be allowed")
	}

	spoofed := httptest.NewRequest(http.MethodGet, "https://sparkapi.tech/ws/chat", nil)
	spoofed.Host = "sparkapi.tech"
	spoofed.Header.Set("Origin", "https://evil.example/?next=://sparkapi.tech")
	if s.checkOrigin(spoofed) {
		t.Fatal("origin containing host as substring should not be allowed")
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	for _, key := range []string{"Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy"} {
		if rr.Header().Get(key) == "" {
			t.Fatalf("missing security header %s", key)
		}
	}
	if csp := rr.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "blob:") {
		t.Fatalf("CSP should allow blob image previews, got %q", csp)
	}
}

func TestStaticHandlerSPAFallbackRoutes(t *testing.T) {
	s := &Server{}

	for _, path := range []string{"/conversation-snapshot", "/share/shr_example"} {
		rr := httptest.NewRecorder()
		s.staticHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))

		if rr.Code != http.StatusOK {
			t.Fatalf("%s route status = %d", path, rr.Code)
		}
		body := rr.Body.String()
		for _, needle := range []string{"Codex Bridge", `<div id="root"></div>`, `type="module"`} {
			if !strings.Contains(body, needle) {
				t.Fatalf("%s route did not return SPA index, missing %q", path, needle)
			}
		}
	}
}

func TestStaticHandlerCacheHeaders(t *testing.T) {
	s := &Server{}
	jsAssets, err := fs.Glob(web.StaticFS, "static/assets/*.js")
	if err != nil {
		t.Fatal(err)
	}
	if len(jsAssets) == 0 {
		t.Fatal("missing embedded JS asset")
	}
	jsAssetPath := "/" + strings.TrimPrefix(jsAssets[0], "static/")

	for _, tc := range []struct {
		path        string
		contentType string
		cache       string
	}{
		{path: "/sw.js", contentType: "application/javascript", cache: "no-store"},
		{path: "/app-recovery.js", contentType: "application/javascript", cache: "no-store"},
		{path: jsAssetPath, cache: "public, max-age=60, stale-while-revalidate=300"},
		{path: "/", cache: "no-store"},
	} {
		rr := httptest.NewRecorder()
		s.staticHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("%s route status = %d", tc.path, rr.Code)
		}
		if got := rr.Header().Get("Cache-Control"); got != tc.cache {
			t.Fatalf("%s Cache-Control = %q, want %q", tc.path, got, tc.cache)
		}
		if tc.contentType != "" {
			if got := rr.Header().Get("Content-Type"); !strings.Contains(got, tc.contentType) {
				t.Fatalf("%s Content-Type = %q, want substring %q", tc.path, got, tc.contentType)
			}
		}
	}
}

func TestValidatePromptAttachments(t *testing.T) {
	cfg := config.Default()
	cfg.Hub.MaxAttachmentBytes = 10
	s := &Server{cfg: &cfg}

	if err := s.validatePromptAttachments([]protocol.AttachmentPayload{{
		Name:     "image.png",
		MimeType: "image/png",
		Size:     10,
		Data:     "abcd",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := s.validatePromptAttachments([]protocol.AttachmentPayload{{
		Name:     "file.txt",
		MimeType: "text/plain",
		Size:     4,
		Data:     "abcd",
	}}); err == nil {
		t.Fatal("text attachment should be rejected")
	}
	if err := s.validatePromptAttachments([]protocol.AttachmentPayload{{
		Name:     "big.png",
		MimeType: "image/png",
		Size:     11,
		Data:     "abcd",
	}}); err == nil {
		t.Fatal("oversized image should be rejected")
	}
}
