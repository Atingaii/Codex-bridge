package hub

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
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
