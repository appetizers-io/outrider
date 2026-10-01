//go:build windows

package guard

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// execReal runs the real binary and exits with its status (Windows has no exec).
func execReal(real string, args, env []string, stderr io.Writer) int {
	cmd := exec.Command(real, args...)
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "llm-review-agent guard: cannot run %s: %v\n", real, err)
		return 1
	}
	return 0
}
