package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/appetizers-io/outrider/internal/config"
)

func configCmd(stdout, stderr io.Writer, migrateDirs func()) *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Inspect the configuration", Args: cobra.NoArgs}
	cmd.AddCommand(&cobra.Command{
		Use:   "schema",
		Short: "Print the JSON Schema of the config file",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			_, err := fmt.Fprint(stdout, config.SchemaText())
			return err
		},
	})

	var output string
	var write, force bool
	gen := &cobra.Command{
		Use:   "generate",
		Short: "A config with every default and option, documented",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			text := config.Example
			target := output
			if write {
				migrateDirs()
				target = config.DefaultPath()
			}
			if target == "" {
				_, err := fmt.Fprint(stdout, text)
				return err
			}
			if _, err := os.Stat(target); err == nil && !force {
				_, _ = fmt.Fprintf(stderr, "%s exists; use --force to overwrite\n", target)
				return exitCode(1)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return fmt.Errorf("config dir: %w", err)
			}
			if err := os.WriteFile(target, []byte(text), 0o600); err != nil {
				return fmt.Errorf("write config: %w", err)
			}
			schema := filepath.Join(filepath.Dir(target), "config.schema.json")
			if err := os.WriteFile(schema, []byte(config.SchemaText()), 0o600); err != nil {
				return fmt.Errorf("write schema: %w", err)
			}
			_, err := fmt.Fprintf(stdout, "wrote %s (and config.schema.json next to it)\n", target)
			return err
		},
	}
	gen.Flags().StringVarP(&output, "output", "o", "", "write to this file (default: stdout)")
	gen.Flags().BoolVar(&write, "write", false, "write to "+config.DefaultPath())
	gen.Flags().BoolVar(&force, "force", false, "overwrite an existing file")
	gen.MarkFlagsMutuallyExclusive("output", "write")
	cmd.AddCommand(gen)

	load := func(args []string) (config.Config, string, error) {
		migrateDirs()
		path := ""
		if len(args) > 0 {
			path = args[0]
		}
		path = config.Find(path)
		cfg, err := config.Load(path)
		var cerr *config.Error
		if errors.As(err, &cerr) {
			_, _ = fmt.Fprintln(stderr, err)
			return cfg, path, exitCode(1)
		}
		return cfg, path, err
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "check [PATH]",
		Short: "Validate a config file (default: the config in use)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			_, path, err := load(args)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(stdout, "ok: "+config.Describe(path))
			return err
		},
	})
	var repo, prs string
	show := &cobra.Command{
		Use:   "show [PATH]",
		Short: "Print the effective config, defaults filled in",
		Long:  "Print the effective config, defaults filled in. With --repo: the config of a PR there, its overrides applied.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if prs != "own" && prs != "others" {
				return fmt.Errorf("--prs: want own or others, got %q", prs)
			}
			cfg, _, err := load(args)
			if err != nil {
				return err
			}
			var b bytes.Buffer
			if repo != "" {
				repo = config.RepoPattern(repo)
				applied := cfg.Applied(repo, prs == "own")
				if len(applied) == 0 {
					applied = []string{"none"}
				}
				whose := map[string]string{"own": "your own", "others": "others'"}[prs]
				fmt.Fprintf(&b, "# %s, %s PRs; overrides applied: %s\n", repo, whose, strings.Join(applied, ", "))
				cfg = cfg.For(repo, prs == "own")
			}
			enc := yaml.NewEncoder(&b)
			enc.SetIndent(2)
			if err := enc.Encode(cfg); err != nil {
				return fmt.Errorf("encode config: %w", err)
			}
			_, err = stdout.Write(b.Bytes())
			return err
		},
	}
	show.Flags().StringVar(&repo, "repo", "", "owner/repo or URL: show the config of a PR there")
	show.Flags().StringVar(&prs, "prs", "own", "with --repo: own or others, whose PR")
	cmd.AddCommand(show)
	return cmd
}
