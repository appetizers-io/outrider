package session

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNetworkAccessLaunch(t *testing.T) {
	for _, agent := range []string{"codex", "claude"} {
		for _, mode := range []string{"supervised", "autonomous"} {
			for _, value := range []string{"null", "true", "false"} {
				t.Run(agent+"/"+mode+"/"+value, func(t *testing.T) {
					l, _ := newLauncher(t, "mode: "+mode+"\nnetwork_access: "+value)
					l.Cfg.Agent = agent
					got := l.launched(t, "me")
					if value == "null" {
						require.NotContains(t, strings.Join(got.spec.Agent, " "), "network_access")
						require.NotContains(t, got.settings, "sandbox")
						require.NotContains(t, got.policy, "network_access")
						return
					}
					require.Equal(t, value == "true", got.policy["network_access"])
					require.Equal(t, mode == "supervised", got.spec.Env["OUTRIDER_PUSH"] == "ask")
					require.Equal(t, mode == "supervised", got.spec.Env["OUTRIDER_GH_WRITES"] == "ask")
					if agent == "codex" {
						require.Contains(t, got.spec.Agent, "sandbox_workspace_write.network_access="+value)
						require.Nil(t, got.settings)
					} else {
						require.True(t, got.usesSettings())
						want := `{"network":{"allowedDomains":["*"]}}`
						if value == "false" {
							want = `{"network":{"deniedDomains":["*"],"strictAllowlist":true}}`
						}
						require.JSONEq(t, want, asJSON(t, got.settings["sandbox"]))
						if mode == "supervised" {
							require.NotEmpty(t, got.deny())
						} else {
							require.NotContains(t, got.settings, "permissions")
							require.NotContains(t, got.settings, "hooks")
						}
					}
				})
			}
		}
	}
}

func TestNetworkAccessFalsePreservesReadOnlySandbox(t *testing.T) {
	l, _ := newLauncher(t, "sandbox: read-only\nnetwork_access: false")
	got := l.launched(t, "bob")
	sandbox := got.settings["sandbox"].(map[string]any)
	require.Equal(t, true, sandbox["enabled"])
	require.Equal(t, false, sandbox["allowUnsandboxedCommands"])
	require.JSONEq(t, `{"allowedDomains":[],"strictAllowlist":true}`, asJSON(t, sandbox["network"]))
	require.Equal(t, "never", got.spec.Env["OUTRIDER_GH_WRITES"])
}
