// Package forge is the small interface between outrider and a code forge.
// GitHub (internal/github, through gh) is the first implementation and the
// only one the watcher uses; Forgejo (internal/forgejo) implements Reader,
// which is what rendering a review needs.
//
// The types are the ones internal/github already uses: another forge maps
// its pull requests, users and comments onto them.
package forge

import (
	"context"

	"github.com/appetizers-io/outrider/internal/github"
)

// Reader is what a review of one pull request needs: who is logged in, the
// repository and pull request, and the offline context of a read-only
// session. repo is owner/repo on the reader's own host.
type Reader interface {
	Me(ctx context.Context) (github.User, error)
	Repo(ctx context.Context, repo string) (github.Repo, error)
	PRView(ctx context.Context, repo string, n int) (github.PR, error)
	// SaveContext writes the pull request's context files into dir.
	SaveContext(ctx context.Context, repo string, n int, dir string) error
	// SaveReviewContext writes linked issues and docs from the base commit
	// into dir, with a manifest. worktree is a checkout that holds the base
	// commit, for a forge whose docs are read with git.
	SaveReviewContext(ctx context.Context, repo string, n int, worktree string, issues bool, docs []string, dir string) error
}

// Watcher is what the poll loop reads besides a Reader: the activity that
// starts sessions.
type Watcher interface {
	Reader
	Notifications(ctx context.Context, since string) ([]github.Notification, error)
	Activity(ctx context.Context, repo string, n int) ([]github.Activity, error)
	PendingReplies(ctx context.Context, repo string, n int, login string) ([][]github.Comment, error)
	InvolvedPRs(ctx context.Context, login string) (map[string]github.Involved, error)
	MyEyes(ctx context.Context, prs []github.Ref, reaction string, where []string) map[string]*string
	DiscussionsResolved(ctx context.Context, repo string, n int) (bool, error)
}

var _ Watcher = (*github.Client)(nil)
