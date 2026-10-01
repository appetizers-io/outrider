//go:build !windows

package session

import (
	"errors"
	"syscall"
)

// alive tells whether a process with this pid exists.
func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
