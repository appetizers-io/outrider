package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

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
	cmd.AddCommand(scan, crewImportSettingsCmd(), crewDefaultConfigCmd())
	cmd.AddCommand(&cobra.Command{Use: "settings", Short: "Validate and convert Crew repository YAML from stdin", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			raw, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 131073))
			if err != nil {
				return err
			}
			if len(raw) > 131072 {
				return errors.New("repository settings exceed 128 KiB")
			}
			var settings map[string]any
			if err := yaml.Unmarshal(raw, &settings); err != nil {
				return err
			}
			if settings == nil {
				settings = map[string]any{}
			}
			allowed := map[string]bool{"triggers": true, "ignore_authors": true, "launch_check": true, "prompts": true, "workflows": true, "overrides": true}
			for key := range settings {
				if !allowed[key] {
					return fmt.Errorf("unsupported repository setting %q; session settings belong to Crew", key)
				}
			}
			if entries, exists := settings["overrides"]; exists {
				overrides, ok := entries.([]any)
				if !ok {
					return errors.New("overrides must be a list")
				}
				for _, entry := range overrides {
					layer, ok := entry.(map[string]any)
					if !ok {
						return errors.New("override must be a mapping")
					}
					for key := range layer {
						if key != "match" && (!allowed[key] || key == "overrides") {
							return fmt.Errorf("unsupported ownership setting %q", key)
						}
					}
					matches, ok := layer["match"].([]any)
					if !ok || len(matches) == 0 {
						return errors.New("ownership override needs a match")
					}
					for _, entry := range matches {
						match, ok := entry.(map[string]any)
						if !ok || len(match) != 1 || (match["prs"] != "own" && match["prs"] != "others") {
							return errors.New("repository ownership overrides match only prs: own or others")
						}
					}
				}
			}
			encoded, err := yaml.Marshal(settings)
			if err != nil {
				return err
			}
			cfg, err := config.Parse(encoded, "repository settings")
			if err != nil {
				return err
			}
			// Show the same effective trigger values as the assisted form.
			triggerData, err := yaml.Marshal(cfg.Triggers)
			if err != nil {
				return err
			}
			var triggers map[string]any
			if err := yaml.Unmarshal(triggerData, &triggers); err != nil {
				return err
			}
			settings["triggers"] = triggers
			encoded, err = yaml.Marshal(settings)
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"settings": settings, "yaml": string(encoded)})
		}})
	return cmd
}
