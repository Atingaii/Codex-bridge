//go:build linux

package bridge

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

type repeatedPaths []string

func (p *repeatedPaths) String() string { return strings.Join(*p, string(os.PathListSeparator)) }
func (p *repeatedPaths) Set(value string) error {
	*p = append(*p, value)
	return nil
}

func RunStrictWorkspaceSandbox(args []string) error {
	fs := flag.NewFlagSet("sandbox-exec", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "writable workspace root")
	runtimeDir := fs.String("runtime", "", "writable private runtime directory")
	var readOnly repeatedPaths
	var state repeatedPaths
	fs.Var(&readOnly, "read-only", "read-only filesystem path (repeatable)")
	fs.Var(&state, "state", "writable CLI state path (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	target := fs.Args()
	if *workspace == "" || *runtimeDir == "" || len(target) == 0 {
		return errors.New("sandbox-exec requires --workspace, --runtime, and a target command after --")
	}
	if err := applyLandlockRules(*workspace, *runtimeDir, readOnly, state); err != nil {
		return fmt.Errorf("strict workspace isolation unavailable: %w", err)
	}
	path, err := resolveSandboxTarget(target[0])
	if err != nil {
		return err
	}
	return syscall.Exec(path, target, os.Environ())
}

func ValidateStrictWorkspaceSupport() error {
	abi, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION, 0, 0, 0)
	if errno != 0 {
		return fmt.Errorf("strict workspace requires Linux Landlock (kernel 5.13 or newer with Landlock enabled): %w", errno)
	}
	if abi < 3 {
		return fmt.Errorf("strict workspace requires Landlock ABI 3 or newer; kernel reported ABI %d", abi)
	}
	return nil
}

func resolveSandboxTarget(target string) (string, error) {
	if strings.ContainsRune(target, os.PathSeparator) {
		return target, nil
	}
	return lookPathWithoutStat(target)
}

func lookPathWithoutStat(target string) (string, error) {
	for _, dir := range filepathSplitList(os.Getenv("PATH")) {
		candidate := dir + string(os.PathSeparator) + target
		if unix.Access(candidate, unix.X_OK) == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("strict workspace target %q not found in PATH", target)
}

func filepathSplitList(value string) []string {
	parts := strings.Split(value, string(os.PathListSeparator))
	for index, part := range parts {
		if part == "" {
			parts[index] = "."
		}
	}
	return parts
}

func applyLandlockRules(workspace, runtimeDir string, readOnly, state []string) error {
	if err := ValidateStrictWorkspaceSupport(); err != nil {
		return err
	}
	abi, _, _ := unix.Syscall6(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION, 0, 0, 0)
	handled := uint64(unix.LANDLOCK_ACCESS_FS_EXECUTE |
		unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
		unix.LANDLOCK_ACCESS_FS_MAKE_CHAR |
		unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
		unix.LANDLOCK_ACCESS_FS_MAKE_REG |
		unix.LANDLOCK_ACCESS_FS_MAKE_SOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_FIFO |
		unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_SYM)
	if abi >= 2 {
		handled |= unix.LANDLOCK_ACCESS_FS_REFER
	}
	if abi >= 3 {
		handled |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	attr := unix.LandlockRulesetAttr{Access_fs: handled}
	rulesetFD, _, errno := unix.Syscall6(
		unix.SYS_LANDLOCK_CREATE_RULESET,
		uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0, 0, 0, 0,
	)
	if errno != 0 {
		return fmt.Errorf("create Landlock ruleset: %w", errno)
	}
	defer unix.Close(int(rulesetFD))
	readAccess := handled & (unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_READ_DIR)
	for _, path := range existingUniquePaths(readOnly) {
		if err := addLandlockPathRule(int(rulesetFD), path, readAccess); err != nil {
			return err
		}
	}
	for _, path := range existingUniquePaths(append([]string{workspace, runtimeDir}, state...)) {
		if err := addLandlockPathRule(int(rulesetFD), path, handled); err != nil {
			return err
		}
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("set no_new_privs: %w", err)
	}
	_, _, errno = unix.Syscall6(unix.SYS_LANDLOCK_RESTRICT_SELF, rulesetFD, 0, 0, 0, 0, 0)
	if errno != 0 {
		return fmt.Errorf("restrict process with Landlock: %w", errno)
	}
	return nil
}

func addLandlockPathRule(rulesetFD int, path string, access uint64) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect Landlock allowlist path %s: %w", path, err)
	}
	if !info.IsDir() {
		access &= unix.LANDLOCK_ACCESS_FS_EXECUTE |
			unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
			unix.LANDLOCK_ACCESS_FS_READ_FILE |
			unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	fd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open Landlock allowlist path %s: %w", path, err)
	}
	defer unix.Close(fd)
	attr := unix.LandlockPathBeneathAttr{Allowed_access: access, Parent_fd: int32(fd)}
	_, _, errno := unix.Syscall6(
		unix.SYS_LANDLOCK_ADD_RULE,
		uintptr(rulesetFD), uintptr(unix.LANDLOCK_RULE_PATH_BENEATH), uintptr(unsafe.Pointer(&attr)), 0, 0, 0,
	)
	if errno != 0 {
		return fmt.Errorf("add Landlock path %s: %w", path, errno)
	}
	return nil
}
