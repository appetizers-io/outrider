package github

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/appetizers-io/outrider/internal/proc"
)

// fake answers gh calls with fn's output.
func fake(fn func(args []string) (any, error)) *Client {
	return &Client{Run: func(_ context.Context, c proc.Cmd) (proc.Result, error) {
		out, err := fn(c.Args[1:])
		if err != nil {
			return proc.Result{Code: 1}, err
		}
		if s, ok := out.(string); ok {
			return proc.Result{Stdout: s}, nil
		}
		b, _ := json.Marshal(out)
		return proc.Result{Stdout: string(b)}, nil
	}}
}

func TestInvalidJSONIsAnError(t *testing.T) {
	c := fake(func([]string) (any, error) { return "<html>oops</html>", nil })
	_, err := c.Me(t.Context())
	var pe *proc.Error
	require.True(t, errors.As(err, &pe))
	require.Contains(t, pe.Stderr, "invalid JSON")
}

func TestAPIFlattensPages(t *testing.T) {
	c := fake(func(args []string) (any, error) {
		require.Equal(t, []string{"api", "notifications?all=true&since=T&per_page=50", "--paginate", "--slurp"}, args)
		return [][]map[string]any{{{"id": "1"}}, {{"id": "2"}}}, nil
	})
	ns, err := c.Notifications(t.Context(), "T")
	require.NoError(t, err)
	require.Len(t, ns, 2)
	require.Equal(t, "2", ns[1].ID)
}

func TestFailingChecks(t *testing.T) {
	pr := PR{StatusCheckRollup: []Check{
		{Name: "lint", Conclusion: "SUCCESS"},
		{Name: "test", Conclusion: "FAILURE"},
		{Context: "ci/legacy", State: "ERROR"},
		{Name: "slow", Status: "IN_PROGRESS"},
	}}
	require.Equal(t, []string{"test", "ci/legacy"}, FailingChecks(pr))
	require.Equal(t, []string{}, FailingChecks(PR{}))
}

func TestActivityOf(t *testing.T) {
	var c Comment
	require.NoError(t, json.Unmarshal([]byte(`{"id": 7, "state": "APPROVED", "submitted_at": "2026-01-02", "user": null, "body": null}`), &c))
	a := ActivityOf("review", c)
	require.Equal(t, "2026-01-02", a.At)
	require.Nil(t, a.User)
	require.Empty(t, a.Body)
	require.Nil(t, a.UpdatedAt)
}

func TestPendingReplies(t *testing.T) {
	c := fake(func([]string) (any, error) {
		return []any{[]map[string]any{
			{"id": 11, "in_reply_to_id": 10, "user": map[string]any{"login": "bob"}},
			{"id": 10, "user": map[string]any{"login": "ME"}},
			{"id": 20, "user": map[string]any{"login": "carol"}}, // not my thread
			{"id": 30, "user": map[string]any{"login": "bob"}},
			{"id": 31, "in_reply_to_id": 30, "user": map[string]any{"login": "me"}}, // I answered last
		}}, nil
	})
	got, err := c.PendingReplies(t.Context(), "o/r", 1, "me")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(10), *got[0][0].ID)
	require.Equal(t, int64(11), *got[0][1].ID)
}

func TestInvolvedPRs(t *testing.T) {
	var queries []string
	c := fake(func(args []string) (any, error) {
		queries = append(queries, args[5])
		return map[string]any{"items": []map[string]any{
			{"repository_url": "https://api.github.com/repos/o/r", "number": 5, "user": map[string]any{"login": "bob"}},
		}}, nil
	})
	got, err := c.InvolvedPRs(t.Context(), "me")
	require.NoError(t, err)
	require.Equal(t, map[string]Involved{"o/r#5": {Repo: "o/r", N: 5, Author: "bob"}}, got)
	require.Equal(t, []string{"q=is:pr is:open archived:false involves:me", "q=is:pr is:open archived:false review-requested:me"}, queries)
}

// --- the opt-in reaction ---------------------------------------------------

var (
	no    = map[string]any{"reactionGroups": []any{map[string]any{"content": "EYES", "viewerHasReacted": false}}}
	yes   = map[string]any{"reactionGroups": []any{map[string]any{"content": "EYES", "viewerHasReacted": true}}}
	other = map[string]any{"reactionGroups": []any{map[string]any{"content": "HEART", "viewerHasReacted": true}}}
)

func pr(kw map[string]any) map[string]any {
	body := map[string]any{
		"reactionGroups": []any{},
		"comments":       map[string]any{"nodes": []any{no}},
		"reviews":        map[string]any{"nodes": []any{no}},
		"reviewThreads":  map[string]any{"nodes": []any{map[string]any{"comments": map[string]any{"nodes": []any{no}}}}},
	}
	for k, v := range kw {
		body[k] = v
	}
	return map[string]any{"pullRequest": body}
}

func refs(n int) []Ref {
	out := make([]Ref, n)
	for i := range out {
		out[i] = Ref{"o/r", i}
	}
	return out
}

func str(s string) *string { return &s }

func TestEyesFoundAnywhere(t *testing.T) {
	data := map[string]any{
		"p0": pr(yes),
		"p1": pr(map[string]any{"comments": map[string]any{"nodes": []any{no, yes}}}),
		"p2": pr(map[string]any{"reviews": map[string]any{"nodes": []any{yes}}}),
		"p3": pr(map[string]any{"reviewThreads": map[string]any{"nodes": []any{map[string]any{"comments": map[string]any{"nodes": []any{no, yes}}}}}}),
		"p4": pr(map[string]any{"comments": map[string]any{"nodes": []any{other}}}),
		"p5": map[string]any{"pullRequest": nil},
	}
	c := fake(func([]string) (any, error) { return map[string]any{"data": data}, nil })
	got, err := c.EyesQuery(t.Context(), refs(6), "eyes", WhereAll)
	require.NoError(t, err)
	require.Equal(t, map[string]*string{
		"o/r#0": str("PR description"),
		"o/r#1": str("comment"),
		"o/r#2": str("review"),
		"o/r#3": str("review comment"),
		"o/r#4": nil,
		// o/r#5 unknown: missing, so callers don't treat it as "👀 removed"
	}, got)
}

func TestQueryEscapesNames(t *testing.T) {
	var seen string
	c := fake(func(args []string) (any, error) {
		seen = args[len(args)-1]
		return map[string]any{"data": map[string]any{}}, nil
	})
	_, err := c.EyesQuery(t.Context(), []Ref{{`o"x/r`, 5}}, "eyes", WhereAll)
	require.NoError(t, err)
	require.Contains(t, seen, `owner: "o\"x"`)
	require.Contains(t, seen, "pullRequest(number: 5)")
}

var alias = regexp.MustCompile(`p(\d+): repository\(owner: "[^"]*", name: "[^"]*"\) \{ pullRequest\(number: (\d+)\)`)

func TestOneBrokenPRDoesNotHideTheBatch(t *testing.T) {
	c := fake(func(args []string) (any, error) {
		q := args[len(args)-1]
		data := map[string]any{}
		for _, m := range alias.FindAllStringSubmatch(q, -1) {
			if m[2] == "2" {
				return nil, errors.New("broken")
			}
			data["p"+m[1]] = pr(map[string]any{"comments": map[string]any{"nodes": []any{yes}}})
		}
		return map[string]any{"data": data}, nil
	})
	got := c.MyEyes(t.Context(), []Ref{{"o/r", 1}, {"o/r", 2}, {"o/r", 3}}, "eyes", WhereAll)
	require.Equal(t, map[string]*string{"o/r#1": str("comment"), "o/r#3": str("comment")}, got)
}

func TestBatches(t *testing.T) {
	var calls []int
	c := fake(func(args []string) (any, error) {
		calls = append(calls, strings.Count(args[len(args)-1], "repository("))
		return map[string]any{"data": map[string]any{}}, nil
	})
	c.MyEyes(t.Context(), refs(23), "eyes", WhereAll)
	require.Equal(t, []int{10, 10, 3}, calls)
}

func TestConfiguredReactionAndPlaces(t *testing.T) {
	rocket := map[string]any{"reactionGroups": []any{map[string]any{"content": "ROCKET", "viewerHasReacted": true}}}
	data := map[string]any{
		"p0": pr(map[string]any{"comments": map[string]any{"nodes": []any{rocket}}}), // rocket on a comment
		"p1": pr(yes),                                                                // eyes on the description: not the configured reaction
		"p2": pr(rocket),                                                             // rocket on the description
	}
	c := fake(func([]string) (any, error) { return map[string]any{"data": data}, nil })
	got, err := c.EyesQuery(t.Context(), refs(3), "rocket", []string{"description"})
	require.NoError(t, err)
	require.Equal(t, map[string]*string{"o/r#0": nil, "o/r#1": nil, "o/r#2": str("PR description")}, got)
}

func TestSaveContextWritesWhatAnOfflineSessionNeeds(t *testing.T) {
	r := require.New(t)
	c := fake(func(args []string) (any, error) {
		switch args[0] + " " + args[1] {
		case "pr view":
			r.Equal([]string{"pr", "view", "7", "--repo", "o/r", "--json"}, args[:6])
			return map[string]any{"title": "T", "statusCheckRollup": []map[string]any{
				{"name": "lint", "conclusion": "SUCCESS"},
				{"name": "test", "conclusion": "FAILURE", "detailsUrl": "https://ci/1"},
			}}, nil
		case "pr diff":
			return "diff --git a/x b/x\n", nil
		case "api repos/o/r/pulls/7/comments?per_page=100":
			return [][]map[string]any{{{"id": 1, "body": "nit", "diff_hunk": "@@"}}}, nil
		}
		return nil, errors.New("unexpected " + strings.Join(args, " "))
	})
	dir := filepath.Join(t.TempDir(), "pr-context")
	r.NoError(c.SaveContext(t.Context(), "o/r", 7, dir))
	read := func(name string) string {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		r.NoError(err)
		return string(raw)
	}
	r.Contains(read("pr.json"), `"title":"T"`)
	r.Equal("diff --git a/x b/x\n", read("pr.diff"))
	r.Contains(read("review-comments.json"), `"diff_hunk": "@@"`)
	var failing []Check
	r.NoError(json.Unmarshal([]byte(read("failing-checks.json")), &failing))
	r.Equal([]Check{{Name: "test", Conclusion: "FAILURE", DetailsURL: "https://ci/1"}}, failing)
	for _, f := range ContextFiles {
		r.FileExists(filepath.Join(dir, f[0]))
	}
}

func TestSaveContextFailsWhenGitHubDoes(t *testing.T) {
	c := fake(func([]string) (any, error) { return nil, errors.New("offline") })
	require.Error(t, c.SaveContext(t.Context(), "o/r", 7, t.TempDir()))
}
