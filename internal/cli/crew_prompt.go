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
	return &cobra.Command{Use: "session-prompt", Short: "Render a Crew task using current PR metadata and explicit settings",
		Long: "Render a Crew task using current PR metadata and explicit settings.\n\n" +
			"Prints {\"prompt\", \"own\", \"author\", \"fork\", \"head_mine\", \"base_ref\"}: the\n" +
			"task, and the PR ownership the caller needs to decide session trust and\n" +
			"whether the PR's own agent configuration may be loaded.",
		Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
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
			own := strings.EqualFold(user.Login, pr.AuthorLogin())
			cfg = cfg.For(input.Item.Repo, own)
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
			if !own && cfg.OthersPRs.ReviewForks == nil && input.Origin != "" && !strings.EqualFold(input.Origin, input.Item.Repo) {

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
			return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
				"prompt":    prompt,
				"own":       own,
				"author":    pr.AuthorLogin(),
				"fork":      forkHead(input.Item.Repo, pr),
				"head_mine": headMine(user.Login, pr),
				"base_ref":  pr.BaseRefName,
			})
		}}
}

// forkHead tells whether the PR's head is in another repository, so its
// commits are outside the watched repository's access control. Owner and name
// must both match, and head repository data that GitHub does not report
// counts as a fork: the caller uses this to withhold trust.
func forkHead(repo string, pr github.PR) bool {
	owner, name, found := strings.Cut(repo, "/")
	if !found || pr.HeadRepositoryOwner == nil || pr.HeadRepositoryOwner.Login == "" ||
		pr.HeadRepository == nil || pr.HeadRepository.Name == "" {
		return true
	}
	return !strings.EqualFold(owner, pr.HeadRepositoryOwner.Login) || !strings.EqualFold(name, pr.HeadRepository.Name)
}

// headMine tells whether the PR's head lives in this user's own repository,
// which is where a fork workflow puts their own branches. Together with the
// author it separates "my work, pushed to my fork" from a stranger's fork.
func headMine(login string, pr github.PR) bool {
	if login == "" || pr.HeadRepositoryOwner == nil || pr.HeadRepositoryOwner.Login == "" {
		return false
	}
	return strings.EqualFold(login, pr.HeadRepositoryOwner.Login)
}
