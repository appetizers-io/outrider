package session

import (
	"fmt"
	"strings"

	"github.com/appetizers-io/llm-review-agent/internal/classifier"
	"github.com/appetizers-io/llm-review-agent/internal/config"
	"github.com/appetizers-io/llm-review-agent/internal/shell"
)

// dialogDenyRules keep the agent from clicking its own approval dialog.
func dialogDenyRules(goos string) []string {
	switch goos {
	case "darwin":
		return []string{"Bash(osascript:*)"}
	case "windows":
		return []string{"Bash(powershell:*)", "Bash(powershell.exe:*)"}
	}
	return []string{"Bash(zenity:*)", "Bash(kdialog:*)"}
}

// DenyRules are the deterministic Claude Code deny rules for a supervised
// session; push is review-only, never, ask or allow.
func DenyRules(push, goos string) []string {
	if push == "review-only" {
		return append([]string{
			"Edit",
			"Write",
			"NotebookEdit",
			"Bash(git commit:*)",
			"Bash(git push:*)",
			"Bash(git rebase:*)",
			"Bash(git reset --hard:*)",
			"Bash(git cherry-pick:*)",
			"Bash(git merge:*)",
			"Bash(git am:*)",
		}, dialogDenyRules(goos)...)
	}
	rules := append(dialogDenyRules(goos), // the approval dialog is for the owner to click
		"Bash(git push --force:*)",
		"Bash(git push -f:*)",
		"Bash(git push --delete:*)",
		"Bash(git push origin --delete:*)",
		"Bash(git branch -D:*)",
	)
	if push == "never" {
		rules = append(rules, "Bash(git push:*)")
	}
	return rules
}

var pushGateRules = map[string]string{
	"ask": " Every git push must go through the plain `git push` command, which " +
		"asks the owner in a dialog. Deny any push by another route: a git binary by " +
		"absolute path, unsetting or changing GIT_CONFIG_* or LLM_REVIEW_AGENT_* " +
		"variables, git remote/config changes to push URLs, curl or API calls.",
	"never": " Deny every git push, by any route.",
}

var ghWriteGateRules = map[string]string{
	"ask": " Posting a comment, review comment or reply, review or reaction on " +
		"this PR with plain `gh` is fine; the gh guard asks the owner first. Deny " +
		"GitHub writes by any other route (curl, tokens, graphql mutations, changed " +
		"LLM_REVIEW_AGENT_* variables) and every other GitHub write (merge, close, " +
		"labels, edits of the PR).",
	"allow": " Posting a comment, review comment or reply, review or reaction on " +
		"this PR with plain `gh` is fine. Deny every other GitHub write (merge, " +
		"close, labels, edits of the PR) and writes by any other route.",
}

// Rules is what a session may do, for the tool gate.
func Rules(repo string, n int, author, owner string, own bool, push, ghWrites string) string {
	var rules string
	switch {
	case push == "review-only":
		rules = "REVIEW ONLY: deny every file edit, git commit, git push, rebase, " +
			"reset, merge or any other change to the branch or to GitHub " +
			"(except the posts allowed below). " +
			"Reading files, git log/diff/fetch, building and running tests is fine."
	case own:
		rules = "This is " + owner + "'s own PR: edits, commits, rebasing onto the base " +
			"branch and `git push --force-with-lease` to this PR branch are fine. " +
			"Deny pushes to any other branch, deleting branches, plain force " +
			"pushes, and any GitHub write not allowed below (comments, reviews, " +
			"merges)."
	default:
		rules = "Someone else's PR with pushing allowed: fast-forward commits on top " +
			"of the PR head are fine. Deny force pushes, rebases, history " +
			"rewrites, pushes to other branches and any GitHub write not " +
			"allowed below."
	}
	rules += pushGateRules[push] + ghWriteGateRules[ghWrites]
	return fmt.Sprintf("Automated coding-agent session for GitHub PR %s#%d by %s, "+
		"launched by llm-review-agent for %s. Nobody is watching live. ", repo, n, author, owner) + rules
}

// GateText is the session rules plus the user's own tool_gate rules.
func GateText(cfg *config.Config, repo string, n int, author, owner string, own bool, push, ghWrites string) string {
	extra := append([]string{}, cfg.ToolGate.Rules...)
	if p := strings.TrimSpace(cfg.Prompts.Extra); cfg.ToolGate.IncludePromptExtra && p != "" {
		extra = append(extra, p)
	}
	text := Rules(repo, n, author, owner, own, push, ghWrites)
	if len(extra) > 0 {
		numbered := make([]string, len(extra))
		for i, r := range extra {
			numbered[i] = fmt.Sprintf("(%d) %s", i+1, r)
		}
		text += " Also enforce these rules from " + owner + ": " + strings.Join(numbered, " ")
	}
	return text
}

// ClaudeSettings is --settings for a supervised Claude session: deny rules
// plus, when a gate is available, the tool gate hook.
func ClaudeSettings(cfg *config.Config, push, goos string, gate *classifier.Resolved, env map[string]string) map[string]any {
	settings := map[string]any{"permissions": map[string]any{"deny": DenyRules(push, goos)}}
	if gate != nil && gate.HookCmd != nil {
		settings["env"] = env
		settings["hooks"] = map[string]any{
			"PreToolUse": []any{map[string]any{
				"matcher": cfg.ToolGate.Matcher,
				"hooks": []any{map[string]any{
					"type":          "command",
					"command":       shell.Join(gate.HookCmd...),
					"timeout":       30,
					"statusMessage": "Tool gate (" + gate.Name + "): checking this action",
				}},
			}},
		}
	}
	return settings
}

// pushPrefixes are rewritten by the push trap.
var pushPrefixes = []string{"git@", "ssh://", "https://", "http://", "git://"}

// PushTrap is the environment that sends a push around the git guard (the
// real git binary) to a URL no transport handles; the guard drops it for an
// approved push.
func PushTrap() map[string]string {
	env := map[string]string{"GIT_CONFIG_COUNT": fmt.Sprint(len(pushPrefixes))}
	for i, p := range pushPrefixes {
		env[fmt.Sprintf("GIT_CONFIG_KEY_%d", i)] = "url.llm-review-agent-push-blocked://.pushInsteadOf"
		env[fmt.Sprintf("GIT_CONFIG_VALUE_%d", i)] = p
	}
	return env
}
