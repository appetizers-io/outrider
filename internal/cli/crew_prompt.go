package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/appetizers-io/outrider/internal/config"
	"github.com/appetizers-io/outrider/internal/crew"
	"github.com/appetizers-io/outrider/internal/github"
	"github.com/appetizers-io/outrider/internal/poll"
	"github.com/appetizers-io/outrider/internal/session"
	"github.com/appetizers-io/outrider/internal/watch"
)

// Render the shared task prompt, using only explicit Crew configuration.
func crewPromptCmd(d watch.Deps) *cobra.Command {
	return &cobra.Command{Use: "session-prompt", Short: "Render a Crew task using current PR metadata and explicit settings", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		var input struct {
			Item     crew.Item       `json:"item"`
			Settings json.RawMessage `json:"settings"`
			Remote   string          `json:"remote"`
			Origin   string          `json:"origin"`
			Branch   string          `json:"branch"`
		}
		if err := json.NewDecoder(io.LimitReader(cmd.InOrStdin(), 262145)).Decode(&input); err != nil {
			return err
		}
		if !poll.RepoName.MatchString(input.Item.Repo) || input.Item.PR <= 0 || input.Remote == "" || input.Branch == "" {
			return errors.New("missing Crew PR identity or checkout context")
		}
		cfg, err := config.Parse(input.Settings, "Crew session settings")
		if err != nil {
			return err
		}
		gh := &github.Client{Run: d.Run}
		user, err := gh.Me(cmd.Context())
		if err != nil {
			return err
		}
		pr, err := gh.PRView(cmd.Context(), input.Item.Repo, input.Item.PR)
		if err != nil {
			return err
		}
		if !pr.Open() {
			return errors.New("PR is no longer open")
		}
		if user.Login == "" || pr.AuthorLogin() == "" {
			return errors.New("cannot determine PR ownership")
		}
		cfg = cfg.For(input.Item.Repo, strings.EqualFold(user.Login, pr.AuthorLogin()))
		var workflow *config.Workflow
		for i := range cfg.Workflows {
			if cfg.Workflows[i].Name == input.Item.Workflow {
				workflow = &cfg.Workflows[i]
				break
			}
		}
		if input.Item.Workflow != "" && workflow == nil {
			return errors.New("queued workflow changed; check repository settings")
		}
		forks := cfg.OthersPRs.Forks()
		if !strings.EqualFold(user.Login, pr.AuthorLogin()) && cfg.OthersPRs.ReviewForks == nil && input.Origin != "" && !strings.EqualFold(input.Origin, input.Item.Repo) {

			if !poll.RepoName.MatchString(input.Origin) {
				return errors.New("invalid origin repository")
			}
			fork, err := github.JSON[struct {
				Fork   bool        `json:"fork"`
				Owner  github.User `json:"owner"`
				Parent *struct {
					FullName string `json:"full_name"`
				} `json:"parent"`
			}](cmd.Context(), gh, "api", "repos/"+input.Origin)
			if err != nil {
				return fmt.Errorf("verify review fork: %w", err)
			}
			if fork.Fork && fork.Parent != nil && strings.EqualFold(fork.Owner.Login, user.Login) && strings.EqualFold(fork.Parent.FullName, input.Item.Repo) {
				forks = []string{input.Origin}
			}
		}
		push, writes := "ask", "ask"
		if cfg.Push != nil {
			push = *cfg.Push
		}
		if cfg.GitHubWrites != nil {
			writes = *cfg.GitHubWrites
		}
		prompt, err := session.Prompt(session.PromptInput{Crew: true, LocalBranch: input.Branch, Repo: input.Item.Repo, N: input.Item.PR, PR: pr, Login: user.Login, Owner: user.Login, Remote: input.Remote, Trigger: input.Item.Trigger, Scope: input.Item.Scope, ScopeWhy: input.Item.ScopeWhy, Workflow: workflow, Extra: cfg.Prompts.Extra, AllowPush: cfg.OthersPRs.AllowPush != nil && *cfg.OthersPRs.AllowPush, ReviewForks: forks, Push: push, GHWrites: writes})
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{"prompt": prompt})
	}}
}
