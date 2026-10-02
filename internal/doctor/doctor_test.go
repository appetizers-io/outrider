package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/appetizers-io/outrider/internal/approve"
	"github.com/appetizers-io/outrider/internal/config"
	"github.com/appetizers-io/outrider/internal/proc"
	"github.com/appetizers-io/outrider/internal/watch"
)

const loggedIn = `{"hosts": {"github.com": [{"state": "success", "active": true, "host": "github.com", "login": "me",
	"tokenSource": "keyring", "scopes": "gist, read:org, repo"}]}}`

// machine is a fake Linux desktop with every tool installed, logged in to
// GitHub, outside a git checkout. Commands not in outputs fail.
type machine struct {
	tools   []string
	env     map[string]string
	outputs map[string]string
	files   []string // for the dialogs
	goos    string
	cfg     string // YAML
}

func newMachine() *machine {
	return &machine{
		tools: []string{"gh", "git", "codex", "claude", "tmux", "x-terminal-emulator", "jev-use", "bwrap", "socat"},
		env:   map[string]string{"DISPLAY": ":0"},
		outputs: map[string]string{
			"gh auth status --active --hostname github.com --json hosts": loggedIn,
			"gh api user":      `{"login": "me", "name": "Me Person"}`,
			"git --version":    "git version 2.50.0\n",
			"codex --version":  "codex-cli 0.156.1\n",
			"claude --version": "2.1.286 (Claude Code)\n",
		},
		files: []string{"/usr/bin/zenity"},
		goos:  "linux",
	}
}

// input builds the checks' input; home is a fresh temporary home.
func (m *machine) input(t *testing.T) *Input {
	t.Helper()
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv(config.LegacyEnvVar, "")
	for _, k := range config.JevBackendEnv {
		t.Setenv(k, m.env[k])
	}
	cfg, err := config.Parse([]byte(m.cfg+"\n"), "test")
	require.NoError(t, err)
	getenv := func(k string) string { return m.env[k] }
	d := watch.Deps{
		Run: func(_ context.Context, c proc.Cmd) (proc.Result, error) {
			if out, ok := m.outputs[strings.Join(c.Args, " ")]; ok {
				return proc.Result{Stdout: out}, nil
			}
			return proc.Result{Code: 1}, &proc.Error{Args: c.Args, Code: 1, Stderr: "failed"}
		},
		LookPath: func(f string) (string, error) {
			if slices.Contains(m.tools, f) {
				return "/usr/bin/" + f, nil
			}
			return "", errors.New("not found")
		},
		Getenv: getenv, GOOS: m.goos, Home: home, Self: "/bin/outrider",
		Sleep: func(context.Context, time.Duration) error { return nil },
	}
	return &Input{
		Settings: watch.Settings{Cfg: cfg},
		Deps:     d,
		Dialogs:  approve.Platform{GOOS: m.goos, Getenv: getenv, Exists: func(p string) bool { return slices.Contains(m.files, p) }},
	}
}

func without(list []string, drop ...string) []string {
	return slices.DeleteFunc(slices.Clone(list), func(s string) bool { return slices.Contains(drop, s) })
}

type checkCase struct {
	name   string
	setup  func(m *machine)
	edit   func(t *testing.T, in *Input)
	status Status
	detail string // contained in the detail
	fix    string // contained in the fix
}

func runCases(t *testing.T, check func(context.Context, *Input) Result, cases []checkCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := require.New(t)
			m := newMachine()
			if tc.setup != nil {
				tc.setup(m)
			}
			in := m.input(t)
			if tc.edit != nil {
				tc.edit(t, in)
			}
			got := check(t.Context(), in)
			r.Equal(tc.status, got.Status, got.Detail)
			r.Contains(got.Detail, tc.detail)
			r.Contains(got.Fix, tc.fix)
			if got.Status == OK {
				r.Empty(got.Fix)
			}
		})
	}
}

func TestCheckConfig(t *testing.T) {
	runCases(t, checkConfig, []checkCase{
		{name: "defaults", status: OK, detail: "built-in defaults (mode supervised, push ask, github_writes ask, sandbox: off)"},
		{
			name: "read-only sandbox", setup: func(m *machine) { m.cfg = "sandbox: read-only" },
			status: OK, detail: "push never, github_writes never, sandbox: read-only",
		},
		{
			name: "overrides", setup: func(m *machine) { m.cfg = "overrides: [{match: [{prs: others}], ignore_authors: ['*[bot]']}]" },
			status: OK, detail: "sandbox: off); overrides: overrides[0] (prs: others)",
		},
		{
			name: "invalid", edit: func(_ *testing.T, in *Input) { in.ConfigErr = errors.New("c.yaml is invalid:\n- at '/agent'") },
			status: Fail, detail: "at '/agent'", fix: "outrider config check",
		},
	})
}

func TestCheckGitHub(t *testing.T) {
	runCases(t, checkGitHub, []checkCase{
		{name: "ok", status: OK, detail: "github.com as me (prompts call you Me), token from keyring, scopes gist, read:org, repo"},
		{
			name: "no gh", setup: func(m *machine) { m.tools = without(m.tools, "gh") },
			status: Fail, detail: "gh not found", fix: "https://cli.github.com",
		},
		{
			name: "not logged in", setup: func(m *machine) {
				m.outputs["gh auth status --active --hostname github.com --json hosts"] = `{"hosts": {"github.com": [
					{"state": "error", "error": "token is invalid", "active": true}]}}`
			},
			status: Fail, detail: "not logged in to github.com: token is invalid", fix: "gh auth login --hostname github.com",
		},
		{
			name: "no account", setup: func(m *machine) {
				m.outputs["gh auth status --active --hostname github.com --json hosts"] = `{"hosts": {}}`
			},
			status: Fail, detail: "not logged in", fix: "gh auth login",
		},
		{
			name: "enterprise host", setup: func(m *machine) {
				m.env["GH_HOST"] = "ghe.example.com"
				m.outputs["gh auth status --active --hostname ghe.example.com --json hosts"] = strings.ReplaceAll(loggedIn, "github.com", "ghe.example.com")
			},
			status: OK, detail: "ghe.example.com as me",
		},
		{
			name: "old gh", setup: func(m *machine) { delete(m.outputs, "gh auth status --active --hostname github.com --json hosts") },
			status: Fail, fix: "update gh",
		},
		{
			name: "missing repo scope", setup: func(m *machine) {
				m.outputs["gh auth status --active --hostname github.com --json hosts"] = strings.ReplaceAll(loggedIn, ", repo", ", notifications")
			},
			status: Fail, detail: "missing repo", fix: "gh auth refresh --hostname github.com --scopes repo",
		},
		{
			name: "review forks without workflow scope", setup: func(m *machine) { m.cfg = "others_prs: {review_forks: [me/*]}" },
			status: Warn, detail: "missing workflow", fix: "gh auth refresh --hostname github.com --scopes workflow",
		},
		{
			name: "review forks with workflow scope", setup: func(m *machine) {
				m.cfg = "others_prs: {review_forks: [me/*]}"
				m.outputs["gh auth status --active --hostname github.com --json hosts"] = strings.ReplaceAll(loggedIn, ", repo", ", repo, workflow")
			},
			status: OK, detail: "scopes gist, read:org, repo, workflow",
		},
		{
			name: "fine-grained token", setup: func(m *machine) {
				m.outputs["gh auth status --active --hostname github.com --json hosts"] = strings.ReplaceAll(loggedIn, "gist, read:org, repo", "")
			},
			status: Warn, detail: "no OAuth scopes", fix: "notifications",
		},
		{
			name: "user unreadable", setup: func(m *machine) { delete(m.outputs, "gh api user") },
			status: Fail, detail: "cannot read the GitHub user", fix: "gh api user",
		},
		{
			name: "owner name from the config", setup: func(m *machine) { m.cfg = "owner_name: Matze" },
			status: OK, detail: "prompts call you Matze",
		},
	})
}

func TestCheckGitHubNeverPrintsTheToken(t *testing.T) {
	m := newMachine()
	m.outputs["gh auth status --active --hostname github.com --json hosts"] = strings.Replace(loggedIn,
		`"scopes"`, `"token": "gho_secret", "scopes"`, 1)
	got := checkGitHub(t.Context(), m.input(t))
	require.Equal(t, OK, got.Status)
	require.NotContains(t, got.Detail+got.Fix, "gho_secret")
}

func TestCheckGit(t *testing.T) {
	runCases(t, checkGit, []checkCase{
		{name: "ok", status: OK, detail: "git version 2.50.0"},
		{name: "missing", setup: func(m *machine) { m.tools = without(m.tools, "git") }, status: Fail, detail: "git not found", fix: "install git"},
		{name: "broken", setup: func(m *machine) { delete(m.outputs, "git --version") }, status: Fail, detail: "git --version: failed"},
	})
}

func TestCheckAgent(t *testing.T) {
	runCases(t, checkAgent, []checkCase{
		{name: "codex", status: OK, detail: "codex codex-cli 0.156.1 (/usr/bin/codex), the configured agent"},
		{name: "claude", setup: func(m *machine) { m.cfg = "agent: claude" }, status: OK, detail: "claude 2.1.286 (Claude Code)"},
		{
			name: "missing", setup: func(m *machine) { m.cfg = "agent: claude"; m.tools = without(m.tools, "claude") },
			status: Fail, detail: "claude not found on PATH", fix: "install Claude Code",
		},
		{
			name: "no version", setup: func(m *machine) { delete(m.outputs, "codex --version") },
			status: Fail, detail: "codex --version: failed", fix: "install Codex",
		},
	})
	runCases(t, checkOtherAgent, []checkCase{
		{name: "other installed", status: Info, detail: "claude 2.1.286 (Claude Code) (/usr/bin/claude), not configured"},
		{
			name: "other missing", setup: func(m *machine) { m.cfg = "agent: claude"; m.tools = without(m.tools, "codex") },
			status: Info, detail: "codex not found on PATH, not configured",
		},
		{
			name: "other set by overrides", setup: func(m *machine) { m.cfg = "overrides: [{match: [{prs: own}], agent: claude}]" },
			status: OK, detail: "claude 2.1.286 (Claude Code) (/usr/bin/claude), set by overrides",
		},
		{
			name: "other set by overrides, missing", setup: func(m *machine) {
				m.cfg = "overrides: [{match: [{prs: own}], agent: claude}]"
				m.tools = without(m.tools, "claude")
			},
			status: Fail, detail: "claude not found on PATH", fix: "remove agent from the overrides",
		},
	})
}

func TestCheckLauncher(t *testing.T) {
	runCases(t, checkLauncher, []checkCase{
		{name: "linux desktop", status: OK, detail: "terminal (launcher: auto), terminal x-terminal-emulator (fallback)"},
		{
			name: "terminal from the environment", setup: func(m *machine) { m.env["TERM_PROGRAM"] = "kitty"; m.tools = append(m.tools, "kitty") },
			status: OK, detail: "terminal kitty (from $TERM_PROGRAM)",
		},
		{
			name: "no display", setup: func(m *machine) { delete(m.env, "DISPLAY") },
			status: OK, detail: "tmux (launcher: auto)",
		},
		{
			name: "tmux missing", setup: func(m *machine) { m.cfg = "launcher: tmux"; m.tools = without(m.tools, "tmux") },
			status: Fail, detail: "tmux not found on PATH", fix: "install tmux",
		},
		{
			name: "no terminal", setup: func(m *machine) { m.cfg = "launcher: terminal"; m.tools = without(m.tools, "x-terminal-emulator") },
			status: Fail, detail: "no terminal found", fix: "set terminal",
		},
		{
			name: "tmux on windows", setup: func(m *machine) { m.cfg = "launcher: tmux"; m.goos = "windows" },
			status: Fail, detail: "tmux is not supported on Windows",
		},
		{
			name: "macOS aqua", setup: func(m *machine) {
				m.goos = "darwin"
				m.tools = append(m.tools, "open")
				m.outputs["launchctl managername"] = "Aqua\n"
			},
			status: OK, detail: "terminal (launcher: auto), terminal Terminal.app (fallback)",
		},
	})
}

func TestCheckDialogs(t *testing.T) {
	runCases(t, checkDialogs, []checkCase{
		{name: "zenity", status: OK, detail: "/usr/bin/zenity (push ask, github_writes ask)"},
		{name: "not needed", setup: func(m *machine) { m.cfg = "push: never\ngithub_writes: allow" }, status: Info, detail: "not needed"},
		{
			name: "no dialog tool", setup: func(m *machine) { m.files = nil },
			status: Warn, detail: "ask will deny", fix: "install zenity or kdialog",
		},
		{
			name: "no display", setup: func(m *machine) { delete(m.env, "DISPLAY") },
			status: Warn, detail: "no approval dialog", fix: "$DISPLAY",
		},
		{
			name: "macOS over ssh", setup: func(m *machine) {
				m.goos = "darwin"
				m.files = []string{"/usr/bin/osascript"}
				m.outputs["launchctl managername"] = "Background\n"
			},
			status: Warn, detail: "/usr/bin/osascript, but no desktop session", fix: "desktop session",
		},
		{
			name: "macOS", setup: func(m *machine) {
				m.goos = "darwin"
				m.files = []string{"/usr/bin/osascript"}
				m.outputs["launchctl managername"] = "Aqua\n"
			},
			status: OK, detail: "/usr/bin/osascript",
		},
	})
}

func TestCheckClassifiers(t *testing.T) {
	runCases(t, checkLaunchCheck, []checkCase{
		{name: "jev without a key", status: Warn, detail: "off (jev: no backend key", fix: "export a backend key"},
		{name: "jev with a key", setup: func(m *machine) { m.env["TYPESAFE_API_KEY"] = "sk-secret" }, status: OK, detail: "on (jev: jev-use)"},
		{
			name: "jev without jev-use or npx", setup: func(m *machine) { m.tools = without(m.tools, "jev-use", "npx") },
			status: Warn, detail: "npx not found", fix: "install jev-use",
		},
		{name: "disabled", setup: func(m *machine) { m.cfg = "classifiers: {jev: {enabled: false}}" }, status: Info, detail: "off (jev: disabled)"},
		{name: "role off", setup: func(m *machine) { m.cfg = "launch_check: {classifier: null}" }, status: Info, detail: "off (off)"},
		{
			name: "command missing", setup: func(m *machine) {
				m.cfg = "classifiers: {mine: {kind: command, launch_command: /opt/cls}}\nlaunch_check: {classifier: mine}"
			},
			status: Warn, detail: "off (mine: /opt/cls not found)", fix: "classifiers.mine",
		},
		{
			name: "command in the home dir", setup: func(m *machine) {
				m.cfg = "classifiers: {mine: {kind: command, launch_command: ~/bin/cls --x}}\nlaunch_check: {classifier: mine}"
			},
			edit: func(t *testing.T, in *Input) {
				home := t.TempDir()
				t.Setenv("HOME", home)
				t.Setenv("USERPROFILE", home) // Windows
				lookPath := in.Deps.LookPath
				in.Deps.LookPath = func(f string) (string, error) {
					if f == filepath.Join(home, "bin", "cls") {
						return f, nil
					}
					return lookPath(f)
				}
			},
			status: OK, detail: "on (mine: command)",
		},
	})
	runCases(t, checkToolGate, []checkCase{
		{name: "jev with a key", setup: func(m *machine) { m.env["OPENROUTER_API_KEY"] = "k" }, status: OK, detail: "on (jev: jev-use)"},
		{name: "autonomous", setup: func(m *machine) { m.cfg = "mode: autonomous" }, status: Info, detail: "off (off (autonomous mode))"},
	})
}

func TestClassifiersNeverPrintTheJevKey(t *testing.T) {
	for _, check := range []func(context.Context, *Input) Result{checkLaunchCheck, checkToolGate} {
		m := newMachine()
		m.env["TYPESAFE_API_KEY"] = "sk-very-secret"
		got := check(t.Context(), m.input(t))
		require.NotContains(t, got.Detail+got.Fix, "sk-very-secret")
	}
}

func TestCheckSandbox(t *testing.T) {
	runCases(t, checkSandbox, []checkCase{
		{name: "off", status: Info, detail: "off"},
		{
			name: "claude on linux", setup: func(m *machine) { m.cfg = "sandbox: read-only\nagent: claude" },
			status: OK, detail: "read-only (claude: native sandbox + deny rules, no user or project settings)",
		},
		{
			name: "others only, without socat", setup: func(m *machine) {
				m.cfg = "others_prs: {sandbox: read-only}\nagent: claude"
				m.tools = without(m.tools, "socat")
			},
			status: Fail, detail: "off; others' PRs: read-only: the claude sandbox on Linux needs socat; sandboxed sessions are refused",
			fix: "sandbox and others_prs.sandbox to off",
		},
		{
			name: "an override, without socat", setup: func(m *machine) {
				m.cfg = "overrides: [{match: [{repo: o/*}], sandbox: read-only, agent: claude}]"
				m.tools = without(m.tools, "socat")
			},
			status: Fail, detail: "off; overrides: read-only: the claude sandbox on Linux needs socat",
			fix: "or change overrides[0] (repo: o/*)",
		},
		{
			name: "old claude", setup: func(m *machine) {
				m.cfg = "sandbox: read-only\nagent: claude"
				m.outputs["claude --version"] = "2.1.200 (Claude Code)\n"
			},
			status: Fail, detail: "older than 2.1.285",
		},
		{
			name: "codex without file login", setup: func(m *machine) { m.cfg = "sandbox: read-only" },
			status: Fail, detail: "codex login",
		},
		{
			name: "codex with file login", setup: func(m *machine) { m.cfg = "sandbox: read-only" },
			edit: func(t *testing.T, in *Input) {
				codex := filepath.Join(in.Deps.Home, ".codex")
				require.NoError(t, os.MkdirAll(codex, 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(codex, "auth.json"), []byte("{}"), 0o600))
			},
			status: OK, detail: "read-only (codex: --sandbox read-only",
		},
		{
			name: "windows", setup: func(m *machine) { m.cfg = "sandbox: read-only"; m.goos = "windows" },
			status: Fail, detail: "outrider supports no codex sandbox on windows",
		},
	})
}

func TestCheckCheckout(t *testing.T) {
	inRepo := func(m *machine) {
		m.outputs["git rev-parse --show-toplevel"] = "/src/r\n"
		m.outputs["git remote get-url origin"] = "git@github.com:o/r.git\n"
	}
	runCases(t, checkCheckout, []checkCase{
		{name: "outside", status: Info, detail: "not inside a GitHub checkout"},
		{name: "inside", setup: inRepo, status: Info, detail: "o/r at /src/r (remote origin)"},
		{
			name: "remote outside a checkout", edit: func(_ *testing.T, in *Input) { in.Settings.Remote = "upstream" },
			status: Fail, detail: "--remote needs to run inside a git checkout", fix: "git remote -v",
		},
		{
			name: "remote not on GitHub", setup: inRepo, edit: func(_ *testing.T, in *Input) { in.Settings.Remote = "upstream" },
			status: Fail, detail: "remote 'upstream' is not a GitHub repo here",
		},
	})
}

func TestCheckReviewForks(t *testing.T) {
	// the shared resolution runs first, so the github check sees the auto fork
	forkCheckout := func(m *machine) {
		m.outputs["git rev-parse --show-toplevel"] = "/src/r\n"
		m.outputs["git remote get-url upstream"] = "https://github.com/o/r.git\n"
		m.outputs["git remote get-url --push --all -- origin"] = "https://github.com/me/r.git\n"
		m.outputs["gh api repos/me/r"] = `{"full_name": "me/r", "fork": true, "parent": {"full_name": "o/r"}}`
	}
	upstream := func(_ *testing.T, in *Input) { in.Settings.Remote = "upstream" }
	both := func(t *testing.T, in *Input) []Result {
		t.Helper()
		rs := Run(t.Context(), *in)
		i := slices.IndexFunc(rs, func(r Result) bool { return r.Name == "review forks" })
		j := slices.IndexFunc(rs, func(r Result) bool { return r.Name == "github" })
		return []Result{rs[i], rs[j]}
	}
	for _, tc := range []struct {
		name   string
		setup  func(m *machine)
		status Status
		detail string
		github string
	}{
		{name: "outside a checkout", status: Info, detail: "off (not in a local checkout)", github: "scopes gist, read:org, repo"},
		{name: "auto: origin", setup: forkCheckout, status: Info, detail: "me/r (auto: origin)", github: "missing workflow"},
		{
			name: "lookup fails", setup: func(m *machine) { forkCheckout(m); delete(m.outputs, "gh api repos/me/r") },
			status: Warn, detail: "off (cannot look up origin me/r on GitHub",
		},
		{
			name: "explicit list", setup: func(m *machine) { forkCheckout(m); m.cfg = "others_prs: {review_forks: [me/x]}" },
			status: Info, detail: "me/x (config)",
		},
		{
			name: "wildcard", setup: func(m *machine) { forkCheckout(m); m.cfg = "others_prs: {review_forks: [me/x, me/*]}" },
			status: Warn, detail: "me/* is a wildcard, which also matches repos that aren't forks",
		},
		{
			name: "off", setup: func(m *machine) { forkCheckout(m); m.cfg = "others_prs: {review_forks: []}" },
			status: Info, detail: "off (review_forks: [])", github: "scopes gist, read:org, repo",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := require.New(t)
			m := newMachine()
			if tc.setup != nil {
				tc.setup(m)
			}
			in := m.input(t)
			if tc.setup != nil {
				upstream(t, in)
			}
			got := both(t, in)
			r.Equal(tc.status, got[0].Status, got[0].Detail)
			r.Contains(got[0].Detail, tc.detail)
			r.Contains(got[1].Detail, tc.github)
			if !strings.Contains(tc.detail, "*") {
				r.NotContains(got[0].Detail, "wildcard") // an exact or auto-resolved fork never warns
			}
		})
	}
}

func TestCheckFiles(t *testing.T) {
	runCases(t, checkFiles, []checkCase{
		{name: "fresh home", status: OK, detail: filepath.Join(".cache", "outrider")},
		{
			name: "broken state", edit: func(t *testing.T, in *Input) {
				state := watch.StatePath(in.Deps.Home)
				require.NoError(t, os.MkdirAll(filepath.Dir(state), 0o700))
				require.NoError(t, os.WriteFile(state, []byte("{nope"), 0o600))
			},
			status: Warn, detail: "state.json", fix: "--reset-state",
		},
		{
			name: "file in the way", edit: func(t *testing.T, in *Input) {
				require.NoError(t, os.WriteFile(filepath.Join(in.Deps.Home, ".cache"), nil, 0o600))
			},
			status: Warn, detail: "not a directory", fix: "remove or rename",
		},
	})
}

func TestCheckLeftovers(t *testing.T) {
	mkdir := func(parts ...string) func(t *testing.T, in *Input) {
		return func(t *testing.T, in *Input) {
			for _, p := range parts {
				require.NoError(t, os.MkdirAll(filepath.Join(in.Deps.Home, p), 0o700))
			}
		}
	}
	runCases(t, checkLeftovers, []checkCase{
		{name: "clean", status: OK, detail: "nothing left"},
		{
			name: "moved on the next start", edit: mkdir(filepath.Join(".cache", "llm-review-agent")),
			status: Info, detail: "moved to",
		},
		{
			name: "left over", edit: mkdir(filepath.Join(".config", "llm-review-agent"), filepath.Join(".config", "outrider")),
			status: Warn, detail: "left over next to", fix: "remove",
		},
		{
			name: "old binary", setup: func(m *machine) { m.tools = append(m.tools, "llm-review-agent") },
			status: Warn, detail: "binary /usr/bin/llm-review-agent", fix: "remove /usr/bin/llm-review-agent",
		},
		{
			name: "old variable", setup: func(m *machine) { m.env[config.LegacyEnvVar] = "/c.yaml" },
			status: Warn, detail: "$LLM_REVIEW_AGENT_CONFIG", fix: "$OUTRIDER_CONFIG",
		},
	})
}

func TestRunIsReadOnly(t *testing.T) {
	r := require.New(t)
	m := newMachine()
	m.cfg = "sandbox: read-only" // codex sandbox: would write a session CODEX_HOME when launching
	in := m.input(t)
	old := filepath.Join(in.Deps.Home, ".cache", "llm-review-agent")
	r.NoError(os.MkdirAll(old, 0o700))
	results := Run(t.Context(), *in)
	r.Len(results, len(checks))
	r.True(Failed(results)) // no codex login
	r.DirExists(old)        // not migrated
	entries, err := os.ReadDir(in.Deps.Home)
	r.NoError(err)
	r.Len(entries, 1) // only .cache, which the test made
}
