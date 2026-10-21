package session

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/appetizers-io/outrider/internal/config"
	"github.com/appetizers-io/outrider/internal/github"
)

func TestCrewSharedTaskRules(t *testing.T) {
	for _, tc := range []struct {
		name, author, push string
		allow              bool
		forks              []string
		want               string
	}{
		{"own", "me", "ask", false, nil, "OWN PR"},
		{"own-local", "me", "never", false, nil, "commit locally"},
		{"other", "bob", "ask", false, nil, "do NOT edit files, commit or push"},
		{"other-authorized", "bob", "ask", true, nil, "push fast-forward only"},
		{"fork", "bob", "ask", false, []string{"me/fork"}, "REVIEW WITH EVIDENCE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := PromptInput{Crew: true, LocalBranch: "review/task", Repo: "o/r", N: 1, PR: github.PR{Author: &github.User{Login: tc.author}, Title: "Fix API", BaseRefName: "main"}, Login: "me", Owner: "me", Remote: "upstream", Push: tc.push, GHWrites: "ask", AllowPush: tc.allow, ReviewForks: tc.forks, Trigger: "mention", Scope: []string{"https://github.com/o/r/pull/1#issuecomment-2"}, ScopeWhy: "the mentioning comment"}
			prompt, err := Prompt(in)
			require.NoError(t, err)
			require.Contains(t, prompt, tc.want)
			require.Contains(t, prompt, "Title: Fix API")
			require.Contains(t, prompt, "local branch review/task")
			require.Contains(t, prompt, "SCOPE: this session is ONLY")
			require.Contains(t, prompt, "issuecomment-2")
			require.Contains(t, prompt, "Crew controls tool approvals")
			for _, claim := range []string{"opens a dialog", "git guard refuses", "push` is blocked", "policy.json"} {
				require.NotContains(t, prompt, claim)
			}
			if tc.author == "me" {
				require.Contains(t, prompt, "git fetch upstream && git rebase upstream/main")
			}
		})
	}
}
func TestCrewWorkflowPostHonorsTaskPolicy(t *testing.T) {
	for _, writes := range []string{"never", "ask", "allow"} {
		p, err := Prompt(PromptInput{Crew: true, Repo: "o/r", N: 1, PR: github.PR{Author: &github.User{Login: "bob"}}, Login: "me", Owner: "me", GHWrites: writes, Workflow: &config.Workflow{Name: "review", Post: true, Steps: []string{"Check CI", "Approve if safe"}}})
		require.NoError(t, err)
		require.Contains(t, p, "1. Check CI")
		if writes == "never" {
			require.Contains(t, p, "GitHub writes are disabled")
			require.NotContains(t, p, "workflow authorizes")
		} else {
			require.Contains(t, p, "workflow authorizes")
		}
	}
}
