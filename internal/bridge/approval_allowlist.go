package bridge

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"
)

var errUnsafeApprovalCommand = errors.New("command is not eligible for automatic approval")

func isProofCommandAutoApprovable(command, cwd string) bool {
	argv, err := splitSimpleCommand(command)
	if err != nil || len(argv) < 2 {
		return false
	}
	root, err := canonicalWorkspace(cwd)
	if err != nil {
		return false
	}
	executable, ok := trustedApprovalExecutable(root, argv[0])
	if !ok {
		return false
	}
	switch executable {
	case "cat":
		return validateCatArgs(root, argv[1:])
	case "coqc":
		return validateCoqArgs(root, argv[1:], false)
	case "coqtop":
		return validateCoqArgs(root, argv[1:], true)
	default:
		return false
	}
}

func trustedApprovalExecutable(root, name string) (string, bool) {
	if name != "cat" && name != "coqc" && name != "coqtop" {
		return "", false
	}
	resolved, err := exec.LookPath(name)
	if err != nil {
		return "", false
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return "", false
	}
	if evaluated, evalErr := filepath.EvalSymlinks(abs); evalErr == nil {
		abs = evaluated
	}
	info, err := os.Stat(abs)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 || pathWithin(root, abs) {
		return "", false
	}
	return name, true
}

// splitSimpleCommand deliberately implements less than a shell: one argv only,
// with simple quotes for paths containing spaces and no expansion or operators.
func splitSimpleCommand(command string) ([]string, error) {
	if strings.TrimSpace(command) == "" {
		return nil, errUnsafeApprovalCommand
	}
	for _, r := range command {
		if r == 0 || r == '\n' || r == '\r' || unicode.IsControl(r) || strings.ContainsRune(";&|<>`$\\*?[]{}()#!~", r) {
			return nil, errUnsafeApprovalCommand
		}
	}
	var argv []string
	var token strings.Builder
	var quote rune
	haveToken := false
	flush := func() {
		if haveToken {
			argv = append(argv, token.String())
			token.Reset()
			haveToken = false
		}
	}
	for _, r := range command {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				token.WriteRune(r)
			}
			haveToken = true
		case r == '\'' || r == '"':
			quote = r
			haveToken = true
		case unicode.IsSpace(r):
			flush()
		default:
			token.WriteRune(r)
			haveToken = true
		}
	}
	if quote != 0 {
		return nil, errUnsafeApprovalCommand
	}
	flush()
	if len(argv) == 0 || argv[0] == "" {
		return nil, errUnsafeApprovalCommand
	}
	return argv, nil
}

func canonicalWorkspace(cwd string) (string, error) {
	if strings.TrimSpace(cwd) == "" {
		return "", errUnsafeApprovalCommand
	}
	abs, err := filepath.Abs(expandHome(cwd))
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", errUnsafeApprovalCommand
	}
	return filepath.Clean(resolved), nil
}

func workspacePath(root, raw string, mustExist, regular bool) bool {
	if raw == "" || raw == "-" || strings.HasPrefix(raw, "@") {
		return false
	}
	path := raw
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil || !pathWithin(root, abs) {
		return false
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if mustExist {
			return false
		}
		parent, parentErr := filepath.EvalSymlinks(filepath.Dir(abs))
		return parentErr == nil && pathWithin(root, parent)
	}
	if !pathWithin(root, resolved) {
		return false
	}
	if !mustExist && !regular {
		return true
	}
	info, err := os.Stat(resolved)
	return err == nil && (!regular || info.Mode().IsRegular())
}

func pathWithin(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func validateCatArgs(root string, args []string) bool {
	files := 0
	options := true
	for _, arg := range args {
		if options && arg == "--" {
			options = false
			continue
		}
		if options && strings.HasPrefix(arg, "--") {
			switch arg {
			case "--number", "--number-nonblank", "--squeeze-blank", "--show-tabs", "--show-ends", "--show-all":
				continue
			default:
				return false
			}
		}
		if options && strings.HasPrefix(arg, "-") {
			if len(arg) < 2 || strings.Trim(arg[1:], "AbEnsTvet") != "" {
				return false
			}
			continue
		}
		options = false
		if !workspacePath(root, arg, true, true) {
			return false
		}
		files++
	}
	return files > 0
}

func validateCoqArgs(root string, args []string, top bool) bool {
	sources := 0
	batch := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-q", "-quiet", "-time", "-emacs", "-noinit", "-nois", "-noglob":
			continue
		case "-batch":
			batch = true
			continue
		case "-Q", "-R":
			if i+2 >= len(args) || !workspacePath(root, args[i+1], true, false) || strings.HasPrefix(args[i+2], "-") {
				return false
			}
			i += 2
			continue
		case "-I":
			if i+1 >= len(args) || !workspacePath(root, args[i+1], true, false) {
				return false
			}
			i++
			continue
		case "-o":
			if top || i+1 >= len(args) || !workspacePath(root, args[i+1], false, false) {
				return false
			}
			i++
			continue
		case "-w":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return false
			}
			i++
			continue
		case "-l", "-load-vernac-source":
			if !top || i+1 >= len(args) || !strings.HasSuffix(strings.ToLower(args[i+1]), ".v") || !workspacePath(root, args[i+1], true, true) {
				return false
			}
			i++
			sources++
			continue
		}
		if top || strings.HasPrefix(arg, "-") || !strings.HasSuffix(strings.ToLower(arg), ".v") || !workspacePath(root, arg, true, true) {
			return false
		}
		sources++
	}
	return sources > 0 && (!top || batch)
}
