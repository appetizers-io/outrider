package session

import (
	"strings"

	"github.com/appetizers-io/outrider/internal/config"
)

// WorkflowMetadata is the request's PR and triggering event, for matching.
func WorkflowMetadata(r Request, own bool) config.WorkflowPR {
	pr := config.WorkflowPR{Repo: r.Repo, Own: own, Author: r.PR.AuthorLogin(), Title: r.PR.Title, Head: r.PR.HeadRefName, Event: r.Event}
	for _, label := range r.PR.Labels {
		pr.Labels = append(pr.Labels, label.Name)
	}
	return pr
}

func (l *Launcher) workflow(r Request, cfg *config.Config) *config.Workflow {
	if r.Workflow != nil {
		return r.Workflow
	}
	own := strings.EqualFold(r.PR.AuthorLogin(), l.Login)
	return cfg.ActivityWorkflow(WorkflowMetadata(r, own))
}
