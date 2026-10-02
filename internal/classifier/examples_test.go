package classifier

import (
	"encoding/json"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/appetizers-io/outrider/internal/github"
	"github.com/appetizers-io/outrider/internal/proc"
)

// The example scripts in docs/examples/classifiers must work as documented.

func skipWithoutSh(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the examples are POSIX sh scripts")
	}
}

func TestDocsExampleLaunchCheck(t *testing.T) {
	skipWithoutSh(t)
	r := &Resolved{Name: "local", Kind: "command", LaunchCmd: []string{"sh", "../../docs/examples/classifiers/launch-check.sh"}, Timeout: 10 * time.Second}
	act := func(user string) github.Activity {
		return github.Activity{Kind: "comment", User: &user, At: "2026-01-01T00:00:00Z", Body: "hi"}
	}
	for _, tc := range []struct {
		name    string
		items   []github.Activity
		failing bool
		launch  bool
	}{
		{"only bots, CI green", []github.Activity{act("netlify[bot]"), act("dependabot[bot]")}, false, false},
		{"a human", []github.Activity{act("netlify[bot]"), act("alice")}, false, true},
		{"only bots, CI failing", []github.Activity{act("netlify[bot]")}, true, true},
		{"nothing new", nil, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pr := github.PR{Title: "T", Author: &github.User{Login: "me"}}
			if tc.failing {
				pr.StatusCheckRollup = []github.Check{{Name: "test", Conclusion: "FAILURE"}}
			}
			req := NewRequest("o/r", 1, pr, "t", tc.items, "Mona", "me")
			launch, note := CheckLaunch(t.Context(), r, req, 0.5, proc.Exec)
			require.Equal(t, tc.launch, launch, note)
		})
	}
}

func TestDocsExampleGateHook(t *testing.T) {
	skipWithoutSh(t)
	call := func(tool string, input map[string]string, indent bool) int {
		v := map[string]any{"hook_event_name": "PreToolUse", "tool_name": tool, "tool_input": input}
		raw, err := json.Marshal(v)
		if indent {
			raw, err = json.MarshalIndent(v, "", "  ")
		}
		require.NoError(t, err)
		cmd := exec.Command("sh", "../../docs/examples/classifiers/gate-hook.sh")
		cmd.Stdin = strings.NewReader(string(raw))
		cmd.Env = append(cmd.Environ(), "OUTRIDER_GATE_TEXT=review only")
		var exitErr *exec.ExitError
		if err := cmd.Run(); errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		} else {
			require.NoError(t, err)
		}
		return 0
	}
	for _, indent := range []bool{false, true} {
		require.Equal(t, 2, call("Bash", map[string]string{"command": "git push --force origin HEAD"}, indent))
		require.Equal(t, 2, call("Bash", map[string]string{"command": "rm -rf build"}, indent))
		require.Equal(t, 2, call("Write", map[string]string{"file_path": "/wt/generated/api.go", "content": "x"}, indent))
		require.Equal(t, 0, call("Bash", map[string]string{"command": "go test ./..."}, indent))
		require.Equal(t, 0, call("Edit", map[string]string{"file_path": "/wt/main.go"}, indent))
	}
}
