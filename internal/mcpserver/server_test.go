package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestSDKToolsAreReadOnlyScopedAndPreserveSessionStatus(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "settings.json")
	require.NoError(t, os.WriteFile(configPath, []byte(`{"repos":["o/r"],"repo_settings":{},"secret":"must not leak"}`), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox", "queue"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "queue", "a.json"), []byte(`{"id":"a","repo":"o/r","pr":1,"prompt":"untrusted raw PR content","created":"2026-10-21"}`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "validations.json"), []byte(`{"a":{"status":"started","slot":"crew-session"}}`), 0600))
	t.Setenv("OUTRIDER_CONFIG", filepath.Join(t.TempDir(), "must-not-read"))
	server, err := New(Options{ConfigPath: configPath, StateDir: dir, Format: "crew", Version: "test"})
	require.NoError(t, err)
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	require.NoError(t, err)
	defer func() { _ = ss.Close() }()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	require.NoError(t, err)
	defer func() { _ = cs.Close() }()
	tools, err := cs.ListTools(ctx, nil)
	require.NoError(t, err)
	require.Len(t, tools.Tools, 4)
	for _, tool := range tools.Tools {
		require.True(t, tool.Annotations.ReadOnlyHint)
	}
	call := func(name string, args map[string]any) *mcp.CallToolResult {
		t.Helper()
		r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		require.NoError(t, err)
		return r
	}
	text := func(r *mcp.CallToolResult) string {
		t.Helper()
		b, err := json.Marshal(r)
		require.NoError(t, err)
		return string(b)
	}
	repos := call("list_repositories", nil)
	require.False(t, repos.IsError)
	require.Contains(t, text(repos), "o/r")
	require.NotContains(t, text(repos), "must not leak")
	reviews := call("list_reviews", map[string]any{"repo": "o/r"})
	require.False(t, reviews.IsError)
	require.Contains(t, text(reviews), "started")
	require.Contains(t, text(reviews), "crew-session")
	require.NotContains(t, text(reviews), "untrusted raw")
	before, err := os.ReadFile(configPath)
	require.NoError(t, err)
	proposal := call("propose_repository_config", map[string]any{"repo": "o/r", "yaml": "triggers: {opt_in: {reaction: heart}}"})
	require.False(t, proposal.IsError)
	require.Contains(t, text(proposal), "heart")
	after, err := os.ReadFile(configPath)
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.True(t, call("propose_repository_config", map[string]any{"repo": "o/r", "yaml": "agent: claude"}).IsError)
	require.True(t, call("list_reviews", map[string]any{"repo": "../secret"}).IsError)
	require.False(t, call("configuration_schema", nil).IsError)
}
func TestExplicitInstanceRequiredAndCLIConfig(t *testing.T) {
	_, err := New(Options{})
	require.ErrorContains(t, err, "explicit")
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(file, []byte("repos: {include: [o/r]}\ntriggers: {mentions: {enabled: false}}\n"), 0600))
	o := Options{ConfigPath: file, StateDir: dir, Format: "cli"}
	data, err := o.repositories()
	require.NoError(t, err)
	require.Contains(t, data, "repos")
	reviews, err := o.reviews("")
	require.NoError(t, err)
	require.Empty(t, reviews)
}
