package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"

	"github.com/appetizers-io/outrider/internal/config"
	"github.com/appetizers-io/outrider/internal/proc"
	"github.com/appetizers-io/outrider/internal/watch"
)

// isolate keeps the developer's real config and Jev key away.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg"))
	t.Setenv(config.EnvVar, "")
	t.Setenv(config.LegacyEnvVar, "")
	for _, k := range config.JevBackendEnv {
		t.Setenv(k, "")
	}
	return dir
}

// sandbox is this machine with a temporary home: tests never touch the real one.
func sandbox(t *testing.T) watch.Deps {
	d := watch.Host()
	d.Home = t.TempDir()
	if os.Getenv("XDG_CONFIG_HOME") == "" || !strings.HasPrefix(os.Getenv("XDG_CONFIG_HOME"), os.TempDir()) {
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(d.Home, ".config"))
	}
	return d
}

type out struct {
	code           int
	stdout, stderr string
}

func cli(t *testing.T, d watch.Deps, args ...string) out {
	t.Helper()
	var stdout, stderr strings.Builder
	code := run(t.Context(), args, d, &stdout, &stderr)
	return out{code, stdout.String(), stderr.String()}
}

// --- config subcommands ----------------------------------------------------

func TestConfigSchema(t *testing.T) {
	isolate(t)
	o := cli(t, sandbox(t), "config", "schema")
	require.Equal(t, 0, o.code)
	var s map[string]any
	require.NoError(t, json.Unmarshal([]byte(o.stdout), &s))
	require.Equal(t, "outrider configuration", s["title"])
}

func TestConfigCheck(t *testing.T) {
	r := require.New(t)
	dir := isolate(t)
	good, bad := filepath.Join(dir, "good.yaml"), filepath.Join(dir, "bad.yaml")
	r.NoError(os.WriteFile(good, []byte("agent: claude\n"), 0o600))
	r.NoError(os.WriteFile(bad, []byte("agent: gpt\n"), 0o600))
	o := cli(t, sandbox(t), "config", "check", good)
	r.Equal(0, o.code)
	r.Contains(o.stdout, "ok: "+good)
	o = cli(t, sandbox(t), "config", "check", bad)
	r.Equal(1, o.code)
	r.Contains(o.stderr, "at '/agent': value must be one of")
	o = cli(t, sandbox(t), "config", "check")
	r.Equal(0, o.code)
	r.Contains(o.stdout, "built-in defaults")
	o = cli(t, sandbox(t), "config", "check", filepath.Join(dir, "missing.yaml"))
	r.Equal(1, o.code)
	r.Contains(o.stderr, "cannot read config")
}

func TestConfigShowFillsDefaults(t *testing.T) {
	dir := isolate(t)
	p := filepath.Join(dir, "c.yaml")
	require.NoError(t, os.WriteFile(p, []byte("max_agents: 3\n"), 0o600))
	o := cli(t, sandbox(t), "config", "show", p)
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
	o := cli(t, sandbox(t), "config", "generate")
	r.Equal(0, o.code)
	var data map[string]any
	r.NoError(yaml.Unmarshal([]byte(o.stdout), &data))
	r.Equal("supervised", data["mode"])

	target := filepath.Join(dir, "c.yaml")
	r.Equal(0, cli(t, sandbox(t), "config", "generate", "-o", target).code)
	r.FileExists(filepath.Join(dir, "config.schema.json"))
	o = cli(t, sandbox(t), "config", "generate", "-o", target)
	r.Equal(1, o.code) // never overwrite silently
	r.Contains(o.stderr, "use --force")
	r.Equal(0, cli(t, sandbox(t), "config", "generate", "-o", target, "--force").code)

	r.Equal(0, cli(t, sandbox(t), "config", "generate", "--write").code)
	r.Equal(config.DefaultPath(), config.Find(""))
	r.Equal(0, cli(t, sandbox(t), "config", "check").code)
	r.NotEqual(0, cli(t, sandbox(t), "config", "generate", "--write", "-o", target).code)
}

// --- flags over the config file ---------------------------------------------

func effective(t *testing.T, args ...string) (watch.Settings, error) {
	t.Helper()
	f := &flags{}
	cmd := &cobra.Command{}
	f.register(cmd)
	require.NoError(t, cmd.ParseFlags(args))
	return f.settings(cmd)
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

func TestSandboxFlag(t *testing.T) {
	r := require.New(t)
	isolate(t)
	s, err := effective(t, "--sandbox", "read-only")
	r.NoError(err)
	r.Equal("read-only", s.Cfg.Sandbox)
	r.Equal("never", s.Cfg.PushMode())
	r.Equal("never", s.Cfg.GitHubWritesMode())
	// a conflicting explicit setting is refused at startup
	_, err = effective(t, "--sandbox", "read-only", "--github-writes", "ask")
	r.ErrorContains(err, "github_writes: 'ask' conflicts with sandbox: read-only")
	_, err = effective(t, "--sandbox", "maybe")
	r.ErrorContains(err, "/sandbox")
}

func TestInvalidFlagsAndConfig(t *testing.T) {
	dir := isolate(t)
	// flags are checked against the config schema, like the file
	for flag, at := range map[string]string{"--agent=gpt": "/agent", "--terminal=nope": "/terminal", "--max-agents=0": "/max_agents", "--github-writes=maybe": "/github_writes"} {
		_, err := effective(t, flag)
		require.ErrorContains(t, err, "flags and config is invalid", flag)
		require.ErrorContains(t, err, at, flag)
	}
	// a broken glob stops startup instead of silently excluding nothing
	_, err := effective(t, "--exclude-repo", "secret-org/{internal,private")
	require.ErrorContains(t, err, "repos.exclude: bad glob")
	p := filepath.Join(dir, "c.yaml")
	require.NoError(t, os.WriteFile(p, []byte("max_agents: 0\n"), 0o600))
	_, err = effective(t, "--config", p)
	require.ErrorContains(t, err, "/max_agents")
	require.ErrorContains(t, err, p)
}

// --- the watcher -------------------------------------------------------------

// machine: every tool installed, not inside a git checkout, logged in as me.
func machine(t *testing.T, notifications func() (string, error)) watch.Deps {
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
	return watch.Deps{
		Run: run, LookPath: noTerminal,
		Getenv: func(string) string { return "" }, GOOS: "linux", Home: home, Self: "/bin/outrider",
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
	require.FileExists(t, watch.StatePath(d.Home)) // state saved despite the failure
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
		"sandbox: off",
	} {
		require.Contains(t, o.stderr, want)
	}
}

func TestStartupReportsTheSandbox(t *testing.T) {
	d := machine(t, ok)
	run := d.Run
	d.Run = func(ctx context.Context, c proc.Cmd) (proc.Result, error) {
		if strings.Join(c.Args, " ") == "claude --version" {
			return proc.Result{Stdout: "2.1.286 (Claude Code)\n"}, nil
		}
		return run(ctx, c)
	}
	o := cli(t, d, "--once", "--launcher", "tmux", "--no-jev", "--agent", "claude", "--sandbox", "read-only")
	require.Equal(t, 0, o.code, o.stderr)
	require.Contains(t, o.stderr, "sandbox: read-only (claude: native sandbox + deny rules)")

	// fail closed: without the platform sandbox, sandboxed sessions are refused
	d.GOOS = "windows"
	o = cli(t, d, "--once", "--launcher", "terminal", "--terminal", "cmd", "--no-jev", "--agent", "claude", "--sandbox", "read-only")
	require.Equal(t, 0, o.code, o.stderr)
	require.Contains(t, o.stderr, "sandbox: read-only UNAVAILABLE (outrider supports no claude sandbox on windows); sandboxed sessions are refused")
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
	require.NoFileExists(t, watch.StatePath(d.Home))
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
	require.Contains(t, cli(t, d, "--once", "--max-agents", "0", "--launcher", "terminal", "--terminal", "kitty").stderr, "/max_agents")
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
		case "git remote get-url --push --all -- origin":
			return proc.Result{Stdout: "https://github.com/me/r.git\n"}, nil
		case "gh api repos/me/r":
			return proc.Result{Stdout: `{"full_name": "me/r", "fork": true, "parent": {"full_name": "o/r"}}`}, nil
		}
		return inner(ctx, c)
	}
	o := cli(t, d, "--once", "--launcher", "tmux", "--remote", "upstream")
	require.Equal(t, 0, o.code, o.stderr)
	require.Contains(t, o.stderr, "repos: o/r")
	require.Contains(t, o.stderr, "local checkout for o/r: /src/r (remote upstream)")
	require.Contains(t, o.stderr, "review forks: me/r (auto: origin)")

	p := filepath.Join(d.Home, "c.yaml")
	require.NoError(t, os.WriteFile(p, []byte("others_prs: {review_forks: []}\n"), 0o600))
	o = cli(t, d, "--once", "--launcher", "tmux", "--remote", "upstream", "--config", p)
	require.Equal(t, 0, o.code, o.stderr)
	require.Contains(t, o.stderr, "review forks: off (review_forks: [])")
}

func TestOldDirsAreMovedAndTheirConfigLoaded(t *testing.T) {
	r := require.New(t)
	d := machine(t, ok)
	old := filepath.Join(d.Home, "xdg", "llm-review-agent", "config.yaml") // isolate's XDG_CONFIG_HOME
	r.NoError(os.MkdirAll(filepath.Dir(old), 0o700))
	r.NoError(os.WriteFile(old, []byte("owner_name: Matze\n"), 0o600))
	o := cli(t, d, "--once", "--dry-run", "--launcher", "tmux", "--log-format", "json")
	r.Equal(0, o.code, o.stderr)
	moved := filepath.Join(d.Home, "xdg", "outrider")
	msgs := messages(t, o.stderr)
	r.Contains(msgs, "moved "+filepath.Dir(old)+" to "+moved+" (renamed to outrider)")
	r.Contains(msgs, "config: "+filepath.Join(moved, "config.yaml"))
	r.Contains(o.stderr, "prompts call you Matze")
}

func TestLegacyConfigEnvVarStillWorks(t *testing.T) {
	r := require.New(t)
	d := machine(t, ok)
	p := filepath.Join(d.Home, "c.yaml")
	r.NoError(os.WriteFile(p, []byte("owner_name: Matze\n"), 0o600))
	t.Setenv(config.LegacyEnvVar, p)
	o := cli(t, d, "--once", "--dry-run", "--launcher", "tmux", "--log-format", "json")
	r.Equal(0, o.code, o.stderr)
	msgs := messages(t, o.stderr)
	r.Contains(msgs, "$LLM_REVIEW_AGENT_CONFIG is deprecated, use $OUTRIDER_CONFIG")
	r.Contains(msgs, "config: "+p)
	t.Setenv(config.EnvVar, filepath.Join(d.Home, "missing.yaml")) // the new one wins
	r.Contains(cli(t, d, "--once", "--dry-run").stderr, "missing.yaml")
}

func TestSchemaAndSessionCommandsMoveNothing(t *testing.T) {
	isolate(t)
	d := sandbox(t)
	old := filepath.Join(d.Home, ".cache", "llm-review-agent")
	require.NoError(t, os.MkdirAll(old, 0o700))
	require.Equal(t, 0, cli(t, d, "config", "schema").code)
	require.DirExists(t, old)
	require.NoDirExists(t, filepath.Join(d.Home, ".cache", "outrider"))
}

// messages are the msg fields of JSON log lines.
func messages(t *testing.T, log string) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(log), "\n") {
		var rec struct{ Msg string }
		require.NoError(t, json.Unmarshal([]byte(line), &rec), line)
		out = append(out, rec.Msg)
	}
	return out
}

// --- doctor ------------------------------------------------------------------

// doctorMachine is machine with the commands doctor runs answered.
func doctorMachine(t *testing.T) watch.Deps {
	t.Helper()
	d := machine(t, ok)
	d.LookPath = func(f string) (string, error) {
		if f == "llm-review-agent" {
			return "", errors.New("not found")
		}
		return noTerminal(f)
	}
	inner := d.Run
	d.Run = func(ctx context.Context, c proc.Cmd) (proc.Result, error) {
		switch strings.Join(c.Args, " ") {
		case "gh auth status --active --hostname github.com --json hosts":
			return proc.Result{Stdout: `{"hosts": {"github.com": [{"state": "success", "active": true, "login": "me",
				"tokenSource": "keyring", "scopes": "repo, notifications", "token": "gho_secret"}]}}`}, nil
		case "git --version":
			return proc.Result{Stdout: "git version 2.50.0\n"}, nil
		case "codex --version":
			return proc.Result{Stdout: "codex-cli 0.156.1\n"}, nil
		case "claude --version":
			return proc.Result{Stdout: "2.1.286 (Claude Code)\n"}, nil
		}
		return inner(ctx, c)
	}
	return d
}

func TestDoctor(t *testing.T) {
	r := require.New(t)
	d := doctorMachine(t)
	old := filepath.Join(d.Home, "xdg", "llm-review-agent") // isolate's XDG_CONFIG_HOME
	r.NoError(os.MkdirAll(old, 0o700))
	o := cli(t, d, "doctor")
	r.Equal(0, o.code, o.stdout+o.stderr) // warnings don't fail
	for _, want := range []string{
		"ok    config            no config file, built-in defaults (mode supervised, push ask, github_writes ask, sandbox: off)",
		"ok    github            github.com as me (prompts call you Me), token from keyring, scopes repo, notifications",
		"ok    git               git version 2.50.0",
		"ok    agent             codex codex-cli 0.156.1 (/bin/codex), the configured agent",
		"ok    launcher          tmux (launcher: auto), terminal none (none found)",
		"warn  dialogs           no approval dialog here: ask will deny (push ask, github_writes ask)",
		"warn  launch check      off (jev: no backend key",
		"info  llm-review-agent  " + old + " (moved to " + filepath.Join(d.Home, "xdg", "outrider") + " on the next start)",
		"                        fix: install jev-use (or npx) and export a backend key",
	} {
		r.Contains(o.stdout, want)
	}
	r.NotContains(o.stdout, "gho_secret")
	r.DirExists(old) // doctor never migrates
	r.NoDirExists(filepath.Join(d.Home, "xdg", "outrider"))
	r.NoDirExists(watch.CacheRoot(d.Home))
}

func TestDoctorJSON(t *testing.T) {
	r := require.New(t)
	o := cli(t, doctorMachine(t), "doctor", "--json")
	r.Equal(0, o.code, o.stdout+o.stderr)
	var results []map[string]string
	r.NoError(json.Unmarshal([]byte(o.stdout), &results))
	r.Len(results, 14)
	for _, res := range results {
		r.ElementsMatch([]string{"name", "status", "detail", "fix"}, slices.Collect(maps.Keys(res)))
	}
	r.Equal(map[string]string{"name": "git", "status": "ok", "detail": "git version 2.50.0", "fix": ""}, results[2])
}

func TestDoctorFails(t *testing.T) {
	r := require.New(t)
	d := doctorMachine(t)
	bad := filepath.Join(d.Home, "bad.yaml")
	r.NoError(os.WriteFile(bad, []byte("agent: gpt\n"), 0o600))
	o := cli(t, d, "doctor", "--config", bad)
	r.Equal(1, o.code)
	r.Contains(o.stdout, "fail  config")
	r.Contains(o.stdout, "at '/agent': value must be one of")
	r.Contains(o.stdout, "(the checks below use the built-in defaults)")
	r.Contains(o.stdout, "ok    git") // the other checks still run

	lookPath := d.LookPath
	d.LookPath = func(f string) (string, error) {
		if f == "gh" {
			return "", errors.New("not found")
		}
		return lookPath(f)
	}
	o = cli(t, d, "doctor", "--json", "--remote", "upstream")
	r.Equal(1, o.code)
	r.Contains(o.stdout, `"detail": "gh not found on PATH"`)
	r.Contains(o.stdout, `"detail": "--remote needs to run inside a git checkout"`)
}
