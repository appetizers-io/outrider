package session

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/appetizers-io/outrider/internal/config"
	"github.com/appetizers-io/outrider/internal/github"
)

func TestWorkflowPromptOrderAndPermissions(t *testing.T) {
	for _, post := range []bool{false, true} {
		for _, mode := range []string{"never", "ask", "allow"} {
			w := &config.Workflow{Name: "dependency", Steps: []string{"Inspect changelog", "Check compatibility", "Check CI then approve if safe"}, Post: post}
			prompt, err := Prompt(PromptInput{Repo: "o/r", N: 1, PR: github.PR{Author: &github.User{Login: "bot"}}, Login: "me", Owner: "Me", Push: "review-only", GHWrites: mode, Workflow: w})
			require.NoError(t, err)
			require.Contains(t, prompt, "WORKFLOW: dependency")
			require.Less(t, strings.Index(prompt, "1. Inspect changelog"), strings.Index(prompt, "2. Check compatibility"))
			require.Less(t, strings.Index(prompt, "2. Check compatibility"), strings.Index(prompt, "3. Check CI then approve if safe"))
			require.Contains(t, prompt, "do NOT edit files, commit or push")
			switch {
			case mode == "never":
				require.Contains(t, prompt, "do NOT submit/approve/reject reviews")
			case post:
				require.Contains(t, prompt, "workflow authorizes the posts explicitly requested")
			default:
				require.Contains(t, prompt, "post to GitHub only when Me asks")
			}
			if mode == "ask" {
				require.Contains(t, prompt, "runs only if they click Post")
			}
		}
	}
}

func TestWorkflowSessionCarriesInstructionsAndForkPolicy(t *testing.T) {
	l, _ := newLauncher(t, `others_prs:
  allow_push: false
  review_forks: [me/evidence]
workflows:
  - name: tech
    match: [{prs: others}]
    steps: [Read discussions, Write focused repro tests, Prepare findings]
`)
	l.ToolGate = jevGate
	got := l.launched(t, "bob")
	require.Contains(t, got.prompt, "WORKFLOW: tech")
	require.Contains(t, got.prompt, "REVIEW WITH EVIDENCE")
	require.Contains(t, got.prompt, "me/evidence")
	require.Equal(t, "review-only", got.spec.Env["OUTRIDER_PUSH"])
	require.Equal(t, "me/evidence", got.spec.Env["OUTRIDER_REVIEW_FORKS"])
	require.Contains(t, got.settingsEnv()["JEV_GATE_STATE"], "Follow workflow tech")
	w := got.policy["workflow"].(map[string]any)
	require.Equal(t, "tech", w["Name"])
}

func TestWorkflowLaunchBypassesNoiseClassifier(t *testing.T) {
	l, _ := newLauncher(t, `workflows:
  - name: review
    match: [{}]
    steps: [Review the latest PR]
`)
	l.LaunchCheck = jevGate
	gateCalled := false
	require.True(t, l.Launch(t.Context(), Request{Repo: "o/r", N: 1, PR: pr("bob"), Gate: func(context.Context) ([]github.Activity, error) {
		gateCalled = true
		return nil, nil
	}}))
	require.False(t, gateCalled)
}
