package github

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/appetizers-io/outrider/internal/proc"
)

// viewFileCap is how many changed files `gh pr view --json files` returns. It
// reports no more and says nothing about the ones it leaves out, so a list of
// exactly this length may be cut.
const viewFileCap = 100

// diffFileCap is how many files GitHub renders a diff for. Above it the diff
// media type answers 406 and `gh pr diff` fails for good.
const diffFileCap = 300

// apiFileCap is how many files the pull request files API returns at most.
const apiFileCap = 3000

// viewFile is an entry of `gh pr view --json files`.
type viewFile struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Change    string `json:"changeType"`
}

// apiFile is an entry of the pull request files API. Unlike `gh pr view` it
// carries each file's own patch, which is what makes a refused diff
// recoverable. Patch is empty for a binary file and for a file whose own diff
// GitHub considers too large.
type apiFile struct {
	Filename  string `json:"filename"`
	Previous  string `json:"previous_filename"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Patch     string `json:"patch"`
}

// changedFile is a file a pull request touches, from either file source.
type changedFile struct {
	Path      string
	Status    string
	Additions int
	Deletions int
}

// diffRefusals are how gh reports that GitHub will not render a diff this
// large: the REST error it prints first, then its GraphQL fallback's own
// message for the same limit. Both are matched in full, so an unrelated
// failure that happens to carry the word keeps its own error.
var diffRefusals = []string{"the diff exceeded the maximum number of files", "PullRequest.diff too_large"}

// tooManyFiles tells whether gh failed because the PR has more files than
// GitHub renders a diff for. That failure is permanent, so retrying is
// pointless and the files API has to supply the patches instead.
func tooManyFiles(err error) bool {
	var e *proc.Error
	if !errors.As(err, &e) {
		return false
	}
	for _, marker := range diffRefusals {
		if strings.Contains(e.Stderr, marker) {
			return true
		}
	}
	return false
}

// apiFiles are every changed file of the PR, patches included.
func (c *Client) apiFiles(ctx context.Context, repo string, n int) ([]apiFile, error) {
	files, err := API[apiFile](ctx, c, fmt.Sprintf("repos/%s/pulls/%d/files?per_page=100", repo, n))
	if err != nil {
		return nil, err
	}
	return files, nil
}

// fromView turns `gh pr view` entries into the shared shape. GraphQL names a
// change type in upper case; the files API uses the lower-case words.
func fromView(files []viewFile) []changedFile {
	out := make([]changedFile, 0, len(files))
	for _, f := range files {
		out = append(out, changedFile{Path: f.Path, Status: strings.ToLower(f.Change), Additions: f.Additions, Deletions: f.Deletions})
	}
	return out
}

// fromAPI turns files API entries into the shared shape.
func fromAPI(files []apiFile) []changedFile {
	out := make([]changedFile, 0, len(files))
	for _, f := range files {
		out = append(out, changedFile{Path: f.Filename, Status: f.Status, Additions: f.Additions, Deletions: f.Deletions})
	}
	return out
}

// quotePath renders prefix+path the way git renders a path in a diff header:
// one carrying a control character, a quote or a backslash is C-quoted as a
// whole. A file named with a newline would otherwise write a line of its own
// into untrusted context and could forge a diff header or an outrider note.
func quotePath(prefix, path string) string {
	special := func(r rune) bool { return r < ' ' || r == 0x7f || r == '"' || r == '\\' }
	if strings.ContainsFunc(path, special) {
		return strconv.Quote(prefix + path)
	}
	return prefix + path
}

// fileList renders one line per changed file and the totals. A reviewer needs
// it on its own because pr.json lists at most 100 files and the diff of a
// large PR is cut long before its last file.
func fileList(files []changedFile) string {
	additions, deletions := 0, 0
	for _, f := range files {
		additions += f.Additions
		deletions += f.Deletions
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d changed files, +%d -%d\n", len(files), additions, deletions)
	if len(files) >= apiFileCap {
		fmt.Fprintf(&b, "[outrider: GitHub lists at most %d files; a longer pull request is cut here]\n", apiFileCap)
	}
	for _, f := range files {
		fmt.Fprintf(&b, "%-9s +%-6d -%-6d %s\n", f.Status, f.Additions, f.Deletions, quotePath("", f.Path))
	}
	return b.String()
}

// rebuildDiff renders the files API as a unified diff, so a session reads the
// same pr.diff whether or not GitHub rendered one itself.
func rebuildDiff(files []apiFile) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[outrider: GitHub renders no diff for a pull request with more than %d files; rebuilt from the pull request files API, %d files]\n",
		diffFileCap, len(files))
	for _, f := range files {
		old := f.Filename
		if f.Previous != "" {
			old = f.Previous
		}
		// Every branch below ends its text with a newline, so each section
		// starts on its own line as it does in a diff GitHub renders.
		fmt.Fprintf(&b, "diff --git %s %s\n", quotePath("a/", old), quotePath("b/", f.Filename))
		if f.Previous != "" {
			fmt.Fprintf(&b, "rename from %s\nrename to %s\n", quotePath("", f.Previous), quotePath("", f.Filename))
		}
		if f.Patch == "" {
			fmt.Fprintf(&b, "[outrider: GitHub returned no patch for this file (%s, +%d -%d): it is binary or its own diff is too large]\n",
				f.Status, f.Additions, f.Deletions)
			continue
		}
		from, to := quotePath("a/", old), quotePath("b/", f.Filename)
		if f.Status == "added" {
			from = "/dev/null"
		}
		if f.Status == "removed" {
			to = "/dev/null"
		}
		fmt.Fprintf(&b, "--- %s\n+++ %s\n%s", from, to, f.Patch)
		if !strings.HasSuffix(f.Patch, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}
