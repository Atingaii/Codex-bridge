//go:build !linux

package bridge

import (
	"errors"
)

func RunStrictWorkspaceSandbox(args []string) error {
	return errors.New("strict workspace isolation requires Linux Landlock; use review-required or auto-execute on this operating system")
}

func ValidateStrictWorkspaceSupport() error {
	return errors.New("strict workspace isolation requires Linux Landlock; use review-required or auto-execute on this operating system")
}
