//go:build !windows

package guard

import (
	"fmt"
	"io"
	"syscall"
)

// execReal replaces this process with the real binary.
func execReal(real string, args, env []string, stderr io.Writer) int {
	err := syscall.Exec(real, append([]string{real}, args...), env)
	_, _ = fmt.Fprintf(stderr, "llm-review-agent guard: cannot run %s: %v\n", real, err)
	return 1
}
