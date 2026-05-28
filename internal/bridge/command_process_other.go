//go:build !linux

package bridge

import "syscall"

func managedCommandSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
