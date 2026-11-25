package forgejo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/gobwas/glob"

	"github.com/appetizers-io/outrider/internal/github"
)

// API reads a Forgejo host through its REST API with a token.
type API struct {
	Host    string // as configured
	BaseURL string // the web root, without a trailing slash; the API is at BaseURL/api/v1
	HTTP    *http.Client

	token string // sent in the Authorization header, nowhere else
}

// ContextFiles are the files SaveContext writes.
func (c *API) ContextFiles() [][2]string { return apiContextFiles }

// maxResponse caps a response body: a PR's text is untrusted and can be huge.
const maxResponse = 64 << 20

// pageSize is what each page asks for; a server may cap it lower.
const pageSize = 50

// maxPages stops a listing that never ends.
const maxPages = 200

// redact removes the token from text that may end up in an error.
func (c *API) redact(s string) string {
	if c.token == "" {
		return s
	}
	return strings.ReplaceAll(s, c.token, "[redacted]")
}

// apiError is a failed request: the method, the path and what the server
// said, with the token removed.
type apiError struct {
	Status int
	msg    string
}

func (e *apiError) Error() string { return e.msg }

// get fetches BaseURL/api/v1/path. Errors never carry the token.
func (c *API) get(ctx context.Context, path string, query url.Values) ([]byte, http.Header, error) {
	u := c.BaseURL + "/api/v1/" + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("forge %s: %s", c.Host, c.redact(err.Error()))
	}
	req.Header.Set("Authorization", "token "+c.token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("forge %s: GET %s: %s", c.Host, path, c.redact(unwrapURLError(err)))
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return nil, nil, fmt.Errorf("forge %s: GET %s: %s", c.Host, path, c.redact(err.Error()))
	}
	if len(body) > maxResponse {
		return nil, nil, fmt.Errorf("forge %s: GET %s: response larger than %d bytes", c.Host, path, maxResponse)
	}
	if resp.StatusCode/100 != 2 {
		return nil, nil, &apiError{Status: resp.StatusCode, msg: fmt.Sprintf("forge %s: GET %s: %s%s", c.Host, path, resp.Status, serverMessage(body, c.redact))}
	}
	return body, resp.Header, nil
}

// unwrapURLError is a transport error without the request URL, which
// names the query; the caller names the path.
func unwrapURLError(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return err.Error()
}

// serverMessage is the message of an error response, redacted before it
// is cut short so no part of the token survives the cut.
func serverMessage(body []byte, redact func(string) string) string {
	var m struct {
		Message string `json:"message"`
	}
	msg := strings.TrimSpace(string(body))
	if json.Unmarshal(body, &m) == nil && m.Message != "" {
		msg = m.Message
	}
	msg = redact(msg)
	if len(msg) > 200 {
		msg = strings.ToValidUTF8(msg[:200], "") + "…"
	}
	if msg == "" {
		return ""
	}
	return ": " + msg
}

func getJSON[T any](ctx context.Context, c *API, path string, query url.Values) (T, error) {
	var v T
	body, _, err := c.get(ctx, path, query)
	if err != nil {
		return v, err
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return v, fmt.Errorf("forge %s: GET %s: invalid JSON: %w", c.Host, path, err)
	}
	return v, nil
}

// list fetches every page of a list endpoint.
func list[T any](ctx context.Context, c *API, path string, query url.Values) ([]T, error) {
	return pages(ctx, c, path, query, func(body []byte) ([]T, error) {
		var items []T
		err := json.Unmarshal(body, &items)
		return items, err
	})
}

// pages fetches every page of a paged endpoint, decode taking the items out
// of each. It stops once it has the X-Total-Count items the server reports;
// without that header, at a short or empty page, or at a page that repeats
// the one before (an endpoint that ignores paging).
func pages[T any](ctx context.Context, c *API, path string, query url.Values, decode func([]byte) ([]T, error)) ([]T, error) {
	out := []T{}
	var last []byte
	for page := 1; page <= maxPages; page++ {
		q := url.Values{}
		for k, v := range query {
			q[k] = v
		}
		q.Set("page", strconv.Itoa(page))
		q.Set("limit", strconv.Itoa(pageSize))
		body, header, err := c.get(ctx, path, q)
		if err != nil {
			return nil, err
		}
		items, err := decode(body)
		if err != nil {
			return nil, fmt.Errorf("forge %s: GET %s: invalid JSON: %w", c.Host, path, err)
		}
		if len(items) == 0 || bytes.Equal(body, last) {
			return out, nil
		}
		last = body
		out = append(out, items...)
		total, err := strconv.Atoi(header.Get("X-Total-Count"))
		if (err == nil && len(out) >= total) || (err != nil && len(items) < pageSize) {
			return out, nil
		}
	}
	return out, nil
}

// repoPath is repos/owner/name, each part escaped.
func repoPath(repo string) (string, error) {
	if err := checkRepo(repo); err != nil {
		return "", err
	}
	owner, name, _ := strings.Cut(repo, "/")
	return "repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name), nil
}

type apiUser struct {
	Login    string `json:"login"`
	FullName string `json:"full_name"`
	Email    string `json:"email"`
}

type apiRepo struct {
	Name     string   `json:"name"`
	FullName string   `json:"full_name"`
	Owner    *apiUser `json:"owner"`
	HTMLURL  string   `json:"html_url"`
	Fork     bool     `json:"fork"`
	Parent   *struct {
		FullName string `json:"full_name"`
	} `json:"parent"`
}

type apiBranch struct {
	Ref  string   `json:"ref"`
	Sha  string   `json:"sha"`
	Repo *apiRepo `json:"repo"`
}

type apiPR struct {
	Number              int        `json:"number"`
	Title               string     `json:"title"`
	Body                string     `json:"body"`
	State               string     `json:"state"`
	HTMLURL             string     `json:"html_url"`
	User                *apiUser   `json:"user"`
	Labels              []prLabel  `json:"labels"`
	Draft               bool       `json:"draft"`
	Merged              bool       `json:"merged"`
	AllowMaintainerEdit *bool      `json:"allow_maintainer_edit"`
	Head                apiBranch  `json:"head"`
	Base                apiBranch  `json:"base"`
	CreatedAt           string     `json:"created_at"`
	statuses            []apiState // the head's commit statuses, fetched separately
}

type apiState struct {
	Context   string `json:"context"`
	Status    string `json:"status"`
	TargetURL string `json:"target_url"`
}

// Me is the token's user.
func (c *API) Me(ctx context.Context) (github.User, error) {
	u, err := getJSON[apiUser](ctx, c, "user", nil)
	if err != nil {
		return github.User{}, err
	}
	return github.User{Login: u.Login, Name: u.FullName}, nil
}

// Repo looks owner/repo up.
func (c *API) Repo(ctx context.Context, repo string) (github.Repo, error) {
	path, err := repoPath(repo)
	if err != nil {
		return github.Repo{}, err
	}
	return getJSON[github.Repo](ctx, c, path, nil)
}

// pull is a PR with the commit statuses of its head.
func (c *API) pull(ctx context.Context, repo string, n int) (apiPR, error) {
	path, err := repoPath(repo)
	if err != nil {
		return apiPR{}, err
	}
	pr, err := getJSON[apiPR](ctx, c, fmt.Sprintf("%s/pulls/%d", path, n), nil)
	if err != nil {
		return apiPR{}, err
	}
	if pr.Number != n {
		return apiPR{}, fmt.Errorf("forge %s: %s#%d: the server returned PR #%d", c.Host, repo, n, pr.Number)
	}
	if objectID.MatchString(pr.Head.Sha) {
		statuses, err := pages(ctx, c, path+"/commits/"+pr.Head.Sha+"/status", nil, func(body []byte) ([]apiState, error) {
			var combined struct {
				Statuses []apiState `json:"statuses"`
			}
			err := json.Unmarshal(body, &combined)
			return combined.Statuses, err
		})
		if err != nil {
			return apiPR{}, err
		}
		pr.statuses = statuses
	}
	return pr, nil
}

// toPR maps a Forgejo PR onto github.PR. A head repository Forgejo no longer
// knows (a deleted fork) stays nil, which callers treat as a fork.
func toPR(p apiPR) github.PR {
	out := github.PR{
		Number: p.Number, Title: p.Title, URL: p.HTMLURL, Body: p.Body, CreatedAt: p.CreatedAt,
		State: state(p.State == "open", p.Merged), HeadRefName: p.Head.Ref, BaseRefName: p.Base.Ref,
		MaintainerCanModify: p.AllowMaintainerEdit,
	}
	if p.User != nil {
		out.Author = &github.User{Login: p.User.Login, Name: p.User.FullName}
	}
	for _, l := range p.Labels {
		out.Labels = append(out.Labels, struct {
			Name string `json:"name"`
		}{Name: l.Name})
	}
	if r := p.Head.Repo; r != nil && r.Name != "" && r.Owner != nil && r.Owner.Login != "" {
		out.HeadRepository = &struct {
			Name string `json:"name"`
		}{Name: r.Name}
		out.HeadRepositoryOwner = &github.User{Login: r.Owner.Login}
	}
	for _, s := range p.statuses {
		out.StatusCheckRollup = append(out.StatusCheckRollup, github.Check{
			Context: s.Context, State: strings.ToUpper(s.Status), TargetURL: s.TargetURL,
		})
	}
	return out
}

// PRView looks a pull request up, with its head's commit statuses as checks.
func (c *API) PRView(ctx context.Context, repo string, n int) (github.PR, error) {
	pr, err := c.pull(ctx, repo, n)
	if err != nil {
		return github.PR{}, err
	}
	return toPR(pr), nil
}

type apiFile struct {
	Filename  string `json:"filename"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

type apiCommit struct {
	Sha    string   `json:"sha"`
	Author *apiUser `json:"author"`
	Commit struct {
		Message string `json:"message"`
		Author  struct {
			Name  string `json:"name"`
			Email string `json:"email"`
			Date  string `json:"date"`
		} `json:"author"`
		Committer struct {
			Date string `json:"date"`
		} `json:"committer"`
	} `json:"commit"`
}

type apiReview struct {
	ID            int64    `json:"id"`
	User          *apiUser `json:"user"`
	State         string   `json:"state"`
	Body          string   `json:"body"`
	SubmittedAt   string   `json:"submitted_at"`
	HTMLURL       string   `json:"html_url"`
	CommentsCount int      `json:"comments_count"`
}

type apiComment struct {
	ID        int64    `json:"id"`
	User      *apiUser `json:"user"`
	Body      string   `json:"body"`
	CreatedAt string   `json:"created_at"`
	HTMLURL   string   `json:"html_url"`
}

func login(u *apiUser) *person {
	if u == nil {
		return nil
	}
	return &person{Login: u.Login, Name: u.FullName}
}

// SaveContext writes the PR context into dir for a session without network:
// the files in ContextFiles.
func (c *API) SaveContext(ctx context.Context, repo string, n int, dir string) error {
	pr, err := c.pull(ctx, repo, n)
	if err != nil {
		return err
	}
	path, _ := repoPath(repo) // checked by pull
	pull := fmt.Sprintf("%s/pulls/%d", path, n)
	files, err := list[apiFile](ctx, c, pull+"/files", nil)
	if err != nil {
		return err
	}
	commits, err := list[apiCommit](ctx, c, pull+"/commits", url.Values{"stat": {"false"}, "verification": {"false"}, "files": {"false"}})
	if err != nil {
		return err
	}
	reviews, err := list[apiReview](ctx, c, pull+"/reviews", nil)
	if err != nil {
		return err
	}
	comments, err := list[apiComment](ctx, c, fmt.Sprintf("%s/issues/%d/comments", path, n), nil)
	if err != nil {
		return err
	}
	inline := []json.RawMessage{}
	for _, r := range reviews {
		if r.CommentsCount == 0 {
			continue
		}
		// not paged: one request returns every comment of the review
		got, err := getJSON[[]json.RawMessage](ctx, c, fmt.Sprintf("%s/reviews/%d/comments", pull, r.ID), nil)
		if err != nil {
			return err
		}
		inline = append(inline, got...)
	}
	diff, _, err := c.get(ctx, pull+".diff", nil)
	if err != nil {
		return err
	}

	out := fromPR(toPR(pr))
	out.IsDraft, out.HeadRefOid, out.BaseRefOid = pr.Draft, pr.Head.Sha, pr.Base.Sha
	for _, f := range files {
		out.Files = append(out.Files, prFile{Path: f.Filename, Additions: f.Additions, Deletions: f.Deletions, ChangeType: strings.ToUpper(f.Status)})
	}
	for _, cm := range commits {
		headline, body, _ := strings.Cut(strings.TrimRight(cm.Commit.Message, "\n"), "\n")
		author := person{Login: "", Name: cm.Commit.Author.Name}
		if cm.Author != nil {
			author.Login = cm.Author.Login
		}
		out.Commits = append(out.Commits, prCommit{Oid: cm.Sha, MessageHeadline: headline, MessageBody: strings.TrimSpace(body),
			Authors: []person{author}, CommittedDate: cm.Commit.Committer.Date})
	}
	out.Reviews = []prReview{}
	for _, r := range reviews {
		out.Reviews = append(out.Reviews, prReview{ID: r.ID, Author: login(r.User), State: r.State, Body: r.Body, SubmittedAt: r.SubmittedAt, URL: r.HTMLURL})
	}
	out.Comments = []prComment{}
	for _, cm := range comments {
		out.Comments = append(out.Comments, prComment{ID: cm.ID, Author: login(cm.User), Body: cm.Body, CreatedAt: cm.CreatedAt, URL: cm.HTMLURL})
	}
	return writeContext(dir, out, string(diff), inline, failing(out.StatusCheckRollup))
}

// objectID is a full commit id, SHA-1 or SHA-256.
var objectID = regexp.MustCompile(`^[a-f0-9]{40}([a-f0-9]{24})?$`)

// webBase is where the forge says its pages are: the repository's html_url
// without owner/repo. It can differ from BaseURL (the server's ROOT_URL).
func webBase(repoURL, repo string) string {
	base, ok := strings.CutSuffix(strings.TrimRight(repoURL, "/"), "/"+repo)
	if !ok {
		return ""
	}
	return base
}

// SaveReviewContext prefetches capped linked issues on this forge and docs
// from the PR's immutable base commit, read through the API; worktree is
// not used. The PR head never supplies the spec.
func (c *API) SaveReviewContext(ctx context.Context, repo string, n int, _ string, issues bool, docs []string, dir string) error {
	pr, err := c.pull(ctx, repo, n)
	if err != nil {
		return err
	}
	if !objectID.MatchString(pr.Base.Sha) {
		return errors.New("review context: missing immutable base commit")
	}
	path, _ := repoPath(repo)
	web := c.BaseURL
	if pr.Base.Repo != nil {
		if b := webBase(pr.Base.Repo.HTMLURL, repo); b != "" {
			web = b
		}
	}
	w, err := newReviewWriter(dir, pr.Base.Sha)
	if err != nil {
		return err
	}
	if issues {
		for i, ref := range linkedIssues(pr.Body, repo, []string{web, c.BaseURL}) {
			link := fmt.Sprintf("%s/%s/issues/%d", web, ref.Repo, ref.N)
			issuePath, _ := repoPath(ref.Repo)
			raw, _, err := c.get(ctx, fmt.Sprintf("%s/issues/%d", issuePath, ref.N), nil)
			if err != nil {
				w.manifest.Issues = append(w.manifest.Issues, reviewSource{URL: link, Status: "unavailable"})
				continue
			}
			source, err := w.save(fmt.Sprintf("issue-%02d.json", i+1), link, raw)
			if err != nil {
				return err
			}
			w.manifest.Issues = append(w.manifest.Issues, source)
		}
	}
	if len(docs) > 0 {
		patterns := []*glob.Pattern{}
		for _, pattern := range docs {
			g, err := glob.Compile(pattern, '/')
			if err != nil {
				return fmt.Errorf("review docs glob: %w", err)
			}
			patterns = append(patterns, g)
		}
		tree, err := c.tree(ctx, path, pr.Base.Sha)
		if err != nil {
			return err
		}
		for _, file := range tree {
			match := false
			for _, g := range patterns {
				if g.Match(file) {
					match = true
					break
				}
			}
			if !match || len(w.manifest.Docs) >= maxReviewItems {
				continue
			}
			link := web + "/" + repo + "/src/commit/" + pr.Base.Sha + "/" + escapePath(file)
			raw, _, err := c.get(ctx, path+"/raw/"+escapePath(file), url.Values{"ref": {pr.Base.Sha}})
			if err != nil {
				w.manifest.Docs = append(w.manifest.Docs, reviewSource{URL: link, Status: "unavailable"})
				continue
			}
			source, err := w.save(fmt.Sprintf("doc-%02d.txt", len(w.manifest.Docs)+1), link, raw)
			if err != nil {
				return err
			}
			w.manifest.Docs = append(w.manifest.Docs, source)
		}
	}
	return w.close()
}

// escapePath escapes each segment of a repository path.
func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

// tree is every file path at commit sha, in the order the server lists them.
func (c *API) tree(ctx context.Context, path, sha string) ([]string, error) {
	var out []string
	entries := 0
	for page := 1; page <= maxPages; page++ {
		got, err := getJSON[struct {
			Tree []struct {
				Path string `json:"path"`
				Type string `json:"type"`
			} `json:"tree"`
			Truncated  bool `json:"truncated"`
			TotalCount int  `json:"total_count"`
		}](ctx, c, path+"/git/trees/"+sha, url.Values{"recursive": {"true"}, "page": {strconv.Itoa(page)}, "per_page": {"1000"}})
		if err != nil {
			return nil, err
		}
		entries += len(got.Tree)
		for _, e := range got.Tree {
			if e.Type == "blob" {
				out = append(out, e.Path)
			}
		}
		if len(got.Tree) == 0 || (!got.Truncated && entries >= got.TotalCount) {
			return out, nil
		}
	}
	return out, nil
}
