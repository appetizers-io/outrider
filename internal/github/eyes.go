package github

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// ReactionContent maps config reactions to GraphQL reaction contents.
var ReactionContent = map[string]string{
	"+1": "THUMBS_UP", "-1": "THUMBS_DOWN", "laugh": "LAUGH", "confused": "CONFUSED",
	"heart": "HEART", "hooray": "HOORAY", "rocket": "ROCKET", "eyes": "EYES",
}

// ReactionEmoji maps config reactions to their emoji.
var ReactionEmoji = map[string]string{
	"+1": "👍", "-1": "👎", "laugh": "😄", "confused": "😕",
	"heart": "❤️", "hooray": "🎉", "rocket": "🚀", "eyes": "👀",
}

// WhereAll are the places a reaction can be, in the order they are checked.
var WhereAll = []string{"description", "comment", "review", "review_comment"}

var whereLabel = map[string]string{
	"description":    "PR description",
	"comment":        "comment",
	"review":         "review",
	"review_comment": "review comment",
}

const eyes = "reactionGroups { content viewerHasReacted }"

var prEyes = `pullRequest(number: %d) {
  ` + eyes + `
  comments(last: 100) { nodes { ` + eyes + ` } }
  reviews(last: 50) { nodes { ` + eyes + ` } }
  reviewThreads(last: 50) { nodes { comments(first: 30) { nodes { ` + eyes + ` } } } }
}`

// Ref is a pull request: owner/repo and number.
type Ref struct {
	Repo string
	N    int
}

// Key is owner/repo#n.
func (r Ref) Key() string { return fmt.Sprintf("%s#%d", r.Repo, r.N) }

type reactionGroup struct {
	Content          string `json:"content"`
	ViewerHasReacted bool   `json:"viewerHasReacted"`
}

type reactable struct {
	ReactionGroups []reactionGroup `json:"reactionGroups"`
}

func (n reactable) mine(content string) bool {
	return slices.ContainsFunc(n.ReactionGroups, func(g reactionGroup) bool {
		return g.Content == content && g.ViewerHasReacted
	})
}

type nodes struct {
	Nodes []reactable `json:"nodes"`
}

type eyesPR struct {
	reactable
	Comments      nodes `json:"comments"`
	Reviews       nodes `json:"reviews"`
	ReviewThreads struct {
		Nodes []struct {
			Comments nodes `json:"comments"`
		} `json:"nodes"`
	} `json:"reviewThreads"`
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// EyesQuery looks up where I put the reaction on each PR in one GraphQL
// query. A PR GitHub doesn't know is missing from the result; nil is "nowhere".
func (c *Client) EyesQuery(ctx context.Context, prs []Ref, reaction string, where []string) (map[string]*string, error) {
	parts := make([]string, len(prs))
	for i, pr := range prs {
		owner, name, _ := strings.Cut(pr.Repo, "/")
		parts[i] = fmt.Sprintf("p%d: repository(owner: %s, name: %s) { %s }", i, quote(owner), quote(name), fmt.Sprintf(prEyes, pr.N))
	}
	query := "query=query { " + strings.Join(parts, " ") + " }"
	res, err := JSON[struct {
		Data map[string]*struct {
			PullRequest *eyesPR `json:"pullRequest"`
		} `json:"data"`
	}](ctx, c, "api", "graphql", "-f", query)
	if err != nil {
		return nil, err
	}
	content := ReactionContent[reaction]
	out := map[string]*string{}
	for i, ref := range prs {
		repo := res.Data[fmt.Sprintf("p%d", i)]
		if repo == nil || repo.PullRequest == nil {
			continue
		}
		pr := repo.PullRequest
		var threadComments []reactable
		for _, t := range pr.ReviewThreads.Nodes {
			threadComments = append(threadComments, t.Comments.Nodes...)
		}
		places := map[string][]reactable{
			"description":    {pr.reactable},
			"comment":        pr.Comments.Nodes,
			"review":         pr.Reviews.Nodes,
			"review_comment": threadComments,
		}
		var found *string
		for _, w := range WhereAll {
			if slices.Contains(where, w) && slices.ContainsFunc(places[w], func(n reactable) bool { return n.mine(content) }) {
				found = new(whereLabel[w])
				break
			}
		}
		out[ref.Key()] = found
	}
	return out, nil
}

// MyEyes is where I put the opt-in reaction on each PR, or nil. PRs whose
// lookup failed are missing from the result.
func (c *Client) MyEyes(ctx context.Context, prs []Ref, reaction string, where []string) map[string]*string {
	const batch = 10
	out := map[string]*string{}
	for chunk := range slices.Chunk(prs, batch) {
		got, err := c.EyesQuery(ctx, chunk, reaction, where)
		if err != nil && len(chunk) > 1 {
			// one broken PR must not hide the others
			for _, pr := range chunk {
				one, err := c.EyesQuery(ctx, []Ref{pr}, reaction, where)
				if err == nil {
					maps.Copy(out, one)
				}
			}
			continue
		}
		maps.Copy(out, got)
	}
	return out
}
