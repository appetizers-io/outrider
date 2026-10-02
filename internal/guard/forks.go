package guard

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	giturls "github.com/chainguard-dev/git-urls"

	"github.com/appetizers-io/outrider/internal/config"
)

// Review forks: a review session on someone else's PR may push evidence
// (failing tests, repro scripts) to the owner's own forks. The guard resolves
// where a push really goes, the way git does, and lets it through only to a
// repo matching $OUTRIDER_REVIEW_FORKS that is neither the PR's head repo nor
// its base repo, and only to branches under review/. $OUTRIDER_REVIEW_FORKS_PUSH
// (ask, never, allow) then decides as usual. $OUTRIDER_PUSH stays review-only,
// so a session without $OUTRIDER_REVIEW_FORKS pushes nowhere.
//
// An allowed push runs against the resolved URL, not the remote name, with
// the transport and hooks pinned: what git would read again at push time
// (ssh command, remote helper, pre-push hook) can't send it elsewhere.
const (
	EnvReviewForks     = "OUTRIDER_REVIEW_FORKS"      // owner/repo globs, one per line
	EnvReviewForksPush = "OUTRIDER_REVIEW_FORKS_PUSH" // ask | never | allow
	EnvHeadRepo        = "OUTRIDER_HEAD_REPO"         // the PR head's owner/repo
)

// reviewRefs is where review-fork pushes may go.
const reviewRefs = "refs/heads/review/"

// ReviewForks is what the git guard knows about a review-forks session.
type ReviewForks struct {
	Forks      config.Globs
	Head, Base string // owner/repo, lower case; never pushed to
}

// ReviewForksFromEnv reads the session from the environment; nil when it
// has no review forks.
func ReviewForksFromEnv(getenv func(string) string) (*ReviewForks, error) {
	raw := getenv(EnvReviewForks)
	if raw == "" {
		return nil, nil
	}
	var patterns []string
	for p := range strings.SplitSeq(strings.ToLower(raw), "\n") {
		if p = strings.TrimSpace(p); p != "" {
			patterns = append(patterns, p)
		}
	}
	forks, err := config.RepoGlobs(patterns)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", EnvReviewForks, err)
	}
	return &ReviewForks{Forks: forks, Head: strings.ToLower(getenv(EnvHeadRepo)), Base: strings.ToLower(getenv(EnvRepo))}, nil
}

// forkPushFlags are the git push options a review-fork push may use; any
// other option (--mirror, --all, --delete, --force, --prune, --repo, ...,
// abbreviations, combined short flags) is refused.
var forkPushFlags = []string{"-u", "--set-upstream", "-n", "--dry-run", "-q", "--quiet", "-v", "--verbose",
	"--progress", "--no-progress", "--force-with-lease", "--force-if-includes", "--no-verify", "--atomic", "--porcelain"}

// redirectEnv can point git at other config, another repository, another
// transport or other programs between the guard's check and the push.
var redirectEnv = []string{"GIT_CONFIG_PARAMETERS", "GIT_CONFIG", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM",
	"GIT_DIR", "GIT_COMMON_DIR", "GIT_WORK_TREE", "GIT_NAMESPACE",
	"GIT_SSH", "GIT_SSH_COMMAND", "GIT_SSH_VARIANT", "GIT_EXEC_PATH", "GIT_PROXY_COMMAND"}

// trapKey is the only GIT_CONFIG_KEY_<n> the session itself sets (PushTrap).
const trapKey = "url.outrider-push-blocked://.pushInsteadOf"

var repoName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*)/[a-z0-9._-]+$`)

// GitHubRepo is the lower-case owner/repo of a github.com push URL (https,
// ssh or scp-like git@github.com:owner/repo), or an error.
func GitHubRepo(raw string) (string, error) {
	u, err := giturls.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("cannot parse %q: %w", raw, err)
	}
	if (u.Scheme != "https" && u.Scheme != "ssh") || !strings.EqualFold(u.Hostname(), "github.com") {
		return "", fmt.Errorf("%q is not a github.com https or ssh URL", raw)
	}
	repo := strings.TrimSuffix(strings.ToLower(strings.Trim(u.Path, "/")), ".git")
	if !repoName.MatchString(repo) || strings.HasSuffix(repo, "/.") || strings.HasSuffix(repo, "/..") {
		return "", fmt.Errorf("%q is not a github.com/owner/repo URL", raw)
	}
	return repo, nil
}

// Resolve gives the URLs `git push <repo>` pushes to: a remote's push URLs,
// or the URL itself.
type Resolve func(repo string) ([]string, error)

// reviewRef is the refspec src:refs/heads/review/<name> for a refspec whose
// destination is review/<name> or refs/heads/review/<name>, or "".
func reviewRef(ref string) string {
	src, dst, ok := strings.Cut(ref, ":")
	if !ok {
		dst = src
	}
	if !strings.HasPrefix(dst, "refs/") {
		dst = "refs/heads/" + dst
	}
	name := strings.TrimPrefix(dst, reviewRefs)
	if src == "" || name == dst || name == "" || strings.ContainsAny(name, "*:^") || strings.Contains(name, "..") {
		return ""
	}
	return src + ":" + dst
}

// Decide decides a push in a review-forks session; mode is
// $OUTRIDER_REVIEW_FORKS_PUSH. env is the session's environment.
func (rf *ReviewForks) Decide(args []string, mode string, env []string, resolve Resolve) GitDecision {
	deny := func(why string) GitDecision {
		return GitDecision{Deny: gitDeny(args, "Review session: you may push only to "+
			"the owner's review forks, with plain `git push <remote> <refspec>...`. "+why)}
	}
	if len(args) == 0 || args[0] != "push" {
		return deny("Use plain `git push` without global options (-c, -C, --git-dir, ...) or aliases.")
	}
	if mode == "" {
		return deny("$" + EnvReviewForksPush + " is not set.")
	}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if slices.Contains(redirectEnv, k) || strings.HasPrefix(k, "GIT_CONFIG_KEY_") && v != trapKey {
			return deny("Unset " + k + "; it can redirect the push.")
		}
	}
	var pos, flags []string
	opts := true
	for _, a := range args[1:] {
		switch {
		case opts && a == "--":
			opts = false
		case opts && strings.HasPrefix(a, "-"):
			name, _, _ := strings.Cut(a, "=")
			if !slices.Contains(forkPushFlags, name) || name != a && name != "--force-with-lease" {
				return deny("`" + a + "` is not allowed (no --mirror, --all, --delete, --force, --prune, --repo).")
			}
			flags = append(flags, a)
		default:
			pos = append(pos, a)
		}
	}
	if len(pos) < 2 {
		return deny("Name the remote or URL and the refspec, e.g. `git push fork HEAD:review/pr-5`.")
	}
	var refs []string
	for _, ref := range pos[1:] {
		if ref == "" || strings.HasPrefix(ref, ":") || strings.HasPrefix(ref, "+") || strings.HasPrefix(ref, "-") {
			return deny("Refspec `" + ref + "` deletes or force-pushes.")
		}
		r := reviewRef(ref)
		if r == "" {
			return deny("Refspec `" + ref + "` must push to a branch under review/, e.g. HEAD:review/pr-5-<topic>.")
		}
		refs = append(refs, r)
	}
	if strings.HasPrefix(pos[0], "-") {
		return deny("Remote `" + pos[0] + "` is not a remote or URL.")
	}
	urls, err := resolve(pos[0])
	if err != nil {
		return deny("Cannot resolve where it goes: " + err.Error())
	}
	if len(urls) != 1 {
		return deny(fmt.Sprintf("%s pushes to %d URLs (%s); it must be exactly one.", pos[0], len(urls), strings.Join(urls, ", ")))
	}
	dest, err := GitHubRepo(urls[0])
	if err != nil {
		return deny(err.Error())
	}
	switch {
	case rf.Head == "" || rf.Base == "":
		return deny("The PR's head or base repo is unknown, so no push can be checked.")
	case dest == rf.Head || dest == rf.Base:
		return deny("github.com/" + dest + " is this PR's head or base repo; never push there.")
	case !rf.Forks.Match(dest):
		return deny("github.com/" + dest + " is not one of the review forks.")
	}
	d := GitDecision{Dest: "github.com/" + dest, Refs: refs, URL: urls[0], Flags: flags}
	switch mode {
	case "allow":
	case "ask":
		d.Ask = true
	default:
		d.Deny = gitDeny(args, "Pushing is off for these sessions. Keep the commits local "+
			"and tell the owner what is ready to push.")
	}
	return d
}

// PinnedPush is the real git invocation of an allowed review-fork push: to
// the resolved URL, with the ssh command and hooks pinned, so nothing read at
// push time (core.sshCommand, core.hooksPath, a pre-push hook, a remote's
// vcs helper) can send it anywhere else.
func PinnedPush(d GitDecision) []string {
	args := []string{"-c", "core.hooksPath=" + os.DevNull, "-c", "core.sshCommand=ssh", "push", "--no-verify"}
	args = append(append(args, d.Flags...), "--", d.URL)
	return append(args, d.Refs...)
}
