package poll

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/appetizers-io/outrider/internal/proc"
	"github.com/appetizers-io/outrider/internal/session"
)

const ciWorkflow = `triggers:
  own_prs: {enabled: false}
  opt_in: {enabled: false}
  review_replies: {enabled: false}
  mentions: {enabled: false}
workflows:
  - name: green
    on: ci_passed
    match: [{repo: o/r}]
    steps: [Check current head, Prepare approval]
`

func TestWorkflowConditionRetryRestartAndRearm(t *testing.T) {
	h := newHub(t)
	h.pr(1, "me")
	h.notify("1", 1, "2026-01-01T00:00:00Z")
	p := h.poller(ciWorkflow)
	s := liveState(t)
	h.prs["o/r#1"]["statusCheckRollup"] = []any{map[string]any{"state": "PENDING"}}
	h.poll(p, s)
	require.Empty(t, h.launches)
	require.Contains(t, s.WorkflowPRs, "o/r#1")
	h.notifications = nil // no new notification required
	h.prs["o/r#1"]["statusCheckRollup"] = []any{map[string]any{"state": "SUCCESS"}}
	h.launchOK = false
	h.poll(p, s)
	require.Len(t, h.launches, 1)
	h.launchOK = true
	h.poll(p, s)
	require.Len(t, h.launches, 2)
	require.Equal(t, "workflow green: ci_passed", h.launches[1].trigger)
	require.False(t, h.launches[1].hasGate)
	s, err := LoadState(p.StatePath)
	require.NoError(t, err)
	h.poll(p, s)
	require.Len(t, h.launches, 2)
	h.prs["o/r#1"]["statusCheckRollup"] = []any{map[string]any{"state": "FAILURE"}}
	h.poll(p, s)
	h.prs["o/r#1"]["statusCheckRollup"] = []any{map[string]any{"state": "SUCCESS"}}
	h.poll(p, s)
	require.Len(t, h.launches, 3)
	h.prs["o/r#1"]["state"] = "CLOSED"
	h.poll(p, s)
	require.Empty(t, s.WorkflowPRs)
	require.Empty(t, s.WorkflowConditions)
}

func TestWorkflowConditionBaselineAndDryRun(t *testing.T) {
	for _, mode := range []string{"baseline", "existing", "dry-run"} {
		t.Run(mode, func(t *testing.T) {
			h := newHub(t)
			h.pr(1, "me")
			h.prs["o/r#1"]["statusCheckRollup"] = []any{map[string]any{"state": "SUCCESS"}}
			h.notify("1", 1, "2026-01-01T00:00:00Z")
			p := h.poller(ciWorkflow)
			p.ProcessExisting = mode == "existing"
			p.DryRun = mode == "dry-run"
			s := emptyState(t)
			h.poll(p, s)
			if mode == "baseline" {
				require.Empty(t, h.launches)
			} else {
				require.Len(t, h.launches, 1)
			}
			if p.DryRun {
				require.Empty(t, s.WorkflowConditions)
				require.Empty(t, s.WorkflowPRs)
			}
			h.launches = nil
			if !p.DryRun {
				h.poll(p, s)
				require.Empty(t, h.launches)
			}
		})
	}
}

func TestResolvedDiscussionsRetryLookup(t *testing.T) {
	h := newHub(t)
	h.pr(1, "me")
	h.notify("1", 1, "2026-01-01T00:00:00Z")
	p := h.poller(strings.ReplaceAll(ciWorkflow, "ci_passed", "discussions_resolved"))
	resolved, unavailable := false, false
	p.GH.Run = func(ctx context.Context, cmd proc.Cmd) (proc.Result, error) {
		if len(cmd.Args) > 2 && cmd.Args[2] == "graphql" {
			if unavailable {
				return proc.Result{Stdout: `{"data":{"repository":null}}`}, nil
			}
			value := "false"
			if resolved {
				value = "true"
			}
			return proc.Result{Stdout: `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{"isResolved":` + value + `}],"pageInfo":{"hasNextPage":false}}}}}}`}, nil
		}
		return h.gh(ctx, cmd)
	}
	s := liveState(t)
	h.poll(p, s)
	require.Empty(t, h.launches)
	resolved, unavailable = true, true
	h.poll(p, s)
	require.Empty(t, h.launches)
	unavailable = false
	h.poll(p, s)
	require.Len(t, h.launches, 1)
	h.poll(p, s)
	require.Len(t, h.launches, 1)
}

func TestDependencyWorkflowNotificationWithoutOptIn(t *testing.T) {
	h := newHub(t)
	h.pr(1, "dependabot[bot]")
	h.notify("1", 1, "2026-01-01T00:00:00Z")
	p := h.poller(`triggers:
  opt_in: {enabled: false}
workflows:
  - name: dependency
    match: [{authors: ['dependabot[bot]'], events: [notification]}]
    steps: [Read changelog, Check compatibility, Check CI]
`)
	p.Launch = func(ctx context.Context, r session.Request) bool {
		require.Equal(t, "notification", r.Event)
		cfg, _ := p.scoped(r.Repo, false)
		require.Equal(t, "dependency", cfg.ActivityWorkflow(session.WorkflowMetadata(r, false)).Name)
		return h.launch(ctx, r)
	}
	s := liveState(t)
	h.launchOK = false
	h.poll(p, s)
	require.Len(t, h.launches, 1)
	h.launchOK = true
	h.poll(p, s)
	require.Len(t, h.launches, 2)
	h.poll(p, s)
	require.Len(t, h.launches, 2)
}

func TestConditionalWorkflowMetadataFilter(t *testing.T) {
	h := newHub(t)
	h.pr(1, "me")
	h.notify("1", 1, "2026-01-01T00:00:00Z")
	h.prs["o/r#1"]["statusCheckRollup"] = []any{map[string]any{"state": "SUCCESS"}}
	p := h.poller(strings.ReplaceAll(ciWorkflow, "repo: o/r", "authors: [someone-else]"))
	s := liveState(t)
	h.poll(p, s)
	require.Empty(t, h.launches)
	require.Empty(t, s.WorkflowPRs)
}
