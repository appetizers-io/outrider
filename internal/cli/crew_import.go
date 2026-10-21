package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"unicode/utf8"

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
			if profile != "preserve" && profile != "common" && profile != "own" && profile != "others" {
				return errors.New("--profile must be preserve, own or others")
			}
			raw, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 131073))
			if err != nil {
				return err
			}
			if len(raw) > 131072 {
				return errors.New("CLI configuration exceeds 128 KiB")
			}
			var source map[string]any
			if err := yaml.Unmarshal(raw, &source); err != nil {
				return err
			}
			skipped := []string{}
			// Crew owns execution and trust. Strip these before standalone validation,
			// including override layers, so irrelevant CLI values cannot block import.
			dropSession := func(layer map[string]any, prefix string) {
				for _, key := range []string{"agent", "mode", "launcher", "terminal", "max_agents", "stale_lock_hours", "push", "github_writes", "sandbox", "isolation", "network_access", "others_prs", "tool_gate", "review", "owner_name"} {
					if _, exists := layer[key]; exists {
						skipped = append(skipped, prefix+key)
						delete(layer, key)
					}
				}
			}
			dropSession(source, "")
			if overrides, ok := source["overrides"].([]any); ok {
				for i, entry := range overrides {
					if layer, ok := entry.(map[string]any); ok {
						dropSession(layer, fmt.Sprintf("overrides[%d].", i))
					}
				}
			}
			filtered, err := yaml.Marshal(source)
			if err != nil {
				return err
			}
			cfg, err := config.Parse(filtered, "CLI import")
			if err != nil {
				return err
			}
			warnings := []string{"Crew-owned execution settings are removed; Crew keeps its trust and session settings.", "This imports a snapshot of repository behavior, not a live link to the CLI file."}
			extract := func(own bool) map[string]any {
				effective := cfg.For(repo, own)
				if effective.LaunchCheck.Classifier != nil && *effective.LaunchCheck.Classifier != "jev" {
					effective.LaunchCheck.Classifier = nil
					warnings = append(warnings, "Custom launch classifier is not imported; launch checking is disabled for that profile.")
				}
				// Explicit empty lists clear inherited values in ownership overrides.
				if effective.IgnoreAuthors == nil {
					effective.IgnoreAuthors = []string{}
				}
				if effective.Workflows == nil {
					effective.Workflows = []config.Workflow{}
				}
				return map[string]any{"triggers": effective.Triggers, "ignore_authors": effective.IgnoreAuthors, "launch_check": effective.LaunchCheck, "prompts": effective.Prompts, "workflows": effective.Workflows}
			}
			own, others := extract(true), extract(false)
			settings := others
			if profile == "own" {
				settings = own
			}
			if profile == "preserve" || profile == "common" {
				if !reflect.DeepEqual(own, others) {
					own["match"] = []map[string]string{{"prs": "own"}}
					settings["overrides"] = []any{own}
					warnings = append(warnings, "Own-PR and other-PR behavior is preserved. Base settings apply to other PRs; the own-PR override applies to your PRs.")
				}
			} else {
				warnings = append(warnings, fmt.Sprintf("Explicit %s profile applies repository-wide.", profile))
			}
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
			if profile == "preserve" || profile == "common" {
				for _, entry := range cfg.Applied(repo, false) {
					found := false
					for _, existing := range applied {
						if existing == entry {
							found = true
						}
					}
					if !found {
						applied = append(applied, entry)
					}
				}
			}
			if applied == nil {
				applied = []string{}
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"settings": normalized, "yaml": string(encoded), "warnings": warnings, "skipped": skipped, "applied_overrides": applied})
		}}
	cmd.Flags().StringVar(&repo, "repo", "", "target repository")
	cmd.Flags().StringVar(&profile, "profile", "preserve", "preserve ownership behavior (default), or explicitly flatten to own/others")
	return cmd
}

// crewDefaultConfigCmd reads only the location selected by the CLI's own rules.
func crewDefaultConfigCmd() *cobra.Command {
	return &cobra.Command{Use: "default-config", Short: "Read the default CLI config for an import preview", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := config.Find("")
			if path == "" {
				path = config.DefaultPath()
			}
			path, err := filepath.Abs(path)
			if err != nil {
				return err
			}
			info, err := os.Stat(path)
			if errors.Is(err, os.ErrNotExist) {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"path": path, "found": false, "content": ""})
			}
			if err != nil {
				return fmt.Errorf("read config %s: %w", path, err)
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("config %s is not a regular file", path)
			}
			if info.Size() > 100000 {
				return errors.New("CLI configuration exceeds 100 KB; choose a smaller file or paste the relevant settings")
			}
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer func() { _ = f.Close() }()
			raw, err := io.ReadAll(io.LimitReader(f, 100001))
			if err != nil {
				return err
			}
			if len(raw) > 100000 || !utf8.Valid(raw) {
				return errors.New("CLI configuration must be UTF-8 text under 100 KB")
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"path": path, "found": true, "content": string(raw)})
		}}
}
