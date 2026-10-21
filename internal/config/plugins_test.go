package config

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClaudePluginDirectories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review plugin")
	base := fmt.Sprintf("agent: claude\nclaude_plugin_dirs: [%q]\n", path)
	c, err := Parse([]byte(base+"overrides: [{match: [{repo: private/*}], claude_plugin_dirs: []}]\n"), "test")
	require.NoError(t, err)
	require.Equal(t, []string{path}, c.For("public/repo", false).ClaudePluginDirs)
	require.Empty(t, c.For("private/repo", false).ClaudePluginDirs)
	for _, input := range []string{
		fmt.Sprintf("claude_plugin_dirs: [%q]", path),
		"agent: claude\nclaude_plugin_dirs: [relative/path]",
		base + "sandbox: read-only\n",
		base + "others_prs: {sandbox: read-only}\n",
	} {
		_, err := Parse([]byte(input), "test")
		require.Error(t, err, input)
	}
}
