// Package cli holds the cobra commands: the watcher (root), `config …` and
// the hidden `session run`. It parses flags and hands over to the packages
// that do the work.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/appetizers-io/llm-review-agent/internal/session"
	"github.com/appetizers-io/llm-review-agent/internal/watch"
)

// Version is set at release time.
var Version = "dev"

// Execute runs the command line and returns the exit status.
func Execute(ctx context.Context) int {
	return run(ctx, os.Args[1:], watch.Host(), os.Stdout, os.Stderr)
}

func run(ctx context.Context, args []string, d watch.Deps, stdout, stderr io.Writer) int {
	root := newRoot(d, stdout, stderr)
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	var code exitCode
	switch {
	case err == nil:
		return 0
	case errors.As(err, &code):
		return int(code)
	}
	_, _ = fmt.Fprintln(stderr, err)
	return 1
}

// exitCode ends the command with this status, its message already printed.
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

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

func newRoot(d watch.Deps, stdout, stderr io.Writer) *cobra.Command {
	f := &flags{}
	root := &cobra.Command{
		Use:   "llm-review-agent",
		Short: "Watch GitHub PR activity and hand actionable PRs to a local coding agent",
		Long: "Polls your GitHub notifications and opens a local interactive coding agent " +
			"(Codex or Claude Code) for pull requests that need your attention.\n\n" +
			"Settings come from the config file (see `llm-review-agent config --help`); these flags override it.",
		Version:       Version,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			log, err := newLogger(stderr, f.logLevel, f.logFormat)
			if err != nil {
				return err
			}
			s, err := f.settings(cmd)
			if err != nil {
				return err
			}
			return watch.Run(cmd.Context(), s, d, log)
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
