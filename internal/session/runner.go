package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
)

// Run is `llm-review-agent session run <dir>`: it stamps its pid into the
// lock, runs the agent in the worktree with the guards first on PATH, prints
// the exit status, waits for Enter and removes the lock.
func Run(dir string, stdin io.Reader, stdout io.Writer) int {
	raw, err := os.ReadFile(filepath.Join(dir, "session.json"))
	if err != nil {
		_, _ = fmt.Fprintf(stdout, "llm-review-agent: cannot read session: %v\n", err)
		return 1
	}
	var spec Spec
	if err := json.Unmarshal(raw, &spec); err != nil {
		_, _ = fmt.Fprintf(stdout, "llm-review-agent: invalid session %s: %v\n", dir, err)
		return 1
	}
	meta := spec.Meta
	meta.PID = new(os.Getpid())
	if err := writeJSON(spec.Lock, meta); err != nil {
		_, _ = fmt.Fprintf(stdout, "llm-review-agent: %v\n", err)
	}
	defer func() { _ = os.Remove(spec.Lock) }()

	// Ctrl-C belongs to the agent; a closed window or kill ends the session.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)
	go func() {
		for s := range signals {
			if s != os.Interrupt {
				_ = os.Remove(spec.Lock)
				os.Exit(1)
			}
		}
	}()

	prompt, err := os.ReadFile(spec.PromptFile)
	if err != nil {
		_, _ = fmt.Fprintf(stdout, "llm-review-agent: cannot read prompt: %v\n", err)
		return 1
	}
	env := os.Environ()
	for k, v := range spec.Env {
		env = append(env, k+"="+v)
	}
	env = append(env, "PATH="+spec.Path+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, _ = fmt.Fprint(stdout, "\033[H\033[2J") // clear
	for _, line := range spec.Header {
		_, _ = fmt.Fprintln(stdout, line)
	}
	cmd := exec.Command(spec.Agent[0], append(spec.Agent[1:], string(prompt))...)
	cmd.Dir, cmd.Env = spec.Dir, env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	status := 0
	if err := cmd.Run(); err != nil {
		status = 1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			status = exitErr.ExitCode()
		} else {
			_, _ = fmt.Fprintf(stdout, "llm-review-agent: cannot run %s: %v\n", spec.Agent[0], err)
		}
	}
	_ = os.Remove(spec.Lock)
	_, _ = fmt.Fprintf(stdout, "\nagent exited: %d\npress Enter to close\n", status)
	_, _ = bufio.NewReader(stdin).ReadString('\n')
	return status
}
