package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/appetizers-io/llm-review-agent/internal/config"
	"github.com/appetizers-io/llm-review-agent/internal/watch"
)

// flags of the root command. A flag overrides the config file only when it
// was given (Changed); unset flags take the file's values.
type flags struct {
	config, agent, remote, launcher, githubWrites, terminal, jevCmd string
	repo, excludeRepo                                               []string
	interval, lookbackHours, maxAgents, candidateLimit              int
	staleLockHours                                                  float64
	processExisting, once, dryRun, noJev, resetState                bool
	logLevel, logFormat                                             string
}

func (f *flags) register(cmd *cobra.Command) {
	fs := cmd.Flags()
	fs.StringVar(&f.config, "config", "", "YAML config (default: $"+config.EnvVar+", else "+config.DefaultPath()+" when present)")
	fs.StringArrayVar(&f.repo, "repo", nil, "owner/repo glob or URL (repeatable)")
	fs.StringArrayVar(&f.excludeRepo, "exclude-repo", nil, "owner/repo glob or URL (repeatable)")
	fs.StringVar(&f.agent, "agent", "", "codex or claude")
	fs.StringVar(&f.remote, "remote", "", "remote of the local checkout to watch (default: origin; e.g. upstream for a fork)")
	fs.StringVar(&f.launcher, "launcher", "", "auto, terminal or tmux (auto: the terminal on a desktop, else tmux)")
	fs.StringVar(&f.terminal, "terminal", "", "terminal app: "+strings.Join(config.TerminalNames, ", ")+", or a command with {cmd}")
	fs.StringVar(&f.githubWrites, "github-writes", "", "comments, reviews and reactions the agent posts on its PR with gh: "+
		"ask, never or allow (ask: a dialog asks you first; default: ask in supervised mode)")
	fs.IntVar(&f.interval, "interval", 0, "seconds between polls")
	fs.IntVar(&f.lookbackHours, "lookback-hours", 0, "notification window in hours")
	fs.IntVar(&f.maxAgents, "max-agents", 0, "agent sessions allowed to run at once")
	fs.IntVar(&f.candidateLimit, "candidate-limit", 0, "non-owned PRs checked for the opt-in reaction per poll")
	fs.Float64Var(&f.staleLockHours, "stale-lock-hours", 0, "a session lock older than this is dead")
	fs.BoolVar(&f.processExisting, "process-existing", false, "on the first run, launch for existing notifications too")
	fs.BoolVar(&f.once, "once", false, "poll once and exit")
	fs.BoolVar(&f.dryRun, "dry-run", false, "log what would launch; change nothing")
	fs.StringVar(&f.jevCmd, "jev-cmd", "", "jev-use command for the pre-launch check")
	fs.BoolVar(&f.noJev, "no-jev", false, "launch on every trigger without asking Jev first")
	fs.BoolVar(&f.resetState, "reset-state", false, "forget what was handled before")
	fs.StringVar(&f.logLevel, "log-level", "info", "debug, info, warn or error")
	fs.StringVar(&f.logFormat, "log-format", "text", "text or json")
}

// settings are the config file with the given flags on top, validated
// against the config schema like the file itself.
func (f *flags) settings(cmd *cobra.Command) (watch.Settings, error) {
	source := config.Find(f.config)
	cfg, err := config.Load(source)
	if err != nil {
		return watch.Settings{}, err
	}
	changed := cmd.Flags().Changed
	set := func(name string, apply func()) {
		if changed(name) {
			apply()
		}
	}
	set("agent", func() { cfg.Agent = f.agent })
	set("launcher", func() { cfg.Launcher = f.launcher })
	set("github-writes", func() { cfg.GitHubWrites = &f.githubWrites })
	set("terminal", func() { cfg.Terminal = config.ParseTerminal(f.terminal) })
	set("interval", func() { cfg.IntervalSeconds = f.interval })
	set("lookback-hours", func() { cfg.LookbackHours = f.lookbackHours })
	set("max-agents", func() { cfg.MaxAgents = f.maxAgents })
	set("candidate-limit", func() { cfg.CandidateLimit = f.candidateLimit })
	set("stale-lock-hours", func() { cfg.StaleLockHours = f.staleLockHours })
	set("repo", func() { cfg.Repos.Include = f.repo })
	set("exclude-repo", func() { cfg.Repos.Exclude = f.excludeRepo })
	if jev, ok := cfg.Classifiers["jev"]; ok && jev.Kind == config.KindJev {
		set("jev-cmd", func() { jev.Jev.Command = &f.jevCmd })
		if f.noJev {
			jev.Jev.Enabled = config.EnabledFalse
		}
		cfg.Classifiers["jev"] = jev
	}
	if cfg, err = config.Validate(cfg, "flags and config"); err != nil {
		return watch.Settings{}, err
	}
	s := watch.Settings{
		Cfg: cfg, ConfigSource: source, Remote: f.remote,
		ProcessExisting: f.processExisting, Once: f.once, DryRun: f.dryRun, ResetState: f.resetState,
	}
	for _, p := range cfg.Repos.Include {
		s.Include = append(s.Include, config.RepoPattern(p))
	}
	for _, p := range cfg.Repos.Exclude {
		s.Exclude = append(s.Exclude, config.RepoPattern(p))
	}
	return s, nil
}
