package classifier

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/appetizers-io/outrider/internal/config"
	"github.com/appetizers-io/outrider/internal/proc"
)

func TestJevEnvKeepsOnlyWhatJevNeeds(t *testing.T) {
	env := jevEnv([]string{
		"PATH=/usr/bin", "HOME=/home/me", "LANG=C.UTF-8", "HTTPS_PROXY=http://proxy:3128",
		"OPENROUTER_API_KEY=backend", "JEV_MODEL=small", "TYPESAFE_PROJECT=p",
		"GH_TOKEN=secret", "GITHUB_TOKEN=secret", "AWS_SECRET_ACCESS_KEY=secret",
		"KIROCREW_PROXY_SECRET=secret", "OUTRIDER_BIN=/usr/local/bin/outrider", "malformed",
	})
	require.ElementsMatch(t, []string{
		"PATH=/usr/bin", "HOME=/home/me", "LANG=C.UTF-8", "HTTPS_PROXY=http://proxy:3128",
		"OPENROUTER_API_KEY=backend", "JEV_MODEL=small", "TYPESAFE_PROJECT=p",
	}, env)
	// An empty result must still replace the environment rather than inherit it.
	require.NotNil(t, jevEnv([]string{"GH_TOKEN=secret"}))
	require.Empty(t, jevEnv([]string{"GH_TOKEN=secret"}))
}

func TestLaunchCheckRunsJevWithoutUnrelatedSecrets(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("OPENROUTER_API_KEY", "backend")
	t.Setenv("GH_TOKEN", "secret")
	jev, note := Resolve("jev", config.DefaultJev(), func(string) (string, error) { return "/usr/bin/npx", nil })
	require.NotNil(t, jev, note)
	require.NotContains(t, jev.Env, "GH_TOKEN=secret")
	require.Contains(t, jev.Env, "OPENROUTER_API_KEY=backend")

	var got proc.Cmd
	run := func(_ context.Context, c proc.Cmd) (proc.Result, error) {
		got = c
		return proc.Result{Stdout: `{"answers":{"act":{"probability":0.9}}}`}, nil
	}
	launch, _ := CheckLaunch(context.Background(), jev, Request{Owner: "me"}, 0.5, run)
	require.True(t, launch)
	require.Equal(t, jev.Env, got.Env)

	// A command classifier is the user's own binary and keeps the environment.
	cfg := config.Classifier{Kind: config.KindCommand, Command: config.CommandClassifier{
		Kind: config.KindCommand, Enabled: true, TimeoutSeconds: 5,
	}, LaunchCommand: ptr("my-check"), HookCommand: ptr("my-gate")}
	own, note := Resolve("mine", cfg, func(string) (string, error) { return "/usr/bin/my-check", nil })
	require.NotNil(t, own, note)
	require.Nil(t, own.Env)
}

func ptr(value string) *string { return &value }
