package guard

import (
	"slices"
	"strings"
)

// git: $LLM_REVIEW_AGENT_PUSH decides what `git push` (also through aliases) does:
//
//	review-only / never: refused
//	ask:   a native dialog asks the owner; only a click on "Push" lets it through
//	allow: passes
//
// The runner also sets pushInsteadOf through GIT_CONFIG_* so a push that goes
// around this guard (the real git binary) hits a dead URL; an approved push
// drops those entries before running the real git.

var gitWithValue = []string{"-C", "-c", "--git-dir", "--work-tree", "--namespace", "--exec-path", "--config-env"}

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
	return alias != "" && slices.Contains(strings.Fields(strings.TrimLeft(alias, "!")), "push")
}

// GitDecision is what to do with a git invocation.
type GitDecision struct {
	Deny string // non-empty: refuse with this message
	Ask  bool   // a push that needs the owner's approval
}

const deniedNotApproved = "The owner did not approve this push. Do not retry or work around " +
	"it; keep the commits local and explain what is ready to push."

func gitDeny(args []string, why string) string {
	return "llm-review-agent guard: blocked `git " + strings.Join(args, " ") + "`. " + why
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
	sub, i := Subcommand(args)
	if sub != "push" && (sub == "" || !AliasPushes(alias(args[:i], sub))) {
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

// PushDialog is the approval dialog's text for a push.
func PushDialog(session string, args []string, branch, dir string) string {
	if session == "" {
		session = "agent session"
	}
	if branch == "" {
		branch = "?"
	}
	return session + " wants to run:\n\ngit " + strings.Join(args, " ") + "\n\nbranch: " + branch + "\nin: " + dir
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
	return append(out, EnvPush+"=allow")
}
