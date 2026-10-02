package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/appetizers-io/outrider/internal/proc"
)

func (l *Launcher) worktreeOf(n int) string {
	return filepath.Join(l.Root, "worktrees", "o__r", "pr-"+itoa(n))
}

func asJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	return string(raw)
}

func TestReadOnlyClaudeSessionRunsInTheNativeSandbox(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		for _, cfg := range []string{"sandbox: read-only", "mode: autonomous\nsandbox: read-only"} {
			for _, author := range []string{"bob", "me"} {
				r := require.New(t)
				l, _ := newLauncher(t, cfg)
				l.GOOS = goos
				got := l.launched(t, author)
				wt := l.worktreeOf(1)

				// Bash runs in the OS sandbox or not at all: no writes to the
				// worktree or its git dir, no network
				r.JSONEq(asJSON(t, map[string]any{
					"enabled": true, "failIfUnavailable": true, "allowUnsandboxedCommands": false,
					"autoAllowBashIfSandboxed": true,
					"filesystem":               map[string]any{"denyWrite": []string{wt, "/repo/.git"}},
					"network":                  map[string]any{"allowedDomains": []string{}, "strictAllowlist": true},
				}), asJSON(t, got.settings["sandbox"]), cfg)
				r.Equal("disable", got.settings["permissions"].(map[string]any)["disableBypassPermissionsMode"])
				r.Subset(got.deny(), []string{"Edit", "Write", "NotebookEdit", "WebFetch", "WebSearch",
					"Bash(git commit:*)", "Bash(git push:*)", dialogDenyRules(goos)[0]})
				r.Equal(got.deny(), toStrings(got.policy["deny_rules"]))

				settings := filepath.Join(l.Root, "sessions", "o__r", "pr-1", "claude-settings.json")
				r.Equal(append([]string{"/bin/claude", "--name", "PR o/r#1", "--remote-control", "PR o/r#1", "--settings", settings},
					"--setting-sources", "user", "--strict-mcp-config", "--permission-mode", "manual", "--tools", "Bash,Read,Glob,Grep"),
					got.spec.Agent)

				// nothing is pushed or posted, on your own PR too
				r.Equal("review-only", got.spec.Env["OUTRIDER_PUSH"])
				r.Equal("never", got.spec.Env["OUTRIDER_GH_WRITES"])
				r.Equal("read-only", got.policy["sandbox"])
				r.Equal(true, got.policy["review_only"])
				r.Contains(got.spec.Header[1], "read-only sandbox")
			}
		}
	}
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func TestReadOnlySessionGetsThePRContextAndSaysSo(t *testing.T) {
	r := require.New(t)
	l, _ := newLauncher(t, "sandbox: read-only")
	l.ToolGate = jevGate
	got := l.launched(t, "me")
	dir := filepath.Join(l.Root, "sessions", "o__r", "pr-1", "pr-context")
	for _, f := range []string{"pr.json", "pr.diff", "review-comments.json", "failing-checks.json"} {
		r.FileExists(filepath.Join(dir, f))
	}
	r.Equal(dir, got.policy["pr_context"])
	r.Contains(got.prompt, "READ-ONLY SANDBOX")
	r.Contains(got.prompt, dir+":\n  - pr.json: ")
	r.Contains(got.prompt, "Use the PR context files and git")
	r.Contains(got.prompt, "do NOT commit or push anything")
	r.NotContains(got.prompt, "OWN PR")
	r.NotContains(got.prompt, "git push --force-with-lease")
	r.Contains(got.settingsEnv()["OUTRIDER_POLICY_FILE"], "policy.json")
	r.Contains(got.settingsEnv()["JEV_GATE_STATE"], "READ-ONLY SANDBOX") // the gate knows too
}

func TestReadOnlyCodexSessionRunsInItsSandboxWithAPrivateHome(t *testing.T) {
	r := require.New(t)
	l, _ := newLauncher(t, "sandbox: read-only")
	l.Agent = "codex"
	l.CodexHome = "/home/me/.codex"
	got := l.launched(t, "bob")
	wt, checkout := tomlString(l.worktreeOf(1)), tomlString(filepath.Dir("/repo/.git"))
	r.Equal([]string{"/bin/codex",
		"--sandbox", "read-only", "--ask-for-approval", "never",
		"-c", `web_search="disabled"`,
		"-c", "projects." + wt + `.trust_level="untrusted"`,
		"-c", "projects." + checkout + `.trust_level="untrusted"`,
		"--disable", "apps", "--disable", "plugins", "--disable", "browser_use",
		"--disable", "computer_use", "--disable", "in_app_browser",
	}, got.spec.Agent)
	r.Nil(got.settings)
	home := filepath.Join(l.Root, "sessions", "o__r", "pr-1", "codex-home")
	r.Equal(home, got.spec.Env["CODEX_HOME"])
	link, err := os.Readlink(filepath.Join(home, "auth.json"))
	r.NoError(err)
	r.Equal(filepath.Join("/home/me/.codex", "auth.json"), link)
	cfg, err := os.ReadFile(filepath.Join(home, "config.toml"))
	r.NoError(err)
	r.Equal("[projects."+wt+"]\ntrust_level = \"untrusted\"\n\n[projects."+checkout+"]\ntrust_level = \"untrusted\"\n\n", string(cfg))
	r.Equal("never", got.spec.Env["OUTRIDER_GH_WRITES"])
	r.Contains(got.prompt, "READ-ONLY SANDBOX")
}

func TestSandboxOffChangesNothing(t *testing.T) {
	r := require.New(t)
	l, _ := newLauncher(t, "sandbox: off")
	got := l.launched(t, "me")
	r.NotContains(got.settings, "sandbox")
	r.NotContains(got.spec.Agent, "--strict-mcp-config")
	r.Equal("off", got.policy["sandbox"])
	r.Nil(got.policy["pr_context"])
	r.NotContains(got.prompt, "SANDBOX")
	r.NoDirExists(filepath.Join(l.Root, "sessions", "o__r", "pr-1", "pr-context"))

	l, _ = newLauncher(t, "")
	l.Agent = "codex"
	r.Equal([]string{"/bin/codex"}, l.launched(t, "me").spec.Agent)
}

func TestOthersPRsSandboxLeavesOwnPRsAlone(t *testing.T) {
	r := require.New(t)
	l, _ := newLauncher(t, "others_prs: {sandbox: read-only}\npush: allow")
	r.Equal("off", l.launched(t, "me").policy["sandbox"])
	l, _ = newLauncher(t, "others_prs: {sandbox: read-only}\npush: allow")
	r.Equal("read-only", l.launched(t, "bob").policy["sandbox"])
}

func TestUnavailableSandboxRefusesTheSession(t *testing.T) {
	r := require.New(t)
	l, c := newLauncher(t, "sandbox: read-only")
	l.SandboxErr = errors.New("no claude sandbox on windows")
	r.True(l.Launch(t.Context(), Request{Repo: "o/r", N: 1, PR: pr("bob"), Trigger: "t"})) // handled: not retried
	r.Empty(c.args)                                                                        // no worktree, no agent
	r.NoFileExists(filepath.Join(l.Root, "sessions", "o__r", "pr-1", "session.json"))

	// sessions that aren't sandboxed still start
	l, c = newLauncher(t, "others_prs: {sandbox: read-only}")
	l.SandboxErr = errors.New("no claude sandbox on windows")
	r.True(l.Launch(t.Context(), Request{Repo: "o/r", N: 1, PR: pr("me"), Trigger: "t"}))
	r.NotEmpty(c.args)
}

func TestFailedPrefetchKeepsTheEventPending(t *testing.T) {
	l, _ := newLauncher(t, "sandbox: read-only")
	l.Run = func(_ context.Context, cmd proc.Cmd) (proc.Result, error) {
		if cmd.Args[0] == "gh" && cmd.Args[1] == "pr" && cmd.Args[2] == "view" {
			return proc.Result{}, &proc.Error{Args: cmd.Args, Code: 1, Stderr: "offline"}
		}
		return proc.Result{}, nil
	}
	require.False(t, l.Launch(t.Context(), Request{Repo: "o/r", N: 1, PR: pr("bob"), Trigger: "t"}))
	require.NoFileExists(t, filepath.Join(l.Root, "sessions", "o__r", "pr-1", "session.json"))
}

func TestSandboxSupport(t *testing.T) {
	codexHome := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(codexHome, "auth.json"), []byte("{}"), 0o600))
	versions := map[string]string{"claude": "2.1.286 (Claude Code)\n", "codex": "codex-cli 0.156.1\n"}
	for _, tc := range []struct {
		agent, goos string
		missing     string // a tool not installed
		version     string // "": the verified one
		home        string // "": codexHome
		want        string // "": supported
	}{
		{agent: "claude", goos: "darwin"},
		{agent: "codex", goos: "darwin"},
		{agent: "claude", goos: "linux"},
		{agent: "codex", goos: "linux"},
		{agent: "claude", goos: "linux", missing: "bwrap", want: "the claude sandbox on Linux needs bwrap"},
		{agent: "claude", goos: "linux", missing: "socat", want: "needs socat"},
		{agent: "codex", goos: "linux", missing: "bwrap", want: "the codex sandbox on Linux needs bwrap"},
		{agent: "codex", goos: "linux", missing: "socat"},
		{agent: "claude", goos: "windows", want: "no claude sandbox on windows"},
		{agent: "codex", goos: "windows", want: "no codex sandbox on windows"},
		{agent: "claude", goos: "darwin", version: "2.1.284 (Claude Code)", want: "older than 2.1.285"},
		{agent: "codex", goos: "darwin", version: "codex-cli 0.120.0", want: "older than 0.156.0"},
		{agent: "claude", goos: "darwin", version: "garbage", want: "older than"},
		{agent: "codex", goos: "darwin", home: "/nonexistent", want: "need file-based login"},
	} {
		lookPath := func(f string) (string, error) {
			if f == tc.missing {
				return "", errors.New("not found")
			}
			return "/bin/" + f, nil
		}
		run := func(_ context.Context, cmd proc.Cmd) (proc.Result, error) {
			require.Equal(t, []string{tc.agent, "--version"}, cmd.Args)
			if tc.version != "" {
				return proc.Result{Stdout: tc.version}, nil
			}
			return proc.Result{Stdout: versions[tc.agent]}, nil
		}
		home := codexHome
		if tc.home != "" {
			home = tc.home
		}
		err := SandboxSupport(t.Context(), tc.agent, tc.goos, home, lookPath, run)
		if tc.want == "" {
			require.NoError(t, err, tc)
		} else {
			require.ErrorContains(t, err, tc.want, tc)
		}
	}
	failing := func(context.Context, proc.Cmd) (proc.Result, error) { return proc.Result{}, errors.New("boom") }
	require.ErrorContains(t, SandboxSupport(t.Context(), "claude", "darwin", codexHome,
		func(f string) (string, error) { return f, nil }, failing), "claude --version")
}
