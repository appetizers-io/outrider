package watch

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/appetizers-io/outrider/internal/config"
	"github.com/appetizers-io/outrider/internal/github"
	"github.com/appetizers-io/outrider/internal/proc"
	"github.com/appetizers-io/outrider/internal/session"
)

func term(name string) session.Terminal { return session.Terminal{Name: name} }

func TestPickLauncher(t *testing.T) {
	d := Deps{Getenv: func(string) string { return "" }}
	d.Run = func(context.Context, proc.Cmd) (proc.Result, error) { return proc.Result{Stdout: "Aqua\n"}, nil }
	d.GOOS = "darwin"
	require.Equal(t, "terminal", pickLauncher(t.Context(), "auto", term("terminal-app"), d))
	d.Run = func(context.Context, proc.Cmd) (proc.Result, error) { return proc.Result{Stdout: "Background\n"}, nil }
	require.Equal(t, "tmux", pickLauncher(t.Context(), "auto", term("terminal-app"), d))
	d.GOOS = "windows"
	require.Equal(t, "terminal", pickLauncher(t.Context(), "auto", term("cmd"), d))
	d.GOOS = "linux"
	require.Equal(t, "tmux", pickLauncher(t.Context(), "auto", term("kitty"), d)) // no display
	d.Getenv = func(k string) string { return map[string]string{"WAYLAND_DISPLAY": "w"}[k] }
	require.Equal(t, "terminal", pickLauncher(t.Context(), "auto", term("kitty"), d))
	require.Equal(t, "tmux", pickLauncher(t.Context(), "auto", term(""), d)) // nothing found
	require.Equal(t, "tmux", pickLauncher(t.Context(), "tmux", term("kitty"), d))
}

func TestLauncherTools(t *testing.T) {
	d := Deps{GOOS: "linux", Self: "/bin/outrider"}
	for _, tc := range []struct {
		name, launcher, goos string
		term                 session.Terminal
		want                 []string
		err                  string
	}{
		{name: "tmux", launcher: "tmux", goos: "linux", want: []string{"tmux"}},
		{name: "tmux on windows", launcher: "tmux", goos: "windows", err: "tmux is not supported on Windows"},
		{name: "kitty", launcher: "terminal", goos: "linux", term: term("kitty"), want: []string{"kitty"}},
		{name: "iTerm2 via AppleScript", launcher: "terminal", goos: "darwin", term: term("iterm"), want: []string{"osascript"}},
		{name: "Terminal.app via open", launcher: "terminal", goos: "darwin", term: term("terminal-app"), want: []string{"open"}},
		{name: "no terminal", launcher: "terminal", goos: "linux", err: "no terminal found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := require.New(t)
			d.GOOS = tc.goos
			got, err := LauncherTools(tc.launcher, tc.term, d)
			if tc.err != "" {
				r.ErrorContains(err, tc.err)
				return
			}
			r.NoError(err)
			r.Equal(tc.want, got)
		})
	}
}

func TestSandboxMode(t *testing.T) {
	ro := config.ReadOnly
	for _, tc := range []struct {
		sandbox string
		others  *string
		want    string
		on      bool
	}{
		{sandbox: "off", want: "sandbox: off"},
		{sandbox: ro, want: "sandbox: read-only", on: true},
		{sandbox: "off", others: &ro, want: "sandbox: off; others' PRs: read-only", on: true},
		{sandbox: ro, others: new("off"), want: "sandbox: read-only; others' PRs: off", on: true},
	} {
		cfg := config.Default()
		cfg.Sandbox, cfg.OthersPRs.Sandbox = tc.sandbox, tc.others
		mode, on := SandboxMode(&cfg)
		require.Equal(t, tc.want, mode)
		require.Equal(t, tc.on, on)
	}
}

func TestOwner(t *testing.T) {
	cfg := config.Default()
	require.Equal(t, "Mona", Owner(&cfg, github.User{Login: "octocat", Name: "Mona Lisa"}))
	require.Equal(t, "octocat", Owner(&cfg, github.User{Login: "octocat"}))
	cfg.OwnerName = new("Matze")
	require.Equal(t, "Matze", Owner(&cfg, github.User{Login: "octocat", Name: "Mona Lisa"}))
}
