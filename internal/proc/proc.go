// Package proc runs external commands (gh, git, tmux, classifiers).
package proc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// DefaultTimeout bounds a command that sets no Timeout.
const DefaultTimeout = 120 * time.Second

// Cmd is a command to run.
type Cmd struct {
	Args    []string
	Dir     string        // working directory; "" is the current one
	Stdin   string        // written to the command's stdin
	Timeout time.Duration // 0: DefaultTimeout
}

// Result is what a command printed and how it exited.
type Result struct {
	Stdout, Stderr string
	Code           int // -1: it did not run to the end
}

// Runner runs commands; tests swap in a fake.
type Runner func(ctx context.Context, c Cmd) (Result, error)

// Error is a command that failed to start, exited non-zero or timed out.
type Error struct {
	Args   []string
	Code   int
	Stderr string
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = fmt.Sprintf("exit status %d", e.Code)
	}
	return fmt.Sprintf("%s: %s", strings.Join(e.Args, " "), msg)
}

// Exec runs c. Every failure, a hung command included, is an *Error, so
// callers that tolerate a failed command also tolerate a hung one.
func Exec(ctx context.Context, c Cmd) (Result, error) {
	timeout := c.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Args[0], c.Args[1:]...)
	cmd.Dir = c.Dir
	if c.Stdin != "" {
		cmd.Stdin = strings.NewReader(c.Stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	res := Result{Stdout: stdout.String(), Stderr: stderr.String(), Code: cmd.ProcessState.ExitCode()}
	if err == nil {
		return res, nil
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		res.Code = -1
		return res, &Error{Args: c.Args, Code: -1, Stderr: fmt.Sprintf("timed out after %s", timeout)}
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		res.Code = -1
		return res, &Error{Args: c.Args, Code: -1, Stderr: err.Error()}
	}
	return res, &Error{Args: c.Args, Code: res.Code, Stderr: res.Stderr}
}
