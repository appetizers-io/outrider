package github

import (
	"context"
	"fmt"
	"strings"
)

// CIPassed requires at least one check and every reported check to succeed.
// Pending, missing, skipped, neutral and failed checks do not satisfy it.
func CIPassed(pr PR) bool {
	if len(pr.StatusCheckRollup) == 0 {
		return false
	}
	for _, check := range pr.StatusCheckRollup {
		if check.Conclusion != "" {
			if check.Status != "COMPLETED" || check.Conclusion != "SUCCESS" {
				return false
			}
		} else if check.State != "SUCCESS" {
			return false
		}
	}
	return true
}

// DiscussionsResolved checks every review-thread page. No threads means false:
// a PR without review discussion has nothing that could have been resolved.
func (c *Client) DiscussionsResolved(ctx context.Context, repo string, n int) (bool, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok {
		return false, fmt.Errorf("invalid repository %q", repo)
	}
	cursor := ""
	found := false
	for {
		query := fmt.Sprintf(`query { repository(owner: %q, name: %q) { pullRequest(number: %d) { reviewThreads(first: 100%s) { nodes { isResolved } pageInfo { hasNextPage endCursor } } } } }`, owner, name, n, cursor)
		result, err := JSON[struct {
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
			Data struct {
				Repository *struct {
					PR *struct {
						Threads *struct {
							Nodes []struct {
								Resolved *bool `json:"isResolved"`
							} `json:"nodes"`
							Page *struct {
								Next   *bool  `json:"hasNextPage"`
								Cursor string `json:"endCursor"`
							} `json:"pageInfo"`
						} `json:"reviewThreads"`
					} `json:"pullRequest"`
				} `json:"repository"`
			} `json:"data"`
		}](ctx, c, "api", "graphql", "-f", "query="+query)
		if err != nil {
			return false, err
		}
		if len(result.Errors) > 0 || result.Data.Repository == nil || result.Data.Repository.PR == nil || result.Data.Repository.PR.Threads == nil {
			return false, fmt.Errorf("review threads unavailable for %s#%d", repo, n)
		}
		threads := result.Data.Repository.PR.Threads
		if threads.Page == nil || threads.Page.Next == nil || threads.Nodes == nil {
			return false, fmt.Errorf("review thread page incomplete for %s#%d", repo, n)
		}
		for _, t := range threads.Nodes {
			if t.Resolved == nil {
				return false, fmt.Errorf("review thread resolution missing for %s#%d", repo, n)
			}
			found = true
			if !*t.Resolved {
				return false, nil
			}
		}
		if !*threads.Page.Next {
			return found, nil
		}
		next := fmt.Sprintf(", after: %q", threads.Page.Cursor)
		if threads.Page.Cursor == "" || next == cursor {
			return false, fmt.Errorf("review thread pagination stalled for %s#%d", repo, n)
		}
		cursor = next
	}
}
