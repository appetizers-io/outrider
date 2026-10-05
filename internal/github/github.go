// Package github reads GitHub through the gh CLI, reusing the user's login.
package github

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/appetizers-io/outrider/internal/proc"
)

// Client runs gh.
type Client struct {
	Run proc.Runner
}

// JSON runs `gh args...` and decodes its output.
func JSON[T any](ctx context.Context, c *Client, args ...string) (T, error) {
	var v T
	cmd := append([]string{"gh"}, args...)
	res, err := c.Run(ctx, proc.Cmd{Args: cmd})
	if err != nil {
		return v, err //nolint:wrapcheck // a *proc.Error names the gh command already
	}
	if err := json.Unmarshal([]byte(res.Stdout), &v); err != nil {
		return v, &proc.Error{Args: cmd, Code: -1, Stderr: "invalid JSON: " + err.Error()}
	}
	return v, nil
}

// API fetches every page of a REST list endpoint.
func API[T any](ctx context.Context, c *Client, endpoint string) ([]T, error) {
	pages, err := JSON[[][]T](ctx, c, "api", endpoint, "--paginate", "--slurp")
	if err != nil {
		return nil, err
	}
	var out []T
	for _, p := range pages {
		out = append(out, p...)
	}
	return out, nil
}

// User is a GitHub account.
type User struct {
	Login string `json:"login"`
	Name  string `json:"name,omitempty"`
}

// Me is the gh user.
func (c *Client) Me(ctx context.Context) (User, error) {
	return JSON[User](ctx, c, "api", "user")
}

// Repo is what GitHub tells about a repository.
type Repo struct {
	FullName string `json:"full_name"`
	Fork     bool   `json:"fork"`
	Parent   *struct {
		FullName string `json:"full_name"`
	} `json:"parent"`
}

// Repo looks an owner/repo up.
func (c *Client) Repo(ctx context.Context, repo string) (Repo, error) {
	return JSON[Repo](ctx, c, "api", "repos/"+repo)
}

// Notification is an entry of the notification feed.
type Notification struct {
	ID         string `json:"id"`
	UpdatedAt  string `json:"updated_at"`
	Reason     string `json:"reason"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Subject struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"subject"`
}

// Notifications are all notifications (read ones too) since an ISO time.
func (c *Client) Notifications(ctx context.Context, since string) ([]Notification, error) {
	return API[Notification](ctx, c, "notifications?all=true&since="+since+"&per_page=50")
}

// Check is an entry of a PR's statusCheckRollup.
type Check struct {
	Name       string `json:"name,omitempty"`
	Context    string `json:"context,omitempty"`
	Conclusion string `json:"conclusion,omitempty"`
	State      string `json:"state,omitempty"`
	Status     string `json:"status,omitempty"`
	Workflow   string `json:"workflowName,omitempty"`
	DetailsURL string `json:"detailsUrl,omitempty"`
	TargetURL  string `json:"targetUrl,omitempty"`
}

// PR is what `gh pr view --json` tells about a pull request.
type PR struct {
	Number int `json:"number"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Title          string `json:"title"`
	URL            string `json:"url"`
	State          string `json:"state"`
	Author         *User  `json:"author"`
	HeadRefName    string `json:"headRefName"`
	BaseRefName    string `json:"baseRefName"`
	HeadRepository *struct {
		Name string `json:"name"`
	} `json:"headRepository"`
	HeadRepositoryOwner *User   `json:"headRepositoryOwner"`
	MaintainerCanModify *bool   `json:"maintainerCanModify"`
	ReviewDecision      string  `json:"reviewDecision"`
	StatusCheckRollup   []Check `json:"statusCheckRollup"`
	Body                string  `json:"body"`
	CreatedAt           string  `json:"createdAt"`
}

// AuthorLogin is the PR author's login, "" when unknown.
func (p PR) AuthorLogin() string {
	if p.Author == nil {
		return ""
	}
	return p.Author.Login
}

// Open tells whether the PR is open.
func (p PR) Open() bool { return p.State == "OPEN" }

const prFields = "number,title,url,state,author,headRefName,baseRefName," +
	"headRepository,headRepositoryOwner,maintainerCanModify,reviewDecision," +
	"statusCheckRollup,body,createdAt,labels"

// PRView looks a pull request up.
func (c *Client) PRView(ctx context.Context, repo string, n int) (PR, error) {
	return JSON[PR](ctx, c, "pr", "view", strconv.Itoa(n), "--repo", repo, "--json", prFields)
}

var badConclusions = []string{"FAILURE", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "ERROR", "STARTUP_FAILURE"}

// FailingChecks are the names of the PR's failed checks.
func FailingChecks(p PR) []string {
	out := []string{}
	for _, c := range p.StatusCheckRollup {
		if failed(c) {
			name := c.Name
			if name == "" {
				name = c.Context
			}
			if name == "" {
				name = "?"
			}
			out = append(out, name)
		}
	}
	return out
}

func failed(c Check) bool {
	state := c.Conclusion
	if state == "" {
		state = c.State
	}
	return slices.Contains(badConclusions, state)
}

// Comment is a review, an inline review comment or a conversation comment as
// the REST API returns it. Pointers are null when GitHub leaves them out.
type Comment struct {
	ID          *int64  `json:"id"`
	State       *string `json:"state"`
	UpdatedAt   *string `json:"updated_at"`
	SubmittedAt *string `json:"submitted_at"`
	CreatedAt   string  `json:"created_at"`
	User        *User   `json:"user"`
	Path        *string `json:"path"`
	Body        *string `json:"body"`
	HTMLURL     *string `json:"html_url"`
	InReplyToID *int64  `json:"in_reply_to_id"`
}

// UserLogin is the comment author's login, "" when unknown.
func (c Comment) UserLogin() string {
	if c.User == nil {
		return ""
	}
	return c.User.Login
}

// Activity is a review, inline comment or conversation comment on a PR.
type Activity struct {
	Kind        string  `json:"kind"`
	ID          *int64  `json:"-"`
	State       *string `json:"state"`
	UpdatedAt   *string `json:"-"`
	SubmittedAt *string `json:"-"`
	User        *string `json:"user"`
	Path        *string `json:"path"`
	At          string  `json:"at"` // updated_at or submitted_at: when it last changed
	Body        string  `json:"body"`
	URL         *string `json:"url"`
}

// UserLogin is the author's login, "" when unknown.
func (a Activity) UserLogin() string {
	if a.User == nil {
		return ""
	}
	return *a.User
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ActivityOf turns an API comment into an Activity of the given kind.
func ActivityOf(kind string, c Comment) Activity {
	a := Activity{
		Kind: kind, ID: c.ID, State: c.State, UpdatedAt: c.UpdatedAt,
		SubmittedAt: c.SubmittedAt, Path: c.Path, Body: deref(c.Body), URL: c.HTMLURL,
	}
	if c.User != nil {
		a.User = &c.User.Login
	}
	a.At = deref(c.UpdatedAt)
	if a.At == "" {
		a.At = deref(c.SubmittedAt)
	}
	return a
}

// Activity is the reviews, inline comments and conversation comments of a PR.
func (c *Client) Activity(ctx context.Context, repo string, n int) ([]Activity, error) {
	var out []Activity
	for _, src := range []struct{ kind, endpoint string }{
		{"review", fmt.Sprintf("repos/%s/pulls/%d/reviews?per_page=100", repo, n)},
		{"inline comment", fmt.Sprintf("repos/%s/pulls/%d/comments?per_page=100", repo, n)},
		{"comment", fmt.Sprintf("repos/%s/issues/%d/comments?per_page=100", repo, n)},
	} {
		items, err := API[Comment](ctx, c, src.endpoint)
		if err != nil {
			return nil, err
		}
		for _, x := range items {
			out = append(out, ActivityOf(src.kind, x))
		}
	}
	return out, nil
}

// PendingReplies are the review threads login took part in whose last
// comment isn't theirs, oldest comment first.
func (c *Client) PendingReplies(ctx context.Context, repo string, n int, login string) ([][]Comment, error) {
	comments, err := API[Comment](ctx, c, fmt.Sprintf("repos/%s/pulls/%d/comments?per_page=100", repo, n))
	if err != nil {
		return nil, err
	}
	var order []int64
	threads := map[int64][]Comment{}
	for _, cm := range comments {
		root := int64(0)
		if cm.InReplyToID != nil && *cm.InReplyToID != 0 {
			root = *cm.InReplyToID
		} else if cm.ID != nil {
			root = *cm.ID
		}
		if _, ok := threads[root]; !ok {
			order = append(order, root)
		}
		threads[root] = append(threads[root], cm)
	}
	var out [][]Comment
	me := strings.ToLower(login)
	for _, root := range order {
		cs := threads[root]
		slices.SortStableFunc(cs, func(a, b Comment) int { return cmp.Compare(idOf(a), idOf(b)) })
		took, last := false, ""
		for _, cm := range cs {
			last = strings.ToLower(cm.UserLogin())
			took = took || last == me
		}
		if took && last != me {
			out = append(out, cs)
		}
	}
	return out, nil
}

func idOf(c Comment) int64 {
	if c.ID == nil {
		return 0
	}
	return *c.ID
}

// Involved is an open PR found by search.
type Involved struct {
	Repo   string
	N      int
	Author string
}

// InvolvedPRs are open PRs login is involved in or asked to review,
// notification or not, keyed by owner/repo#n.
func (c *Client) InvolvedPRs(ctx context.Context, login string) (map[string]Involved, error) {
	type result struct {
		Items []struct {
			RepositoryURL string `json:"repository_url"`
			Number        int    `json:"number"`
			User          *User  `json:"user"`
		} `json:"items"`
	}
	out := map[string]Involved{}
	for _, q := range []string{"involves:" + login, "review-requested:" + login} {
		res, err := JSON[result](ctx, c, "api", "-X", "GET", "search/issues",
			"-f", "q=is:pr is:open archived:false "+q, "-f", "per_page=100")
		if err != nil {
			return nil, err
		}
		for _, x := range res.Items {
			_, repo, _ := strings.Cut(x.RepositoryURL, "/repos/")
			author := ""
			if x.User != nil {
				author = x.User.Login
			}
			out[fmt.Sprintf("%s#%d", repo, x.Number)] = Involved{Repo: repo, N: x.Number, Author: author}
		}
	}
	return out, nil
}
