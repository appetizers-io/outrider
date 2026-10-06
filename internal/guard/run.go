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
func RunGH(args []string, ask Ask, stderr io.Writer) int {
	getenv, policy, err := trustedEnv()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "outrider guard:", err)
		return 1
	}
	ask = trustedAsk(ask, policy.Guard.Display)
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
func RunGit(args []string, ask Ask, stderr io.Writer) int {
	getenv, policy, err := trustedEnv()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "outrider guard:", err)
		return 1
	}
	ask = trustedAsk(ask, policy.Guard.Display)
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
		decide := func() GitDecision {
			return rf.Decide(args, getenv(EnvReviewForksPush), env, resolvePush(real, pushEnv))
		}
		if d = decide(); d.Ask {
			if !askPush(real, getenv, ask, args, d) {
				d.Deny = gitDeny(args, deniedNotApproved)
			} else if again := decide(); again.Deny != "" || again.URL != d.URL {
				// the remote's config changed while the dialog was open
				d.Deny = gitDeny(args, "The push destination changed after approval; refused.")
			}
		}
		env, args = pushEnv, PinnedPush(d)
	} else {
		d = DecideGit(args, getenv(EnvPush), alias)
		if d.Ask {
			if _, i := Subcommand(args); i != 0 {
				d = GitDecision{Deny: gitDeny(args, "Approval pushes cannot use global options or aliases.")}
			}
			for _, k := range redirectEnv {
				if os.Getenv(k) != "" {
					d = GitDecision{Deny: gitDeny(args, "Unset "+k+" before requesting approval.")}
				}
			}
		}
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
	if rf != nil && d.URL != "" {
		return isolatedPush(d, policy.Guard, env, stderr)
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
// applied); anything else is a URL, used as is. The guard then pushes to the
// URL itself, so a URL that a url.*.insteadOf rule would rewrite again, or
// that names a remote in some config file, is ambiguous and refused, and so
// is a remote with a vcs helper.
func resolvePush(real string, env []string) Resolve {
	git := func(args ...string) (string, error) {
		cmd := exec.Command(real, args...)
		cmd.Env = env
		out, err := cmd.Output()
		return string(out), err
	}
	return func(repo string) ([]string, error) {
		var ee *exec.ExitError
		cfg, err := git("config", "--null", "--get-regexp", `^(url\..*\.(push)?insteadof|remote\..*)$`)
		if err != nil && (!errors.As(err, &ee) || ee.ExitCode() != 1) { // 1: no such keys
			return nil, fmt.Errorf("git config: %w", err)
		}
		var entries [][2]string
		for entry := range strings.SplitSeq(cfg, "\x00") {
			key, value, _ := strings.Cut(entry, "\n")
			entries = append(entries, [2]string{key, value})
		}
		// changedBy names the config that would change a push to url
		changedBy := func(url string) string {
			for _, e := range entries {
				if strings.HasPrefix(e[0], "remote."+url+".") ||
					strings.HasPrefix(e[0], "url.") && e[1] != "" && strings.HasPrefix(url, e[1]) {
					return e[0]
				}
			}
			return ""
		}
		out, err := git("remote", "get-url", "--push", "--all", "--", repo)
		urls := strings.Fields(out)
		switch {
		case err == nil:
			for _, e := range entries {
				if strings.EqualFold(e[0], "remote."+repo+".vcs") && e[1] != "" {
					return nil, fmt.Errorf("remote %s uses the remote helper %q", repo, e[1])
				}
			}
		case errors.As(err, &ee) && ee.ExitCode() == 2: // no such remote: a URL
			urls = []string{repo}
		default:
			return nil, fmt.Errorf("git remote get-url %s: %w", repo, err)
		}
		for _, u := range urls {
			if key := changedBy(u); key != "" {
				return nil, fmt.Errorf("%s is changed by the git config %s; push to a remote whose URL no rule rewrites", u, key)
			}
		}
		return urls, nil
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
	case "outrider-gate":
		return func(_ []string) int { return RunGate(os.Stdin, os.Stdout, os.Stderr) }
	case "gh":
		return func(args []string) int { return RunGH(args, ask, os.Stderr) }
	case "git":
		return func(args []string) int { return RunGit(args, ask, os.Stderr) }
	}
	return nil
}

// Approval tools see the watcher's display, never a replacement supplied by
// the agent. Guards are short-lived standalone processes.
func trustedAsk(ask Ask, display map[string]string) Ask {
	return func(title, body, ok string) bool {
		for _, k := range []string{"DISPLAY", "WAYLAND_DISPLAY", "XAUTHORITY", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS"} {
			_ = os.Setenv(k, display[k])
		}
		return ask(title, body, ok)
	}
}
