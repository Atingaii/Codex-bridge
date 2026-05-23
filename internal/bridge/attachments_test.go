package bridge

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
)

func TestPreparePromptContentWritesImageAttachment(t *testing.T) {
	cfg := config.Default()
	cfg.Bridge.CWD = t.TempDir()
	m := NewSessionManager(&cfg)

	content, cleanup, err := m.preparePromptContent("sid", "describe it", []protocol.AttachmentPayload{
		{
			Name:     "../screen shot.png",
			MimeType: "image/png",
			Size:     4,
			Data:     base64.StdEncoding.EncodeToString([]byte("test")),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	if !strings.Contains(content, "describe it") || !strings.Contains(content, "Uploaded image files:") {
		t.Fatalf("content = %q", content)
	}
	var path string
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "- ") {
			path = strings.TrimSpace(strings.TrimPrefix(line, "- "))
			break
		}
	}
	if path == "" {
		t.Fatalf("missing attachment path in content: %q", content)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "test" {
		t.Fatalf("attachment data = %q", data)
	}
	if strings.Contains(path, "..") || !strings.HasSuffix(path, "screen-shot.png") {
		t.Fatalf("unsafe path = %q", path)
	}
}
