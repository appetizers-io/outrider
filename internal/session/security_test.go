package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGuardReplacementFailureIsAnError(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	require.NoError(t, os.WriteFile(source, []byte("new guard"), 0o700))
	destination := t.TempDir()
	require.ErrorContains(t, copyFile(source, destination), "copy guard")
}

func TestSessionsHaveSeparateGuardsAndCapturedPolicy(t *testing.T) {
	l, _ := newLauncher(t, "")
	first := l.launched(t, "bob")
	second, err := l.Prepare(Request{Repo: "o/r", N: 2, PR: pr("bob")}, t.TempDir(), "", nil)
	require.NoError(t, err)
	require.NotEqual(t, first.spec.Path, second.Spec.Path)
	require.Equal(t, "/bin/git", first.policy["guard"].(map[string]any)["git"])
	require.Contains(t, first.spec.Agent, "--setting-sources")
	require.Contains(t, first.spec.Agent, "user")
	require.Contains(t, first.spec.Agent, "--strict-mcp-config")
}
