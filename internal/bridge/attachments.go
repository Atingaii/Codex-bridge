package bridge

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tencent/codex-bridge/internal/protocol"
)

var safeUploadName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func (m *SessionManager) preparePromptContent(sid, content string, attachments []protocol.AttachmentPayload) (string, func(), error) {
	if len(attachments) == 0 {
		return content, func() {}, nil
	}
	baseDir := m.cfg.Bridge.CWD
	if baseDir == "" {
		baseDir = "."
	}
	uploadDir := filepath.Join(expandHome(baseDir), ".codex-bridge", "uploads", sid)
	if err := os.MkdirAll(uploadDir, 0o700); err != nil {
		return "", func() {}, fmt.Errorf("create upload directory: %w", err)
	}

	var paths []string
	for i, attachment := range attachments {
		if !strings.HasPrefix(attachment.MimeType, "image/") {
			return "", func() {}, errors.New("only image uploads are supported")
		}
		raw, err := base64.StdEncoding.DecodeString(attachment.Data)
		if err != nil {
			return "", func() {}, fmt.Errorf("decode image %q: %w", attachment.Name, err)
		}
		if len(raw) == 0 {
			return "", func() {}, errors.New("image data is empty")
		}
		maxBytes := m.cfg.Hub.MaxAttachmentBytes
		if maxBytes <= 0 {
			maxBytes = 8 * 1024 * 1024
		}
		if int64(len(raw)) > maxBytes {
			return "", func() {}, errors.New("image is too large")
		}
		name := safeFileName(attachment.Name)
		if name == "" {
			name = fmt.Sprintf("image-%d%s", i+1, extensionForMime(attachment.MimeType))
		}
		path := filepath.Join(uploadDir, fmt.Sprintf("%s-%s", attachmentID(i), name))
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			return "", func() {}, fmt.Errorf("write image %q: %w", attachment.Name, err)
		}
		abs, err := filepath.Abs(path)
		if err == nil {
			path = abs
		}
		paths = append(paths, path)
	}

	var b strings.Builder
	b.WriteString(content)
	b.WriteString("\n\nUploaded image files:\n")
	for _, path := range paths {
		b.WriteString("- ")
		b.WriteString(path)
		b.WriteByte('\n')
	}
	b.WriteString("\nUse the uploaded image file paths above when the user asks about the images.")

	return b.String(), func() {}, nil
}

func safeFileName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = safeUploadName.ReplaceAllString(name, "-")
	return strings.Trim(name, ".-")
}

func extensionForMime(mimeType string) string {
	switch strings.ToLower(mimeType) {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".img"
	}
}

func attachmentID(index int) string {
	return fmt.Sprintf("%02d", index+1)
}
