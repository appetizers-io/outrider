// Command llm-review-agent polls your GitHub notifications and opens a local
// interactive coding agent (Codex or Claude Code) for pull requests that need
// your attention.
//
// The binary is multi-call: run as `gh` or `git` (the guards on an agent
// session's PATH link to it) it is the gh or git guard.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/appetizers-io/llm-review-agent/internal/approve"
	"github.com/appetizers-io/llm-review-agent/internal/guard"
	"github.com/appetizers-io/llm-review-agent/internal/proc"
	"github.com/appetizers-io/llm-review-agent/internal/session"
)

// version is set at release time.
var version = "dev"

func main() {
	os.Exit(run(os.Args, os.Stdout, os.Stderr))
}

func ask(title, body, ok string) bool {
	approved, _ := approve.Ask(context.Background(), title, body, ok)
	return approved
}

func run(args []string, stdout, stderr io.Writer) int {
	name := strings.TrimSuffix(strings.ToLower(filepath.Base(args[0])), ".exe")
	switch name {
	case "gh":
		return guard.RunGH(args[1:], os.Getenv, ask, stderr)
	case "git":
		return guard.RunGit(args[1:], os.Getenv, ask, stderr)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	root := newRoot(host(), stdout, stderr)
	root.SetArgs(args[1:])
	if err := root.ExecuteContext(ctx); err != nil {
		var code exitCode
		if !errors.As(err, &code) {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		return int(code)
	}
	return 0
}

// exitCode ends the command with this status, its message already printed.
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// deps are what the watcher needs from the machine; tests swap them.
type deps struct {
	Run      proc.Runner
	LookPath func(string) (string, error)
	Getenv   func(string) string
	GOOS     string
	Home     string
	Self     string
	Sleep    func(ctx context.Context, d time.Duration) error
}

func host() deps {
	home, _ := os.UserHomeDir()
	self, err := os.Executable()
	if err == nil {
		if resolved, err := filepath.EvalSymlinks(self); err == nil {
			self = resolved
		}
	}
	return deps{
		Run: proc.Exec, LookPath: exec.LookPath, Getenv: os.Getenv, GOOS: runtime.GOOS,
		Home: home, Self: self, Sleep: sleep,
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("sleep: %w", ctx.Err())
	case <-t.C:
		return nil
	}
}

func newLogger(w io.Writer, level, format string) (*slog.Logger, error) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("--log-level must be debug, info, warn or error: %w", err)
	}
	opts := &slog.HandlerOptions{Level: lvl}
	switch format {
	case "text":
		return slog.New(slog.NewTextHandler(w, opts)), nil
	case "json":
		return slog.New(slog.NewJSONHandler(w, opts)), nil
	}
	return nil, fmt.Errorf("--log-format must be text or json, not %q", format)
}

func newRoot(d deps, stdout, stderr io.Writer) *cobra.Command {
	f := &flags{}
	root := &cobra.Command{
		Use:   "llm-review-agent",
		Short: "Watch GitHub PR activity and hand actionable PRs to a local coding agent",
		Long: "Polls your GitHub notifications and opens a local interactive coding agent " +
			"(Codex or Claude Code) for pull requests that need your attention.\n\n" +
			"Settings come from the config file (see `llm-review-agent config --help`); these flags override it.",
		Version:       version,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			log, err := newLogger(stderr, f.logLevel, f.logFormat)
			if err != nil {
				return err
			}
			s, err := settingsFrom(cmd, f)
			if err != nil {
				return err
			}
			return watch(cmd.Context(), s, d, log)
		},
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetOut(stdout)
	root.SetErr(stderr)
	f.register(root)
	root.AddCommand(configCmd(stdout, stderr), sessionCmd(stdout))
	return root
}

func sessionCmd(stdout io.Writer) *cobra.Command {
	cmd := &cobra.Command{Use: "session", Hidden: true, Short: "Internal: agent sessions"}
	cmd.AddCommand(&cobra.Command{
		Use:   "run DIR",
		Short: "Run the agent session prepared in DIR (started in a terminal or tmux)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if code := session.Run(args[0], os.Stdin, stdout); code != 0 {
				return exitCode(code)
			}
			return nil
		},
	})
	return cmd
}
