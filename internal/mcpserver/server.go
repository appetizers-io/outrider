// Package mcpserver exposes read-only Outrider tools over the official MCP SDK.
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.yaml.in/yaml/v3"

	"github.com/appetizers-io/outrider/internal/config"
	"github.com/appetizers-io/outrider/internal/crew"
	"github.com/appetizers-io/outrider/internal/poll"
)

// Options selects one instance explicitly. No environment or host defaults are read.
type Options struct{ ConfigPath, StateDir, Format, Version string }

// New creates the portable MCP server. Tools never launch sessions or mutate state.
func New(o Options) (*mcp.Server, error) {
	if o.ConfigPath == "" || o.StateDir == "" {
		return nil, errors.New("MCP requires explicit --config and --state-dir")
	}
	if o.Format != "crew" && o.Format != "cli" {
		return nil, errors.New("--config-format must be cli or crew")
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "outrider", Version: o.Version}, &mcp.ServerOptions{Instructions: "Read Outrider state or prepare configuration drafts. No tool applies settings, starts a session or writes to GitHub. Review drafts in the appropriate Outrider configuration UI or file."})
	tool := func(name, description string) *mcp.Tool {
		return &mcp.Tool{Name: name, Description: description, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: new(false), IdempotentHint: true, OpenWorldHint: new(false)}}
	}
	mcp.AddTool(s, tool("list_repositories", "Read watched repositories and task settings from the explicitly selected instance."), func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		data, err := o.repositories()
		return nil, data, err
	})
	mcp.AddTool(s, tool("configuration_schema", "Read the Outrider schema and the supported repository-draft fields."), func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		return nil, map[string]any{"schema": config.Schema(), "repository_fields": []string{"triggers", "ignore_authors", "launch_check", "prompts", "workflows", "overrides", "others_prs", "push", "github_writes"}, "notes": "propose_repository_config validates task settings only. Ownership overrides match prs: own or others. Crew owns agent, model, sandbox and trust. others_prs supports allow_push and review_forks. Drafts are never applied."}, nil
	})
	type reviewArgs struct {
		Repo string `json:"repo,omitempty" jsonschema:"Optional owner/repo filter"`
	}
	mcp.AddTool(s, tool("list_reviews", "List up to 100 recent queued reviews, including linked Crew session status, without raw PR content. Reads the selected state directory's inbox/queue, runs.json and validations.json."), func(_ context.Context, _ *mcp.CallToolRequest, a reviewArgs) (*mcp.CallToolResult, any, error) {
		data, err := o.reviews(a.Repo)
		return nil, data, err
	})
	type proposeArgs struct {
		Repo string `json:"repo"`
		YAML string `json:"yaml"`
	}
	mcp.AddTool(s, tool("propose_repository_config", "Validate repository task YAML and return a draft. Does not save it or start work."), func(_ context.Context, _ *mcp.CallToolRequest, a proposeArgs) (*mcp.CallToolResult, any, error) {
		if !validRepo(a.Repo) {
			return nil, nil, errors.New("repo must be owner/repo")
		}
		if len(a.YAML) > 65536 {
			return nil, nil, errors.New("YAML exceeds 64 KiB")
		}
		draft, err := crew.ConvertSettings([]byte(a.YAML))
		if err != nil {
			return nil, nil, err
		}
		draft["repo"] = a.Repo
		draft["applied"] = false
		return nil, draft, nil
	})
	return s, nil
}

func read(path string, dest any, missingOK bool) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) && missingOK {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, 4*1024*1024+1))
	if err != nil {
		return err
	}
	if len(data) > 4*1024*1024 {
		return fmt.Errorf("state file exceeds 4 MiB: %s", path)
	}
	return json.Unmarshal(data, dest)
}
func (o Options) repositories() (map[string]any, error) {
	if o.Format == "crew" {
		data := map[string]any{}
		if err := read(o.ConfigPath, &data, true); err != nil {
			return nil, err
		}
		result := map[string]any{"repos": []string{}, "repo_settings": map[string]any{}}
		for _, key := range []string{"repos", "repo_settings", "interval", "lookback_hours", "candidate_limit"} {
			if value, ok := data[key]; ok {
				result[key] = value
			}
		}
		return result, nil
	}
	// Parse only the provided file; do not discover ~/.config or OUTRIDER_CONFIG.
	data, err := os.ReadFile(o.ConfigPath)
	if err != nil {
		return nil, err
	}
	c, err := config.Parse(data, o.ConfigPath)
	if err != nil {
		return nil, err
	}
	// Exclude classifier commands/environment and execution settings from tool output.
	encoded, err := yaml.Marshal(c)
	if err != nil {
		return nil, err
	}
	var source map[string]any
	if err := yaml.Unmarshal(encoded, &source); err != nil {
		return nil, err
	}
	result := map[string]any{}
	for _, key := range []string{"repos", "triggers", "ignore_authors", "workflows", "prompts", "interval_seconds", "lookback_hours", "candidate_limit"} {
		result[key] = source[key]
	}
	return result, nil
}
func (o Options) reviews(repo string) ([]map[string]any, error) {
	if repo != "" && !validRepo(repo) {
		return nil, errors.New("repo must be owner/repo")
	}
	runs, validations := map[string]map[string]any{}, map[string]map[string]any{}
	if err := read(filepath.Join(o.StateDir, "runs.json"), &runs, true); err != nil {
		return nil, err
	}
	if err := read(filepath.Join(o.StateDir, "validations.json"), &validations, true); err != nil {
		return nil, err
	}
	paths, err := filepath.Glob(filepath.Join(o.StateDir, "inbox", "queue", "*.json"))
	if err != nil {
		return nil, err
	}
	items := []map[string]any{}
	for _, path := range paths {
		var item crew.Item
		if err := read(path, &item, false); err != nil {
			return nil, err
		}
		if repo != "" && item.Repo != repo {
			continue
		}
		status := "pending"
		if value, ok := runs[item.ID]["status"].(string); ok {
			status = value
		}
		validation := validations[item.ID]
		if value, ok := validation["status"].(string); ok {
			status = value
		}
		items = append(items, map[string]any{"id": item.ID, "repo": item.Repo, "pr": item.PR, "title": item.Title, "url": item.URL, "head": item.Head, "created": item.Created, "workflow": item.Workflow, "status": status, "session": validation["slot"]})
	}
	sort.Slice(items, func(i, j int) bool { return items[i]["created"].(string) > items[j]["created"].(string) })
	if len(items) > 100 {
		items = items[:100]
	}
	return items, nil
}

func validRepo(repo string) bool {
	if !poll.RepoName.MatchString(repo) {
		return false
	}
	for _, part := range strings.Split(repo, "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	return true
}
