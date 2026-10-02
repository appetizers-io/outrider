package cli

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/appetizers-io/outrider/internal/approve"
	"github.com/appetizers-io/outrider/internal/config"
	"github.com/appetizers-io/outrider/internal/doctor"
	"github.com/appetizers-io/outrider/internal/watch"
)

// doctorCmd checks the setup. It reads only: no migration of the old dirs,
// no writes, no GitHub writes, no session.
func doctorCmd(d watch.Deps, stdout io.Writer) *cobra.Command {
	f := &flags{}
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check the local setup and say what to fix",
		Long: "Checks the config, gh and its login, git, the agent, the launcher and terminal, " +
			"the approval dialogs, the classifiers, the sandbox and outrider's files. " +
			"Prints ok, info, warn or fail per check, with the fix for anything not ok. " +
			"Exits 1 when a check failed. Reads only; changes nothing.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			in := doctor.Input{
				Deps:    d,
				Dialogs: approve.Platform{GOOS: d.GOOS, Getenv: d.Getenv, Exists: approve.Host().Exists},
			}
			in.Settings, in.ConfigErr = f.settings(cmd)
			if in.ConfigErr != nil {
				in.Settings = watch.Settings{Cfg: config.Default(), Remote: f.remote}
			}
			results := doctor.Run(cmd.Context(), in)
			write := doctor.WriteText
			if asJSON {
				write = doctor.WriteJSON
			}
			if err := write(stdout, results); err != nil {
				return err
			}
			if doctor.Failed(results) {
				return exitCode(1)
			}
			return nil
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&f.config, "config", "", "YAML config (default: $"+config.EnvVar+", else "+config.DefaultPath()+" when present)")
	fs.StringVar(&f.remote, "remote", "", "remote of the local checkout (default: origin)")
	fs.BoolVar(&asJSON, "json", false, "print a JSON list of {name, status, detail, fix}")
	return cmd
}
