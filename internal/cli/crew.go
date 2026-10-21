package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/spf13/cobra"

	"github.com/appetizers-io/outrider/internal/config"
	"github.com/appetizers-io/outrider/internal/crew"
	"github.com/appetizers-io/outrider/internal/github"
	"github.com/appetizers-io/outrider/internal/watch"
)

func crewCmd(d watch.Deps) *cobra.Command {
	var dir, source string
	var repos []string
	var existing bool
	var budget time.Duration
	cmd := &cobra.Command{Use: "crew", Short: "Kiro Crew snapshot review integration"}
	scan := &cobra.Command{Use: "scan", Short: "Poll once into a durable Crew inbox (no checkout or agent launch)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if dir == "" {
				return errors.New("--state-dir is required (use a dedicated Crew data directory)")
			}
			cfg, err := config.Load(source)
			if err != nil {
				return err
			}
			if len(repos) > 0 {
				cfg.Repos.Include = repos
			}
			for i, repo := range cfg.Repos.Include {
				cfg.Repos.Include[i] = config.RepoPattern(repo)
			}
			for i, repo := range cfg.Repos.Exclude {
				cfg.Repos.Exclude[i] = config.RepoPattern(repo)
			}
			s := crew.Scanner{Dir: dir, Config: cfg, GH: &github.Client{Run: d.Run}, LookPath: d.LookPath, Log: slog.Default(), ProcessExisting: existing}
			if budget <= 0 {
				return errors.New("scan timeout must be positive")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), budget)
			defer cancel()
			err = s.Scan(ctx)
			if err != nil && !errors.Is(err, crew.ErrScanBudget) {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]bool{"complete": err == nil})
		}}
	scan.Flags().DurationVar(&budget, "timeout", 150*time.Second, "scan budget; save progress before returning a partial pass")
	scan.Flags().StringVar(&dir, "state-dir", "", "dedicated inbox and poll-state directory")
	scan.Flags().StringVar(&source, "config", "", "optional Outrider YAML config (otherwise built-in defaults)")
	scan.Flags().StringArrayVar(&repos, "repo", nil, "repository allowlist, owner/repo or glob (repeatable)")
	scan.Flags().BoolVar(&existing, "process-existing", false, "queue existing activity on the first scan")
	cmd.AddCommand(crewCaptureCmd(d), crewPromptCmd(d), scan, crewImportSettingsCmd(), crewDefaultConfigCmd())
	cmd.AddCommand(&cobra.Command{Use: "settings", Short: "Validate and convert Crew repository YAML from stdin", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			raw, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 131073))
			if err != nil {
				return err
			}
			if len(raw) > 131072 {
				return errors.New("repository settings exceed 128 KiB")
			}
			result, err := crew.ConvertSettings(raw)
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
		}})
	return cmd
}
