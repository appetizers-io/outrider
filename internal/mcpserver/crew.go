package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (o Options) crewRequest(ctx context.Context, path string, payload any) (any, error) {
	var control struct {
		Port  int    `json:"port"`
		Token string `json:"token"`
	}
	if err := read(filepath.Join(o.StateDir, "mcp-control.json"), &control, false); err != nil {
		return nil, errors.New("crew setup backend is unavailable; enable Outrider in Crew and retry")
	}
	if control.Port < 1 || control.Port > 65535 || len(control.Token) < 32 {
		return nil, errors.New("invalid Crew setup connection")
	}
	var body io.Reader
	method := http.MethodGet
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(data)
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, fmt.Sprintf("http://127.0.0.1:%d%s", control.Port, path), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+control.Token)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	response, err := client.Do(req)
	if err != nil {
		return nil, errors.New("crew setup request failed; inspect registered repositories before retrying an add")
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 1024*1024 {
		return nil, errors.New("crew response too large")
	}
	if response.StatusCode != http.StatusOK {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &failure)
		return nil, fmt.Errorf("crew setup returned HTTP %d: %s", response.StatusCode, failure.Error)
	}
	var result any
	err = json.Unmarshal(data, &result)
	return result, err
}

func addCrewTools(s *mcp.Server, o Options) {
	mcp.AddTool(s, &mcp.Tool{Name: "list_crew_folders", Description: "List Crew folder IDs, names and project directories. Match the user's current project; ask if ambiguous.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		result, err := o.crewRequest(ctx, "/mcp/folders", nil)
		return nil, result, err
	})
	type folderArgs struct {
		FolderID string `json:"folder_id" jsonschema:"Existing Crew folder ID"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "inspect_crew_folder", Description: "Resolve the folder's inherited local checkout and GitHub remotes. Prefer upstream for upstream PRs and origin otherwise.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *mcp.CallToolRequest, args folderArgs) (*mcp.CallToolResult, any, error) {
		result, err := o.crewRequest(ctx, "/mcp/checkout", args)
		return nil, result, err
	})
	type addArgs struct {
		Repo     string `json:"repo" jsonschema:"GitHub owner/repo to watch"`
		FolderID string `json:"folder_id" jsonschema:"Crew folder containing the checkout"`
		Remote   string `json:"remote,omitempty" jsonschema:"Matching Git remote; defaults to upstream then origin"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "add_repository", Description: "Register a repository when the user asks. Uses an existing Crew folder and matching Git remote, preserves existing configuration and inherits polling/automatic-session defaults (so reviews may start). Already registered repositories are unchanged. Does not change trust or permissions.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(false), IdempotentHint: true, OpenWorldHint: new(true)}}, func(ctx context.Context, _ *mcp.CallToolRequest, args addArgs) (*mcp.CallToolResult, any, error) {
		if !validRepo(args.Repo) {
			return nil, nil, errors.New("repo must be owner/repo")
		}
		result, err := o.crewRequest(ctx, "/mcp/repositories", args)
		return nil, result, err
	})
}
