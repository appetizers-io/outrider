package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/appetizers-io/outrider/internal/config"
	"github.com/appetizers-io/outrider/internal/poll"
)

// crewImportSettingsCmd previews compatible CLI settings without writing them.
func crewImportSettingsCmd() *cobra.Command {
	var repo, profile string
	cmd := &cobra.Command{Use: "import-settings", Short: "Preview CLI YAML as Crew repository settings", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !poll.RepoName.MatchString(repo) {
				return errors.New("--repo must be owner/repo")
			}
			if profile != "common" && profile != "own" && profile != "others" {
				return errors.New("--profile must be common, own or others")
			}
			raw, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 131073))
			if err != nil {
				return err
			}
			if len(raw) > 131072 {
				return errors.New("CLI configuration exceeds 128 KiB")
			}
			cfg, err := config.Parse(raw, "CLI import")
			if err != nil {
				return err
			}
			extract := func(own bool) map[string]any {
				effective := cfg.For(repo, own)
				return map[string]any{"triggers": effective.Triggers, "ignore_authors": effective.IgnoreAuthors, "launch_check": effective.LaunchCheck, "prompts": effective.Prompts, "workflows": effective.Workflows}
			}
			settings := extract(profile != "others")
			if profile == "common" && !reflect.DeepEqual(settings, extract(false)) {
				return errors.New("this CLI config has different own-PR and other-PR settings; choose which profile to import. The chosen settings apply repository-wide in Crew")
			}
			warnings := []string{"Only repository behavior is imported. Crew keeps its model, agent, permissions, and session settings.", "This imports a snapshot of effective settings, not a live link to the CLI file."}
			if profile != "common" {
				warnings = append(warnings, fmt.Sprintf("Selected %s-PR settings will apply to all PRs in this repository.", profile))
			}
			layer := cfg.For(repo, profile != "others")
			if layer.LaunchCheck.Classifier != nil && *layer.LaunchCheck.Classifier != "jev" {
				layer.LaunchCheck.Classifier = nil
				settings["launch_check"] = layer.LaunchCheck
				warnings = append(warnings, "Custom launch classifier is not imported; launch checking is disabled in this draft.")
			}
			// Nil slices must become omitted fields, not YAML null (the schema expects arrays).
			if layer.IgnoreAuthors == nil {
				delete(settings, "ignore_authors")
			}
			if layer.Workflows == nil {
				delete(settings, "workflows")
			}
			var source map[string]any
			if err := yaml.Unmarshal(raw, &source); err != nil {
				return err
			}
			skipped := []string{}
			for key := range source {
				if _, ok := settings[key]; !ok && key != "overrides" {
					skipped = append(skipped, key)
				}
			}
			sort.Strings(skipped)
			encoded, err := yaml.Marshal(settings)
			if err != nil {
				return err
			}
			if _, err := config.Parse(encoded, "imported repository settings"); err != nil {
				return err
			}
			var normalized map[string]any
			if err := yaml.Unmarshal(encoded, &normalized); err != nil {
				return err
			}
			applied := cfg.Applied(repo, profile != "others")
			if applied == nil {
				applied = []string{}
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"settings": normalized, "yaml": string(encoded), "warnings": warnings, "skipped": skipped, "applied_overrides": applied})
		}}
	cmd.Flags().StringVar(&repo, "repo", "", "target repository")
	cmd.Flags().StringVar(&profile, "profile", "common", "common, own or others; chosen settings apply repository-wide")
	return cmd
}
