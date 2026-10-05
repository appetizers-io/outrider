package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkflowMatchingAndOverrides(t *testing.T) {
	c, err := Parse([]byte(`workflows:
  - name: dependencies
    match:
      - repo: org/*
        prs: others
        authors: ['dependabot[bot]', renovate*]
        head: 'deps/*'
        title: '*update*'
        labels: [dependencies]
        events: [notification, opt_in]
    steps: [Read changelog, Check compatibility]
  - name: fallback
    match: [{}]
    steps: [Review]
overrides:
  - match: [{repo: private/*}]
    workflows: []
`), "test")
	require.NoError(t, err)
	pr := WorkflowPR{Repo: "org/repo", Author: "Dependabot[bot]", Head: "deps/lib", Title: "Please update lib", Labels: []string{"dependencies"}, Event: "notification"}
	require.Equal(t, "dependencies", c.ActivityWorkflow(pr).Name)
	for _, mutate := range []func(*WorkflowPR){
		func(p *WorkflowPR) { p.Own = true },
		func(p *WorkflowPR) { p.Author = "human" },
		func(p *WorkflowPR) { p.Head = "feature/lib" },
		func(p *WorkflowPR) { p.Title = "Fix lib" },
		func(p *WorkflowPR) { p.Labels = nil },
		func(p *WorkflowPR) { p.Event = "mention" },
	} {
		other := pr
		mutate(&other)
		require.Equal(t, "fallback", c.ActivityWorkflow(other).Name)
	}
	private := c.For("private/repo", false)
	require.Nil(t, private.ActivityWorkflow(pr))
}

func TestWorkflowValidation(t *testing.T) {
	for _, entry := range []string{
		`{name: x, match: [{}], steps: []}`,
		`{name: x, match: [], steps: [Check]}`,
		`{name: x, match: [{}], steps: [' ']}`,
		`{name: ' ', match: [{}], steps: [Check]}`,
		`{name: x, match: [{head: '['}], steps: [Check]}`,
		`{name: x, match: [{}], on: unknown, steps: [Check]}`,
		`{name: x, match: [{events: [mention]}], on: ci_passed, steps: [Check]}`,
	} {
		_, err := Parse([]byte("workflows: ["+entry+"]"), "test")
		require.Error(t, err, entry)
	}
	_, err := Parse([]byte(`workflows:
- {name: x, match: [{}], steps: [Check]}
- {name: x, match: [{}], steps: [Check]}`), "test")
	require.ErrorContains(t, err, "unique")
}

func TestWorkflowTextGlobsPreserveLiteralSuffixes(t *testing.T) {
	w := Workflow{Match: []WorkflowMatch{{Title: "Update foo.git", Head: "feature/", Labels: []string{"ci.git"}}}}
	require.True(t, w.Matches(WorkflowPR{Title: "Update foo.git", Head: "feature/", Labels: []string{"ci.git"}}))
	require.False(t, w.Matches(WorkflowPR{Title: "Update foo", Head: "feature", Labels: []string{"ci"}}))
}
