package forgejo

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/appetizers-io/outrider/internal/config"
	"github.com/appetizers-io/outrider/internal/proc"
)

// fjOutputs is what fj 0.7 prints when its output is not a terminal, keyed
// by its arguments after the global options.
var fjOutputs = map[string]string{
	"whoami":                                "currently signed into bot@forge.example\n",
	"repo view alice/fork":                  "alice/fork\nFork of o/r\n> a fork\n\n0 stars - 1 watching - 0 forks\n",
	"repo view o/r":                         "o/r\n> the repo\n\nView online at https://forge.example/o/r\n",
	"pr view o/r#7":                         "Fix main #7\nBy alice — Draft — +5 -1\nFrom `alice/fork:feat/x` into `main`\nbug\nneeds review\n\n> Fixes #3 and see\n> \n> https://forge.example/o/r/issues/4 too.\n\n\n2 comments\n",
	"pr status o/r#7":                       "Draft — Can't merge draft PR\n• Failure ci/build\n• Success ci/lint\n",
	"pr view o/r#7 files":                   "+1 -1 main.go\n+3 -0 docs/new.md\n",
	"pr view o/r#7 commits":                 "commit 2222222222222222222222222222222222222222 (+1, -1)\nAuthor: Alice <alice@example>\nDate:   Wed Nov 25 09:42:20 2026 +0100\n\n    fix: main\n\n    Longer text.\n\ncommit 3333333333333333333333333333333333333333 (+3, -0)\nAuthor: Alice <alice@example>\nDate:   Wed Nov 25 09:42:20 2026 +0100\n\n    docs: add\n\n",
	"pr view o/r#7 comments":                "frank said:\n> Any news?\n\n\n",
	"pr review o/r#7 list --comments --all": "Changes requested by carol\n1 comment\n> Please fix.\n\n\nIn main.go:1:\n@@ -1 +1 @@\ncarol commented :\n> Rename this.\n\n",
	"pr view o/r#7 diff":                    "diff --git a/main.go b/main.go\n+package main\n\n",
	"issue view o/r#3":                      "Issue 3 #3\"\nBy o — Open\n\n> Spec for 3\n\n\n0 comments\n",
	"issue view o/r#4":                      "Issue 4 #4\"\nBy o — Open\n\n> Spec for 4\n\n\n0 comments\n",
	"pr view o/r#8":                         "Plain #8\nBy bob — Open — +1 -0\nFrom `feat/y` into `main`\n\n2 comments\n",
	"pr status o/r#8":                       "",
	"pr view o/r#8 files":                   "+1 -0 a.txt\n",
	"pr view o/r#8 commits":                 "",
	"pr view o/r#8 comments":                "",
	"pr review o/r#8 list --comments --all": "No reviews.\n",
	"pr view o/r#8 diff":                    "diff --git a/a.txt b/a.txt\n\n",
}

type fakeFJ struct {
	calls  [][]string
	failOn string
}

func (f *fakeFJ) run(_ context.Context, c proc.Cmd) (proc.Result, error) {
	f.calls = append(f.calls, c.Args)
	if len(c.Args) < 5 || c.Args[0] != "fj" || strings.Join(c.Args[1:5], " ") != "--style minimal --host https://forge.example" {
		return proc.Result{}, &proc.Error{Args: c.Args, Code: 2, Stderr: "unexpected global options"}
	}
	key := strings.Join(c.Args[5:], " ")
	if key == f.failOn {
		return proc.Result{}, &proc.Error{Args: c.Args, Code: 1, Stderr: "Error: the response from forgejo was not properly structured"}
	}
	out, ok := fjOutputs[key]
	if !ok {
		return proc.Result{}, &proc.Error{Args: c.Args, Code: 1, Stderr: "Error: not found"}
	}
	return proc.Result{Stdout: out}, nil
}

func openFJ(t *testing.T, f *fakeFJ) Reader {
	t.Helper()
	r, err := Open(config.Forge{Kind: "forgejo", Host: "forge.example", Access: "fj"}, Deps{Run: f.run})
	require.NoError(t, err)
	return r
}

func TestFJMeRepoAndPRView(t *testing.T) {
	r := require.New(t)
	fake := &fakeFJ{}
	c := openFJ(t, fake)

	me, err := c.Me(t.Context())
	r.NoError(err)
	r.Equal("bot", me.Login)

	repo, err := c.Repo(t.Context(), "alice/fork")
	r.NoError(err)
	r.True(repo.Fork)
	r.Equal("o/r", repo.Parent.FullName)
	repo, err = c.Repo(t.Context(), "o/r")
	r.NoError(err)
	r.False(repo.Fork)

	pr, err := c.PRView(t.Context(), "o/r", 7)
	r.NoError(err)
	r.Equal("WIP: Fix main", pr.Title) // fj drops the draft prefix; it is put back
	r.Equal("OPEN", pr.State)
	r.Equal("alice", pr.AuthorLogin())
	r.Equal("https://forge.example/o/r/pulls/7", pr.URL)
	r.Equal("feat/x", pr.HeadRefName)
	r.Equal("alice", pr.HeadRepositoryOwner.Login) // a fork: owner/name:branch
	r.Equal("fork", pr.HeadRepository.Name)
	r.Equal("main", pr.BaseRefName)
	r.Nil(pr.MaintainerCanModify)
	r.Equal([]string{"bug", "needs review"}, []string{pr.Labels[0].Name, pr.Labels[1].Name})
	r.Equal("Fixes #3 and see\n\nhttps://forge.example/o/r/issues/4 too.", pr.Body)
	r.Len(pr.StatusCheckRollup, 2)
	r.Equal("FAILURE", pr.StatusCheckRollup[0].State)

	pr, err = c.PRView(t.Context(), "o/r", 8)
	r.NoError(err)
	r.Equal("Plain", pr.Title)
	r.Equal("o", pr.HeadRepositoryOwner.Login) // same repository
	r.Equal("r", pr.HeadRepository.Name)
	r.Empty(pr.Body)
	r.Empty(pr.Labels)
}

func TestFJUnexpectedOutputFails(t *testing.T) {
	fake := &fakeFJ{}
	c := openFJ(t, fake)
	fjOutputs["pr view o/r#9"] = "something else entirely\n"
	t.Cleanup(func() { delete(fjOutputs, "pr view o/r#9") })
	_, err := c.PRView(t.Context(), "o/r", 9)
	require.ErrorContains(t, err, "unexpected output")
	_, err = c.PRView(t.Context(), "o/r --help", 1)
	require.ErrorContains(t, err, "invalid repository")
}

func TestFJSaveContext(t *testing.T) {
	r := require.New(t)
	c := openFJ(t, &fakeFJ{})
	dir := t.TempDir()
	r.NoError(c.SaveContext(t.Context(), "o/r", 7, dir))
	for _, f := range c.ContextFiles() {
		r.FileExists(filepath.Join(dir, f[0]))
	}
	// fj's own newline after the diff is dropped
	r.Equal("diff --git a/main.go b/main.go\n+package main\n", read(t, filepath.Join(dir, "pr.diff")))
	var pr prJSON
	r.NoError(json.Unmarshal([]byte(read(t, filepath.Join(dir, "pr.json"))), &pr))
	r.True(pr.IsDraft)
	r.Len(pr.Files, 2)
	r.Equal(prFile{Path: "docs/new.md", Additions: 3}, pr.Files[1])
	r.Equal([]prCommit{
		{Oid: strings.Repeat("2", 40), MessageHeadline: "fix: main", MessageBody: "Longer text."},
		{Oid: strings.Repeat("3", 40), MessageHeadline: "docs: add"},
	}, pr.Commits)
	r.Contains(pr.CommentsText, "Any news?")
	r.Contains(pr.ReviewsText, "Rename this.")
	r.NotEmpty(pr.Notes)
	r.Contains(read(t, filepath.Join(dir, "review-comments.json")), "Rename this.")
	r.Contains(read(t, filepath.Join(dir, "pr-files.txt")), "2 changed files, +4 -1")
	var failing []map[string]any
	r.NoError(json.Unmarshal([]byte(read(t, filepath.Join(dir, "failing-checks.json"))), &failing))
	r.Len(failing, 1)
	r.Equal("ci/build", failing[0]["context"])
}

func TestFJStatusFailureIsANote(t *testing.T) {
	r := require.New(t)
	// fj 0.7 fails on a commit without statuses
	c := openFJ(t, &fakeFJ{failOn: "pr status o/r#8"})
	dir := t.TempDir()
	r.NoError(c.SaveContext(t.Context(), "o/r", 8, dir))
	var pr prJSON
	r.NoError(json.Unmarshal([]byte(read(t, filepath.Join(dir, "pr.json"))), &pr))
	r.Contains(pr.Notes[0], "checks are unknown")
	r.Equal("[]\n", read(t, filepath.Join(dir, "failing-checks.json")))
	r.Equal("diff --git a/a.txt b/a.txt\n", read(t, filepath.Join(dir, "pr.diff")))
}

func TestFJSaveReviewContext(t *testing.T) {
	r := require.New(t)
	c := openFJ(t, &fakeFJ{})
	dir := t.TempDir()
	r.NoError(c.SaveReviewContext(t.Context(), "o/r", 7, "", true, []string{"docs/**"}, dir))
	var m reviewManifest
	r.NoError(json.Unmarshal([]byte(read(t, filepath.Join(dir, "manifest.json"))), &m))
	r.Len(m.Issues, 2)
	r.Equal("https://forge.example/o/r/issues/4", m.Issues[0].URL)
	r.Equal("https://forge.example/o/r/issues/3", m.Issues[1].URL)
	r.Contains(read(t, filepath.Join(dir, "issue-01.txt")), "Spec for 4")
	r.Len(m.Docs, 1)
	r.Contains(m.Docs[0].Status, "use access: api")
}

func TestFJReadsNoToken(t *testing.T) {
	_, err := Open(config.Forge{Kind: "forgejo", Host: "forge.example", Access: "fj"}, Deps{
		Run:    (&fakeFJ{}).run,
		Getenv: func(string) string { t.Fatal("fj mode read the environment"); return "" },
	})
	require.NoError(t, err)
}
