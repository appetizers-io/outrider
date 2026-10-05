// Package watch is the watcher: startup checks (local checkout, launcher,
// required tools, GitHub user), then the poll loop.
package watch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v5"

	"github.com/appetizers-io/outrider/internal/classifier"
	"github.com/appetizers-io/outrider/internal/config"
	"github.com/appetizers-io/outrider/internal/github"
	"github.com/appetizers-io/outrider/internal/poll"
	"github.com/appetizers-io/outrider/internal/proc"
	"github.com/appetizers-io/outrider/internal/session"
)

// CacheRoot holds worktrees, sessions, locks and the guards.
func CacheRoot(home string) string { return filepath.Join(home, ".cache", "outrider") }

// StatePath is where the poll state is kept. Both paths are the same on every
// OS, so state and locks carry over between versions.
func StatePath(home string) string {
	return filepath.Join(home, ".local", "state", "outrider", "state.json")
}

// LocalCheckout is the GitHub repo of the checkout we run in, if any.
func LocalCheckout(ctx context.Context, s *Settings, d Deps) (string, session.Local, error) {
	top, err := d.Run(ctx, proc.Cmd{Args: []string{"git", "rev-parse", "--show-toplevel"}})
	if err != nil {
		if s.Remote != "" {
			return "", session.Local{}, errors.New("--remote needs to run inside a git checkout")
		}
		return "", session.Local{}, nil
	}
	remote := s.Remote
	if remote == "" {
		remote = "origin"
	}
	checkout := strings.TrimSpace(top.Stdout)
	url, _ := d.Run(ctx, proc.Cmd{Args: []string{"git", "remote", "get-url", remote}, Dir: checkout})
	repo := config.RepoPattern(strings.TrimSpace(url.Stdout))
	if !poll.RepoName.MatchString(repo) {
		if s.Remote != "" {
			return "", session.Local{}, fmt.Errorf("remote '%s' is not a GitHub repo here", s.Remote)
		}
		return "", session.Local{}, nil
	}
	return repo, session.Local{Path: checkout, Remote: remote}, nil
}

// Desktop tells whether this is a desktop session that can show windows: an
// Aqua session on macOS, a display on Linux, always on Windows.
func Desktop(ctx context.Context, d Deps) bool {
	switch d.GOOS {
	case "darwin":
		res, _ := d.Run(ctx, proc.Cmd{Args: []string{"launchctl", "managername"}})
		return strings.TrimSpace(res.Stdout) == "Aqua"
	case "windows":
		return true
	}
	return d.Getenv("DISPLAY") != "" || d.Getenv("WAYLAND_DISPLAY") != ""
}

// pickLauncher resolves launcher: auto.
func pickLauncher(ctx context.Context, launcher string, term session.Terminal, d Deps) string {
	if launcher != "auto" {
		return launcher
	}
	switch d.GOOS {
	case "darwin", "windows":
		if Desktop(ctx, d) {
			return "terminal"
		}
	case "linux":
		if term.Found() && Desktop(ctx, d) {
			return "terminal"
		}
	}
	return "tmux"
}

// ResolveLauncher is the launcher and terminal sessions open in.
func ResolveLauncher(ctx context.Context, cfg *config.Config, d Deps) (string, session.Terminal) {
	terminal := session.ResolveTerminal(cfg.Terminal, d.GOOS, d.Getenv, d.LookPath)
	return pickLauncher(ctx, cfg.Launcher, terminal, d), terminal
}

// LauncherTools are the commands the launcher needs on PATH, or why it
// can't work here.
func LauncherTools(launcher string, terminal session.Terminal, d Deps) ([]string, error) {
	if launcher == "tmux" {
		if d.GOOS == "windows" {
			return nil, errors.New("tmux is not supported on Windows; use --launcher terminal")
		}
		return []string{"tmux"}, nil
	}
	open := terminal.OpenCommand(d.GOOS, "", "", []string{d.Self})
	if open == nil {
		return nil, errors.New("no terminal found: set terminal in the config, or use --launcher tmux")
	}
	return open[:1], nil
}

// Classifiers are the launch check and the tool gate, each nil with a note
// when off.
func Classifiers(cfg *config.Config, lookPath classifier.LookPath) (launchCheck *classifier.Resolved, checkNote string, toolGate *classifier.Resolved, gateNote string) {
	launchCheck, checkNote = classifier.ResolveRole(cfg, cfg.LaunchCheck.Classifier, lookPath)
	toolGate, gateNote = classifier.ResolveRole(cfg, cfg.ToolGate.Classifier, lookPath)
	if cfg.Mode == "autonomous" {
		toolGate, gateNote = nil, "off (autonomous mode)"
	}
	return launchCheck, checkNote, toolGate, gateNote
}

// Owner is how prompts call the user: owner_name, else the first name of
// the GitHub profile, else the login.
func Owner(cfg *config.Config, user github.User) string {
	if cfg.OwnerName != nil {
		return *cfg.OwnerName
	}
	if fields := strings.Fields(user.Name); len(fields) > 0 {
		return fields[0]
	}
	return user.Login
}

// CodexHome is the user's CODEX_HOME.
func CodexHome(d Deps) string {
	if h := d.Getenv("CODEX_HOME"); h != "" {
		return h
	}
	return filepath.Join(d.Home, ".codex")
}

// SandboxMode describes the sandbox of own and others' PRs; on tells
// whether any session is sandboxed, overrides included.
func SandboxMode(cfg *config.Config) (mode string, on bool) {
	own, others := cfg.SandboxFor(true), cfg.SandboxFor(false)
	mode = "sandbox: read-only"
	switch {
	case own != config.ReadOnly && others != config.ReadOnly:
		mode = "sandbox: off"
	case own != config.ReadOnly:
		mode = "sandbox: off; others' PRs: read-only"
	case others != config.ReadOnly:
		mode = "sandbox: read-only; others' PRs: off"
	}
	for _, layer := range cfg.Layers() {
		if layer.Isolation.Enabled {
			mode += "; Docker Sandboxes isolation enabled"
			break
		}
	}
	on = len(SandboxAgents(cfg)) > 0
	if on && mode == "sandbox: off" {
		mode += "; overrides: read-only"
	}
	return mode, on
}

// Agents are the agents sessions can run, overrides included.
func Agents(cfg *config.Config) []string {
	var agents []string
	for _, c := range cfg.Layers() {
		if !slices.Contains(agents, c.Agent) {
			agents = append(agents, c.Agent)
		}
	}
	return agents
}

// AgentTools are executables needed on the host; isolated agents live in sbx.
func AgentTools(cfg *config.Config) []string {
	var out []string
	for _, layer := range cfg.Layers() {
		name := layer.Agent
		if layer.Isolation.Enabled {
			name = "sbx"
		}
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}

// SandboxAgents are the agents of sandboxed sessions, overrides included.
func SandboxAgents(cfg *config.Config) []string {
	var agents []string
	for _, c := range cfg.Layers() {
		sandboxed := c.SandboxFor(true) == config.ReadOnly || c.SandboxFor(false) == config.ReadOnly
		if sandboxed && !slices.Contains(agents, c.Agent) {
			agents = append(agents, c.Agent)
		}
	}
	return agents
}

// SandboxSupport is why read-only sessions can't run here, by agent; an
// agent whose sandbox works is missing.
func SandboxSupport(ctx context.Context, cfg *config.Config, codexHome string, d Deps) map[string]error {
	errs := map[string]error{}
	for _, agent := range SandboxAgents(cfg) {
		if err := session.SandboxSupport(ctx, agent, d.GOOS, codexHome, d.LookPath, d.Run); err != nil {
			errs[agent] = err
		}
	}
	return errs
}

// githubUser is the gh user, retried until GitHub answers (unless once).
func githubUser(ctx context.Context, gh *github.Client, once bool, d Deps, log *slog.Logger) (github.User, error) {
	for {
		user, err := gh.Me(ctx)
		if err == nil && user.Login != "" {
			return user, nil
		}
		if err == nil {
			err = errors.New("empty login")
		}
		if once {
			return github.User{}, fmt.Errorf("cannot determine GitHub user: %w", err)
		}
		log.Warn(fmt.Sprintf("cannot determine GitHub user, retrying in 30s: %v", err))
		if err := d.Sleep(ctx, 30*time.Second); err != nil {
			return github.User{}, err
		}
	}
}

func onOff(r *classifier.Resolved) string {
	if r == nil {
		return "off"
	}
	return "on"
}

// logSandbox logs the sandbox mode and returns why read-only sessions can't
// run here, by agent; those agents' sandboxed sessions are then refused.
func logSandbox(ctx context.Context, cfg *config.Config, codexHome string, d Deps, log *slog.Logger) map[string]error {
	mode, on := SandboxMode(cfg)
	if !on {
		log.Info(mode)
		return nil
	}
	errs := SandboxSupport(ctx, cfg, codexHome, d)
	for _, agent := range SandboxAgents(cfg) {
		if err := errs[agent]; err != nil {
			log.Error(fmt.Sprintf("%s UNAVAILABLE for %s (%v); its sandboxed sessions are refused", mode, agent, err))
		}
	}
	if len(errs) == 0 {
		log.Info(fmt.Sprintf("%s (%s)", mode, session.SandboxNote(cfg.Agent)))
	}
	return errs
}

// Settings are the effective settings: the config file, overridden by flags.
type Settings struct {
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

// Deps are what the watcher needs from the machine; tests swap them.
type Deps struct {
	Run      proc.Runner
	LookPath func(string) (string, error)
	Getenv   func(string) string
	GOOS     string
	Home     string
	Self     string // this binary: the guards and the session runner
	Sleep    func(ctx context.Context, d time.Duration) error
}

// Host is the machine this process runs on.
func Host() Deps {
	home, _ := os.UserHomeDir()
	self, err := os.Executable()
	if err == nil {
		if resolved, err := filepath.EvalSymlinks(self); err == nil {
			self = resolved
		}
	}
	return Deps{
		Run: proc.Exec, LookPath: exec.LookPath, Getenv: os.Getenv, GOOS: runtime.GOOS,
		Home: home, Self: self, Sleep: sleep,
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("sleep: %w", ctx.Err())
	case <-t.C:
		return nil
	}
}

// Run polls forever (or once with Settings.Once) and launches sessions.
func Run(ctx context.Context, s Settings, d Deps, log *slog.Logger) error {
	cfg := &s.Cfg
	local := map[string]session.Local{}
	repo, loc, err := LocalCheckout(ctx, &s, d)
	if err != nil {
		return err
	}
	if repo != "" {
		// inside a local checkout: watch its GitHub repo and branch worktrees off it
		local[repo] = loc
		if len(s.Include) == 0 {
			s.Include = []string{repo}
		}
	}
	// globs are compiled once; config and flags were checked when loaded
	include, err := config.RepoGlobs(s.Include)
	if err != nil {
		return fmt.Errorf("repos: %w", err)
	}
	exclude, err := config.RepoGlobs(s.Exclude)
	if err != nil {
		return fmt.Errorf("excluded repos: %w", err)
	}
	launchCheck, checkNote, toolGate, gateNote := Classifiers(cfg, d.LookPath)
	launcher, terminal := ResolveLauncher(ctx, cfg, d)
	launcherTools, err := LauncherTools(launcher, terminal, d)
	if err != nil {
		return err
	}
	for _, layer := range cfg.Layers() {
		if layer.Isolation.Enabled {
			if err := session.IsolationSupport(ctx, &layer, d.Self, d.LookPath, d.Run); err != nil {
				return err
			}
		}
	}
	for _, tool := range append(append([]string{"gh", "git"}, AgentTools(cfg)...), launcherTools...) {
		if _, err := d.LookPath(tool); err != nil {
			return fmt.Errorf("missing required command: %s", tool)
		}
	}
	state := StatePath(d.Home)
	if s.ResetState {
		if err := os.Remove(state); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("reset state: %w", err)
		}
	}
	root := CacheRoot(d.Home)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("cache dir: %w", err)
	}
	st, err := poll.LoadState(state)
	if err != nil {
		return err
	}
	gh := &github.Client{Run: d.Run}
	user, err := githubUser(ctx, gh, s.Once, d, log)
	if err != nil {
		return err
	}
	owner := Owner(cfg, user)
	// the effective review forks from here on
	forks := ResolveReviewForks(ctx, cfg, repo, loc, user.Login, d)
	cfg.OthersPRs.ReviewForks = &forks.Forks

	log.Info("config: " + config.Describe(s.ConfigSource))
	log.Info(fmt.Sprintf("GitHub user: %s (prompts call you %s)", user.Login, owner))
	log.Info(fmt.Sprintf("agent: %s (interactive)", cfg.Agent))
	for _, name := range cfg.Names() {
		log.Info("override: " + name)
	}
	log.Info("launcher: " + launcher)
	log.Info("terminal: " + terminal.String())
	log.Info(fmt.Sprintf("max active agents: %d", cfg.MaxAgents))
	log.Info(fmt.Sprintf("launch check: %s (%s)", onOff(launchCheck), checkNote))
	log.Info(fmt.Sprintf("tool gate: %s (%s)", onOff(toolGate), gateNote))
	codexHome := CodexHome(d)
	sandboxErrs := logSandbox(ctx, cfg, codexHome, d, log)
	if forks.Failed {
		log.Warn("review forks: " + forks.String())
	} else {
		log.Info("review forks: " + forks.String())
	}
	log.Info("GitHub notifications: READ ONLY")
	log.Info("review output: LOCAL SESSION ONLY")
	if len(s.Include) > 0 {
		log.Info("repos: " + strings.Join(s.Include, ", "))
	}
	for repo, l := range local {
		log.Info(fmt.Sprintf("local checkout for %s: %s (remote %s)", repo, l.Path, l.Remote))
	}

	launch := &session.Launcher{
		Root: root, Self: d.Self, Cfg: cfg, ConfigSource: s.ConfigSource, Login: user.Login, Owner: owner,
		Launcher: launcher, Terminal: terminal, LaunchCheck: launchCheck, ToolGate: toolGate,
		Local: local, DryRun: s.DryRun, SandboxErrs: sandboxErrs, CodexHome: codexHome, GOOS: d.GOOS, Run: d.Run, LookPath: d.LookPath, Log: log,
	}
	p := &poll.Poller{
		GH: gh, Cfg: cfg, Login: user.Login, Include: include, Exclude: exclude,
		ProcessExisting: s.ProcessExisting, DryRun: s.DryRun, StatePath: state,
		Launch: launch.Launch, Log: log, Now: time.Now,
	}
	// back off on repeated failures (network down etc.): 2x, 4x, 8x, 16x the
	// interval, at most 15 minutes
	interval := time.Duration(max(10, cfg.IntervalSeconds)) * time.Second
	retry := &backoff.ExponentialBackOff{
		InitialInterval: 2 * interval, Multiplier: 2, MaxInterval: min(16*interval, 15*time.Minute),
	}
	failures := 0
	for {
		err := p.Poll(ctx, st)
		switch {
		case ctx.Err() != nil:
			return nil //nolint:nilerr // interrupted: a normal stop
		case err != nil:
			failures++
			log.Warn(fmt.Sprintf("poll failed (%dx in a row), will retry: %v", failures, err))
			if !s.DryRun {
				if err := st.Save(state); err != nil { // keep launches recorded so far
					log.Warn(fmt.Sprintf("could not save state: %v", err))
				}
			}
		default:
			failures = 0
			retry.Reset()
		}
		if s.Once {
			return nil
		}
		wait := interval
		if failures > 0 {
			wait = retry.NextBackOff()
		}
		if err := d.Sleep(ctx, min(wait, 15*time.Minute)); err != nil {
			return nil //nolint:nilerr // interrupted: a normal stop
		}
	}
}
