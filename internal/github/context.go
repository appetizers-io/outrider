package github

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/appetizers-io/outrider/internal/proc"
)

// ContextFiles are what SaveContext writes, with what each holds.
var ContextFiles = [][2]string{
	{"pr.json", "metadata, description, files, commits, reviews, conversation comments, checks"},
	{"pr-files.txt", "every changed file with its status and line counts, complete where pr.json's list is cut"},
	{"pr.diff", "the diff"},
	{"review-comments.json", "inline review comments; in_reply_to_id links a reply to its thread"},
	{"failing-checks.json", "the failed checks, with links"},
}

const contextFields = "number,title,url,state,isDraft,author,body,baseRefName,headRefName,headRefOid," +
	"headRepository,headRepositoryOwner,labels,files,commits,reviewDecision,reviews,comments,statusCheckRollup"

// MaxContextFile caps each prefetched file: a PR's text is untrusted and can
// be huge. A cut file ends with a note saying so.
const MaxContextFile = 2 << 20

// capped is data cut to MaxContextFile, with a note when it was cut.
func capped(data []byte) []byte {
	if len(data) <= MaxContextFile {
		return data
	}
	note := fmt.Sprintf("\n[outrider: truncated, the first %d of %d bytes]\n", MaxContextFile, len(data))
	return append(data[:MaxContextFile:MaxContextFile], note...)
}

// SaveContext writes the PR context into dir for a session without network:
// the files in ContextFiles.
func (c *Client) SaveContext(ctx context.Context, repo string, n int, dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("pr context: %w", err)
	}
	num := strconv.Itoa(n)
	view, err := c.Run(ctx, proc.Cmd{Args: []string{"gh", "pr", "view", num, "--repo", repo, "--json", contextFields}})
	if err != nil {
		return err //nolint:wrapcheck // a *proc.Error names the gh command already
	}
	var pr struct {
		StatusCheckRollup []Check    `json:"statusCheckRollup"`
		Files             []viewFile `json:"files"`
	}
	if err := json.Unmarshal([]byte(view.Stdout), &pr); err != nil {
		return fmt.Errorf("pr context: gh pr view: %w", err)
	}
	failing := []Check{}
	for _, ch := range pr.StatusCheckRollup {
		if failed(ch) {
			failing = append(failing, ch)
		}
	}
	// The files API is the only complete source of both the file list and, for
	// a PR GitHub renders no diff for, the patches. It costs a paginated
	// request, so it is asked only when one of the two is actually cut, and
	// once when both are.
	changed := fromView(pr.Files)
	var fetched []apiFile
	asked := false
	fetchFiles := func() error {
		if asked {
			return nil
		}
		got, err := c.apiFiles(ctx, repo, n)
		if err != nil {
			return err
		}
		fetched, asked = got, true
		changed = fromAPI(got)
		return nil
	}
	if len(pr.Files) >= viewFileCap {
		if err := fetchFiles(); err != nil {
			return err
		}
	}
	diff, diffErr := c.Run(ctx, proc.Cmd{Args: []string{"gh", "pr", "diff", num, "--repo", repo}})
	patch := diff.Stdout
	switch {
	case diffErr == nil:
	case tooManyFiles(diffErr):
		if err := fetchFiles(); err != nil {
			return err
		}
		patch = rebuildDiff(fetched)
	default:
		return diffErr //nolint:wrapcheck // a *proc.Error names the gh command already
	}
	inline, err := API[json.RawMessage](ctx, c, fmt.Sprintf("repos/%s/pulls/%d/comments?per_page=100", repo, n))
	if err != nil {
		return err
	}
	if inline == nil {
		inline = []json.RawMessage{}
	}
	files := map[string][]byte{
		"pr.json":      []byte(view.Stdout),
		"pr-files.txt": []byte(fileList(changed)),
		"pr.diff":      []byte(patch),
	}
	for name, v := range map[string]any{"review-comments.json": inline, "failing-checks.json": failing} {
		raw, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return fmt.Errorf("pr context: %s: %w", name, err)
		}
		files[name] = raw
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), capped(data), 0o600); err != nil {
			return fmt.Errorf("pr context: %w", err)
		}
	}
	return nil
}
