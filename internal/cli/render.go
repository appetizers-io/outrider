package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/appetizers-io/outrider/internal/config"
	"github.com/appetizers-io/outrider/internal/forgejo"
	"github.com/appetizers-io/outrider/internal/poll"
	"github.com/appetizers-io/outrider/internal/session"
	"github.com/appetizers-io/outrider/internal/watch"
)

// splitForgeRepo splits host/owner/repo.
func splitForgeRepo(s string) (host, repo string, err error) {
	host, repo, ok := strings.Cut(strings.TrimSpace(s), "/")
	if !ok || host == "" || !poll.RepoName.MatchString(repo) {
		return "", "", fmt.Errorf("--repo %q: want host/owner/repo, the host as configured in forges", s)
	}
	return host, repo, nil
}

// agentPath joins name onto dir, a path where the agent runs rather than on
// this machine, so it keeps dir's separator instead of the host's.
func agentPath(dir, name string) string {
	sep := "/"
	if strings.Contains(dir, `\`) && !strings.Contains(dir, "/") {
		sep = `\`
	}
	return strings.TrimRight(dir, `/\`) + sep + name
}

// The --workspace values: what the agent may do in its working directory.
const (
	workspaceReadOnly   = "readonly"
	workspaceDisposable = "disposable"
)

// renderCmd renders the offline prompt of one PR on a configured forge and
// writes its context, for a runner that starts the agent itself.
func renderCmd(d watch.Deps) *cobra.Command {
	var source, repoArg, workflowName, contextDir, promptDir, trigger, workspace string
	var number int
	cmd := &cobra.Command{
		Use:   "render",
		Short: "Render the review prompt for a PR on a configured forge and write its offline context",
		Long: "Render the review prompt for a pull request on a forge configured under\n" +
			"forges (Forgejo), and write the PR context the prompt cites into --context-dir.\n" +
			"For a runner that starts the agent itself, offline: the prompt tells the agent to\n" +
			"read the prefetched files instead of calling the forge. Outrider starts no agent,\n" +
			"installs no guards and posts nothing.\n\n" +
			"--workspace disposable tells the agent its working directory is a throwaway copy\n" +
			"of the PR head, so it may edit files and run local tests there; the forge stays\n" +
			"read-only either way.\n\n" +
			"Prints {\"prompt\", \"own\", \"author\", \"fork\", \"head_mine\", \"base_ref\", \"head_ref\",\n" +
			"\"repo\", \"number\", \"workflow\", \"review\", \"context_dir\", \"prompt_context_dir\",\n" +
			"\"files\"}: the prompt, the PR ownership the runner needs to decide trust, and where\n" +
			"the context went.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			host, repo, err := splitForgeRepo(repoArg)
			if err != nil {
				return err
			}
			if number <= 0 {
				return errors.New("--pr must be a pull request number")
			}
			if contextDir == "" {
				return errors.New("--context-dir is required")
			}
			if workspace != workspaceReadOnly && workspace != workspaceDisposable {
				return fmt.Errorf("--workspace %q: want %s or %s", workspace, workspaceReadOnly, workspaceDisposable)
			}
			disposable := workspace == workspaceDisposable
			if contextDir, err = filepath.Abs(contextDir); err != nil {
				return err
			}
			if promptDir == "" {
				promptDir = contextDir
			}
			path := config.Find(source)
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			f := cfg.Forge(host)
			if f == nil {
				return fmt.Errorf("no forge for host %s in %s; add it under forges", host, config.Describe(path))
			}
			reader, err := forgejo.Open(*f, forgejo.Deps{Run: d.Run, Getenv: d.Getenv, Home: d.Home})
			if err != nil {
				return err
			}
			user, err := reader.Me(ctx)
			if err != nil {
				return err
			}
			pr, err := reader.PRView(ctx, repo, number)
			if err != nil {
				return err
			}
			if !pr.Open() {
				return errors.New("PR is not open")
			}
			if user.Login == "" || pr.AuthorLogin() == "" {
				return errors.New("cannot determine PR ownership")
			}
			full := host + "/" + repo
			own := strings.EqualFold(user.Login, pr.AuthorLogin())
			scoped := cfg.For(full, own)
			var workflow *config.Workflow
			for i := range scoped.Workflows {
				if scoped.Workflows[i].Name == workflowName {
					workflow = &scoped.Workflows[i]
					break
				}
			}
			if workflowName != "" && workflow == nil {
				return fmt.Errorf("no workflow %q for %s in %s", workflowName, full, config.Describe(path))
			}
			r := session.Request{Repo: full, N: number, PR: pr, Trigger: trigger}
			review, err := session.ReviewFor(r, &scoped, path, user.Login, contextDir, disposable)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(contextDir, 0o700); err != nil {
				return fmt.Errorf("context dir: %w", err)
			}
			reviewDir := filepath.Join(contextDir, "review-context")
			if err := os.RemoveAll(reviewDir); err != nil {
				return fmt.Errorf("context dir: %w", err)
			}
			if err := reader.SaveContext(ctx, repo, number, contextDir); err != nil {
				return err
			}
			files := []string{}
			for _, file := range reader.ContextFiles() {
				files = append(files, file[0])
			}
			if review != nil && review.Context != "" {
				requested := scoped.Review.Profiles[review.Name].Context
				if err := reader.SaveReviewContext(ctx, repo, number, "", requested.Issues, requested.Docs, reviewDir); err != nil {
					return err
				}
				review.Context = agentPath(promptDir, "review-context")
				files = append(files, "review-context/")
			}
			prompt, err := session.Prompt(session.PromptInput{
				Repo: full, N: number, PR: pr, Trigger: trigger, Owner: watch.Owner(&cfg, user), Login: user.Login,
				Extra: scoped.Prompts.Extra, Push: "review-only", GHWrites: "never", ContextDir: &promptDir,
				Review: review, Workflow: workflow, Forge: "Forgejo", ForgeCLI: "fj", External: true, Disposable: disposable,
				ContextFiles: reader.ContextFiles(),
			})
			if err != nil {
				return err
			}
			reviewName := ""
			if review != nil {
				reviewName = review.Name
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
				"prompt":             prompt,
				"own":                own,
				"author":             pr.AuthorLogin(),
				"fork":               forkHead(repo, pr),
				"head_mine":          headMine(user.Login, pr),
				"base_ref":           pr.BaseRefName,
				"head_ref":           pr.HeadRefName,
				"repo":               full,
				"number":             number,
				"workflow":           workflowName,
				"review":             reviewName,
				"context_dir":        contextDir,
				"prompt_context_dir": promptDir,
				"files":              files,
			})
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&source, "config", "", "YAML config with forges, workflows and prompts.extra (default: $"+config.EnvVar+", else "+config.DefaultPath()+" when present)")
	fs.StringVar(&repoArg, "repo", "", "host/owner/repo, the host as configured in forges")
	fs.IntVar(&number, "pr", 0, "pull request number")
	fs.StringVar(&workflowName, "workflow", "", "workflow to render, by name (default: the built-in review task)")
	fs.StringVar(&contextDir, "context-dir", "", "where to write the PR context files")
	fs.StringVar(&promptDir, "prompt-context-dir", "", "the path the prompt cites for the context files, where the agent sees them (default: --context-dir)")
	fs.StringVar(&workspace, "workspace", workspaceReadOnly, "the agent's working directory: readonly, or disposable (a throwaway copy of the PR head it may edit and run local tests in)")
	fs.StringVar(&trigger, "trigger", "review requested by the runner", "the trigger line of the prompt")
	return cmd
}
