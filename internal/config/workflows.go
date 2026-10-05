package config

import (
	"fmt"
	"slices"
	"strings"

	"github.com/gobwas/glob"
)

// Workflow is an ordered set of instructions for an agent session.
type Workflow struct {
	Name  string          `yaml:"name" jsonschema:"required,minLength=1"`
	Match []WorkflowMatch `yaml:"match" jsonschema:"required,minItems=1"`
	On    string          `yaml:"on,omitempty" jsonschema:"enum=activity,enum=discussions_resolved,enum=ci_passed" jsonschema_description:"activity (default): use for an existing session trigger. Other values launch on a false-to-true condition transition on a discovered PR."`
	Steps []string        `yaml:"steps" jsonschema:"required,minItems=1" jsonschema_description:"Instructions executed by the agent in this order. Stop and explain when a required step is blocked."`
	Post  bool            `yaml:"post" jsonschema_description:"Authorize GitHub posts explicitly requested by the steps. Existing github_writes guards still apply. Default: prepare drafts in chat."`
}

// WorkflowMatch extends the existing repo/ownership identity with PR metadata.
// Attributes are ANDed; entries and values within a list are ORed.
type WorkflowMatch struct {
	Identity `yaml:",inline"`
	Authors  []string `yaml:"authors,omitempty" jsonschema:"minLength=1"`
	Labels   []string `yaml:"labels,omitempty" jsonschema:"minLength=1"`
	Title    string   `yaml:"title,omitempty" jsonschema:"minLength=1"`
	Head     string   `yaml:"head,omitempty" jsonschema:"minLength=1"`
	Events   []string `yaml:"events,omitempty" jsonschema:"enum=notification,enum=own_pr,enum=opt_in,enum=review_change,enum=review_reply,enum=mention,minItems=1,uniqueItems=true"`
}

// WorkflowPR is the metadata used when selecting a workflow.
type WorkflowPR struct {
	Repo, Author, Title, Head, Event string
	Own                              bool
	Labels                           []string
}

// When returns the workflow trigger, including its default.
func (w Workflow) When() string {
	if w.On == "" {
		return "activity"
	}
	return w.On
}

// Matches reports whether any match entry selects this PR and event.
func (w Workflow) Matches(pr WorkflowPR) bool {
	return slices.ContainsFunc(w.Match, func(m WorkflowMatch) bool {
		if !m.matches(pr.Repo, pr.Own) || (len(m.Events) > 0 && !slices.Contains(m.Events, pr.Event)) {
			return false
		}
		if len(m.Authors) > 0 {
			g, err := LoginGlobs(m.Authors)
			if err != nil || !g.MatchLogin(pr.Author) {
				return false
			}
		}
		for _, a := range [][2]string{{m.Title, pr.Title}, {m.Head, pr.Head}} {
			if a[0] != "" {
				g, err := textGlobs([]string{a[0]})
				if err != nil || !g.Match(a[1]) {
					return false
				}
			}
		}
		if len(m.Labels) > 0 {
			g, err := textGlobs(m.Labels)
			if err != nil || !slices.ContainsFunc(pr.Labels, g.Match) {
				return false
			}
		}
		return true
	})
}

// ActivityWorkflow selects the first matching activity workflow in file order.
func (c *Config) ActivityWorkflow(pr WorkflowPR) *Workflow {
	for _, w := range c.Workflows {
		if w.When() == "activity" && w.Matches(pr) {
			return &w
		}
	}
	return nil
}

func (c *Config) checkWorkflows() []string {
	var errs []string
	names := map[string]bool{}
	for i, w := range c.Workflows {
		prefix := fmt.Sprintf("workflows[%d]", i)
		if strings.TrimSpace(w.Name) == "" || names[w.Name] {
			errs = append(errs, prefix+": name must be nonblank and unique")
		}
		names[w.Name] = true
		for _, step := range w.Steps {
			if strings.TrimSpace(step) == "" {
				errs = append(errs, prefix+": steps must not be blank")
			}
		}
		for _, m := range w.Match {
			if _, err := RepoGlobs([]string{m.Repo}); m.Repo != "" && err != nil {
				errs = append(errs, prefix+".match: "+err.Error())
			}
			patterns := append([]string{}, m.Labels...)
			for _, p := range []string{m.Title, m.Head} {
				if p != "" {
					patterns = append(patterns, p)
				}
			}
			if _, err := textGlobs(patterns); err != nil {
				errs = append(errs, prefix+".match: "+err.Error())
			}
			if w.When() != "activity" && len(m.Events) > 0 {
				errs = append(errs, prefix+": events only apply to activity workflows")
			}
		}
	}
	return errs
}

// textGlobs matches titles, labels and branches literally, without the URL
// normalization used by repository globs.
func textGlobs(patterns []string) (Globs, error) {
	out := make(Globs, 0, len(patterns))
	for _, pattern := range patterns {
		compiled, err := glob.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("bad glob %q: %w", pattern, err)
		}
		out = append(out, compiled)
	}
	return out, nil
}
