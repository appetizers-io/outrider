package session

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClaudePluginLaunch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review plugin")
	require.NoError(t, os.Mkdir(path, 0o700))
	l, _ := newLauncher(t, fmt.Sprintf("agent: claude\nclaude_plugin_dirs: [%q]\n", path))
	got := l.launched(t, "bob")
	require.Equal(t, []string{"--plugin-dir", path}, got.spec.Agent[1:3])
	require.Contains(t, got.spec.Agent, "--setting-sources")
	require.NotEmpty(t, got.deny())
}

func TestClaudePluginMissingRefusesLaunch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing")
	l, _ := newLauncher(t, fmt.Sprintf("agent: claude\nclaude_plugin_dirs: [%q]\n", path))
	require.False(t, l.Launch(t.Context(), Request{Repo: "o/r", N: 1, PR: pr("bob"), Trigger: "t"}))
}

func TestClaudePluginsInIsolation(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	path := filepath.Join(t.TempDir(), "review plugin")
	require.NoError(t, os.MkdirAll(filepath.Join(path, ".claude-plugin"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(path, ".claude-plugin", "plugin.json"), []byte(`{"name":"review"}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(path, "hook.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o700))
	l, _ := newLauncher(t, fmt.Sprintf("agent: claude\nmode: autonomous\nisolation: {enabled: true}\nclaude_plugin_dirs: [%q]\n", path))
	l.Cfg.Isolation.GuardBinary = new(linuxGuard(t))
	got := l.launched(t, "me")
	require.Contains(t, got.spec.Agent, "--plugin-dir")
	require.Contains(t, got.spec.Agent, isolatedHome+"/plugin-0/review plugin")
	require.NotContains(t, got.spec.Agent, path)
	data, err := os.ReadFile(filepath.Join(got.spec.Isolation.Inputs, "plugin-0", "review plugin", ".claude-plugin", "plugin.json"))
	require.NoError(t, err)
	require.JSONEq(t, `{"name":"review"}`, string(data))
	info, err := os.Stat(filepath.Join(got.spec.Isolation.Inputs, "plugin-0", "review plugin", "hook.sh"))
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		require.NotZero(t, info.Mode()&0o100)
	}
}
