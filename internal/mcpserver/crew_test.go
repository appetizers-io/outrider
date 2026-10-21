package mcpserver

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestCrewSetupToolsRequireOptInAndUseBackend(t *testing.T) {
	dir := t.TempDir()
	token := "01234567890123456789012345678901"
	calls := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "Bearer "+token, r.Header.Get("Authorization"))
		require.Equal(t, "/mcp/repositories", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "o/r", body["repo"])
		require.Equal(t, "folder", body["folder_id"])
		_, _ = w.Write([]byte(`{"added":true,"repo":"o/r"}`))
	}))
	defer backend.Close()
	_, portString, err := net.SplitHostPort(backend.Listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portString)
	require.NoError(t, err)
	data, err := json.Marshal(map[string]any{"port": port, "token": token})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mcp-control.json"), data, 0600))
	o := Options{ConfigPath: filepath.Join(dir, "settings.json"), StateDir: dir, Format: "crew", CrewControl: true}
	server, err := New(o)
	require.NoError(t, err)
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	require.NoError(t, err)
	defer func() { _ = ss.Close() }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	require.NoError(t, err)
	defer func() { _ = cs.Close() }()
	tools, err := cs.ListTools(ctx, nil)
	require.NoError(t, err)
	require.Len(t, tools.Tools, 7)
	for _, tool := range tools.Tools {
		if tool.Name == "add_repository" {
			require.False(t, tool.Annotations.ReadOnlyHint)
			require.True(t, tool.Annotations.IdempotentHint)
		}
	}
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "add_repository", Arguments: map[string]any{"repo": "o/r", "folder_id": "folder"}})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, 1, calls)
	o.Format = "cli"
	_, err = New(o)
	require.Error(t, err)
}

func TestCrewControlDoesNotFollowRedirectOrRetry(t *testing.T) {
	calls := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Redirect(w, r, "http://example.com", http.StatusTemporaryRedirect)
	}))
	defer backend.Close()
	_, p, err := net.SplitHostPort(backend.Listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(p)
	require.NoError(t, err)
	dir := t.TempDir()
	data, err := json.Marshal(map[string]any{"port": port, "token": "01234567890123456789012345678901"})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mcp-control.json"), data, 0600))
	o := Options{StateDir: dir}
	_, err = o.crewRequest(context.Background(), "/mcp/repositories", map[string]any{"repo": "o/r"})
	require.ErrorContains(t, err, "HTTP 307")
	require.Equal(t, 1, calls)
	_, err = (Options{StateDir: t.TempDir()}).crewRequest(context.Background(), "/mcp/folders", nil)
	require.ErrorContains(t, err, "enable Outrider")
}
