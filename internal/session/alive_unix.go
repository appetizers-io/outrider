//go:build !windows

package session

import (
	"errors"
	"math"
	"syscall"
)

// alive tells whether a process with this pid exists.
func alive(pid int) bool {
	if pid <= 0 || pid > math.MaxInt32 { // 0 and negatives would signal process groups
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
