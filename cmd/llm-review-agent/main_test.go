package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"

	"github.com/appetizers-io/llm-review-agent/internal/config"
	"github.com/appetizers-io/llm-review-agent/internal/proc"
	"github.com/appetizers-io/llm-review-agent/internal/session"
)

func sessionTerminal(name string) session.Terminal { return session.Terminal{Name: name} }

// isolate keeps the developer's real config and Jev key away.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg"))
	t.Setenv(config.EnvVar, "")
	for _, k := range config.JevBackendEnv {
		t.Setenv(k, "")
	}
	return dir
}

type out struct {
	code           int
	stdout, stderr string
}

func cli(t *testing.T, d deps, args ...string) out {
	t.Helper()
	var stdout, stderr strings.Builder
	root := newRoot(d, &stdout, &stderr)
	root.SetArgs(args)
	code := 0
	if err := root.ExecuteContext(t.Context()); err != nil {
		var ec exitCode
		if errors.As(err, &ec) {
			code = int(ec)
		} else {
			code = 1
			stderr.WriteString(err.Error())
		}
	}
	return out{code, stdout.String(), stderr.String()}
}

// --- config subcommands ----------------------------------------------------

func TestConfigSchema(t *testing.T) {
	isolate(t)
	o := cli(t, host(), "config", "schema")
	require.Equal(t, 0, o.code)
	var s map[string]any
	require.NoError(t, json.Unmarshal([]byte(o.stdout), &s))
	require.Equal(t, "llm-review-agent configuration", s["title"])
}

func TestConfigCheck(t *testing.T) {
	r := require.New(t)
	dir := isolate(t)
	good, bad := filepath.Join(dir, "good.yaml"), filepath.Join(dir, "bad.yaml")
	r.NoError(os.WriteFile(good, []byte("agent: claude\n"), 0o600))
	r.NoError(os.WriteFile(bad, []byte("agent: gpt\n"), 0o600))
	o := cli(t, host(), "config", "check", good)
	r.Equal(0, o.code)
	r.Contains(o.stdout, "ok: "+good)
	o = cli(t, host(), "config", "check", bad)
	r.Equal(1, o.code)
	r.Contains(o.stderr, "agent: Input should be")
	o = cli(t, host(), "config", "check")
	r.Equal(0, o.code)
	r.Contains(o.stdout, "built-in defaults")
	o = cli(t, host(), "config", "check", filepath.Join(dir, "missing.yaml"))
	r.Equal(1, o.code)
	r.Contains(o.stderr, "cannot read config")
}

func TestConfigShowFillsDefaults(t *testing.T) {
	dir := isolate(t)
	p := filepath.Join(dir, "c.yaml")
	require.NoError(t, os.WriteFile(p, []byte("max_agents: 3\n"), 0o600))
	o := cli(t, host(), "config", "show", p)
	require.Equal(t, 0, o.code)
	var shown map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(o.stdout), &shown))
	require.Equal(t, 3, shown["max_agents"])
	require.Equal(t, true, shown["triggers"].(map[string]any)["opt_in"].(map[string]any)["on_change"].(map[string]any)["ignore_own_activity"])
	_, err := config.Parse([]byte(o.stdout), "shown") // show's output is a valid config
	require.NoError(t, err)
}

func TestConfigGenerate(t *testing.T) {
	r := require.New(t)
	dir := isolate(t)
	o := cli(t, host(), "config", "generate")
	r.Equal(0, o.code)
	var data map[string]any
	r.NoError(yaml.Unmarshal([]byte(o.stdout), &data))
	r.Equal("supervised", data["mode"])

	target := filepath.Join(dir, "c.yaml")
	r.Equal(0, cli(t, host(), "config", "generate", "-o", target).code)
	r.FileExists(filepath.Join(dir, "config.schema.json"))
	o = cli(t, host(), "config", "generate", "-o", target)
	r.Equal(1, o.code) // never overwrite silently
	r.Contains(o.stderr, "use --force")
	r.Equal(0, cli(t, host(), "config", "generate", "-o", target, "--force").code)

	r.Equal(0, cli(t, host(), "config", "generate", "--write").code)
	r.Equal(config.DefaultPath(), config.Find(""))
	r.Equal(0, cli(t, host(), "config", "check").code)
	r.NotEqual(0, cli(t, host(), "config", "generate", "--write", "-o", target).code)
}

// --- flags over the config file ---------------------------------------------

func effective(t *testing.T, args ...string) (settings, error) {
	t.Helper()
	f := &flags{}
	cmd := &cobra.Command{}
	f.register(cmd)
	require.NoError(t, cmd.ParseFlags(args))
	return settingsFrom(cmd, f)
}

func TestFlagsOverrideTheConfigFile(t *testing.T) {
	r := require.New(t)
	dir := isolate(t)
	p := filepath.Join(dir, "c.yaml")
	r.NoError(os.WriteFile(p, []byte("owner_name: Matze\nmax_agents: 4\nagent: claude\ngithub_writes: never\nrepos: {include: [o/*]}\n"), 0o600))
	s, err := effective(t, "--config", p, "--agent", "codex")
	r.NoError(err)
	r.Equal(4, s.Cfg.MaxAgents) // from the file
	r.Equal("codex", s.Cfg.Agent)
	r.Equal("Matze", *s.Cfg.OwnerName)
	r.Equal("never", s.Cfg.GitHubWritesMode())
	r.Equal([]string{"o/*"}, s.Include)
	r.Equal(p, s.ConfigSource)

	s, err = effective(t, "--config", p, "--github-writes", "allow", "--max-agents", "2", "--repo", "https://github.com/x/y.git", "--repo", "z/*",
		"--interval", "30", "--lookback-hours", "5", "--candidate-limit", "7", "--stale-lock-hours", "1.5", "--launcher", "tmux",
		"--terminal", "iterm", "--exclude-repo", "x/skip")
	r.NoError(err)
	r.Equal("allow", s.Cfg.GitHubWritesMode())
	r.Equal(2, s.Cfg.MaxAgents)
	r.Equal([]string{"x/y", "z/*"}, s.Include)
	r.Equal([]string{"x/skip"}, s.Exclude)
	r.Equal(30, s.Cfg.IntervalSeconds)
	r.Equal(5, s.Cfg.LookbackHours)
	r.Equal(7, s.Cfg.CandidateLimit)
	r.InDelta(1.5, s.Cfg.StaleLockHours, 0)
	r.Equal("tmux", s.Cfg.Launcher)
	r.Equal("iterm", s.Cfg.Terminal.Name)
}

func TestNoJevAndJevCmdFlags(t *testing.T) {
	isolate(t)
	s, err := effective(t, "--no-jev")
	require.NoError(t, err)
	require.Equal(t, config.EnabledFalse, s.Cfg.Classifiers["jev"].Jev.Enabled)
	s, err = effective(t, "--jev-cmd", "x y")
	require.NoError(t, err)
	require.Equal(t, "x y", *s.Cfg.Classifiers["jev"].Jev.Command)
	require.Equal(t, config.EnabledAuto, s.Cfg.Classifiers["jev"].Jev.Enabled)
}

func TestInvalidFlagsAndConfig(t *testing.T) {
	dir := isolate(t)
	_, err := effective(t, "--agent", "gpt")
	require.ErrorContains(t, err, "--agent: invalid choice")
	_, err = effective(t, "--terminal", "nope")
	require.ErrorContains(t, err, "--terminal: unknown terminal")
	p := filepath.Join(dir, "c.yaml")
	require.NoError(t, os.WriteFile(p, []byte("max_agents: 0\n"), 0o600))
	_, err = effective(t, "--config", p)
	require.ErrorContains(t, err, "max_agents: Input should be greater than or equal to 1")
	require.ErrorContains(t, err, p)
}

// --- the watcher -------------------------------------------------------------

// machine: every tool installed, not inside a git checkout, logged in as me.
func machine(t *testing.T, notifications func() (string, error)) deps {
	t.Helper()
	home := isolate(t)
	run := func(_ context.Context, c proc.Cmd) (proc.Result, error) {
		a := strings.Join(c.Args, " ")
		fail := func(msg string) (proc.Result, error) {
			return proc.Result{Code: 1}, &proc.Error{Args: c.Args, Code: 1, Stderr: msg}
		}
		switch {
		case strings.HasPrefix(a, "git rev-parse"):
			return fail("not a git repository")
		case a == "gh api user":
			return proc.Result{Stdout: `{"login": "me", "name": "Me Person"}`}, nil
		case strings.HasPrefix(a, "gh api notifications"):
			stdout, err := notifications()
			if err != nil {
				return fail(err.Error())
			}
			return proc.Result{Stdout: stdout}, nil
		case strings.Contains(a, "search/issues"):
			return proc.Result{Stdout: `{"items": []}`}, nil
		case strings.Contains(a, "graphql"):
			return proc.Result{Stdout: `{"data": {}}`}, nil
		}
		t.Fatalf("unexpected command %s", a)
		return proc.Result{}, nil
	}
	return deps{
		Run: run, LookPath: noTerminal,
		Getenv: func(string) string { return "" }, GOOS: "linux", Home: home, Self: "/bin/llm-review-agent",
		Sleep: func(context.Context, time.Duration) error { return errors.New("stop") },
	}
}

// noTerminal finds every tool but a terminal app.
func noTerminal(f string) (string, error) {
	if f == "x-terminal-emulator" {
		return "", errors.New("not found")
	}
	return "/bin/" + f, nil
}

func ok() (string, error) { return "[[]]", nil }

func TestMainSurvivesAFailingPoll(t *testing.T) {
	d := machine(t, func() (string, error) { return "", errors.New("connection reset") })
	o := cli(t, d, "--once", "--launcher", "tmux", "--no-jev")
	require.Equal(t, 0, o.code, o.stderr)
	require.Contains(t, o.stderr, "poll failed (1x in a row), will retry")
	require.Contains(t, o.stderr, "connection reset")
	require.FileExists(t, statePath(d.Home)) // state saved despite the failure
}

func TestMainRetriesWithBackoff(t *testing.T) {
	calls := 0
	d := machine(t, func() (string, error) { calls++; return "", errors.New("surprise") })
	var sleeps []time.Duration
	d.Sleep = func(_ context.Context, w time.Duration) error {
		sleeps = append(sleeps, w)
		if len(sleeps) == 3 {
			return errors.New("stop")
		}
		return nil
	}
	o := cli(t, d, "--launcher", "tmux", "--no-jev", "--interval", "60")
	require.Equal(t, 0, o.code, o.stderr)
	require.Equal(t, 3, calls)
	require.Equal(t, []time.Duration{120 * time.Second, 240 * time.Second, 480 * time.Second}, sleeps) // exponential backoff
	require.Contains(t, o.stderr, "poll failed (3x in a row)")
}

func TestStartupReportsConfigOwnerAndClassifiers(t *testing.T) {
	d := machine(t, ok)
	o := cli(t, d, "--once", "--launcher", "tmux")
	require.Equal(t, 0, o.code, o.stderr)
	for _, want := range []string{
		"config: no config file, built-in defaults",
		"prompts call you Me", // first name from the GitHub profile
		"launch check: off (jev: no backend key",
		"tool gate: off (jev: no backend key",
		"launcher: tmux",
		"terminal: none (none found)",
		"summary: own_new=0",
	} {
		require.Contains(t, o.stderr, want)
	}
}

func TestOwnerNameFromTheConfig(t *testing.T) {
	d := machine(t, ok)
	p := filepath.Join(d.Home, "c.yaml")
	require.NoError(t, os.WriteFile(p, []byte("owner_name: Matze\nmode: autonomous\n"), 0o600))
	o := cli(t, d, "--once", "--launcher", "tmux", "--config", p)
	require.Contains(t, o.stderr, "prompts call you Matze")
	require.Contains(t, o.stderr, "tool gate: off (off (autonomous mode))")
}

func TestJSONLogs(t *testing.T) {
	d := machine(t, ok)
	o := cli(t, d, "--once", "--launcher", "tmux", "--log-format", "json", "--dry-run")
	require.Equal(t, 0, o.code, o.stderr)
	sc := bufio.NewScanner(strings.NewReader(o.stderr))
	lines := 0
	for sc.Scan() {
		var line map[string]any
		require.NoError(t, json.Unmarshal(sc.Bytes(), &line), sc.Text())
		require.Contains(t, line, "msg")
		lines++
	}
	require.Greater(t, lines, 5)
	require.Contains(t, o.stderr, "dry-run summary")
	require.NoFileExists(t, statePath(d.Home))
}

func TestBadLogFlags(t *testing.T) {
	d := machine(t, ok)
	require.Contains(t, cli(t, d, "--once", "--log-level", "loud").stderr, "--log-level")
	require.Contains(t, cli(t, d, "--once", "--log-format", "xml").stderr, "--log-format")
}

func TestMissingToolsAndBadSetups(t *testing.T) {
	d := machine(t, ok)
	d.LookPath = func(f string) (string, error) {
		if f == "tmux" {
			return "", errors.New("not found")
		}
		return noTerminal(f)
	}
	require.Contains(t, cli(t, d, "--once", "--launcher", "tmux").stderr, "missing required command: tmux")
	require.Contains(t, cli(t, d, "--once", "--remote", "upstream").stderr, "--remote needs to run inside a git checkout")
	require.Contains(t, cli(t, d, "--once", "--launcher", "terminal").stderr, "no terminal found")
	require.Contains(t, cli(t, d, "--once", "--max-agents", "0", "--launcher", "terminal", "--terminal", "kitty").stderr, "--max-agents must be >= 1")
	d.GOOS = "windows"
	require.Contains(t, cli(t, d, "--once", "--launcher", "tmux").stderr, "tmux is not supported on Windows")
}

func TestLocalCheckoutIsWatched(t *testing.T) {
	d := machine(t, ok)
	inner := d.Run
	d.Run = func(ctx context.Context, c proc.Cmd) (proc.Result, error) {
		switch strings.Join(c.Args, " ") {
		case "git rev-parse --show-toplevel":
			return proc.Result{Stdout: "/src/r\n"}, nil
		case "git remote get-url upstream":
			return proc.Result{Stdout: "git@github.com:o/r.git\n"}, nil
		}
		return inner(ctx, c)
	}
	o := cli(t, d, "--once", "--launcher", "tmux", "--remote", "upstream")
	require.Equal(t, 0, o.code, o.stderr)
	require.Contains(t, o.stderr, "repos: o/r")
	require.Contains(t, o.stderr, "local checkout for o/r: /src/r (remote upstream)")
}

func TestPickLauncher(t *testing.T) {
	d := machine(t, ok)
	d.Run = func(context.Context, proc.Cmd) (proc.Result, error) { return proc.Result{Stdout: "Aqua\n"}, nil }
	d.GOOS = "darwin"
	require.Equal(t, "terminal", pickLauncher(t.Context(), "auto", sessionTerminal("terminal-app"), d))
	d.Run = func(context.Context, proc.Cmd) (proc.Result, error) { return proc.Result{Stdout: "Background\n"}, nil }
	require.Equal(t, "tmux", pickLauncher(t.Context(), "auto", sessionTerminal("terminal-app"), d))
	d.GOOS = "windows"
	require.Equal(t, "terminal", pickLauncher(t.Context(), "auto", sessionTerminal("cmd"), d))
	d.GOOS = "linux"
	require.Equal(t, "tmux", pickLauncher(t.Context(), "auto", sessionTerminal("kitty"), d)) // no display
	d.Getenv = func(k string) string { return map[string]string{"WAYLAND_DISPLAY": "w"}[k] }
	require.Equal(t, "terminal", pickLauncher(t.Context(), "auto", sessionTerminal("kitty"), d))
	require.Equal(t, "tmux", pickLauncher(t.Context(), "auto", sessionTerminal(""), d)) // nothing found
	require.Equal(t, "tmux", pickLauncher(t.Context(), "tmux", sessionTerminal("kitty"), d))
}

func TestGuardDispatchOnArgv0(t *testing.T) {
	t.Setenv("LLM_REVIEW_AGENT_REAL_GH", "")
	t.Setenv("LLM_REVIEW_AGENT_REAL_GIT", "")
	var stdout, stderr strings.Builder
	require.Equal(t, 1, run([]string{"/x/bin/gh", "pr", "merge", "1"}, &stdout, &stderr))
	require.Contains(t, stderr.String(), "llm-review-agent guard: blocked `gh pr merge 1`")
	stderr.Reset()
	require.Equal(t, 1, run([]string{"/x/bin/GIT.EXE", "push"}, &stdout, &stderr))
	require.Contains(t, stderr.String(), "LLM_REVIEW_AGENT_REAL_GIT is not set")
}
