package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/appetizers-io/llm-review-agent/internal/classifier"
	"github.com/appetizers-io/llm-review-agent/internal/config"
	"github.com/appetizers-io/llm-review-agent/internal/github"
	"github.com/appetizers-io/llm-review-agent/internal/proc"
)

// As the agent of a session the test binary reports what it sees.
func TestMain(m *testing.M) {
	if os.Getenv("FAKE_AGENT") != "" {
		lock, _ := os.ReadFile(os.Getenv("FAKE_AGENT_LOCK"))
		path := strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))[0]
		wd, _ := os.Getwd()
		report := fmt.Sprintf("args=%q\nlock=%s\npush=%s\npath=%s\nwd=%s\n", os.Args[1:], lock, os.Getenv("LLM_REVIEW_AGENT_PUSH"), path, wd)
		_ = os.WriteFile(os.Getenv("FAKE_AGENT_OUT"), []byte(report), 0o600)
		os.Exit(3)
	}
	os.Exit(m.Run())
}

var (
	jevGate   = &classifier.Resolved{Name: "jev", Kind: "jev", HookCmd: []string{"jev-use", "hook", "gate"}, Timeout: 5 * time.Second}
	localGate = &classifier.Resolved{Name: "local", Kind: "command", HookCmd: []string{"cls", "hook"}, Timeout: 5 * time.Second}
)

type calls struct{ args [][]string }

func (c *calls) run(_ context.Context, cmd proc.Cmd) (proc.Result, error) {
	c.args = append(c.args, cmd.Args)
	if cmd.Args[0] == "jev-use" { // the launch check: skip "noise"
		answer := 0.9
		if strings.Contains(cmd.Stdin, "noise") {
			answer = 0.1
		}
		return proc.Result{Stdout: fmt.Sprintf(`{"verdicts": [{"answer": %v, "confidence": 0.9}]}`, answer)}, nil
	}
	return proc.Result{}, nil
}

func newLauncher(t *testing.T, cfgText string) (*Launcher, *calls) {
	t.Helper()
	cfg, err := config.Parse([]byte(cfgText), "c.yaml")
	require.NoError(t, err)
	c := &calls{}
	return &Launcher{
		Root: t.TempDir(), Self: os.Args[0], Cfg: &cfg, Login: "me", Owner: "Matthias",
		Agent: "claude", Launcher: "tmux", GOOS: runtime.GOOS, Run: c.run,
		LookPath: func(f string) (string, error) { return "/bin/" + f, nil },
		Log:      slog.New(slog.DiscardHandler),
	}, c
}

func pr(author string) github.PR {
	return github.PR{Title: "T", Author: &github.User{Login: author}, URL: "https://github.com/o/r/pull/1"}
}

type launched struct {
	policy   map[string]any
	prompt   string
	spec     Spec
	settings map[string]any
}

func (l *Launcher) launched(t *testing.T, author string) launched {
	t.Helper()
	require.True(t, l.Launch(t.Context(), Request{Repo: "o/r", N: 1, PR: pr(author), Trigger: "t"}))
	dir := filepath.Join(l.Root, "sessions", "o__r", "pr-1")
	var got launched
	read := func(name string, v any) bool {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) {
			return false
		}
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, v))
		return true
	}
	read("policy.json", &got.policy)
	read("session.json", &got.spec)
	if !read("claude-settings.json", &got.settings) {
		got.settings = nil
	}
	p, err := os.ReadFile(filepath.Join(dir, "prompt.txt"))
	require.NoError(t, err)
	got.prompt = string(p)
	return got
}

func (g launched) deny() []string {
	var out []string
	for _, r := range g.settings["permissions"].(map[string]any)["deny"].([]any) {
		out = append(out, r.(string))
	}
	return out
}

func (g launched) settingsEnv() map[string]any { return g.settings["env"].(map[string]any) }

func (g launched) usesSettings() bool {
	return strings.Contains(strings.Join(g.spec.Agent, " "), "--settings")
}

func dialogRule() string { return dialogDenyRules(runtime.GOOS)[0] }

func TestReviewOnlySessionDeniesEditsAndPushes(t *testing.T) {
	r := require.New(t)
	l, _ := newLauncher(t, "")
	l.ToolGate = jevGate
	got := l.launched(t, "bob")
	r.Subset(got.deny(), []string{"Edit", "Write", "Bash(git commit:*)", "Bash(git push:*)"})
	hook := got.settings["hooks"].(map[string]any)["PreToolUse"].([]any)[0].(map[string]any)
	r.Equal("Bash|Write|Edit|NotebookEdit", hook["matcher"])
	r.Equal("jev-use hook gate", hook["hooks"].([]any)[0].(map[string]any)["command"])
	r.Contains(got.settingsEnv()["JEV_GATE_STATE"], "REVIEW ONLY")
	r.True(got.usesSettings())
	r.Equal("review-only", got.spec.Env["LLM_REVIEW_AGENT_PUSH"])
	r.Contains(got.prompt, "REVIEW ONLY")
	r.Equal([]string{"/bin/claude", "--name", "PR o/r#1", "--remote-control", "PR o/r#1", "--settings"}, got.spec.Agent[:6])
}

func TestOwnPRSessionAllowsWorkButNotForce(t *testing.T) {
	r := require.New(t)
	l, _ := newLauncher(t, "")
	l.ToolGate = jevGate
	got := l.launched(t, "me")
	r.NotContains(got.deny(), "Edit")
	r.Contains(got.deny(), "Bash(git push --force:*)")
	r.NotContains(got.deny(), "Bash(git push:*)")
	r.Contains(got.deny(), dialogRule()) // can't click its own approval dialog
	r.Contains(got.settingsEnv()["JEV_GATE_STATE"], "own PR")
	r.Contains(got.settingsEnv()["JEV_GATE_STATE"], "asks the owner")
	r.Equal("ask", got.spec.Env["LLM_REVIEW_AGENT_PUSH"])
	r.Equal("5", got.spec.Env["GIT_CONFIG_COUNT"])
	r.Equal("url.llm-review-agent-push-blocked://.pushInsteadOf", got.spec.Env["GIT_CONFIG_KEY_0"])
	r.Equal("git@", got.spec.Env["GIT_CONFIG_VALUE_0"])
	r.Equal("ask", got.policy["push"])
	r.Contains(got.prompt, "opens a dialog for")
}

func TestPushNeverKeepsCommitsLocal(t *testing.T) {
	l, _ := newLauncher(t, "push: never\n")
	l.ToolGate = jevGate
	got := l.launched(t, "me")
	require.Contains(t, got.deny(), "Bash(git push:*)")
	require.Equal(t, "never", got.spec.Env["LLM_REVIEW_AGENT_PUSH"])
	require.Contains(t, got.prompt, "do not push")
	require.Equal(t, false, got.policy["push_allowed"])
}

func TestWithoutAGateDenyRulesStillApply(t *testing.T) {
	l, _ := newLauncher(t, "")
	got := l.launched(t, "bob")
	require.NotContains(t, got.settings, "hooks")
	require.Contains(t, got.deny(), "Bash(git push:*)")
	require.Contains(t, got.spec.Header[1], "tools gated by deny rules")
}

func TestAutonomousModeHasNoGatingAndMayPush(t *testing.T) {
	l, _ := newLauncher(t, "mode: autonomous\n")
	l.ToolGate = jevGate // resolved, but autonomous mode ignores it at startup; here: not supervised
	got := l.launched(t, "bob")
	require.Nil(t, got.settings)
	require.False(t, got.usesSettings())
	require.Equal(t, "allow", got.spec.Env["LLM_REVIEW_AGENT_PUSH"])
	require.NotContains(t, got.spec.Env, "GIT_CONFIG_COUNT")
}

func TestAutonomousModeCanStillAskBeforePushing(t *testing.T) {
	l, _ := newLauncher(t, "mode: autonomous\npush: ask\n")
	require.Equal(t, "ask", l.launched(t, "me").spec.Env["LLM_REVIEW_AGENT_PUSH"])
}

func TestAutonomousModeRespectsExplicitReviewOnly(t *testing.T) {
	l, _ := newLauncher(t, "mode: autonomous\nothers_prs: {allow_push: false}\n")
	got := l.launched(t, "bob")
	require.Equal(t, "review-only", got.spec.Env["LLM_REVIEW_AGENT_PUSH"])
	require.Nil(t, got.settings)
}

func TestPolicyNamesThePRAndRules(t *testing.T) {
	text := Rules("o/r", 7, "bob", "Matthias", false, "review-only", "never")
	require.Contains(t, text, "o/r#7 by bob")
	require.Contains(t, text, "deny every file edit, git commit, git push")
}

func TestCustomRulesAndThresholdReachTheGate(t *testing.T) {
	r := require.New(t)
	l, _ := newLauncher(t, "tool_gate:\n  threshold: 0.8\n  matcher: Bash\n"+
		"  rules: [\"never modify generated/\"]\n  include_prompt_extra: true\n"+
		"prompts: {extra: 'no make release'}\n")
	l.ToolGate = jevGate
	got := l.launched(t, "me")
	env := got.settingsEnv()
	r.Contains(env["JEV_GATE_STATE"], "(1) never modify generated/ (2) no make release")
	r.Equal("0.8", env["JEV_GATE_THRESHOLD"])
	r.Equal("Bash", got.settings["hooks"].(map[string]any)["PreToolUse"].([]any)[0].(map[string]any)["matcher"])
	r.Equal("0.8", got.spec.Env["JEV_GATE_THRESHOLD"]) // for codex too
	r.InDelta(0.8, got.policy["tool_gate"].(map[string]any)["threshold"], 0)
}

func TestLocalClassifierAsToolGate(t *testing.T) {
	r := require.New(t)
	l, _ := newLauncher(t, "")
	l.ToolGate = localGate
	got := l.launched(t, "bob")
	hook := got.settings["hooks"].(map[string]any)["PreToolUse"].([]any)[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
	r.Equal("cls hook", hook["command"])
	r.Equal("Tool gate (local): checking this action", hook["statusMessage"])
	env := got.settingsEnv()
	r.Contains(env["LLM_REVIEW_AGENT_GATE_TEXT"], "REVIEW ONLY")
	r.NotContains(env, "JEV_GATE_STATE")
	r.True(strings.HasSuffix(env["LLM_REVIEW_AGENT_POLICY_FILE"].(string), "policy.json"))
}

func TestPolicyFileDescribesTheSession(t *testing.T) {
	r := require.New(t)
	l, _ := newLauncher(t, "")
	l.ToolGate = jevGate
	l.ConfigSource = "/c.yaml"
	got := l.launched(t, "bob")
	p := got.policy
	r.Equal(true, p["review_only"])
	r.Equal(false, p["own_pr"])
	r.Equal("supervised", p["mode"])
	r.Equal("jev", p["tool_gate"].(map[string]any)["classifier"])
	r.Contains(p["deny_rules"], "Bash(git push:*)")
	r.Nil(p["scope"])
	r.Equal("/c.yaml", p["config"])
	r.Contains(got.prompt, "policy.json")
	r.Contains(got.spec.Env, "LLM_REVIEW_AGENT_POLICY_FILE")
}

func TestGitHubWritesAskByDefaultInSupervisedMode(t *testing.T) {
	r := require.New(t)
	l, _ := newLauncher(t, "")
	got := l.launched(t, "bob")
	r.Equal("ask", got.spec.Env["LLM_REVIEW_AGENT_GH_WRITES"])
	r.Equal("o/r", got.spec.Env["LLM_REVIEW_AGENT_REPO"])
	r.Equal("1", got.spec.Env["LLM_REVIEW_AGENT_PR"])
	r.Equal("PR o/r#1", got.spec.Env["LLM_REVIEW_AGENT_SESSION"])
	r.Equal("ask", got.policy["github_writes"])
	r.Contains(got.prompt, "post to GitHub only when")
	r.Contains(got.prompt, "opens a dialog for")
	r.NotContains(got.prompt, "do NOT post comments")
	r.Contains(got.prompt, "do NOT merge/close the PR")
	r.Equal("agent: claude (gh posts to this PR ask you first, review only: git push blocked, tools gated by deny rules)", got.spec.Header[1])
}

func TestGitHubWritesNeverKeepsGitHubReadOnly(t *testing.T) {
	l, _ := newLauncher(t, "github_writes: never\n")
	got := l.launched(t, "me")
	require.Equal(t, "never", got.spec.Env["LLM_REVIEW_AGENT_GH_WRITES"])
	require.Contains(t, got.spec.Header[1], "gh is read-only")
	require.Contains(t, got.prompt, "do NOT post comments")
}

func TestGitHubWritesFollowsMode(t *testing.T) {
	l, _ := newLauncher(t, "mode: autonomous\n")
	got := l.launched(t, "me")
	require.Equal(t, "allow", got.policy["github_writes"])
	require.NotContains(t, got.prompt, "opens a dialog for Matthias showing")
}

func TestGateTextAllowsPostsOnlyThroughGH(t *testing.T) {
	text := Rules("o/r", 7, "bob", "Matthias", false, "review-only", "ask")
	require.Contains(t, text, "with plain `gh` is fine; the gh guard asks the owner")
	require.Contains(t, text, "except the posts allowed below")
	require.NotContains(t, Rules("o/r", 7, "bob", "Matthias", false, "review-only", "never"), "with plain `gh`")
}

func TestCodexSessionHasNoSettings(t *testing.T) {
	l, _ := newLauncher(t, "")
	l.Agent = "codex"
	l.ToolGate = jevGate
	got := l.launched(t, "bob")
	require.Equal(t, []string{"/bin/codex"}, got.spec.Agent)
	require.Nil(t, got.settings)
	require.Empty(t, got.policy["deny_rules"])
	require.Contains(t, got.spec.Env["JEV_GATE_STATE"], "REVIEW ONLY") // the gate's environment still reaches codex
}

// --- launching --------------------------------------------------------------

func (l *Launcher) lock(t *testing.T, n int) string {
	t.Helper()
	p := lockPath(l.Root, "o/r", n)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
	return p
}

func gate(context.Context) ([]github.Activity, error) { return nil, nil }

func TestSkipCountsAsHandled(t *testing.T) {
	l, c := newLauncher(t, "")
	l.DryRun = true
	l.LaunchCheck = &classifier.Resolved{Name: "jev", Kind: "jev", LaunchCmd: []string{"jev-use", "judge"}}
	require.True(t, l.Launch(t.Context(), Request{Repo: "o/r", N: 1, PR: pr("bob"), Trigger: "noise", Gate: gate}))
	require.Len(t, c.args, 1) // asked, said skip, nothing started
}

func TestNoGateNeverAsks(t *testing.T) {
	l, c := newLauncher(t, "")
	l.DryRun = true
	l.LaunchCheck = &classifier.Resolved{Name: "jev", Kind: "jev", LaunchCmd: []string{"jev-use", "judge"}}
	require.True(t, l.Launch(t.Context(), Request{Repo: "o/r", N: 1, PR: pr("bob"), Trigger: "👀"}))
	require.Empty(t, c.args)
}

func TestRunningAgentKeepsEventPendingWithoutAsking(t *testing.T) {
	l, c := newLauncher(t, "")
	l.LaunchCheck = &classifier.Resolved{Name: "jev", Kind: "jev", LaunchCmd: []string{"jev-use", "judge"}}
	require.NoError(t, os.WriteFile(l.lock(t, 1), []byte("{}"), 0o600))
	require.False(t, l.Launch(t.Context(), Request{Repo: "o/r", N: 1, PR: pr("bob"), Trigger: "t", Gate: gate}))
	require.Empty(t, c.args)
}

func TestAgentLimitKeepsEventPendingWithoutAsking(t *testing.T) {
	l, c := newLauncher(t, "max_agents: 1\n")
	l.LaunchCheck = &classifier.Resolved{Name: "jev", Kind: "jev", LaunchCmd: []string{"jev-use", "judge"}}
	require.NoError(t, os.WriteFile(l.lock(t, 2), []byte("{}"), 0o600)) // another PR's agent
	require.False(t, l.Launch(t.Context(), Request{Repo: "o/r", N: 1, PR: pr("bob"), Trigger: "t", Gate: gate}))
	require.Empty(t, c.args)
}

func TestLaunchErrorsKeepEventPending(t *testing.T) {
	l, _ := newLauncher(t, "")
	l.Run = func(_ context.Context, c proc.Cmd) (proc.Result, error) {
		return proc.Result{Code: 1}, &proc.Error{Args: c.Args, Code: 1, Stderr: "network down"}
	}
	require.False(t, l.Launch(t.Context(), Request{Repo: "o/r", N: 1, PR: pr("bob"), Trigger: "t"}))
	_, err := os.Stat(l.lock(t, 1))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestFailedOpenRemovesTheLock(t *testing.T) {
	l, _ := newLauncher(t, "")
	l.Run = func(_ context.Context, c proc.Cmd) (proc.Result, error) {
		if c.Args[0] == "tmux" && c.Args[1] == "new-session" {
			return proc.Result{Code: 1}, &proc.Error{Args: c.Args, Code: 1}
		}
		return proc.Result{}, nil
	}
	require.False(t, l.Launch(t.Context(), Request{Repo: "o/r", N: 1, PR: pr("bob"), Trigger: "t"}))
	_, err := os.Stat(l.lock(t, 1))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestSessionStartsInTmuxWithTheRunner(t *testing.T) {
	r := require.New(t)
	l, c := newLauncher(t, "")
	l.launched(t, "bob")
	last := c.args[len(c.args)-1]
	dir := filepath.Join(l.Root, "sessions", "o__r", "pr-1")
	wt := filepath.Join(l.Root, "worktrees", "o__r", "pr-1")
	r.Equal([]string{"tmux", "new-session", "-d", "-s", "pr-o-r-1", "-c", wt, os.Args[0], "session", "run", dir}, last)
	r.Contains(c.args, []string{"gh", "repo", "clone", "o/r", filepath.Join(l.Root, "repos", "o__r"), "--", "--filter=blob:none"})
	r.Contains(c.args, []string{"gh", "pr", "checkout", "1", "--repo", "o/r", "--branch", "review/pr-1"})
	var meta LockMeta
	raw, err := os.ReadFile(l.lock(t, 1))
	r.NoError(err)
	r.NoError(json.Unmarshal(raw, &meta))
	r.Nil(meta.PID) // stamped by the runner
	r.Equal("pr-o-r-1", *meta.Tmux)
	if runtime.GOOS != "windows" {
		target, err := os.Readlink(filepath.Join(l.Root, "bin", "git"))
		r.NoError(err)
		r.Equal(os.Args[0], target) // the guard is this binary
	}
}

func TestSessionOpensInTheTerminal(t *testing.T) {
	r := require.New(t)
	l, c := newLauncher(t, "")
	l.Launcher = "terminal"
	l.GOOS = "darwin"
	l.Terminal = Terminal{Name: "iterm"}
	l.launched(t, "bob")
	dir := filepath.Join(l.Root, "sessions", "o__r", "pr-1")
	script := filepath.Join(dir, "run-agent.command")
	r.Equal([]string{"open", "-g", "-a", "iTerm", script}, c.args[len(c.args)-1])
	text, err := os.ReadFile(script)
	r.NoError(err)
	r.Contains(string(text), "session run")
}

func TestOthersPRsAreReviewOnlyOwnAsk(t *testing.T) {
	for _, tc := range []struct{ author, push string }{{"bob", "review-only"}, {"me", "ask"}} {
		l, _ := newLauncher(t, "")
		got := l.launched(t, tc.author)
		require.Equal(t, tc.push, got.spec.Env["LLM_REVIEW_AGENT_PUSH"])
		require.Equal(t, tc.push == "review-only", strings.Contains(got.prompt, "REVIEW ONLY"))
	}
}

func TestLocksDropDeadAndNeverStartedAgents(t *testing.T) {
	l, _ := newLauncher(t, "")
	old := float64(time.Now().Add(-2 * LaunchGrace).Unix())
	cases := map[int]string{
		1: fmt.Sprintf(`{"started": %v, "pid": %d, "tmux": null}`, old, os.Getpid()), // running
		2: fmt.Sprintf(`{"started": %v, "pid": %d, "tmux": null}`, old, 1<<22+12345), // exited without cleanup
		3: fmt.Sprintf(`{"started": %v, "pid": null, "tmux": null}`, old),            // runner never started
		4: fmt.Sprintf(`{"started": %v, "pid": null, "tmux": null}`, float64(time.Now().Unix())),
		5: fmt.Sprintf(`{"started": %v, "tmux": null}`, old), // lock from before pid stamping
	}
	for n, meta := range cases {
		require.NoError(t, os.WriteFile(l.lock(t, n), []byte(meta), 0o600))
	}
	var names []string
	for _, p := range Locks(t.Context(), l.Root, 24, l.Run, l.Log) {
		names = append(names, filepath.Base(p))
	}
	require.ElementsMatch(t, []string{"o__r__1.lock", "o__r__4.lock", "o__r__5.lock"}, names)
}

func TestGoneTmuxSessionAndOldLocksAreStale(t *testing.T) {
	l, _ := newLauncher(t, "")
	run := func(_ context.Context, c proc.Cmd) (proc.Result, error) {
		return proc.Result{Code: 1}, &proc.Error{Args: c.Args, Code: 1} // no such tmux session
	}
	require.NoError(t, os.WriteFile(l.lock(t, 1), []byte(`{"tmux": "pr-o-r-1"}`), 0o600))
	old := l.lock(t, 2)
	require.NoError(t, os.WriteFile(old, []byte(`{}`), 0o600))
	require.NoError(t, os.Chtimes(old, time.Now().Add(-25*time.Hour), time.Now().Add(-25*time.Hour)))
	require.Empty(t, Locks(t.Context(), l.Root, 24, run, l.Log))
}

func TestRunnerStampsItsPidRunsTheAgentAndCleansUp(t *testing.T) {
	r := require.New(t)
	l, _ := newLauncher(t, "")
	l.LookPath = func(f string) (string, error) {
		if f == "claude" {
			return os.Args[0], nil
		}
		return "/bin/" + f, nil
	}
	got := l.launched(t, "bob")
	wt := t.TempDir()
	got.spec.Dir = wt
	dir := filepath.Join(l.Root, "sessions", "o__r", "pr-1")
	require.NoError(t, writeJSON(filepath.Join(dir, "session.json"), got.spec))
	t.Setenv("FAKE_AGENT", "1")
	t.Setenv("FAKE_AGENT_LOCK", got.spec.Lock)
	report := filepath.Join(t.TempDir(), "report")
	t.Setenv("FAKE_AGENT_OUT", report)

	var out strings.Builder
	status := Run(dir, strings.NewReader("\n"), &out)
	r.Equal(3, status)
	r.Contains(out.String(), "GitHub PR review agent: o/r#1")
	r.Contains(out.String(), "agent exited: 3")
	raw, err := os.ReadFile(report)
	r.NoError(err)
	text := string(raw)
	r.Contains(text, fmt.Sprintf(`"pid": %d`, os.Getpid()))
	r.Contains(text, "push=review-only")
	r.Contains(text, "path="+filepath.Join(l.Root, "bin"))
	r.Contains(text, "REVIEW ONLY") // the prompt is the last argument
	resolved, _ := filepath.EvalSymlinks(wt)
	r.True(strings.Contains(text, "wd="+wt) || strings.Contains(text, "wd="+resolved))
	_, err = os.Stat(got.spec.Lock)
	r.ErrorIs(err, os.ErrNotExist)
}

// --- terminals --------------------------------------------------------------

func env(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }

func found(names ...string) func(string) (string, error) {
	return func(f string) (string, error) {
		for _, n := range names {
			if n == f {
				return "/usr/bin/" + f, nil
			}
		}
		return "", errors.New("not found")
	}
}

func TestDetectTerminal(t *testing.T) {
	for _, tc := range []struct {
		goos string
		env  map[string]string
		path []string
		want string
	}{
		{"darwin", map[string]string{"TERM_PROGRAM": "iTerm.app"}, nil, "iTerm2 (from $TERM_PROGRAM)"},
		{"darwin", map[string]string{"TERM_PROGRAM": "Apple_Terminal"}, nil, "Terminal.app (from $TERM_PROGRAM)"},
		{"darwin", map[string]string{"TERM_PROGRAM": "tmux", "LC_TERMINAL": "iTerm2"}, nil, "iTerm2 (from $LC_TERMINAL)"},
		{"darwin", map[string]string{"TERM_PROGRAM": "tmux", "__CFBundleIdentifier": "com.mitchellh.ghostty"}, nil, "Ghostty (from $__CFBundleIdentifier)"},
		{"linux", map[string]string{"TERM_PROGRAM": "tmux", "GHOSTTY_RESOURCES_DIR": "/x"}, nil, "Ghostty (from $GHOSTTY_RESOURCES_DIR)"},
		{"linux", map[string]string{"WEZTERM_EXECUTABLE": "/x"}, nil, "WezTerm (from $WEZTERM_EXECUTABLE)"},
		{"linux", map[string]string{"KITTY_WINDOW_ID": "1"}, nil, "kitty (from $KITTY_WINDOW_ID)"},
		{"windows", map[string]string{"WT_SESSION": "x"}, nil, "Windows Terminal (from $WT_SESSION)"},
		{"linux", map[string]string{"TERMINAL": "foot"}, nil, "foot -e {cmd} (from $TERMINAL)"},
		{"darwin", nil, nil, "Terminal.app (fallback)"},
		{"linux", nil, []string{"x-terminal-emulator"}, "x-terminal-emulator (fallback)"},
		{"linux", nil, nil, "none (none found)"},
		{"windows", nil, []string{"wt.exe"}, "Windows Terminal (fallback)"},
		{"windows", nil, nil, "cmd (fallback)"},
		{"darwin", map[string]string{"TERM_PROGRAM": "vscode"}, nil, "Terminal.app (fallback)"},
	} {
		require.Equal(t, tc.want, DetectTerminal(tc.goos, env(tc.env), found(tc.path...)).String(), tc)
	}
}

func TestResolveTerminal(t *testing.T) {
	iterm := env(map[string]string{"TERM_PROGRAM": "iTerm.app"})
	require.Equal(t, "iTerm2 (from $TERM_PROGRAM)", ResolveTerminal(config.Terminal{Name: "auto"}, "darwin", iterm, found()).String())
	require.Equal(t, "kitty (config)", ResolveTerminal(config.Terminal{Name: "kitty"}, "darwin", iterm, found()).String())
	custom := ResolveTerminal(config.Terminal{Command: []string{"alacritty", "-e", "{cmd}"}}, "linux", iterm, found())
	require.Equal(t, []string{"alacritty", "-e", "/x", "session", "run", "/s"}, custom.OpenCommand("linux", "", "t", []string{"/x", "session", "run", "/s"}))
}

func TestOpenCommands(t *testing.T) {
	cmd := []string{"/bin/lra", "session", "run", "/s dir"}
	for _, tc := range []struct {
		name, goos string
		want       []string
	}{
		{"terminal-app", "darwin", []string{"open", "-g", "-a", "Terminal", "/s/run-agent.command"}},
		{"iterm", "darwin", []string{"open", "-g", "-a", "iTerm", "/s/run-agent.command"}},
		{"ghostty", "linux", append([]string{"ghostty", "-e"}, cmd...)},
		{"wezterm", "darwin", append([]string{"open", "-g", "-n", "-a", "WezTerm", "--args", "start", "--"}, cmd...)},
		{"windows-terminal", "windows", append([]string{"wt.exe", "-w", "new", "new-tab", "--title", "PR o/r#1"}, cmd...)},
		{"cmd", "windows", append([]string{"cmd", "/c", "start", "PR o/r#1"}, cmd...)},
		{"x-terminal-emulator", "linux", append([]string{"x-terminal-emulator", "-e"}, cmd...)},
	} {
		require.Equal(t, tc.want, Terminal{Name: tc.name}.OpenCommand(tc.goos, "/s/run-agent.command", "PR o/r#1", cmd), tc.name)
	}
	quoted := Terminal{Command: []string{"sh", "-c", "exec {cmd}"}}.OpenCommand("linux", "", "", cmd)
	require.Equal(t, []string{"sh", "-c", "exec /bin/lra session run '/s dir'"}, quoted)
}
