package github

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/appetizers-io/outrider/internal/proc"
)

// refusal is what gh prints for open-component-model/open-component-model#4206,
// a 370-file PR: the REST error, then its GraphQL fallback's own message.
var refusal = &proc.Error{Args: []string{"gh", "pr", "diff"}, Code: 1, Stderr: "could not find pull request diff: " +
	"HTTP 406: Sorry, the diff exceeded the maximum number of files (300). Consider using 'List pull requests " +
	"files' API or locally cloning the repository instead. (https://api.github.com/repos/o/r/pulls/7)\n" +
	"PullRequest.diff too_large\n"}

func TestTooManyFilesMatchesOnlyTheSizeRefusal(t *testing.T) {
	r := require.New(t)
	r.True(tooManyFiles(refusal))
	r.True(tooManyFiles(&proc.Error{Code: 1, Stderr: "PullRequest.diff too_large"}))
	r.False(tooManyFiles(&proc.Error{Code: 1, Stderr: "HTTP 404: Not Found"}))
	r.False(tooManyFiles(&proc.Error{Code: -1, Stderr: "timed out after 2m0s"}))
	r.False(tooManyFiles(errors.New("exceeded the maximum number of files")))
	r.False(tooManyFiles(nil))
}

// viewFiles are n entries of `gh pr view --json files`.
func viewFiles(n int) []map[string]any {
	out := []map[string]any{}
	for i := range n {
		out = append(out, map[string]any{"path": "v" + strings.Repeat("x", i%3), "additions": 1, "deletions": 2, "changeType": "MODIFIED"})
	}
	return out
}

// snapshot answers a snapshot's gh calls, counting the file API's pages.
func snapshot(t *testing.T, view []map[string]any, diff any, files []map[string]any) (*Client, *int) {
	t.Helper()
	calls := 0
	c := fake(func(args []string) (any, error) {
		switch args[0] + " " + args[1] {
		case "pr view":
			return map[string]any{"title": "T", "files": view}, nil
		case "pr diff":
			if err, ok := diff.(error); ok {
				return nil, err
			}
			return diff, nil
		case "api repos/o/r/pulls/7/files?per_page=100":
			calls++
			return [][]map[string]any{files}, nil
		case "api repos/o/r/pulls/7/comments?per_page=100":
			return [][]map[string]any{{}}, nil
		}
		return nil, errors.New("unexpected " + strings.Join(args, " "))
	})
	return c, &calls
}

func saved(t *testing.T, c *Client) func(string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "pr-context")
	require.NoError(t, c.SaveContext(t.Context(), "o/r", 7, dir))
	return func(name string) string {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
		return string(raw)
	}
}

func TestSaveContextRebuildsADiffGitHubRefuses(t *testing.T) {
	r := require.New(t)
	c, calls := snapshot(t, viewFiles(2), refusal, []map[string]any{
		{"filename": "a.go", "status": "modified", "additions": 1, "deletions": 1, "patch": "@@ -1 +1 @@\n-old\n+new"},
		{"filename": "new.go", "previous_filename": "old.go", "status": "renamed", "patch": "@@ -1 +1 @@\n-x\n+y\n"},
		{"filename": "added.go", "status": "added", "additions": 3, "patch": "@@ -0,0 +1 @@\n+hi\n"},
		{"filename": "gone.go", "status": "removed", "deletions": 4, "patch": "@@ -1 +0,0 @@\n-bye\n"},
		{"filename": "logo.png", "status": "modified", "additions": 0, "deletions": 0},
	})
	read := saved(t, c)
	diff := read("pr.diff")
	r.Equal(1, *calls, "the files API answers both the list and the patches in one pass")
	r.Contains(diff, "[outrider: GitHub renders no diff for a pull request with more than 300 files; rebuilt from the pull request files API, 5 files]")
	r.Contains(diff, "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n+new\n")
	r.Contains(diff, "diff --git a/old.go b/new.go\nrename from old.go\nrename to new.go\n--- a/old.go\n+++ b/new.go\n")
	r.Contains(diff, "diff --git a/added.go b/added.go\n--- /dev/null\n+++ b/added.go\n")
	r.Contains(diff, "diff --git a/gone.go b/gone.go\n--- a/gone.go\n+++ /dev/null\n")
	r.Contains(diff, "diff --git a/logo.png b/logo.png\n[outrider: GitHub returned no patch for this file (modified, +0 -0): it is binary or its own diff is too large]")
	// The list covers every file, including the one with no patch.
	list := read("pr-files.txt")
	r.Equal("5 changed files, +4 -5", strings.Split(list, "\n")[0])
	r.Contains(list, "renamed   +0      -0      new.go")
	r.Contains(list, "modified  +0      -0      logo.png")
}

func TestSaveContextListsEveryFileWhenPRViewCutsTheList(t *testing.T) {
	r := require.New(t)
	c, calls := snapshot(t, viewFiles(viewFileCap), "diff --git a/x b/x\n", []map[string]any{
		{"filename": "a.go", "status": "modified", "additions": 7, "deletions": 1, "patch": "@@"},
		{"filename": "b.go", "status": "added", "additions": 2, "patch": "@@"},
	})
	read := saved(t, c)
	r.Equal(1, *calls)
	r.Equal("diff --git a/x b/x\n", read("pr.diff"), "a diff GitHub renders is kept as it is")
	r.Equal("2 changed files, +9 -1\nmodified  +7      -1      a.go\nadded     +2      -0      b.go\n", read("pr-files.txt"))
}

func TestSaveContextKeepsThePRViewListWhenItIsComplete(t *testing.T) {
	r := require.New(t)
	// The files API is not stubbed: asking it would fail the snapshot.
	c, calls := snapshot(t, viewFiles(viewFileCap-1), "diff", nil)
	read := saved(t, c)
	r.Equal(0, *calls)
	r.Equal("99 changed files, +99 -198", strings.Split(read("pr-files.txt"), "\n")[0])
	r.Contains(read("pr-files.txt"), "modified  +1      -2      vxx\n")
}

func TestSaveContextStillFailsOnAnyOtherDiffError(t *testing.T) {
	c, calls := snapshot(t, viewFiles(1), &proc.Error{Code: 1, Stderr: "HTTP 404: Not Found"}, nil)
	require.ErrorContains(t, c.SaveContext(t.Context(), "o/r", 7, t.TempDir()), "HTTP 404")
	require.Zero(t, *calls)
}

func TestFileListMarksGitHubsOwnFileCap(t *testing.T) {
	files := make([]changedFile, apiFileCap)
	require.Contains(t, fileList(files), "[outrider: GitHub lists at most 3000 files; a longer pull request is cut here]")
	require.NotContains(t, fileList(files[:1]), "at most")
}
