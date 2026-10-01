package guard

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
)

// Ask shows the owner an approval dialog and tells whether they clicked ok.
type Ask func(title, body, ok string) bool

// RunGH is the gh guard: decide, maybe ask, then run the real gh.
func RunGH(args []string, getenv func(string) string, ask Ask, stderr io.Writer) int {
	s := GHSessionFromEnv(getenv)
	d := DecideGH(args, s)
	if d.Deny == "" && d.Write && s.Mode == "ask" &&
		!ask("llm-review-agent: post to GitHub?", PostDialog(getenv(EnvSession), d.Args, d.Text), "Post") {
		d.Deny = s.denyMsg(args, "the owner did not approve this post. Do not retry or work around it; "+
			"keep the text in this session")
	}
	if d.Deny != "" {
		_, _ = fmt.Fprintln(stderr, d.Deny)
		return 1
	}
	real := getenv(EnvRealGH)
	if real == "" {
		_, _ = fmt.Fprintln(stderr, "llm-review-agent guard: "+EnvRealGH+" is not set")
		return 1
	}
	return execReal(real, d.Args, os.Environ(), stderr)
}

// RunGit is the git guard: decide, maybe ask, then run the real git.
func RunGit(args []string, getenv func(string) string, ask Ask, stderr io.Writer) int {
	real := getenv(EnvRealGit)
	if real == "" {
		_, _ = fmt.Fprintln(stderr, "llm-review-agent guard: "+EnvRealGit+" is not set")
		return 1
	}
	alias := func(global []string, name string) string {
		out, _ := exec.Command(real, append(slices.Clone(global), "config", "--get", "alias."+name)...).Output()
		return string(out)
	}
	d := DecideGit(args, getenv(EnvPush), alias)
	env := os.Environ()
	if d.Ask {
		out, _ := exec.Command(real, "rev-parse", "--abbrev-ref", "HEAD").Output()
		dir, _ := os.Getwd()
		if !ask("llm-review-agent: approve push?", PushDialog(getenv(EnvSession), args, strings.TrimSpace(string(out)), dir), "Push") {
			d.Deny = gitDeny(args, deniedNotApproved)
		}
		env = approvedPushEnv(env)
	}
	if d.Deny != "" {
		_, _ = fmt.Fprintln(stderr, d.Deny)
		return 1
	}
	return execReal(real, args, env, stderr)
}
