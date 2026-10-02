package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/appetizers-io/outrider/internal/proc"
)

// worktree checks the PR out in its own worktree, off the local checkout of
// the repo or a cached clone, and returns its path.
func (l *Launcher) worktree(ctx context.Context, repo string, n int) (string, error) {
	slug := strings.ReplaceAll(repo, "/", "__")
	clone, remote := filepath.Join(l.Root, "repos", slug), "origin"
	if loc, ok := l.Local[repo]; ok {
		clone, remote = loc.Path, loc.Remote
	}
	wt := filepath.Join(l.Root, "worktrees", slug, "pr-"+itoa(n))
	branch := "review/pr-" + itoa(n)
	run := func(dir string, timeout time.Duration, args ...string) (proc.Result, error) {
		res, err := l.Run(ctx, proc.Cmd{Args: args, Dir: dir, Timeout: timeout})
		if err != nil {
			return res, fmt.Errorf("worktree for %s#%d: %w", repo, n, err)
		}
		return res, nil
	}

	if _, err := os.Stat(clone); err != nil {
		if err := os.MkdirAll(filepath.Dir(clone), 0o700); err != nil {
			return "", fmt.Errorf("clone dir: %w", err)
		}
		l.Log.Info("cloning " + repo)
		if _, err := run("", 30*time.Minute, "gh", "repo", "clone", repo, clone, "--", "--filter=blob:none"); err != nil {
			return "", err
		}
	}

	checkout := []string{"gh", "pr", "checkout", itoa(n), "--repo", repo, "--branch", branch}
	if _, err := os.Stat(wt); err != nil {
		if err := os.MkdirAll(filepath.Dir(wt), 0o700); err != nil {
			return "", fmt.Errorf("worktree dir: %w", err)
		}
		l.Log.Info(fmt.Sprintf("creating worktree for %s#%d from %s", repo, n, clone))
		for _, step := range [][]string{
			{"git", "fetch", "--quiet", remote},
			{"git", "worktree", "prune"},
			{"git", "worktree", "add", "--detach", wt},
		} {
			timeout := time.Duration(0)
			if step[1] == "fetch" {
				timeout = 15 * time.Minute
			}
			if _, err := run(clone, timeout, step...); err != nil {
				return "", err
			}
		}
		// gh sets up the head branch + push remote (also for forks)
		if _, err := run(wt, 0, checkout...); err != nil {
			return "", err
		}
		return wt, nil
	}
	status, err := run(wt, 0, "git", "status", "--porcelain")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(status.Stdout) == "" {
		_, _ = run(wt, 0, checkout...)
	} else {
		l.Log.Info(fmt.Sprintf("%s#%d: preserving existing local changes", repo, n))
	}
	return wt, nil
}
