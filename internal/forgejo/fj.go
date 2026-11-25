package forgejo

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/appetizers-io/outrider/internal/github"
	"github.com/appetizers-io/outrider/internal/proc"
)

// FJ reads a Forgejo host through the fj CLI (forgejo-cli), with fj's own
// login: outrider never sees a token.
//
// fj prints text, not JSON, and only in English when its output is not a
// terminal; FJ reads that text (fj 0.7). It gives less than API: no inline
// review comments as data (pr.json carries fj's text of the reviews), no
// commit ids of the base, so no docs from the base commit, and no review
// decision. What is missing is listed in pr.json's outriderNotes.
type FJ struct {
	Host    string // as configured
	BaseURL string // passed to fj as --host
	Run     proc.Runner
}

// fjContextFiles are what FJ.SaveContext writes.
var fjContextFiles = [][2]string{
	{"pr.json", "metadata, description, files, commits, and fj's text of the reviews and conversation comments (commentsText, reviewsText); outriderNotes lists what fj does not provide"},
	{"pr-files.txt", "every changed file with its line counts"},
	{"pr.diff", "the diff"},
	{"review-comments.json", "the reviews and their inline comments as fj prints them (text)"},
	{"failing-checks.json", "the failed commit statuses"},
}

// ContextFiles are the files SaveContext writes.
func (c *FJ) ContextFiles() [][2]string { return fjContextFiles }

// fj runs fj against this host, with plain output.
func (c *FJ) fj(ctx context.Context, args ...string) (string, error) {
	cmd := append([]string{"fj", "--style", "minimal", "--host", c.BaseURL}, args...)
	res, err := c.Run(ctx, proc.Cmd{Args: cmd})
	if err != nil {
		return "", err //nolint:wrapcheck // a *proc.Error names the fj command already
	}
	return res.Stdout, nil
}

func prRef(repo string, n int) (string, error) {
	if err := checkRepo(repo); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s#%d", repo, n), nil
}

var whoami = regexp.MustCompile(`(?m)^currently signed into (\S+)@\S+\s*$`)

// Me is fj's user on this host.
func (c *FJ) Me(ctx context.Context) (github.User, error) {
	out, err := c.fj(ctx, "whoami")
	if err != nil {
		return github.User{}, err
	}
	m := whoami.FindStringSubmatch(out)
	if m == nil {
		return github.User{}, fmt.Errorf("forge %s: fj whoami: unexpected output", c.Host)
	}
	return github.User{Login: m[1]}, nil
}

var forkOf = regexp.MustCompile(`(?m)^Fork of (\S+/\S+)\s*$`)

// Repo looks owner/repo up.
func (c *FJ) Repo(ctx context.Context, repo string) (github.Repo, error) {
	if err := checkRepo(repo); err != nil {
		return github.Repo{}, err
	}
	out, err := c.fj(ctx, "repo", "view", repo)
	if err != nil {
		return github.Repo{}, err
	}
	name, _, _ := strings.Cut(out, "\n")
	r := github.Repo{FullName: strings.TrimSpace(name)}
	if m := forkOf.FindStringSubmatch(out); m != nil {
		r.Fork = true
		r.Parent = &struct {
			FullName string `json:"full_name"`
		}{FullName: m[1]}
	}
	if !strings.EqualFold(r.FullName, repo) {
		return github.Repo{}, fmt.Errorf("forge %s: fj repo view %s: unexpected output", c.Host, repo)
	}
	return r, nil
}

var (
	prTitle = regexp.MustCompile(`^(.*) #([0-9]+)$`)
	prBy    = regexp.MustCompile(`^By (\S+) \S+ (Draft|Open|Merged|Closed) \S+ \+([0-9]+) -([0-9]+)$`)
	prFrom  = regexp.MustCompile("^(?:From `([^`]+)` into|Into) `([^`]+)`$")
)

// fjPR is what `fj pr view` prints.
type fjPR struct {
	pr    github.PR
	draft bool
	notes []string
}

// parseView reads `fj pr view`: the title and number, author, state and
// line counts, head and base, labels, then the body quoted with "> ".
func parseView(out, repo string, n int) (fjPR, error) {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	bad := func(what string) (fjPR, error) {
		return fjPR{}, fmt.Errorf("fj pr view %s#%d: unexpected output (%s)", repo, n, what)
	}
	if len(lines) < 3 {
		return bad("too short")
	}
	title := prTitle.FindStringSubmatch(lines[0])
	if title == nil || title[2] != strconv.Itoa(n) {
		return bad("title")
	}
	by := prBy.FindStringSubmatch(lines[1])
	if by == nil {
		return bad("author")
	}
	from := prFrom.FindStringSubmatch(lines[2])
	if from == nil {
		return bad("branches")
	}
	p := fjPR{draft: by[2] == "Draft"}
	p.pr.Number, p.pr.Title = n, title[1]
	if p.draft {
		p.pr.Title = "WIP: " + title[1] // fj drops the prefix that makes it a draft
	}
	p.pr.Author = &github.User{Login: by[1]}
	p.pr.State = map[string]string{"Draft": "OPEN", "Open": "OPEN", "Merged": "MERGED", "Closed": "CLOSED"}[by[2]]
	p.pr.BaseRefName = from[2]
	head := from[1]
	owner, name, _ := strings.Cut(repo, "/")
	if fork, branch, ok := strings.Cut(head, ":"); ok {
		// another repository: owner/name:branch
		owner, name, _ = strings.Cut(fork, "/")
		head = branch
	}
	if head != "" && owner != "" && name != "" {
		p.pr.HeadRefName = head
		p.pr.HeadRepository = &struct {
			Name string `json:"name"`
		}{Name: name}
		p.pr.HeadRepositoryOwner = &github.User{Login: owner}
	}
	i := 3
	for ; i < len(lines) && lines[i] != ""; i++ {
		p.pr.Labels = append(p.pr.Labels, struct {
			Name string `json:"name"`
		}{Name: lines[i]})
	}
	for i < len(lines) && lines[i] == "" {
		i++
	}
	var body []string
	for ; i < len(lines) && strings.HasPrefix(lines[i], ">"); i++ {
		line := strings.TrimPrefix(lines[i], ">")
		body = append(body, strings.TrimPrefix(line, " "))
	}
	p.pr.Body = strings.Join(body, "\n")
	return p, nil
}

var statusLine = regexp.MustCompile(`^\S+ (Success|Pending|Warning|Failure|Skipped|Error) (.+)$`)

// checks are the head's commit statuses from `fj pr status`. fj 0.7 fails on
// a commit without any status; that is reported as a note, not an error.
func (c *FJ) checks(ctx context.Context, ref string) ([]github.Check, string) {
	out, err := c.fj(ctx, "pr", "status", ref)
	if err != nil {
		return nil, "fj pr status failed, so the checks are unknown: " + firstLine(err.Error())
	}
	var checks []github.Check
	for _, line := range strings.Split(out, "\n") {
		if m := statusLine.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			checks = append(checks, github.Check{Context: m[2], State: strings.ToUpper(m[1])})
		}
	}
	return checks, ""
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

func (c *FJ) view(ctx context.Context, repo string, n int) (fjPR, string, error) {
	ref, err := prRef(repo, n)
	if err != nil {
		return fjPR{}, "", err
	}
	out, err := c.fj(ctx, "pr", "view", ref)
	if err != nil {
		return fjPR{}, "", err
	}
	p, err := parseView(out, repo, n)
	if err != nil {
		return fjPR{}, "", fmt.Errorf("forge %s: %w", c.Host, err)
	}
	p.pr.URL = fmt.Sprintf("%s/%s/pulls/%d", c.BaseURL, repo, n)
	var note string
	p.pr.StatusCheckRollup, note = c.checks(ctx, ref)
	if note != "" {
		p.notes = append(p.notes, note)
	}
	return p, ref, nil
}

// PRView looks a pull request up. Labels and checks are what fj prints;
// maintainerCanModify is unknown.
func (c *FJ) PRView(ctx context.Context, repo string, n int) (github.PR, error) {
	p, _, err := c.view(ctx, repo, n)
	return p.pr, err
}

var (
	fileLine   = regexp.MustCompile(`^\+([0-9]+)\s+-([0-9]+) (.*)$`)
	commitLine = regexp.MustCompile(`^commit ([0-9a-f]{40,64})\b`)
)

// SaveContext writes the PR context into dir: the files in ContextFiles.
func (c *FJ) SaveContext(ctx context.Context, repo string, n int, dir string) error {
	p, ref, err := c.view(ctx, repo, n)
	if err != nil {
		return err
	}
	out := fromPR(p.pr)
	out.IsDraft = p.draft
	out.Notes = append(slices.Clone(p.notes),
		"read with fj, which prints text: no head or base commit ids, no review decision, inline review comments only as text in review-comments.json")
	files, err := c.fj(ctx, "pr", "view", ref, "files")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(files, "\n") {
		if m := fileLine.FindStringSubmatch(line); m != nil {
			add, _ := strconv.Atoi(m[1])
			del, _ := strconv.Atoi(m[2])
			out.Files = append(out.Files, prFile{Path: strings.TrimLeft(m[3], " "), Additions: add, Deletions: del})
		}
	}
	commits, err := c.fj(ctx, "pr", "view", ref, "commits")
	if err != nil {
		return err
	}
	out.Commits = parseCommits(commits)
	if out.CommentsText, err = c.fj(ctx, "pr", "view", ref, "comments"); err != nil {
		return err
	}
	reviews, err := c.fj(ctx, "pr", "review", ref, "list", "--comments", "--all")
	if err != nil {
		return err
	}
	out.ReviewsText = reviews
	diff, err := c.fj(ctx, "pr", "view", ref, "diff")
	if err != nil {
		return err
	}
	// fj prints the diff with a newline of its own after it
	diff = strings.TrimSuffix(diff, "\n")
	inline := []map[string]string{{"source": "fj pr review list --comments --all", "text": reviews}}
	return writeContext(dir, out, diff, inline, failing(out.StatusCheckRollup))
}

// parseCommits reads `fj pr view commits`, which prints like git log.
func parseCommits(out string) []prCommit {
	commits := []prCommit{}
	lines := strings.Split(out, "\n")
	for i := 0; i < len(lines); i++ {
		m := commitLine.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		cm := prCommit{Oid: m[1]}
		for j := i + 1; j < len(lines) && !commitLine.MatchString(lines[j]); j++ {
			if msg, ok := strings.CutPrefix(lines[j], "    "); ok {
				if cm.MessageHeadline == "" {
					cm.MessageHeadline = msg
				} else {
					cm.MessageBody = strings.TrimSpace(cm.MessageBody + "\n" + msg)
				}
			}
		}
		commits = append(commits, cm)
	}
	return commits
}

// SaveReviewContext prefetches the issues the PR body links on this forge,
// as fj prints them. fj reports no base commit, so docs from the base
// commit are listed as unavailable; use access: api for them.
func (c *FJ) SaveReviewContext(ctx context.Context, repo string, n int, _ string, issues bool, docs []string, dir string) error {
	p, _, err := c.view(ctx, repo, n)
	if err != nil {
		return err
	}
	w, err := newReviewWriter(dir, "")
	if err != nil {
		return err
	}
	if issues {
		for i, ref := range linkedIssues(p.pr.Body, repo, []string{c.BaseURL}) {
			link := fmt.Sprintf("%s/%s/issues/%d", c.BaseURL, ref.Repo, ref.N)
			text, err := c.fj(ctx, "issue", "view", fmt.Sprintf("%s#%d", ref.Repo, ref.N))
			if err != nil {
				w.manifest.Issues = append(w.manifest.Issues, reviewSource{URL: link, Status: "unavailable"})
				continue
			}
			source, err := w.save(fmt.Sprintf("issue-%02d.txt", i+1), link, []byte(text))
			if err != nil {
				return err
			}
			w.manifest.Issues = append(w.manifest.Issues, source)
		}
	}
	for _, pattern := range docs {
		w.manifest.Docs = append(w.manifest.Docs, reviewSource{URL: pattern, Status: "unavailable: fj reports no base commit; use access: api"})
	}
	return w.close()
}
