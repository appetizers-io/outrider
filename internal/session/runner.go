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
	"runtime"
	"strings"
	"syscall"
)

// watcherEnvFile holds the watcher's environment (KEY=value lines as a JSON
// array) for a runner a terminal app starts: the app gives it its own
// environment, without what the owner's shell init exports (API keys the
// tool gate needs, proxies). The runner deletes it once read.
const watcherEnvFile = "watcher-env.json"

// terminalVars describe the terminal the runner is in, not the watcher's.
var terminalVars = map[string]bool{
	"TERM": true, "TERM_PROGRAM": true, "TERM_PROGRAM_VERSION": true, "TERM_SESSION_ID": true,
	"COLORTERM": true, "TMUX": true, "TMUX_PANE": true, "ITERM_SESSION_ID": true, "ITERM_PROFILE": true,
	"LC_TERMINAL": true, "LC_TERMINAL_VERSION": true, "WINDOWID": true, "KITTY_WINDOW_ID": true,
	"WEZTERM_PANE": true, "WT_SESSION": true, "SHLVL": true, "PWD": true, "OLDPWD": true, "_": true,
}

// watcherEnv reads and deletes the watcher's environment; nil without one
// (tmux). Terminal variables and the watcher's own OUTRIDER_* are dropped.
func watcherEnv(dir string) ([]string, error) {
	p := filepath.Join(dir, watcherEnvFile)
	raw, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	_ = os.Remove(p)
	if err != nil {
		return nil, fmt.Errorf("read watcher environment: %w", err)
	}
	var all []string
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, fmt.Errorf("invalid watcher environment: %w", err) // no values: they may be secrets
	}
	var env []string
	for _, kv := range all {
		k, _, _ := strings.Cut(kv, "=")
		if k != "" && !terminalVars[k] && !strings.HasPrefix(k, "OUTRIDER_") {
			env = append(env, kv)
		}
	}
	return env, nil
}

// lookup is os.Getenv for env, where the last entry for a key wins.
func lookup(env []string, key string) string {
	for i := len(env) - 1; i >= 0; i-- {
		k, v, _ := strings.Cut(env[i], "=")
		if k == key || runtime.GOOS == "windows" && strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

// Run is `outrider session run <dir>`: it stamps its pid into the
// lock, runs the agent in the worktree with the guards first on PATH, prints
// the exit status, waits for Enter and removes the lock.
func Run(dir string, stdin io.Reader, stdout io.Writer) int {
	watcher, err := watcherEnv(dir)
	if err != nil {
		_, _ = fmt.Fprintf(stdout, "outrider: %v\n", err)
		return 1
	}
	raw, err := os.ReadFile(filepath.Join(dir, "session.json"))
	if err != nil {
		_, _ = fmt.Fprintf(stdout, "outrider: cannot read session: %v\n", err)
		return 1
	}
	var spec Spec
	if err := json.Unmarshal(raw, &spec); err != nil {
		_, _ = fmt.Fprintf(stdout, "outrider: invalid session %s: %v\n", dir, err)
		return 1
	}
	meta := spec.Meta
	meta.PID = new(os.Getpid())
	if err := writeJSON(spec.Lock, meta); err != nil {
		_, _ = fmt.Fprintf(stdout, "outrider: %v\n", err)
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

	if spec.Isolation != nil {
		return runIsolated(spec, watcher, stdin, stdout)
	}

	prompt, err := os.ReadFile(spec.PromptFile)
	if err != nil {
		_, _ = fmt.Fprintf(stdout, "outrider: cannot read prompt: %v\n", err)
		return 1
	}
	// the watcher's environment over the terminal's; the session's own
	// variables and the guards on PATH over both
	env := append(os.Environ(), watcher...)
	for k, v := range spec.Env {
		env = append(env, k+"="+v)
	}
	env = append(env, "PATH="+spec.Path+string(os.PathListSeparator)+lookup(env, "PATH"))

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
			_, _ = fmt.Fprintf(stdout, "outrider: cannot run %s: %v\n", spec.Agent[0], err)
		}
	}
	_ = os.Remove(spec.Lock)
	_, _ = fmt.Fprintf(stdout, "\nagent exited: %d\npress Enter to close\n", status)
	_, _ = bufio.NewReader(stdin).ReadString('\n')
	return status
}
