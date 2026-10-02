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

// localCheckout is the GitHub repo of the checkout we run in, if any.
func localCheckout(ctx context.Context, s *Settings, d Deps) (string, session.Local, error) {
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

// pickLauncher resolves launcher: auto.
func pickLauncher(ctx context.Context, launcher string, term session.Terminal, d Deps) string {
	if launcher != "auto" {
		return launcher
	}
	switch d.GOOS {
	case "darwin":
		res, _ := d.Run(ctx, proc.Cmd{Args: []string{"launchctl", "managername"}})
		if strings.TrimSpace(res.Stdout) == "Aqua" {
			return "terminal"
		}
	case "windows":
		return "terminal"
	case "linux":
		if (d.Getenv("DISPLAY") != "" || d.Getenv("WAYLAND_DISPLAY") != "") && term.Found() {
			return "terminal"
		}
	}
	return "tmux"
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
	repo, loc, err := localCheckout(ctx, &s, d)
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
	ignore, err := config.LoginGlobs(cfg.IgnoreAuthors)
	if err != nil {
		return fmt.Errorf("ignore_authors: %w", err)
	}

	launchCheck, checkNote := classifier.ResolveRole(cfg, cfg.LaunchCheck.Classifier, d.LookPath)
	toolGate, gateNote := classifier.ResolveRole(cfg, cfg.ToolGate.Classifier, d.LookPath)
	if cfg.Mode == "autonomous" {
		toolGate, gateNote = nil, "off (autonomous mode)"
	}
	terminal := session.ResolveTerminal(cfg.Terminal, d.GOOS, d.Getenv, d.LookPath)
	launcher := pickLauncher(ctx, cfg.Launcher, terminal, d)
	if launcher == "tmux" && d.GOOS == "windows" {
		return errors.New("tmux is not supported on Windows; use --launcher terminal")
	}
	tools := []string{"gh", "git", cfg.Agent}
	if launcher == "tmux" {
		tools = append(tools, "tmux")
	} else {
		open := terminal.OpenCommand(d.GOOS, "", "", []string{d.Self})
		if open == nil {
			return errors.New("no terminal found: set terminal in the config, or use --launcher tmux")
		}
		tools = append(tools, open[0])
	}
	for _, tool := range tools {
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
	owner := user.Login
	if fields := strings.Fields(user.Name); len(fields) > 0 {
		owner = fields[0]
	}
	if cfg.OwnerName != nil {
		owner = *cfg.OwnerName
	}

	log.Info("config: " + config.Describe(s.ConfigSource))
	log.Info(fmt.Sprintf("GitHub user: %s (prompts call you %s)", user.Login, owner))
	log.Info(fmt.Sprintf("agent: %s (interactive)", cfg.Agent))
	log.Info("launcher: " + launcher)
	log.Info("terminal: " + terminal.String())
	log.Info(fmt.Sprintf("max active agents: %d", cfg.MaxAgents))
	log.Info(fmt.Sprintf("launch check: %s (%s)", onOff(launchCheck), checkNote))
	log.Info(fmt.Sprintf("tool gate: %s (%s)", onOff(toolGate), gateNote))
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
		Agent: cfg.Agent, Launcher: launcher, Terminal: terminal, LaunchCheck: launchCheck, ToolGate: toolGate,
		Local: local, DryRun: s.DryRun, GOOS: d.GOOS, Run: d.Run, LookPath: d.LookPath, Log: log,
	}
	p := &poll.Poller{
		GH: gh, Cfg: cfg, Login: user.Login, Include: include, Exclude: exclude, IgnoreAuthors: ignore,
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
