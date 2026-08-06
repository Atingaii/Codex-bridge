package bridge

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
)

const (
	formalProofNotesFileName       = "proof-notes.md"
	formalProofFollowupStart       = "<!-- bridge-followups:start -->"
	formalProofFollowupEnd         = "<!-- bridge-followups:end -->"
	formalProofFollowupWindow      = 8
	formalProofFollowupPromptLimit = 2 * 1024
)

var formalProofFollowupPattern = regexp.MustCompile(`(?ms)\n?### 请求 ([0-9]+)\n\n` + "```text\\n" + `(.*?)\n` + "```\\n?")

type formalProofFollowup struct {
	Sequence string
	Prompt   string
}

type formalProofHarnessResult struct {
	RunDir        string
	ProjectDir    string
	NotesPath     string
	Assistant     string
	Created       bool
	Extracted     []string
	Copied        []string
	Prompt        string
	BootstrapNote string
}

type formalProofHarnessFile struct {
	Name     string
	MimeType string
	Raw      []byte
}

func prepareFormalProofHarness(cfg *config.Config, payload protocol.OrchestrationStartPayload, baseCWD string) (formalProofHarnessResult, error) {
	runDir := formalProofRunDir(baseCWD, payload.RunID)
	if payload.Resume && strings.TrimSpace(payload.RunCWD) != "" {
		runDir = formalProofExistingRunDir(baseCWD)
	}
	projectDir := filepath.Join(runDir, "project")
	notesPath := filepath.Join(runDir, formalProofNotesFileName)
	result := formalProofHarnessResult{
		RunDir:     runDir,
		ProjectDir: projectDir,
		NotesPath:  notesPath,
	}
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		return result, fmt.Errorf("create formal-proof project directory %q: %w", projectDir, err)
	}
	result.Created = !formalProofNotesExists(notesPath)
	decoded, err := decodeFormalProofHarnessFiles(cfg, payload.Files)
	if err != nil {
		return result, err
	}
	if result.Created || payload.Resume {
		if err := materializeFormalProofProjectFiles(projectDir, decoded, &result); err != nil {
			return result, err
		}
	}
	result.Assistant = detectProofAssistantForTask(projectDir, payload.Prompt)
	if result.Assistant == "" {
		result.Assistant = "unknown"
	}
	if result.Created {
		if err := writeFormalProofNotes(payload, result, decoded); err != nil {
			return result, err
		}
	} else if payload.Resume {
		if err := appendFormalProofFollowup(payload, result); err != nil {
			return result, err
		}
	}
	result.Prompt = formalProofHarnessPrompt(payload.Prompt, result, payload.Resume)
	if result.Created {
		result.BootstrapNote = fmt.Sprintf("Formal-proof workspace created at %s. Project: %s. Notes: %s.", runDir, projectDir, notesPath)
	} else {
		result.BootstrapNote = fmt.Sprintf("Formal-proof workspace reused at %s. Latest request was appended to %s.", runDir, notesPath)
	}
	return result, nil
}

func formalProofRunDir(baseCWD, runID string) string {
	base := strings.TrimSpace(baseCWD)
	if base == "" {
		base = "."
	}
	base = expandHome(base)
	if abs, err := filepath.Abs(base); err == nil {
		base = abs
	}
	name := safeFileName(runID)
	if name == "" {
		name = "run"
	}
	return filepath.Join(base, ".codex-bridge", "proof-runs", name)
}

func formalProofExistingRunDir(baseCWD string) string {
	base := strings.TrimSpace(baseCWD)
	if base == "" {
		base = "."
	}
	base = expandHome(base)
	if abs, err := filepath.Abs(base); err == nil {
		base = abs
	}
	return base
}

func formalProofNotesExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func decodeFormalProofHarnessFiles(cfg *config.Config, files []protocol.AttachmentPayload) ([]formalProofHarnessFile, error) {
	if len(files) == 0 {
		return nil, nil
	}
	if len(files) > 12 {
		return nil, errors.New("at most 12 files can be uploaded")
	}
	maxBytes := cfg.Hub.MaxAttachmentBytes
	if maxBytes <= 0 {
		maxBytes = 8 * 1024 * 1024
	}
	out := make([]formalProofHarnessFile, 0, len(files))
	for _, file := range files {
		if file.Size <= 0 || file.Size > maxBytes {
			return nil, fmt.Errorf("file %q is too large", file.Name)
		}
		raw, err := base64.StdEncoding.DecodeString(file.Data)
		if err != nil {
			return nil, fmt.Errorf("decode file %q: %w", file.Name, err)
		}
		if int64(len(raw)) > maxBytes {
			return nil, fmt.Errorf("file %q is too large", file.Name)
		}
		out = append(out, formalProofHarnessFile{Name: file.Name, MimeType: file.MimeType, Raw: raw})
	}
	return out, nil
}

func materializeFormalProofProjectFiles(projectDir string, files []formalProofHarnessFile, result *formalProofHarnessResult) error {
	for i, file := range files {
		name := safeOrchestrationUploadName(file.Name)
		if name == "" {
			name = fmt.Sprintf("upload-%02d.bin", i+1)
		}
		switch {
		case formalProofLooksLikeZip(file):
			entries, err := extractFormalProofZip(projectDir, file.Raw)
			if err != nil {
				return fmt.Errorf("extract zip %q: %w", file.Name, err)
			}
			result.Extracted = append(result.Extracted, entries...)
		case formalProofLooksLikeTarGz(file):
			entries, err := extractFormalProofTar(projectDir, file.Raw, true)
			if err != nil {
				return fmt.Errorf("extract tar.gz %q: %w", file.Name, err)
			}
			result.Extracted = append(result.Extracted, entries...)
		case formalProofLooksLikeTar(file):
			entries, err := extractFormalProofTar(projectDir, file.Raw, false)
			if err != nil {
				return fmt.Errorf("extract tar %q: %w", file.Name, err)
			}
			result.Extracted = append(result.Extracted, entries...)
		default:
			target := uniqueFormalProofProjectPath(projectDir, name)
			if err := os.WriteFile(target, file.Raw, 0o600); err != nil {
				return fmt.Errorf("write project file %q: %w", file.Name, err)
			}
			rel, _ := filepath.Rel(projectDir, target)
			result.Copied = append(result.Copied, filepath.ToSlash(rel))
		}
	}
	sort.Strings(result.Copied)
	sort.Strings(result.Extracted)
	return nil
}

func formalProofLooksLikeZip(file formalProofHarnessFile) bool {
	lower := strings.ToLower(file.Name + " " + file.MimeType)
	return strings.Contains(lower, ".zip") || strings.Contains(lower, "application/zip") ||
		bytes.HasPrefix(file.Raw, []byte("PK\x03\x04"))
}

func formalProofLooksLikeTarGz(file formalProofHarnessFile) bool {
	lower := strings.ToLower(file.Name + " " + file.MimeType)
	return strings.HasSuffix(strings.ToLower(file.Name), ".tar.gz") || strings.HasSuffix(strings.ToLower(file.Name), ".tgz") ||
		strings.Contains(lower, "gzip") || bytes.HasPrefix(file.Raw, []byte{0x1f, 0x8b})
}

func formalProofLooksLikeTar(file formalProofHarnessFile) bool {
	lower := strings.ToLower(file.Name + " " + file.MimeType)
	return strings.HasSuffix(strings.ToLower(file.Name), ".tar") || strings.Contains(lower, "application/x-tar")
}

func uniqueFormalProofProjectPath(projectDir, name string) string {
	target := filepath.Join(projectDir, name)
	if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
		return target
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 2; ; i++ {
		candidate := filepath.Join(projectDir, fmt.Sprintf("%s-%d%s", stem, i, ext))
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate
		}
	}
}

func extractFormalProofZip(projectDir string, raw []byte) ([]string, error) {
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, err
	}
	var entries []string
	for _, item := range reader.File {
		target, rel, err := safeFormalProofExtractPath(projectDir, item.Name)
		if err != nil {
			return nil, err
		}
		if item.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return nil, err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return nil, err
		}
		src, err := item.Open()
		if err != nil {
			return nil, err
		}
		err = writeFormalProofExtractedFile(target, src)
		_ = src.Close()
		if err != nil {
			return nil, err
		}
		entries = append(entries, rel)
	}
	sort.Strings(entries)
	return entries, nil
}

func extractFormalProofTar(projectDir string, raw []byte, gz bool) ([]string, error) {
	var reader io.Reader = bytes.NewReader(raw)
	if gz {
		gzr, err := gzip.NewReader(reader)
		if err != nil {
			return nil, err
		}
		defer gzr.Close()
		reader = gzr
	}
	tr := tar.NewReader(reader)
	var entries []string
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		target, rel, err := safeFormalProofExtractPath(projectDir, header.Name)
		if err != nil {
			return nil, err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return nil, err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return nil, err
			}
			if err := writeFormalProofExtractedFile(target, tr); err != nil {
				return nil, err
			}
			entries = append(entries, rel)
		default:
			// Skip symlinks, devices, and other special entries so archives
			// cannot escape the project root or affect host state.
		}
	}
	sort.Strings(entries)
	return entries, nil
}

func safeFormalProofExtractPath(projectDir, name string) (string, string, error) {
	clean := filepath.Clean(strings.TrimSpace(name))
	if clean == "." || clean == "" || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return "", "", fmt.Errorf("unsafe archive path %q", name)
	}
	target := filepath.Join(projectDir, clean)
	absProject, err := filepath.Abs(projectDir)
	if err != nil {
		return "", "", err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(absProject, absTarget)
	if err != nil {
		return "", "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("archive path escapes project root: %q", name)
	}
	return absTarget, filepath.ToSlash(rel), nil
}

func writeFormalProofExtractedFile(target string, src io.Reader) error {
	dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		dst, err = os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	}
	if err != nil {
		return err
	}
	defer dst.Close()
	_, err = io.Copy(dst, src)
	return err
}

func writeFormalProofNotes(payload protocol.OrchestrationStartPayload, result formalProofHarnessResult, files []formalProofHarnessFile) error {
	var b strings.Builder
	b.WriteString("# Formal Proof Notes\n\n")
	b.WriteString("这是一份轻量、持续更新的证明任务记录。只记录跨轮次真正有用的信息，不需要维护额外状态文件或运行生成的检查脚本。\n\n")
	b.WriteString("## 原始任务\n\n```text\n")
	b.WriteString(strings.TrimSpace(payload.Prompt))
	b.WriteString("\n```\n\n")
	b.WriteString("## 工作区\n\n- 项目目录：`project/`\n- 检测到的证明系统：`")
	b.WriteString(result.Assistant)
	b.WriteString("`\n- 上传输入：")
	if len(files) == 0 {
		b.WriteString("无\n")
	} else {
		b.WriteByte('\n')
		for _, file := range files {
			b.WriteString("  - `")
			b.WriteString(strings.TrimSpace(file.Name))
			b.WriteString("` (")
			b.WriteString(strconv.Itoa(len(file.Raw)))
			b.WriteString(" bytes)\n")
		}
	}
	b.WriteString("\n## 目标与未解决义务\n\n- 第一轮从原始任务和 `project/` 中识别不得弱化的目标 theorem、lemma、fact 或 termination obligation。\n")
	b.WriteString("- 在这里保留当前目标、剩余 goal、关键前提和阻塞点。\n\n")
	b.WriteString("## 验证证据\n\n- 记录实际运行的 proof assistant 构建、目标检查和依赖/可信性审计命令及结果。\n")
	b.WriteString("- 构建通过不等于完整证明；含 `sorry`、`admit`、新增公理、oracle、弱化陈述或剩余 goal 时必须明确标记。\n\n")
	b.WriteString("## 关键决策\n\n- 仅在目标、语义、证明策略或验收标准发生重要变化时追加说明。\n\n")
	b.WriteString("## 后续请求\n\n")
	b.WriteString(formatFormalProofFollowupBlock(0, nil))
	b.WriteByte('\n')
	if err := os.WriteFile(result.NotesPath, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write formal-proof notes %q: %w", result.NotesPath, err)
	}
	return nil
}

func appendFormalProofFollowup(payload protocol.OrchestrationStartPayload, result formalProofHarnessResult) error {
	raw, err := os.ReadFile(result.NotesPath)
	if err != nil {
		return fmt.Errorf("read formal-proof notes for follow-up: %w", err)
	}
	notes, total, followups := parseFormalProofFollowups(string(raw))
	seq := payload.PromptSeq
	if seq <= 0 {
		seq = int64(total + 1)
	}
	followups = append(followups, formalProofFollowup{
		Sequence: strconv.FormatInt(seq, 10),
		Prompt:   trimForPrompt(strings.TrimSpace(payload.Prompt), formalProofFollowupPromptLimit),
	})
	total++
	if len(followups) > formalProofFollowupWindow {
		followups = followups[len(followups)-formalProofFollowupWindow:]
	}
	updated := replaceFormalProofFollowupBlock(notes, formatFormalProofFollowupBlock(total, followups))
	if err := writeFormalProofNotesAtomic(result.NotesPath, []byte(updated)); err != nil {
		return fmt.Errorf("rewrite formal-proof follow-ups: %w", err)
	}
	return nil
}

func parseFormalProofFollowups(notes string) (string, int, []formalProofFollowup) {
	start := strings.Index(notes, formalProofFollowupStart)
	end := strings.Index(notes, formalProofFollowupEnd)
	if start >= 0 && end > start {
		block := notes[start : end+len(formalProofFollowupEnd)]
		total := parseFormalProofFollowupTotal(block)
		return notes, total, parseFormalProofFollowupEntries(block)
	}

	heading := strings.Index(notes, "## 后续请求")
	if heading < 0 {
		return notes, 0, nil
	}
	prefix := notes[:heading]
	section := notes[heading:]
	followups := parseFormalProofFollowupEntries(section)
	section = formalProofFollowupPattern.ReplaceAllString(section, "")
	section = strings.Replace(section, "\n\n- 暂无。", "", 1)
	return prefix + section, len(followups), followups
}

func parseFormalProofFollowupTotal(block string) int {
	const prefix = "- 后续请求总数："
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, prefix) {
			value, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, prefix)))
			if err == nil && value >= 0 {
				return value
			}
		}
	}
	return len(parseFormalProofFollowupEntries(block))
}

func parseFormalProofFollowupEntries(block string) []formalProofFollowup {
	matches := formalProofFollowupPattern.FindAllStringSubmatch(block, -1)
	out := make([]formalProofFollowup, 0, len(matches))
	for _, match := range matches {
		out = append(out, formalProofFollowup{Sequence: match[1], Prompt: match[2]})
	}
	return out
}

func replaceFormalProofFollowupBlock(notes, block string) string {
	start := strings.Index(notes, formalProofFollowupStart)
	end := strings.Index(notes, formalProofFollowupEnd)
	if start >= 0 && end > start {
		end += len(formalProofFollowupEnd)
		return notes[:start] + block + notes[end:]
	}
	heading := strings.Index(notes, "## 后续请求")
	if heading < 0 {
		return strings.TrimRight(notes, "\n") + "\n\n## 后续请求\n\n" + block + "\n"
	}
	headingEnd := strings.IndexByte(notes[heading:], '\n')
	if headingEnd < 0 {
		return notes + "\n\n" + block + "\n"
	}
	headingEnd += heading + 1
	return notes[:headingEnd] + "\n" + block + notes[headingEnd:]
}

func formatFormalProofFollowupBlock(total int, followups []formalProofFollowup) string {
	var b strings.Builder
	b.WriteString(formalProofFollowupStart)
	b.WriteString("\n- 后续请求总数：")
	b.WriteString(strconv.Itoa(total))
	b.WriteString("\n- 已压缩较早请求：")
	b.WriteString(strconv.Itoa(max(0, total-len(followups))))
	b.WriteByte('\n')
	if len(followups) == 0 {
		b.WriteString("- 暂无。\n")
	}
	for _, followup := range followups {
		prompt := strings.ReplaceAll(followup.Prompt, "```", "``\u200b`")
		b.WriteString("\n### 请求 ")
		b.WriteString(followup.Sequence)
		b.WriteString("\n\n```text\n")
		b.WriteString(prompt)
		b.WriteString("\n```\n")
	}
	b.WriteString(formalProofFollowupEnd)
	return b.String()
}

func writeFormalProofNotesAtomic(path string, content []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".proof-notes-*")
	if err != nil {
		return err
	}
	tmpPath := file.Name()
	defer os.Remove(tmpPath)
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(content); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func detectProofAssistant(projectDir string) string {
	return detectProofAssistantForTask(projectDir, "")
}

func detectProofAssistantForTask(projectDir, prompt string) string {
	var hasThy, hasCoq, hasLean, hasLake bool
	_ = filepath.WalkDir(projectDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := strings.ToLower(d.Name())
		switch {
		case name == "root" || strings.HasSuffix(name, ".thy"):
			hasThy = true
		case name == "_coqproject" || strings.HasSuffix(name, ".v"):
			hasCoq = true
		case name == "lakefile.lean" || name == "lakefile.toml":
			hasLake = true
			hasLean = true
		case strings.HasSuffix(name, ".lean"):
			hasLean = true
		}
		return nil
	})
	lowerPrompt := strings.ToLower(prompt)
	if strings.Contains(lowerPrompt, "coq") || strings.Contains(lowerPrompt, "rocq") {
		return "coq"
	}
	if strings.Contains(lowerPrompt, "lean") {
		return "lean4"
	}
	if strings.Contains(lowerPrompt, "isabelle") {
		return "isabelle"
	}
	switch {
	case hasCoq:
		return "coq"
	case hasThy:
		return "isabelle"
	case hasLake || hasLean:
		return "lean4"
	default:
		return "unknown"
	}
}

func formalProofHarnessPrompt(original string, result formalProofHarnessResult, resumed bool) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(original))
	b.WriteString("\n\nFormal-proof workspace:\n")
	b.WriteString("- Proof run folder: ")
	b.WriteString(result.RunDir)
	b.WriteByte('\n')
	b.WriteString("- Project folder: ")
	b.WriteString(result.ProjectDir)
	b.WriteByte('\n')
	b.WriteString("- Persistent notes: ")
	b.WriteString(result.NotesPath)
	b.WriteByte('\n')
	b.WriteString("- Detected proof assistant: ")
	b.WriteString(result.Assistant)
	b.WriteByte('\n')
	if resumed {
		b.WriteString("- This is a follow-up in the same proof run. Continue from the existing project and notes.\n")
	} else {
		b.WriteString("- The workspace was prepared before the first scheduled CLI turn and does not consume a turn.\n")
	}
	b.WriteString("\nWork in `project/`. Read `proof-notes.md` for durable task context, and update it only with material target, obligation, command evidence, blocker, or decision changes that a later worker needs. Run the project-appropriate proof assistant commands directly; no generated harness script or metadata synchronization is required.\n")
	return b.String()
}
