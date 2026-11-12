package crew

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		case strings.Contains(args, "/reviews?"):
			// Activity from someone else: own_prs ignores your own by default.
			return proc.Result{Stdout: `[[{"id":7,"state":"COMMENTED","submitted_at":"2026-01-01T00:00:00Z","user":{"login":"bob"},"body":"take a look"}]]`}, nil
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

// A PR above GitHub's 300-file diff limit used to fail every scan.
func TestSnapshotCapturesAPRGitHubRendersNoDiffFor(t *testing.T) {
	r := require.New(t)
	s := fixture(t, "", false)
	const count = 400
	api := []map[string]any{}
	for i := range count {
		api = append(api, map[string]any{"filename": fmt.Sprintf("dir/file%03d.go", i), "status": "modified",
			"additions": 2, "deletions": 1,
			"patch": "@@ -1,2 +1,2 @@\n-" + strings.Repeat("o", 60) + "\n+" + strings.Repeat("n", 60) + "\n"})
	}
	page, err := json.Marshal([][]map[string]any{api})
	r.NoError(err)
	// gh pr view reports only the first 100 files of such a PR.
	view, err := json.Marshal(map[string]any{"headRefOid": "abc", "state": "OPEN", "title": "test",
		"statusCheckRollup": []any{}, "files": make([]map[string]any, 100)})
	r.NoError(err)
	inner := s.GH.Run
	s.GH.Run = func(ctx context.Context, c proc.Cmd) (proc.Result, error) {
		args := strings.Join(c.Args, " ")
		switch {
		case strings.Contains(args, "--json headRefOid"):
			return proc.Result{Stdout: `{"headRefOid":"abc"}`}, nil
		case strings.Contains(args, "pr view"):
			return proc.Result{Stdout: string(view)}, nil
		case strings.Contains(args, "pr diff"):
			return proc.Result{Code: 1}, &proc.Error{Args: c.Args, Code: 1, Stderr: "could not find pull request diff: " +
				"HTTP 406: Sorry, the diff exceeded the maximum number of files (300).\nPullRequest.diff too_large"}
		case strings.Contains(args, "/files?per_page=100"):
			return proc.Result{Stdout: string(page)}, nil
		default:
			return inner(ctx, c)
		}
	}
	r.NoError(s.Enqueue(t.Context(), request()))
	paths, err := filepath.Glob(filepath.Join(s.Dir, "queue", "*.json"))
	r.NoError(err)
	r.Len(paths, 1)
	raw, err := os.ReadFile(paths[0])
	r.NoError(err)
	var item Item
	r.NoError(json.Unmarshal(raw, &item))
	r.True(item.Truncated)
	r.Less(len(item.Prompt), 39000)
	// The complete file list is in the prompt, so the reviewer sees the scale.
	r.Contains(item.Prompt, fmt.Sprintf("%d changed files, +800 -400", count))
	// The diff is cut between two files, never inside a hunk.
	diff := part(item.Prompt, "pr.diff")
	sections := strings.Count("\n"+diff, fileSection)
	r.Greater(sections, 20)
	r.Contains(diff, fmt.Sprintf("[TRUNCATED: the first %d of %d changed files]", sections, count))
	kept, _, _ := strings.Cut(diff, "[TRUNCATED")
	r.True(strings.HasSuffix(kept, "+"+strings.Repeat("n", 60)+"\n"), "a cut section would leave a half hunk")
}

// part is the text of one prompt section.
func part(prompt, name string) string {
	_, rest, _ := strings.Cut(prompt, "--- UNTRUSTED "+name+" ---\n")
	text, _, _ := strings.Cut(rest, "\n--- END ---")
	return text
}

// 19000 bytes of diff fit only because the short file list passes its unused
// budget on.
func TestAShortFileListLeavesTheDiffItsFullBudget(t *testing.T) {
	r := require.New(t)
	s := fixture(t, strings.Repeat("x", 19000), false)
	r.NoError(s.Enqueue(t.Context(), request()))
	paths, err := filepath.Glob(filepath.Join(s.Dir, "queue", "*.json"))
	r.NoError(err)
	raw, err := os.ReadFile(paths[0])
	r.NoError(err)
	var item Item
	r.NoError(json.Unmarshal(raw, &item))
	r.False(item.Truncated)
	r.Equal(strings.Repeat("x", 19000), part(item.Prompt, "pr.diff"))
	r.Equal("0 changed files, +0 -0\n", part(item.Prompt, "pr-files.txt"))
}

func TestFitSaysHowMuchOfTheDiffSurvived(t *testing.T) {
	r := require.New(t)
	text, cut := fit("short", 10)
	r.Equal("short", text)
	r.False(cut)
	text, cut = fit("0123456789abc", 10)
	r.Equal("0123456789\n[TRUNCATED]", text)
	r.True(cut)
	// A single file, or a first file longer than the budget, has no boundary to
	// cut at, so the marker says the cut landed inside it.
	one := fileSection[1:] + "a/x b/x\n@@ -1 +1 @@\n" + strings.Repeat("+x\n", 100)
	text, _ = fit(one, 40)
	r.Equal(fileSection[1:]+"a/x b/x\n@@ -1 +1 @@\n+x\n+x\n+x\n[TRUNCATED: part of the first of 1 changed files]", text)
	text, _ = fit(one+one, 40)
	r.Contains(text, "[TRUNCATED: part of the first of 2 changed files]")
	// Two files, one of which fits: the cut is a file boundary.
	text, _ = fit(one+one, 400)
	r.True(strings.HasSuffix(text, "+x\n[TRUNCATED: the first 1 of 2 changed files]"), text[len(text)-60:])
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

func TestScanBudgetSavesCompletedNotifications(t *testing.T) {
	s := fixture(t, "diff", false)
	s.Config.Repos.Include = []string{"o/r"}
	s.Config.Triggers.OptIn.Enabled = false
	s.Config.Triggers.OwnPRs.Check = false
	s.LookPath = func(string) (string, error) { return "", errors.New("not installed") }
	s.ProcessExisting = true
	snapshotRun := s.GH.Run
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	s.GH.Run = func(ctx context.Context, c proc.Cmd) (proc.Result, error) {
		args := strings.Join(c.Args, " ")
		switch {
		case args == "gh api user":
			return proc.Result{Stdout: `{"login":"me"}`}, nil
		case strings.Contains(args, "notifications?"):
			return proc.Result{Stdout: `[[{"id":"n1","updated_at":"2026-01-01T00:00:00Z","reason":"comment","repository":{"full_name":"o/r"},"subject":{"type":"PullRequest","url":"https://api.github.com/repos/o/r/pulls/1"}},{"id":"n2","updated_at":"2026-01-01T00:00:00Z","reason":"comment","repository":{"full_name":"o/r"},"subject":{"type":"PullRequest","url":"https://api.github.com/repos/o/r/pulls/2"}}]]`}, nil
		case strings.Contains(args, "pr view 2"):
			<-ctx.Done()
			return proc.Result{}, ctx.Err()
		case strings.Contains(args, "--json number,title,url,state,author"):
			return proc.Result{Stdout: `{"number":1,"title":"test","state":"OPEN","author":{"login":"me"}}`}, nil
		default:
			return snapshotRun(ctx, c)
		}
	}
	require.ErrorIs(t, s.Scan(ctx), ErrScanBudget)
	raw, err := os.ReadFile(filepath.Join(s.Dir, "state.json"))
	require.NoError(t, err)
	var state struct {
		Seen map[string]string `json:"seen"`
	}
	require.NoError(t, json.Unmarshal(raw, &state))
	require.Contains(t, state.Seen, "n1")
	require.NotContains(t, state.Seen, "n2")
	paths, err := filepath.Glob(filepath.Join(s.Dir, "queue", "*.json"))
	require.NoError(t, err)
	require.Len(t, paths, 1)
}
