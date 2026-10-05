package poll

import (
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/appetizers-io/outrider/internal/config"
	"github.com/appetizers-io/outrider/internal/github"
	"github.com/appetizers-io/outrider/internal/session"
)

// workflows watches known PRs independently of new notifications. A condition
// stays pending until Launch accepts it; a later false observation rearms it.
func (ps *pass) workflows(ctx context.Context) {
	if !slices.ContainsFunc(ps.Cfg.Layers(), func(c config.Config) bool {
		return slices.ContainsFunc(c.Workflows, func(w config.Workflow) bool { return w.When() != "activity" })
	}) {
		return
	}
	refs := maps.Clone(ps.workflowRefs)
	for k, c := range ps.s.Candidates {
		refs[k] = github.Ref{Repo: c.Repo, N: c.PR}
	}
	for k, w := range ps.s.Watched {
		refs[k] = github.Ref{Repo: w.Repo, N: w.PR}
	}
	for _, k := range slices.Sorted(maps.Keys(refs)) {
		ref := refs[k]
		if !repoOK(ref.Repo, ps.Include, ps.Exclude) {
			continue
		}
		pr, err := ps.GH.PRView(ctx, ref.Repo, ref.N)
		if err != nil {
			continue
		}
		if !pr.Open() {
			if !ps.DryRun {
				delete(ps.s.WorkflowPRs, k)
				delete(ps.s.WorkflowConditions, k)
			}
			continue
		}
		own := strings.EqualFold(pr.AuthorLogin(), ps.Login)
		cfg, _ := ps.scoped(ref.Repo, own)
		req := session.Request{Repo: ref.Repo, N: ref.N, PR: pr}
		states := maps.Clone(ps.s.WorkflowConditions[k])
		if states == nil {
			states = map[string]bool{}
		}
		active := map[string]bool{}
		conditions := map[string]bool{}
		for _, w := range cfg.Workflows {
			if w.When() == "activity" || !w.Matches(session.WorkflowMetadata(req, own)) {
				continue
			}
			active[w.Name] = true
			ready, cached := conditions[w.When()]
			if !cached {
				switch w.When() {
				case "ci_passed":
					ready = github.CIPassed(pr)
				case "discussions_resolved":
					ready, err = ps.GH.DiscussionsResolved(ctx, ref.Repo, ref.N)
					if err != nil {
						ps.Log.Warn(k + ": " + err.Error())
						continue
					}
				}
				conditions[w.When()] = ready
			}
			was, known := states[w.Name]
			if !ready || (!known && ps.baseline) {
				states[w.Name] = ready
				continue
			}
			if was {
				continue
			}
			req.Workflow = &w
			req.Trigger = "workflow " + w.Name + ": " + w.When()
			if ps.Launch(ctx, req) {
				states[w.Name] = true
			}
		}
		maps.DeleteFunc(states, func(name string, _ bool) bool { return !active[name] })
		if !ps.DryRun {
			if len(active) == 0 {
				delete(ps.s.WorkflowPRs, k)
				delete(ps.s.WorkflowConditions, k)
			} else {
				ps.s.WorkflowPRs[k] = ref
				ps.s.WorkflowConditions[k] = states
			}
		}
	}
}

// nonOwnedNotification lets a workflow opt a matched PR into notification
// processing without requiring a reaction. Otherwise use the existing triggers.
func (ps *pass) nonOwnedNotification(ctx context.Context, repo string, n int, pr *github.PR) bool {
	cfg, _ := ps.scoped(repo, false)
	if len(cfg.Workflows) > 0 {
		if pr == nil {
			got, err := ps.GH.PRView(ctx, repo, n)
			if err != nil {
				return false
			}
			pr = &got
		}
		req := session.Request{Repo: repo, N: n, PR: *pr, Event: "notification", Trigger: "PR notification"}
		if pr.Open() && cfg.ActivityWorkflow(session.WorkflowMetadata(req, false)) != nil {
			if ps.baseline {
				return true
			}
			return ps.Launch(ctx, req)
		}
	}
	return ps.repliesAndMentions(ctx, repo, n, pr)
}
