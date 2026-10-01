package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/appetizers-io/llm-review-agent/internal/config"
	"github.com/appetizers-io/llm-review-agent/internal/poll"
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
	fs.StringVar(&f.terminal, "terminal", "", "terminal app: auto, "+joinNames()+", or a command with {cmd}")
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

func joinNames() string {
	names := slices.DeleteFunc(slices.Clone(config.TerminalNames), func(n string) bool { return n == "auto" })
	return strings.Join(names, ", ")
}

// settings are the effective settings: the config file, overridden by flags.
type settings struct {
	Cfg             config.Config
	ConfigSource    string // "": built-in defaults
	Include         []string
	Exclude         []string
	Remote          string
	ProcessExisting bool
	Once            bool
	DryRun          bool
	ResetState      bool
}

func oneOf(flag, v string, allowed ...string) error {
	if !slices.Contains(allowed, v) {
		return fmt.Errorf("--%s: invalid choice %q (choose from %v)", flag, v, allowed)
	}
	return nil
}

func settingsFrom(cmd *cobra.Command, f *flags) (settings, error) {
	source := config.Find(f.config)
	cfg, err := config.Load(source)
	if err != nil {
		return settings{}, err
	}
	changed := cmd.Flags().Changed
	for _, c := range []struct {
		flag    string
		value   string
		allowed []string
		target  func(string)
	}{
		{"agent", f.agent, []string{"codex", "claude"}, func(v string) { cfg.Agent = v }},
		{"launcher", f.launcher, []string{"auto", "terminal", "tmux"}, func(v string) { cfg.Launcher = v }},
		{"github-writes", f.githubWrites, []string{"ask", "never", "allow"}, func(v string) { cfg.GitHubWrites = &v }},
	} {
		if !changed(c.flag) {
			continue
		}
		if err := oneOf(c.flag, c.value, c.allowed...); err != nil {
			return settings{}, err
		}
		c.target(c.value)
	}
	if changed("terminal") {
		cfg.Terminal = config.ParseTerminal(f.terminal)
		if t := cfg.Terminal; t.Command == nil && !slices.Contains(config.TerminalNames, t.Name) {
			return settings{}, fmt.Errorf("--terminal: unknown terminal %q (choose from auto, %s, or a command with {cmd})", t.Name, joinNames())
		}
	}
	if changed("interval") {
		cfg.IntervalSeconds = f.interval
	}
	if changed("lookback-hours") {
		cfg.LookbackHours = f.lookbackHours
	}
	if changed("max-agents") {
		cfg.MaxAgents = f.maxAgents
	}
	if changed("candidate-limit") {
		cfg.CandidateLimit = f.candidateLimit
	}
	if changed("stale-lock-hours") {
		cfg.StaleLockHours = f.staleLockHours
	}
	if jev, ok := cfg.Classifiers["jev"]; ok && jev.Kind == config.KindJev {
		if changed("jev-cmd") {
			jev.Jev.Command = &f.jevCmd
		}
		if f.noJev {
			jev.Jev.Enabled = config.EnabledFalse
		}
		cfg.Classifiers["jev"] = jev
	}
	s := settings{
		Cfg: cfg, ConfigSource: source, Remote: f.remote,
		ProcessExisting: f.processExisting, Once: f.once, DryRun: f.dryRun, ResetState: f.resetState,
	}
	include, exclude := cfg.Repos.Include, cfg.Repos.Exclude
	if changed("repo") {
		include = f.repo
	}
	if changed("exclude-repo") {
		exclude = f.excludeRepo
	}
	for _, p := range include {
		s.Include = append(s.Include, poll.RepoPattern(p))
	}
	for _, p := range exclude {
		s.Exclude = append(s.Exclude, poll.RepoPattern(p))
	}
	return s, nil
}
