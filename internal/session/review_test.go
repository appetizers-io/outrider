package session

import (
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gotest.tools/v3/golden"

	"github.com/appetizers-io/outrider/internal/config"
)

func TestReviewProfileGoldenScopeMatrix(t *testing.T) {
	for _, profile := range []string{"quick", "standard", "deep"} {
		for _, scope := range []string{"pr", "mention", "reply"} {
			t.Run(profile+"-"+scope, func(t *testing.T) {
				body, err := readProfileFixture(profile)
				require.NoError(t, err)
				in := PromptInput{Repo: "o/r", N: 1, PR: pr("bob"), Owner: "Matthias", Login: "me", Push: "review-only", GHWrites: "never", PolicyFile: "/session/policy.json",
					Review: &Review{Name: profile, Body: body, Tests: "off", Comments: "draft", Context: "/session/review-context"}}
				if scope != "pr" {
					in.Scope = []string{"https://github.com/o/r/pull/1#comment-2"}
					in.ScopeWhy = scope
				}
				text, err := Prompt(in)
				require.NoError(t, err)
				golden.Assert(t, text, "review-"+profile+"-"+scope+".txt")
				if scope != "pr" {
					require.Contains(t, text, "ONLY about the comment")
				}
			})
		}
	}
}

func readProfileFixture(name string) (string, error) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "prompts", "profiles", name+".md"))
	return string(raw), err
}

func TestProfileTrustClampingModelArgsAndPlaybook(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		l, _ := newLauncher(t, `review:
  default: deep
  trusted_authors: [bob]
  profiles:
    deep:
      model: {claude: opus, codex: test-model}
      effort: high
      max_minutes: 10
      evidence: {tests: local, comments: draft}
`)
		l.Cfg.Agent = agent
		got := l.launched(t, "bob")
		require.NotNil(t, got.spec.Review)
		require.Equal(t, "local", got.spec.Review.Tests)
		require.Contains(t, got.prompt, "playbook can't widen")
		require.NotContains(t, got.policy["tool_gate"].(map[string]any)["rules"], "REVIEW PROFILE: deep")
		require.Equal(t, 10, got.spec.Review.MaxMinutes)
		if agent == "claude" {
			require.Contains(t, got.spec.Agent, "--model")
			require.Contains(t, got.spec.Agent, "--effort")
			require.Contains(t, got.deny(), "Write")

			require.Contains(t, got.settings["sandbox"].(map[string]any)["filesystem"].(map[string]any)["allowWrite"], got.spec.Review.Outbox)
		} else {
			require.Contains(t, got.spec.Agent, "-m")
			require.Contains(t, got.spec.Agent, "model_reasoning_effort=high")
		}
		cfg := l.Cfg.For("o/r", false)
		resolved, err := l.review(Request{Repo: "o/r", PR: pr("mallory")}, &cfg, t.TempDir(), true)
		require.NoError(t, err)
		require.Equal(t, "off", resolved.Tests)
		require.Empty(t, resolved.Outbox)
		own, err := l.review(Request{Repo: "o/r", PR: pr("me")}, &cfg, t.TempDir(), false)
		require.NoError(t, err)
		require.Nil(t, own)
	}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "trusted.md"), []byte("trusted playbook"), 0o600))
	raw, err := readPlaybook(dir, "trusted.md")
	require.NoError(t, err)
	require.Equal(t, "trusted playbook", string(raw))
	_, err = readPlaybook(dir, "trusted.md", dir)
	require.ErrorContains(t, err, "PR repository")
	_, err = readPlaybook(dir, "missing.md")
	require.Error(t, err)
	outside := filepath.Join(t.TempDir(), "untrusted.md")
	require.NoError(t, os.WriteFile(outside, []byte("untrusted"), 0o600))
	_, err = readPlaybook(dir, outside)
	require.Error(t, err)
	if runtime.GOOS != "windows" {
		require.NoError(t, os.Symlink(outside, filepath.Join(dir, "escape.md")))
		_, err = readPlaybook(dir, "escape.md")
		require.Error(t, err)
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "huge.md"), []byte(strings.Repeat("x", maxPlaybook+1)), 0o600))
	_, err = readPlaybook(dir, "huge.md")
	require.ErrorContains(t, err, "exceeds")
	l, _ := newLauncher(t, "review: {default: deep, profiles: {deep: {playbook: trusted.md}}}")
	l.ConfigSource = filepath.Join(dir, "config.yaml")
	resolved, err := l.review(Request{Repo: "o/r", PR: pr("bob")}, l.Cfg, t.TempDir(), false)
	require.NoError(t, err)
	require.Len(t, resolved.PlaybookSHA256, 64)
}

func TestReviewConfigRoundTrip(t *testing.T) {
	cfg, err := config.Parse([]byte("review: {default: quick}"), "test.yaml")
	require.NoError(t, err)
	_, err = config.Validate(cfg, "test.yaml")
	require.NoError(t, err)
}

func TestReviewTimeoutStopsAnUncooperativeAgent(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestReviewTimeoutHelper$")
	cmd.Env = append(os.Environ(), "OUTRIDER_TIMEOUT_HELPER=1")
	var output strings.Builder
	start := time.Now()
	timedOut, err := runAgent(cmd, 200*time.Millisecond, 50*time.Millisecond, &output)
	require.True(t, timedOut)
	require.Error(t, err)
	require.Less(t, time.Since(start), 5*time.Second)
	require.Contains(t, output.String(), "time limit reached")
}

func TestReviewTimeoutHelper(t *testing.T) {
	if os.Getenv("OUTRIDER_TIMEOUT_HELPER") != "1" {
		t.Skip("subprocess helper")
	}
	signal.Ignore(os.Interrupt, syscall.SIGTERM)
	time.Sleep(time.Hour)
}

func TestIsolationProfileGetsOnlyAGuestOutbox(t *testing.T) {
	l, _ := newLauncher(t, "mode: autonomous\nisolation: {enabled: true}\nreview: {default: standard, profiles: {standard: {evidence: {comments: draft}}}}")
	l.Cfg.Agent = "codex"
	l.CodexHome = t.TempDir()
	l.Cfg.Isolation.GuardBinary = new(linuxGuard(t))
	got := l.launched(t, "bob")
	require.NotNil(t, got.spec.Review)
	require.DirExists(t, filepath.Join(got.spec.Isolation.Inputs, "outbox"))
	raw, err := os.ReadFile(filepath.Join(got.spec.Isolation.Inputs, "policy.json"))
	require.NoError(t, err)
	require.Contains(t, string(raw), `"outbox": "/tmp/outrider/outbox"`)
	require.NotContains(t, string(raw), got.spec.Review.Outbox)
}
