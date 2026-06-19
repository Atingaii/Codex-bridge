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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
)

const (
	formalProofHarnessDirName      = "proof-harness"
	formalProofHarnessStateVersion = 1
)

type formalProofHarnessResult struct {
	RunDir        string
	ProjectDir    string
	HarnessDir    string
	Assistant     string
	Created       bool
	Extracted     []string
	Copied        []string
	FollowupPath  string
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
	harnessDir := filepath.Join(runDir, formalProofHarnessDirName)
	result := formalProofHarnessResult{
		RunDir:     runDir,
		ProjectDir: projectDir,
		HarnessDir: harnessDir,
	}
	if payload.Resume && strings.TrimSpace(payload.RunCWD) != "" {
		if err := ensureFormalProofHarnessDirs(projectDir, harnessDir); err != nil {
			return result, err
		}
		decoded, err := decodeFormalProofHarnessFiles(cfg, payload.Files)
		if err != nil {
			return result, err
		}
		if len(decoded) > 0 {
			if err := materializeFormalProofProjectFiles(projectDir, decoded, &result); err != nil {
				return result, err
			}
		}
		result.Created = !formalProofHarnessExists(runDir)
		result.Assistant = detectProofAssistant(projectDir)
		if result.Assistant == "" {
			result.Assistant = "unknown"
		}
		if result.Created {
			if err := writeFormalProofHarnessInitialFiles(payload, result, decoded); err != nil {
				return result, err
			}
		}
		followupPath, err := writeFormalProofFollowup(payload, harnessDir)
		if err != nil {
			return result, err
		}
		result.FollowupPath = followupPath
		if err := writeFormalProofState(payload, result); err != nil {
			return result, err
		}
		result.Prompt = formalProofHarnessPrompt(payload.Prompt, result, true)
		result.BootstrapNote = fmt.Sprintf("Formal-proof harness reused at %s. Follow-up recorded at %s.", runDir, followupPath)
		return result, nil
	}

	if err := ensureFormalProofHarnessDirs(projectDir, harnessDir); err != nil {
		return result, err
	}
	result.Created = !formalProofHarnessExists(runDir)
	decoded, err := decodeFormalProofHarnessFiles(cfg, payload.Files)
	if err != nil {
		return result, err
	}
	if result.Created {
		if err := materializeFormalProofProjectFiles(projectDir, decoded, &result); err != nil {
			return result, err
		}
	}
	result.Assistant = detectProofAssistant(projectDir)
	if result.Assistant == "" {
		result.Assistant = "unknown"
	}
	if result.Created {
		if err := writeFormalProofHarnessInitialFiles(payload, result, decoded); err != nil {
			return result, err
		}
	} else if _, err := writeFormalProofFollowup(payload, harnessDir); err != nil {
		return result, err
	}
	if err := writeFormalProofState(payload, result); err != nil {
		return result, err
	}
	result.Prompt = formalProofHarnessPrompt(payload.Prompt, result, false)
	if result.Created {
		result.BootstrapNote = fmt.Sprintf("Formal-proof harness created at %s. Project root: %s.", runDir, projectDir)
	} else {
		result.BootstrapNote = fmt.Sprintf("Formal-proof harness already exists at %s and was reused.", runDir)
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

func formalProofHarnessExists(runDir string) bool {
	for _, rel := range []string{
		"AGENTS.md",
		"CLAUDE.md",
		filepath.Join(formalProofHarnessDirName, "任务说明.md"),
		filepath.Join(formalProofHarnessDirName, "证明义务.md"),
		filepath.Join(formalProofHarnessDirName, "变更影响.md"),
		filepath.Join(formalProofHarnessDirName, "状态.yaml"),
		filepath.Join(formalProofHarnessDirName, "check.sh"),
	} {
		if _, err := os.Stat(filepath.Join(runDir, rel)); err != nil {
			return false
		}
	}
	return true
}

func ensureFormalProofHarnessDirs(projectDir, harnessDir string) error {
	for _, dir := range []string{
		projectDir,
		harnessDir,
		filepath.Join(harnessDir, "证明决策"),
		filepath.Join(harnessDir, "evidence"),
		filepath.Join(harnessDir, "followups"),
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create proof harness directory %q: %w", dir, err)
		}
	}
	return nil
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

func writeFormalProofHarnessInitialFiles(payload protocol.OrchestrationStartPayload, result formalProofHarnessResult, files []formalProofHarnessFile) error {
	writes := map[string]string{
		filepath.Join(result.RunDir, "AGENTS.md"):                  formalProofAgentsMD(),
		filepath.Join(result.RunDir, "CLAUDE.md"):                  formalProofClaudeMD(),
		filepath.Join(result.HarnessDir, "任务说明.md"):                formalProofTaskMD(payload, result, files),
		filepath.Join(result.HarnessDir, "证明义务.md"):                formalProofObligationsMD(payload, result),
		filepath.Join(result.HarnessDir, "变更影响.md"):                formalProofImpactMD(),
		filepath.Join(result.HarnessDir, "检查说明.md"):                formalProofChecksMD(),
		filepath.Join(result.HarnessDir, "证明决策", "000-初始任务.md"):    formalProofInitialDecisionMD(payload, result),
		filepath.Join(result.HarnessDir, "evidence", "README.md"):  "# 证据目录\n\n后续 CLI 轮次在这里记录关键构建、审计、日志或截图证据的路径摘要。\n",
		filepath.Join(result.HarnessDir, "followups", "README.md"): "# 后续需求记录\n\n同一 Proof Run 中用户追加、放宽或收紧的要求记录在本目录。\n",
	}
	for path, content := range writes {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return fmt.Errorf("write proof harness file %q: %w", path, err)
		}
	}
	checkPath := filepath.Join(result.HarnessDir, "check.sh")
	if err := os.WriteFile(checkPath, []byte(formalProofCheckScript()), 0o700); err != nil {
		return fmt.Errorf("write proof harness check script: %w", err)
	}
	return nil
}

func writeFormalProofFollowup(payload protocol.OrchestrationStartPayload, harnessDir string) (string, error) {
	seq := payload.PromptSeq
	if seq <= 0 {
		seq = time.Now().Unix()
	}
	name := fmt.Sprintf("%03d-后续需求.md", seq)
	path := filepath.Join(harnessDir, "followups", name)
	content := "# 后续需求\n\n" +
		"<!-- harness: followup_version=" + strconv.FormatInt(seq, 10) + " -->\n\n" +
		"用户追加输入：\n\n" +
		"```text\n" + strings.TrimSpace(payload.Prompt) + "\n```\n\n" +
		"处理规则：\n\n" +
		"- 不覆盖 `任务说明.md` 中的原始目标。\n" +
		"- 如果本输入放宽或收紧验收标准，必须追加新的 `证明决策/NNN-*.md` 并同步更新 `状态.yaml`。\n" +
		"- 后续 CLI 轮次仍以同一 Proof Run 目录和 `证明义务.md` 为上下文继续。\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", fmt.Errorf("write proof harness follow-up: %w", err)
	}
	return path, nil
}

func writeFormalProofState(payload protocol.OrchestrationStartPayload, result formalProofHarnessResult) error {
	content := formalProofStateYAML(payload, result)
	path := filepath.Join(result.HarnessDir, "状态.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write proof harness state: %w", err)
	}
	return nil
}

func detectProofAssistant(projectDir string) string {
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
	switch {
	case hasThy:
		return "isabelle"
	case hasCoq:
		return "coq"
	case hasLake || hasLean:
		return "lean4"
	default:
		return "unknown"
	}
}

func formalProofHarnessPrompt(original string, result formalProofHarnessResult, resumed bool) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(original))
	b.WriteString("\n\nFormal-proof harness workspace:\n")
	b.WriteString("- Proof run folder: ")
	b.WriteString(result.RunDir)
	b.WriteByte('\n')
	b.WriteString("- Project folder: ")
	b.WriteString(result.ProjectDir)
	b.WriteByte('\n')
	b.WriteString("- Harness folder: ")
	b.WriteString(result.HarnessDir)
	b.WriteByte('\n')
	b.WriteString("- Detected proof assistant: ")
	b.WriteString(result.Assistant)
	b.WriteByte('\n')
	if resumed {
		b.WriteString("- This is a follow-up in the same Proof Run. Reuse and update the existing harness instead of starting a new project.\n")
	} else {
		b.WriteString("- The bootstrap setup already created the harness before this scheduled CLI turn. This setup does not count as a CLI turn.\n")
	}
	b.WriteString("\nBefore editing files, read `AGENTS.md`, `CLAUDE.md` when relevant, and the Chinese files under `proof-harness/`. Work inside this proof-run folder. The user project is under `project/`. After changing proof files, update `proof-harness/证明义务.md`, record any strategy or requirement change under `proof-harness/证明决策/`, and run `./proof-harness/check.sh --harness` plus `./proof-harness/check.sh --proof` when applicable.\n")
	return b.String()
}

func formalProofAgentsMD() string {
	return `# Proof Harness Instructions

本目录是一个 Codex Bridge formal-proof Proof Run。后续 CLI agent 必须把这里视为真实工作目录。

工作规则：

1. 开始前先读取 ` + "`proof-harness/任务说明.md`" + `、` + "`proof-harness/证明义务.md`" + `、` + "`proof-harness/变更影响.md`" + ` 和 ` + "`proof-harness/状态.yaml`" + `。
2. 用户项目位于 ` + "`project/`" + `。除非用户明确要求，不要在本 Proof Run 目录外写文件。
3. 修改证明文件后，同步更新 ` + "`proof-harness/证明义务.md`" + `。
4. 改变证明策略、验收标准、定理陈述或模型语义时，必须在 ` + "`proof-harness/证明决策/`" + ` 追加决策记录。
5. 根据 ` + "`proof-harness/变更影响.md`" + ` 运行 ` + "`./proof-harness/check.sh --harness`" + ` 和适用的 proof 检查。
6. 最终结论必须区分完整证明、可编译骨架、阻塞、证据不足或无效状态，不能只用自然语言声称完成。
`
}

func formalProofClaudeMD() string {
	return `# Claude Proof Harness Entry

本目录由 Codex Bridge 为 formal-proof 编排自动创建。请优先读取：

- ` + "`proof-harness/任务说明.md`" + `
- ` + "`proof-harness/证明义务.md`" + `
- ` + "`proof-harness/变更影响.md`" + `
- ` + "`proof-harness/状态.yaml`" + `

用户项目在 ` + "`project/`" + `。证明工作、构建验证、审计扫描和证据记录都应围绕这些 harness 文件持续更新。
`
}

func formalProofTaskMD(payload protocol.OrchestrationStartPayload, result formalProofHarnessResult, files []formalProofHarnessFile) string {
	var b strings.Builder
	b.WriteString("# 任务说明\n\n")
	b.WriteString("<!-- harness: task_version=1 -->\n\n")
	b.WriteString("## 原始用户需求\n\n")
	b.WriteString("```text\n")
	b.WriteString(strings.TrimSpace(payload.Prompt))
	b.WriteString("\n```\n\n")
	b.WriteString("## 项目位置\n\n")
	b.WriteString("- Proof Run 目录：`")
	b.WriteString(result.RunDir)
	b.WriteString("`\n")
	b.WriteString("- 用户项目目录：`project/`\n")
	b.WriteString("- 检测到的证明系统：`")
	b.WriteString(result.Assistant)
	b.WriteString("`\n\n")
	b.WriteString("## 上传输入\n\n")
	if len(files) == 0 {
		b.WriteString("- 无上传文件；`project/` 为空项目。\n")
	} else {
		for _, file := range files {
			b.WriteString("- `")
			b.WriteString(strings.TrimSpace(file.Name))
			b.WriteString("`，")
			b.WriteString(strconv.Itoa(len(file.Raw)))
			b.WriteString(" bytes\n")
		}
	}
	b.WriteString("\n## 当前验收标准\n\n")
	b.WriteString("- 必须围绕原始用户需求推进，不得静默更换目标。\n")
	b.WriteString("- 构建通过只是证据之一；不能把含占位符的骨架误报为完整证明。\n")
	b.WriteString("- 若用户后续放宽或收紧要求，追加 `proof-harness/证明决策/NNN-*.md`，并保留原始目标。\n\n")
	b.WriteString("## 默认禁止事项\n\n")
	b.WriteString("- 不得弱化定理、引理、终止性义务或原始语义。\n")
	b.WriteString("- 不得未经用户明确允许新增 `Axiom`、`Parameter`、`Conjecture`、`Admitted`、`admit`、`sorry`、`quick_and_dirty`、`unsafe`、`TODO` 或占位实现。\n")
	b.WriteString("- 不得把证明义务转移成 helper-only 目标后宣称原目标完成。\n")
	return b.String()
}

func formalProofObligationsMD(payload protocol.OrchestrationStartPayload, result formalProofHarnessResult) string {
	return "# 证明义务\n\n" +
		"<!-- harness: obligation_version=1 -->\n\n" +
		"## 目标义务\n\n" +
		"- 待识别。第一轮 CLI 应从用户需求和 `project/` 文件中抽取目标 theorem/lemma/fact/termination obligation。\n\n" +
		"## 已解决义务\n\n" +
		"- 暂无。\n\n" +
		"## 未解决义务\n\n" +
		"- 原始用户需求尚未完成。\n\n" +
		"## 当前阻塞点\n\n" +
		"- 暂无。后续 CLI 应在遇到 proof assistant 错误、缺少定义、终止性度量失败或审计失败时更新这里。\n\n" +
		"## 最近验证摘要\n\n" +
		"- 尚未运行 proof 检查。修改 `project/` 后运行 `./proof-harness/check.sh --all`。\n\n" +
		"## 备注\n\n" +
		"- 检测到的证明系统：`" + result.Assistant + "`。\n" +
		"- 本文件必须随证明文件变化持续更新。\n"
}

func formalProofImpactMD() string {
	var b strings.Builder
	b.WriteString("# 变更影响\n\n")
	b.WriteString("<!-- harness: impact_version=1 -->\n\n")
	b.WriteString("## 通用规则\n\n")
	b.WriteString("| 如果修改 | 必须同步 |\n")
	b.WriteString("| --- | --- |\n")
	b.WriteString("| `project/` 下任意证明源码 | 更新 `proof-harness/证明义务.md`，运行 `./proof-harness/check.sh --harness` |\n")
	b.WriteString("| 定理、引理、函数定义、模型语义 | 追加 `proof-harness/证明决策/NNN-*.md`，说明是否改变原始目标 |\n")
	b.WriteString("| 构建配置，例如 `ROOT`、`_CoqProject`、`Makefile`、`lakefile.*` | 说明原因并运行对应构建检查 |\n")
	b.WriteString("| 用户放宽或收紧验收标准 | 在 `proof-harness/followups/` 和 `proof-harness/证明决策/` 记录，不覆盖原始需求 |\n")
	b.WriteString("| 发现 `sorry`、`Admitted`、`Axiom` 等风险 | 在最终结论和 `proof-harness/证明义务.md` 中明确标记未完成或不可信 |\n\n")
	b.WriteString("## Isabelle\n\n")
	b.WriteString("- 修改 `.thy` 或 `ROOT` 后，优先运行 `isabelle build -D project` 或等价命令。\n")
	b.WriteString("- 扫描 `sorry`、`quick_and_dirty`、`oops`、`admit`。\n")
	b.WriteString("- 能够定位目标 fact 时，执行 `thm_oracles` 或等价 oracle 审计。\n\n")
	b.WriteString("## Coq / Rocq\n\n")
	b.WriteString("- 修改 `.v`、`_CoqProject` 或 `Makefile` 后，运行 `make`、`coqc` 或等价构建。\n")
	b.WriteString("- 扫描 `Axiom`、`Parameter`、`Conjecture`、`Admitted`、`admit`、`Abort`。\n")
	b.WriteString("- 能够定位目标 theorem 时，运行 `Print Assumptions` 或等价依赖审计。\n\n")
	b.WriteString("## Lean4\n\n")
	b.WriteString("- 修改 `.lean` 或 `lakefile.*` 后，运行 `lake build` 或等价构建。\n")
	b.WriteString("- 扫描 `sorry`、`axiom`、`admit`、`unsafe`。\n")
	b.WriteString("- 能够定位目标 theorem 时，运行 `#print axioms` 或等价审计。\n")
	return b.String()
}

func formalProofChecksMD() string {
	return `# 检查说明

统一入口：

` + "```bash" + `
./proof-harness/check.sh --harness
./proof-harness/check.sh --proof
./proof-harness/check.sh --all
` + "```" + `

` + "`--harness`" + ` 检查 harness 文件、版本标记、决策记录和证明义务同步状态。  
` + "`--proof`" + ` 根据项目类型运行构建和风险扫描。  
` + "`--all`" + ` 同时运行两者。

如果本机缺少 Isabelle、Coq/Rocq 或 Lean4 工具，脚本会给出跳过说明。CLI agent 必须在最终结论中报告缺少工具导致的证据不足。
`
}

func formalProofInitialDecisionMD(payload protocol.OrchestrationStartPayload, result formalProofHarnessResult) string {
	return "# 初始任务决策\n\n" +
		"<!-- harness: decision=000 -->\n\n" +
		"## 决策\n\n" +
		"- 为本次用户证明任务创建持久 Proof Run 壳子。\n" +
		"- 后续所有 CLI 轮次在本目录下工作，并读取 `proof-harness/` 中的中文约束文档。\n" +
		"- Bootstrap 不占用用户设置的正式 CLI 轮数。\n\n" +
		"## 初始上下文\n\n" +
		"- 证明系统检测结果：`" + result.Assistant + "`。\n" +
		"- 用户设置模式：`" + payload.Mode + "`。\n" +
		"- 用户设置最大轮数：`" + strconv.Itoa(payload.MaxTurns) + "`。\n\n" +
		"## 非目标\n\n" +
		"- Bridge 不直接判定证明语义正确性。\n" +
		"- 本决策不新增隐藏 verifier 或 remediation 轮。\n"
}

func formalProofStateYAML(payload protocol.OrchestrationStartPayload, result formalProofHarnessResult) string {
	var b strings.Builder
	projectFiles := formalProofStateProjectFiles(result)
	b.WriteString("harness_version: ")
	b.WriteString(strconv.Itoa(formalProofHarnessStateVersion))
	b.WriteString("\nrun_id: ")
	b.WriteString(yamlQuote(payload.RunID))
	b.WriteString("\nproject_root: ")
	b.WriteString(yamlQuote("project"))
	b.WriteString("\nharness_root: ")
	b.WriteString(yamlQuote("proof-harness"))
	b.WriteString("\nassistant: ")
	b.WriteString(yamlQuote(result.Assistant))
	b.WriteString("\nphase: ")
	b.WriteString(yamlQuote("full-proof"))
	b.WriteString("\ntask_version: 1\nobligation_version: 1\nimpact_version: 1\nlatest_decision: ")
	b.WriteString(yamlQuote("000-初始任务"))
	b.WriteString("\nlast_prompt_seq: ")
	b.WriteString(strconv.FormatInt(payload.PromptSeq, 10))
	b.WriteString("\ncreated_at: ")
	b.WriteString(yamlQuote(time.Now().UTC().Format(time.RFC3339)))
	b.WriteString("\nrun_dir: ")
	b.WriteString(yamlQuote(result.RunDir))
	b.WriteString("\nproject_dir: ")
	b.WriteString(yamlQuote(result.ProjectDir))
	b.WriteString("\nrequired_checks:\n")
	b.WriteString("  - ./proof-harness/check.sh --harness\n")
	b.WriteString("  - ./proof-harness/check.sh --proof\n")
	if len(projectFiles) == 0 {
		b.WriteString("uploaded_files: []\n")
	} else {
		b.WriteString("uploaded_files:\n")
		for _, file := range projectFiles {
			b.WriteString("  - ")
			b.WriteString(yamlQuote(file))
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func formalProofStateProjectFiles(result formalProofHarnessResult) []string {
	seen := map[string]bool{}
	var out []string
	for _, file := range result.Copied {
		if !seen[file] {
			seen[file] = true
			out = append(out, file)
		}
	}
	for _, file := range result.Extracted {
		if !seen[file] {
			seen[file] = true
			out = append(out, file)
		}
	}
	if len(out) > 0 {
		sort.Strings(out)
		return out
	}
	_ = filepath.WalkDir(result.ProjectDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(result.ProjectDir, path)
		if err != nil || rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if !seen[rel] {
			seen[rel] = true
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func yamlQuote(value string) string {
	return strconv.Quote(strings.TrimSpace(value))
}

func formalProofCheckScript() string {
	return `#!/usr/bin/env bash
set -euo pipefail

mode="${1:---all}"
run_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
harness_dir="$run_dir/proof-harness"
project_dir="$run_dir/project"

err=0
warn() { printf 'WARN: %s\n' "$*" >&2; }
fail() { printf 'ERROR: %s\n' "$*" >&2; err=$((err + 1)); }
ok() { printf 'OK: %s\n' "$*"; }

run_harness() {
  printf '== proof harness sync ==\n'
  required=(
    "$run_dir/AGENTS.md"
    "$run_dir/CLAUDE.md"
    "$harness_dir/任务说明.md"
    "$harness_dir/证明义务.md"
    "$harness_dir/变更影响.md"
    "$harness_dir/状态.yaml"
    "$harness_dir/检查说明.md"
    "$harness_dir/check.sh"
  )
  for path in "${required[@]}"; do
    [[ -f "$path" ]] && ok "$path" || fail "missing required harness file: $path"
  done
  grep -q 'task_version=' "$harness_dir/任务说明.md" 2>/dev/null || fail "任务说明.md missing task_version marker"
  grep -q 'obligation_version=' "$harness_dir/证明义务.md" 2>/dev/null || fail "证明义务.md missing obligation_version marker"
  grep -q 'impact_version=' "$harness_dir/变更影响.md" 2>/dev/null || fail "变更影响.md missing impact_version marker"
  grep -q '^assistant:' "$harness_dir/状态.yaml" 2>/dev/null || fail "状态.yaml missing assistant field"
  compgen -G "$harness_dir/证明决策/*.md" >/dev/null || fail "no proof decision files found"
  if find "$project_dir" -type f \( -name '*.thy' -o -name '*.v' -o -name '*.lean' \) | grep -q .; then
    grep -Eq '目标义务|未解决义务|最近验证摘要' "$harness_dir/证明义务.md" || fail "证明义务.md missing obligation sections"
  fi
  if command -v git >/dev/null 2>&1 && git -C "$run_dir" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    if git -C "$run_dir" status --short -- project | grep -q .; then
      warn "project files changed; ensure 证明义务.md and evidence are updated"
    fi
  fi
  if (( err > 0 )); then
    return 1
  fi
}

scan_forbidden() {
  printf '\n== shortcut scan ==\n'
  if command -v rg >/dev/null 2>&1; then
    rg -n 'sorry|quick_and_dirty|oops|admit|Admitted|Axiom|Parameter|Conjecture|Abort|TODO|placeholder|unsafe' "$project_dir" || true
  else
    grep -RInE 'sorry|quick_and_dirty|oops|admit|Admitted|Axiom|Parameter|Conjecture|Abort|TODO|placeholder|unsafe' "$project_dir" || true
  fi
}

run_proof() {
  printf '== proof checks ==\n'
  if [[ -f "$project_dir/ROOT" ]] || find "$project_dir" -name '*.thy' | grep -q .; then
    if command -v isabelle >/dev/null 2>&1; then
      (cd "$run_dir" && isabelle build -D project)
    else
      warn "isabelle not found; build check skipped"
    fi
    scan_forbidden
    return 0
  fi
  if [[ -f "$project_dir/_CoqProject" ]] || find "$project_dir" -name '*.v' | grep -q .; then
    if [[ -f "$project_dir/Makefile" ]] && command -v make >/dev/null 2>&1; then
      (cd "$project_dir" && make)
    elif command -v coqc >/dev/null 2>&1; then
      find "$project_dir" -name '*.v' -print0 | xargs -0 -n1 coqc
    else
      warn "make/coqc not found; Coq build check skipped"
    fi
    scan_forbidden
    return 0
  fi
  if [[ -f "$project_dir/lakefile.lean" || -f "$project_dir/lakefile.toml" ]] || find "$project_dir" -name '*.lean' | grep -q .; then
    if command -v lake >/dev/null 2>&1; then
      (cd "$project_dir" && lake build)
    else
      warn "lake not found; Lean4 build check skipped"
    fi
    scan_forbidden
    return 0
  fi
  warn "unknown proof assistant; running generic shortcut scan only"
  scan_forbidden
}

case "$mode" in
  --harness) run_harness ;;
  --proof) run_proof ;;
  --all) run_harness; run_proof ;;
  *) printf 'usage: %s [--harness|--proof|--all]\n' "$0" >&2; exit 2 ;;
esac

if (( err > 0 )); then
  exit 1
fi
`
}

func formalProofHarnessSyncReport(runDir string) (string, bool) {
	harnessDir := filepath.Join(runDir, formalProofHarnessDirName)
	projectDir := filepath.Join(runDir, "project")
	var issues []string
	required := []string{
		filepath.Join(runDir, "AGENTS.md"),
		filepath.Join(runDir, "CLAUDE.md"),
		filepath.Join(harnessDir, "任务说明.md"),
		filepath.Join(harnessDir, "证明义务.md"),
		filepath.Join(harnessDir, "变更影响.md"),
		filepath.Join(harnessDir, "状态.yaml"),
		filepath.Join(harnessDir, "检查说明.md"),
		filepath.Join(harnessDir, "check.sh"),
	}
	for _, path := range required {
		if _, err := os.Stat(path); err != nil {
			issues = append(issues, "缺少必要 harness 文件: "+path)
		}
	}
	if !fileContains(filepath.Join(harnessDir, "任务说明.md"), "task_version=") {
		issues = append(issues, "任务说明.md 缺少 task_version 标记")
	}
	if !fileContains(filepath.Join(harnessDir, "证明义务.md"), "obligation_version=") {
		issues = append(issues, "证明义务.md 缺少 obligation_version 标记")
	}
	if !fileContains(filepath.Join(harnessDir, "变更影响.md"), "impact_version=") {
		issues = append(issues, "变更影响.md 缺少 impact_version 标记")
	}
	if !fileContains(filepath.Join(harnessDir, "状态.yaml"), "assistant:") {
		issues = append(issues, "状态.yaml 缺少 assistant 字段")
	}
	matches, err := filepath.Glob(filepath.Join(harnessDir, "证明决策", "*.md"))
	if err != nil || len(matches) == 0 {
		issues = append(issues, "缺少证明决策记录")
	}
	if formalProofProjectHasSources(projectDir) {
		obligationsPath := filepath.Join(harnessDir, "证明义务.md")
		if !fileContains(obligationsPath, "目标义务") || !fileContains(obligationsPath, "未解决义务") || !fileContains(obligationsPath, "最近验证摘要") {
			issues = append(issues, "证明义务.md 缺少目标义务、未解决义务或最近验证摘要章节")
		}
	}
	if len(issues) == 0 {
		return "Proof Harness 同步检查通过。", true
	}
	return "Proof Harness 同步检查未通过：\n- " + strings.Join(issues, "\n- "), false
}

func formalProofProjectHasSources(projectDir string) bool {
	found := false
	_ = filepath.WalkDir(projectDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || found {
			return nil
		}
		name := strings.ToLower(d.Name())
		if strings.HasSuffix(name, ".thy") || strings.HasSuffix(name, ".v") || strings.HasSuffix(name, ".lean") {
			found = true
		}
		return nil
	})
	return found
}

func fileContains(path, needle string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(raw), needle)
}

func (m *OrchestrationManager) emitFormalProofHarnessSync(runID, turnID, runDir string) string {
	content, ok := formalProofHarnessSyncReport(runDir)
	severity := "info"
	if !ok {
		severity = "warning"
	}
	m.emit(runID, protocol.OrchestrationEventPayload{
		Kind:     "turn.delta",
		Source:   "bridge",
		Severity: severity,
		Role:     "harness",
		CLI:      "bridge",
		TurnID:   turnID,
		Content:  content,
		BridgeNoteData: &protocol.BridgeNoteData{
			Category: "formal-proof-harness-sync",
		},
		Data: map[string]any{
			"category": "formal-proof-harness-sync",
			"ok":       ok,
			"cwd":      runDir,
		},
	})
	if ok {
		return ""
	}
	return content
}

func appendFormalProofHarnessSyncContext(contextSummary, syncNote string) string {
	syncNote = strings.TrimSpace(syncNote)
	if syncNote == "" {
		return contextSummary
	}
	contextSummary = strings.TrimSpace(contextSummary)
	addition := "Latest Proof Harness synchronization issue:\n" + syncNote + "\nResolve this harness drift before claiming proof completion."
	if contextSummary == "" {
		return addition
	}
	return contextSummary + "\n\n" + addition
}
