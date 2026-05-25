//go:build !linux && !darwin

package process

import "syscall"

func newSysProcAttr() *syscall.SysProcAttr { return nil }
