//go:build windows

package session

import (
	"errors"
	"math"
	"syscall"
)

const synchronize = 0x00100000

// alive tells whether a process with this pid is still running. Only a
// confirmed exit counts as dead; a process we may not open is alive.
func alive(pid int) bool {
	if pid <= 0 || uint64(pid) > math.MaxUint32 {
		return false
	}
	h, err := syscall.OpenProcess(synchronize, false, uint32(pid))
	if err != nil {
		return errors.Is(err, syscall.ERROR_ACCESS_DENIED)
	}
	defer func() { _ = syscall.CloseHandle(h) }()
	ev, err := syscall.WaitForSingleObject(h, 0)
	return err != nil || ev != syscall.WAIT_OBJECT_0
}
