package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/appetizers-io/outrider/internal/config"
	"github.com/appetizers-io/outrider/internal/crew"
	"github.com/appetizers-io/outrider/internal/github"
	"github.com/appetizers-io/outrider/internal/poll"
	"github.com/appetizers-io/outrider/internal/session"
	"github.com/appetizers-io/outrider/internal/watch"
)

// A manual request opts into one PR without changing subscriptions or GitHub reactions.
func crewCaptureCmd(d watch.Deps) *cobra.Command {
	var repo, dir, source string
	var number int
	cmd := &cobra.Command{Use: "capture", Short: "Queue one explicitly requested PR for Crew", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if !poll.RepoName.MatchString(repo) || (strings.HasPrefix(repo, "../") || strings.HasSuffix(repo, "/..") || strings.HasPrefix(repo, "./") || strings.HasSuffix(repo, "/.")) || number <= 0 || dir == "" || source == "" {
			return errors.New("capture requires --repo owner/repo, --pr, --config and --state-dir")
		}
		cfg, err := config.Load(source)
		if err != nil {
			return err
		}
		allowed := false
		for _, configured := range cfg.Repos.Include {
			if strings.EqualFold(configured, repo) {
				allowed = true
			}
		}
		if !allowed {
			return errors.New("add the repository to Crew before requesting a review")
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 120*time.Second)
		defer cancel()
		gh := &github.Client{Run: d.Run}
		user, err := gh.Me(ctx)
		if err != nil {
			return err
		}
		pr, err := gh.PRView(ctx, repo, number)
		if err != nil {
			return err
		}
		if !pr.Open() || user.Login == "" || pr.AuthorLogin() == "" {
			return errors.New("PR must be open with known ownership")
		}
		own := strings.EqualFold(user.Login, pr.AuthorLogin())
		cfg = cfg.For(repo, own)
		request := session.Request{Repo: repo, N: number, PR: pr, Trigger: "manual opt-in from Crew", Event: "opt_in"}
		request.Workflow = cfg.ActivityWorkflow(session.WorkflowMetadata(request, own))
		scanner := crew.Scanner{Dir: dir, Config: cfg, GH: gh}
		item, err := scanner.Capture(ctx, request, cfg)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"id": item.ID, "repo": item.Repo, "pr": item.PR, "head": item.Head, "workflow": item.Workflow})
	}}
	cmd.Flags().StringVar(&repo, "repo", "", "configured owner/repo")
	cmd.Flags().IntVar(&number, "pr", 0, "pull request number")
	cmd.Flags().StringVar(&source, "config", "", "explicit Crew scanner configuration")
	cmd.Flags().StringVar(&dir, "state-dir", "", "Crew inbox directory")
	return cmd
}
