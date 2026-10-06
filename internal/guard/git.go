package guard

import (
	"slices"
	"strings"

	"github.com/appetizers-io/outrider/internal/shell"
)

// git: $OUTRIDER_PUSH decides what `git push` (also through aliases) does:
//
//	review-only / never: refused
//	ask:   a native dialog asks the owner; only a click on "Push" lets it through
//	allow: passes
//
// In a review-forks session (forks.go) a push goes only to the owner's review
// forks, never to the PR's repos, and then follows the same modes.
//
// The runner also sets pushInsteadOf through GIT_CONFIG_* so a push that goes
// around this guard (the real git binary) hits a dead URL; an approved push
// drops those entries before running the real git.

var gitWithValue = []string{"-C", "-c", "--git-dir", "--work-tree", "--namespace", "--exec-path", "--config-env", "--attr-source"}

// pushCommands send commits to a remote; send-pack and http-push also go
// around the pushInsteadOf trap.
var pushCommands = []string{"push", "send-pack", "http-push"}

func isPush(sub string) bool { return slices.Contains(pushCommands, sub) }

// Subcommand is the git subcommand after the global options and its index,
// or ("", len(args)).
func Subcommand(args []string) (string, int) {
	for i := 0; i < len(args); i++ {
		switch x := args[i]; {
		case slices.Contains(gitWithValue, x):
			i++
		case strings.HasPrefix(x, "-"):
		default:
			return x, i
		}
	}
	return "", len(args)
}

// AliasPushes tells whether an alias definition runs push.
func AliasPushes(alias string) bool {
	alias = strings.TrimSpace(alias)
	return alias != "" && slices.ContainsFunc(strings.Fields(strings.TrimLeft(alias, "!")), isPush)
}

// Pushes tells whether a git invocation pushes, directly or through an alias.
func Pushes(args []string, alias func(global []string, name string) string) bool {
	sub, i := Subcommand(args)
	return isPush(sub) || sub != "" && AliasPushes(alias(args[:i], sub))
}

// GitDecision is what to do with a git invocation.
type GitDecision struct {
	Deny  string   // non-empty: refuse with this message
	Ask   bool     // a push that needs the owner's approval
	Dest  string   // review forks: where the push goes, github.com/owner/repo
	Refs  []string // review forks: the refspecs pushed there, src:refs/heads/review/...
	URL   string   // review forks: the resolved push URL
	Flags []string // review forks: the allowed push options given
}

const deniedNotApproved = "The owner did not approve this push. Do not retry or work around " +
	"it; keep the commits local and explain what is ready to push."

func gitDeny(args []string, why string) string {
	return "outrider guard: blocked `git " + strings.Join(args, " ") + "`. " + why
}

// DecideGit decides a git invocation. alias returns the definition of
// `alias.<name>` ("" when there is none), seen with the invocation's global
// options (so `git -c alias.p=push p` is caught too).
func DecideGit(args []string, mode string, alias func(global []string, name string) string) GitDecision {
	if mode == "" {
		mode = "review-only"
	}
	if mode == "allow" {
		return GitDecision{}
	}
	if !Pushes(args, alias) {
		return GitDecision{}
	}
	switch mode {
	case "review-only":
		return GitDecision{Deny: gitDeny(args, "This is someone else's PR: review only, never push. "+
			"Suggest the change in this session instead.")}
	case "ask":
		return GitDecision{Ask: true}
	}
	return GitDecision{Deny: gitDeny(args, "Pushing is off for these sessions. Keep the commits local "+
		"and tell the owner what is ready to push.")}
}

// PushDialog is the approval dialog's text for a push; d.Dest and d.Refs
// show where a review-fork push goes.
func PushDialog(session string, args []string, branch, dir string, d GitDecision) string {
	session = strings.TrimLeft(session, "-") // from the agent's environment; must not read as an option
	if session == "" {
		session = "agent session"
	}
	if branch == "" {
		branch = "?"
	}
	text := session + " wants to run:\n\ngit " + shell.Join(args...) + "\n\n"
	if d.Dest != "" {
		text += "to: " + d.Dest + "\nrefs: " + strings.Join(d.Refs, " ") + "\n"
	}
	return text + "branch: " + branch + "\nin: " + dir
}

// approvedPushEnv drops the push trap (GIT_CONFIG_*) and marks this one push
// as allowed, so it is not asked twice.
func approvedPushEnv(env []string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if k == "GIT_CONFIG_COUNT" || strings.HasPrefix(k, "GIT_CONFIG_KEY_") ||
			strings.HasPrefix(k, "GIT_CONFIG_VALUE_") || k == EnvPush {
			continue
		}
		out = append(out, kv)
	}
	return out
}
