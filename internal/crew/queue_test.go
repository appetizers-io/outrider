package crew

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/appetizers-io/outrider/internal/config"
	"github.com/appetizers-io/outrider/internal/github"
	"github.com/appetizers-io/outrider/internal/proc"
	"github.com/appetizers-io/outrider/internal/session"
)

func fixture(t *testing.T, diff string, moved bool) *Scanner {
	t.Helper()
	run := func(_ context.Context, c proc.Cmd) (proc.Result, error) {
		require.Equal(t, "gh", c.Args[0], "snapshot collection must not run git or agents")
		args := strings.Join(c.Args, " ")
		switch {
		case strings.Contains(args, "--json headRefOid"):
			head := "abc"
			if moved {
				head = "changed"
			}
			return proc.Result{Stdout: `{"headRefOid":"` + head + `"}`}, nil
		case strings.Contains(args, "pr view"):
			return proc.Result{Stdout: `{"headRefOid":"abc","state":"OPEN","title":"test","statusCheckRollup":[]}`}, nil
		case strings.Contains(args, "pr diff"):
			return proc.Result{Stdout: diff}, nil
		case strings.Contains(args, "/comments?"):
			return proc.Result{Stdout: `[[]]`}, nil
		default:
			t.Fatalf("unexpected command: %s", args)
			return proc.Result{}, nil
		}
	}
	return &Scanner{Dir: t.TempDir(), Config: config.Default(), GH: &github.Client{Run: run}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}
func request() session.Request {
	return session.Request{Repo: "o/r", N: 1, PR: github.PR{Title: "PR title"}, Trigger: "mention", Scope: []string{"https://github.com/o/r/pull/1#discussion_r1"}}
}
func TestSnapshotIsBoundedScopedAndIdempotent(t *testing.T) {
	s := fixture(t, strings.Repeat("é", 15000), false)
	require.NoError(t, s.Enqueue(t.Context(), request()))
	require.NoError(t, s.Enqueue(t.Context(), request()))
	paths, err := filepath.Glob(filepath.Join(s.Dir, "queue", "*.json"))
	require.NoError(t, err)
	require.Len(t, paths, 1)
	raw, err := os.ReadFile(paths[0])
	require.NoError(t, err)
	var item Item
	require.NoError(t, json.Unmarshal(raw, &item))
	require.True(t, item.Truncated)
	require.Less(t, len(item.Prompt), 39000)
	require.Contains(t, item.Prompt, "discussion_r1")
	require.Contains(t, item.Prompt, "Never claim tests ran")
	require.Equal(t, "https://github.com/o/r/pull/1", item.URL)
	require.Equal(t, item.ID+".json", filepath.Base(paths[0]))
}
func TestMovedHeadDoesNotQueue(t *testing.T) {
	s := fixture(t, "diff", true)
	require.ErrorContains(t, s.Enqueue(t.Context(), request()), "head changed")
	paths, err := filepath.Glob(filepath.Join(s.Dir, "queue", "*.json"))
	require.NoError(t, err)
	require.Empty(t, paths)
}
func TestScanRequiresAllowlistAndReleasesLockOnFailure(t *testing.T) {
	s := fixture(t, "", false)
	require.ErrorContains(t, s.Scan(t.Context()), "requires at least one")
	s.Config.Repos.Include = []string{"o/r"}
	s.GH.Run = func(context.Context, proc.Cmd) (proc.Result, error) { return proc.Result{}, errors.New("offline") }
	require.ErrorContains(t, s.Scan(t.Context()), "offline")
	require.ErrorContains(t, s.Scan(t.Context()), "offline")
}
func TestSnapshotFailureLeavesNoQueueItem(t *testing.T) {
	s := fixture(t, "", false)
	s.GH.Run = func(context.Context, proc.Cmd) (proc.Result, error) { return proc.Result{}, errors.New("offline") }
	require.ErrorContains(t, s.Enqueue(t.Context(), request()), "offline")
	files, err := os.ReadDir(s.Dir)
	require.NoError(t, err)
	require.Empty(t, files)
}

func TestScannerAcknowledgesOnlyDurableSnapshots(t *testing.T) {
	s := fixture(t, "diff", false)
	s.Config.Repos.Include = []string{"o/r"}
	s.Config.Triggers.OptIn.Enabled = false
	s.Config.Triggers.OwnPRs.Check = false
	s.LookPath = func(string) (string, error) { return "", errors.New("not installed") }
	s.ProcessExisting = true
	snapshotRun := s.GH.Run
	fail := true
	s.GH.Run = func(ctx context.Context, c proc.Cmd) (proc.Result, error) {
		args := strings.Join(c.Args, " ")
		switch {
		case args == "gh api user":
			return proc.Result{Stdout: `{"login":"me"}`}, nil
		case strings.Contains(args, "notifications?"):
			return proc.Result{Stdout: `[[{"id":"n1","updated_at":"2026-01-01T00:00:00Z","reason":"comment","repository":{"full_name":"o/r"},"subject":{"type":"PullRequest","url":"https://api.github.com/repos/o/r/pulls/1"}}]]`}, nil
		case strings.Contains(args, "--json number,title,url,state,author"):
			return proc.Result{Stdout: `{"number":1,"title":"test","state":"OPEN","author":{"login":"me"}}`}, nil
		case strings.Contains(args, "pr diff") && fail:
			return proc.Result{}, errors.New("diff unavailable")
		default:
			return snapshotRun(ctx, c)
		}
	}
	require.ErrorContains(t, s.Scan(t.Context()), "diff unavailable")
	raw, err := os.ReadFile(filepath.Join(s.Dir, "state.json"))
	require.NoError(t, err)
	var state struct {
		Seen map[string]string `json:"seen"`
	}
	require.NoError(t, json.Unmarshal(raw, &state))
	require.Empty(t, state.Seen)
	fail = false
	require.NoError(t, s.Scan(t.Context()))
	raw, err = os.ReadFile(filepath.Join(s.Dir, "state.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &state))
	require.Contains(t, state.Seen, "n1")
	require.NoError(t, s.Scan(t.Context()))
	paths, err := filepath.Glob(filepath.Join(s.Dir, "queue", "*.json"))
	require.NoError(t, err)
	require.Len(t, paths, 1)
}

func TestWorkflowAndRepositoryInstructionsAreCaptured(t *testing.T) {
	s := fixture(t, "diff", false)
	cfg, err := config.Parse([]byte(`overrides:
  - match: [{repo: "o/r"}]
    prompts: {extra: "Focus on backwards compatibility."}
    workflows:
      - name: compatibility
        match: [{prs: others}]
        steps: ["Inspect changed APIs", "Explain migration risks"]
        post: true
`), "test")
	require.NoError(t, err)
	effective := cfg.For("o/r", false)
	r := request()
	r.Workflow = effective.ActivityWorkflow(session.WorkflowMetadata(r, false))
	require.NotNil(t, r.Workflow)
	require.NoError(t, s.enqueue(t.Context(), r, effective))
	paths, err := filepath.Glob(filepath.Join(s.Dir, "queue", "*.json"))
	require.NoError(t, err)
	require.Len(t, paths, 1)
	raw, err := os.ReadFile(paths[0])
	require.NoError(t, err)
	var item Item
	require.NoError(t, json.Unmarshal(raw, &item))
	require.Equal(t, "compatibility", item.Workflow)
	require.Contains(t, item.Prompt, "1. Inspect changed APIs\n2. Explain migration risks")
	require.Contains(t, item.Prompt, "Focus on backwards compatibility.")
	require.Contains(t, item.Prompt, "GitHub posts remain drafts")
	require.Contains(t, item.Prompt, "You have no tools")
	other := cfg.For("other/repo", false)
	require.Empty(t, other.Prompts.Extra)
	require.Empty(t, other.Workflows)
}
