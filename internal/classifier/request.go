package classifier

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/appetizers-io/outrider/internal/github"
)

// Request is what a launch-check classifier judges.
type Request struct {
	Version       int               `json:"version"`
	Repo          string            `json:"repo"`
	PR            int               `json:"pr"`
	Title         string            `json:"title"`
	URL           string            `json:"url"`
	Author        string            `json:"author"`
	Own           bool              `json:"own"`
	Owner         string            `json:"owner"`
	OwnerLogin    string            `json:"owner_login"`
	Trigger       string            `json:"trigger"`
	FailingChecks []string          `json:"failing_checks"`
	Activity      []github.Activity `json:"activity"`
	Question      string            `json:"question"`
	StateText     string            `json:"state_text"`
}

func orNone(s *string) string {
	if s == nil {
		return "None"
	}
	return *s
}

// NewRequest describes new activity on a PR for the launch check. owner is
// how prompts call the user, login their GitHub login.
func NewRequest(repo string, n int, pr github.PR, trigger string, items []github.Activity, owner, login string) Request {
	author := pr.AuthorLogin()
	own := strings.EqualFold(author, login)
	whose := owner + "'s own PR"
	if !own {
		whose = "someone else's PR; " + owner + " is a reviewer"
	}
	failing := github.FailingChecks(pr)
	none := "none"
	if len(failing) > 0 {
		none = strings.Join(failing, ", ")
	}
	lines := []string{
		fmt.Sprintf("GitHub pull request %s#%d: %s", repo, n, pr.Title),
		fmt.Sprintf("PR author: %s (%s)", author, whose),
		fmt.Sprintf("%s's GitHub login: %s", owner, login),
		"Why this check runs: " + trigger,
		"Failing CI checks: " + none,
		"",
		"New activity since the last check (oldest first):",
	}
	budget := 40000
	var entries []string
	for _, x := range slices.Backward(items) { // keep the newest when trimming
		head := fmt.Sprintf("- %s by %s at %s", x.Kind, orNone(x.User), x.At)
		if x.State != nil && *x.State != "" {
			head += " [" + *x.State + "]"
		}
		if x.Path != nil && *x.Path != "" {
			head += " on " + *x.Path
		}
		body := strings.TrimSpace(x.Body)
		if r := []rune(body); len(r) > 1500 {
			body = string(r[:1500])
		}
		body = strings.ReplaceAll(body, "\n", "\n  ")
		entry := head
		if body != "" {
			entry += ":\n  " + body
		}
		budget -= utf8.RuneCountInString(entry)
		if budget < 0 {
			break
		}
		entries = append(entries, entry)
	}
	if len(entries) == 0 {
		lines = append(lines, "- (none)")
	}
	slices.Reverse(entries)
	lines = append(lines, entries...)
	if items == nil {
		items = []github.Activity{}
	}
	return Request{
		Version: 1, Repo: repo, PR: n, Title: pr.Title, URL: pr.URL, Author: author, Own: own,
		Owner: owner, OwnerLogin: login, Trigger: trigger, FailingChecks: failing, Activity: items,
		Question: fmt.Sprintf(Question, owner), StateText: strings.Join(lines, "\n"),
	}
}
