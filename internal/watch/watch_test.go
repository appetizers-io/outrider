package watch

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/appetizers-io/llm-review-agent/internal/proc"
	"github.com/appetizers-io/llm-review-agent/internal/session"
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
