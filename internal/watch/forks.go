package watch

import (
	"context"
	"fmt"
	"strings"

	"github.com/appetizers-io/outrider/internal/config"
	"github.com/appetizers-io/outrider/internal/github"
	"github.com/appetizers-io/outrider/internal/guard"
	"github.com/appetizers-io/outrider/internal/proc"
	"github.com/appetizers-io/outrider/internal/session"
)

// ReviewForks are the effective others_prs.review_forks and why.
type ReviewForks struct {
	Forks  []string // owner/repo globs; empty: off
	Reason string   // "config", "auto: origin", or why auto is off
	Failed bool     // auto is off because a lookup failed
}

func (rf ReviewForks) String() string {
	if len(rf.Forks) == 0 {
		return "off (" + rf.Reason + ")"
	}
	return strings.Join(rf.Forks, ", ") + " (" + rf.Reason + ")"
}

// ResolveReviewForks decides others_prs.review_forks. A list in the config
// is used as is ([] is off). Unset is auto: the checkout's origin is a
// review fork when repo is watched through another remote, origin's push
// URL is a GitHub repo of login, and GitHub says it is a fork of repo. Any
// failed lookup leaves auto off.
func ResolveReviewForks(ctx context.Context, cfg *config.Config, repo string, loc session.Local, login string, d Deps) ReviewForks {
	off := func(format string, a ...any) ReviewForks { return ReviewForks{Reason: fmt.Sprintf(format, a...)} }
	failed := func(format string, a ...any) ReviewForks {
		rf := off(format, a...)
		rf.Failed = true
		return rf
	}
	switch {
	case cfg.OthersPRs.ReviewForks != nil && len(*cfg.OthersPRs.ReviewForks) == 0:
		return off("review_forks: []")
	case cfg.OthersPRs.ReviewForks != nil:
		return ReviewForks{Forks: *cfg.OthersPRs.ReviewForks, Reason: "config"}
	case cfg.SandboxFor(false) == config.ReadOnly:
		return off("others' PRs run read-only")
	case repo == "":
		return off("not in a local checkout")
	case loc.Remote == "origin":
		return off("watching origin itself")
	}
	res, err := d.Run(ctx, proc.Cmd{Args: []string{"git", "remote", "get-url", "--push", "--all", "--", "origin"}, Dir: loc.Path})
	if err != nil {
		return failed("cannot read origin's push URL: %v", err)
	}
	urls := strings.Fields(res.Stdout)
	if len(urls) != 1 {
		return off("origin has %d push URLs", len(urls))
	}
	origin, err := guard.GitHubRepo(urls[0])
	if err != nil {
		return off("origin is not a GitHub repo: %v", err)
	}
	owner, _, _ := strings.Cut(origin, "/")
	if login == "" {
		return failed("GitHub user unknown")
	}
	if !strings.EqualFold(owner, login) {
		return off("origin %s is not your fork", origin)
	}
	info, err := (&github.Client{Run: d.Run}).Repo(ctx, origin)
	switch {
	case err != nil:
		return failed("cannot look up origin %s on GitHub: %v", origin, err)
	case !strings.EqualFold(info.FullName, origin):
		return off("origin %s is now %s on GitHub", origin, info.FullName)
	case !info.Fork || info.Parent == nil:
		return off("origin %s is not a fork", origin)
	case !strings.EqualFold(info.Parent.FullName, repo):
		return off("origin %s is a fork of %s, not of %s", origin, info.Parent.FullName, repo)
	}
	return ReviewForks{Forks: []string{origin}, Reason: "auto: origin"}
}
