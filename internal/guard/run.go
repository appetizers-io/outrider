package guard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/appetizers-io/outrider/internal/approve"
)

// Ask shows the owner an approval dialog and tells whether they clicked ok.
type Ask func(title, body, ok string) bool

// RunGH is the gh guard: decide, maybe ask, then run the real gh.
func RunGH(args []string, getenv func(string) string, ask Ask, stderr io.Writer) int {
	s := GHSessionFromEnv(getenv)
	d := DecideGH(args, s)
	if d.Deny == "" && d.Write && s.Mode == "ask" &&
		!ask("outrider: post to GitHub?", PostDialog(getenv(EnvSession), d.Args, d.Text), "Post") {
		d.Deny = s.denyMsg(args, "the owner did not approve this post. Do not retry or work around it; "+
			"keep the text in this session")
	}
	if d.Deny != "" {
		_, _ = fmt.Fprintln(stderr, d.Deny)
		return 1
	}
	real := getenv(EnvRealGH)
	if real == "" {
		_, _ = fmt.Fprintln(stderr, "outrider guard: "+EnvRealGH+" is not set")
		return 1
	}
	return execReal(real, d.Args, os.Environ(), stderr)
}

// RunGit is the git guard: decide, maybe ask, then run the real git.
func RunGit(args []string, getenv func(string) string, ask Ask, stderr io.Writer) int {
	real := getenv(EnvRealGit)
	if real == "" {
		_, _ = fmt.Fprintln(stderr, "outrider guard: "+EnvRealGit+" is not set")
		return 1
	}
	alias := func(global []string, name string) string {
		out, _ := exec.Command(real, append(slices.Clone(global), "config", "--get", "alias."+name)...).Output()
		return string(out)
	}
	rf, err := ReviewForksFromEnv(getenv)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "outrider guard: "+err.Error())
		return 1
	}
	env := os.Environ()
	var d GitDecision
	if rf != nil && Pushes(args, alias) {
		pushEnv := approvedPushEnv(env)
		decide := func() GitDecision { return rf.Decide(args, getenv(EnvPush), env, resolvePush(real, pushEnv)) }
		if d = decide(); d.Ask {
			if !askPush(real, getenv, ask, args, d) {
				d.Deny = gitDeny(args, deniedNotApproved)
			} else if again := decide(); again.Deny != "" || again.Dest != d.Dest {
				// the remote's config changed while the dialog was open
				d.Deny = gitDeny(args, "The push destination changed after approval; refused.")
			}
		}
		env = pushEnv
	} else {
		d = DecideGit(args, getenv(EnvPush), alias)
		if d.Ask {
			if !askPush(real, getenv, ask, args, d) {
				d.Deny = gitDeny(args, deniedNotApproved)
			}
			env = approvedPushEnv(env)
		}
	}
	if d.Deny != "" {
		_, _ = fmt.Fprintln(stderr, d.Deny)
		return 1
	}
	return execReal(real, args, env, stderr)
}

func askPush(real string, getenv func(string) string, ask Ask, args []string, d GitDecision) bool {
	out, _ := exec.Command(real, "rev-parse", "--abbrev-ref", "HEAD").Output()
	dir, _ := os.Getwd()
	return ask("outrider: approve push?", PushDialog(getenv(EnvSession), args, strings.TrimSpace(string(out)), dir, d), "Push")
}

// resolvePush resolves like `git push <repo>` does: a remote configured in
// this repository gives its push URLs (pushurl, pushInsteadOf and insteadOf
// applied); anything else is a URL, used as is. A URL that a
// url.*.insteadOf rule rewrites, or that names a remote in some other config
// file, is ambiguous and refused.
func resolvePush(real string, env []string) Resolve {
	git := func(args ...string) (string, error) {
		cmd := exec.Command(real, args...)
		cmd.Env = env
		out, err := cmd.Output()
		return string(out), err
	}
	return func(repo string) ([]string, error) {
		out, err := git("remote", "get-url", "--push", "--all", "--", repo)
		var ee *exec.ExitError
		if err == nil {
			return strings.Fields(out), nil
		}
		if !errors.As(err, &ee) || ee.ExitCode() != 2 { // 2: no such remote
			return nil, fmt.Errorf("git remote get-url %s: %w", repo, err)
		}
		out, err = git("config", "--null", "--get-regexp", `^(url\..*\.(push)?insteadof|remote\..*)$`)
		if err != nil && (!errors.As(err, &ee) || ee.ExitCode() != 1) { // 1: no such keys
			return nil, fmt.Errorf("git config: %w", err)
		}
		for entry := range strings.SplitSeq(out, "\x00") {
			key, value, _ := strings.Cut(entry, "\n")
			if strings.HasPrefix(key, "remote."+repo+".") ||
				strings.HasPrefix(key, "url.") && value != "" && strings.HasPrefix(repo, value) {
				return nil, fmt.Errorf("%s is changed by the git config %s; add it as a remote instead", repo, key)
			}
		}
		return []string{repo}, nil
	}
}

// Command is the guard a binary called argv0 is (`gh` or `git`, also with
// `.exe`), or nil for any other name.
func Command(argv0 string) func(args []string) int {
	ask := func(title, body, ok string) bool {
		approved, _ := approve.Ask(context.Background(), title, body, ok)
		return approved
	}
	switch strings.TrimSuffix(strings.ToLower(filepath.Base(argv0)), ".exe") {
	case "gh":
		return func(args []string) int { return RunGH(args, os.Getenv, ask, os.Stderr) }
	case "git":
		return func(args []string) int { return RunGit(args, os.Getenv, ask, os.Stderr) }
	}
	return nil
}
