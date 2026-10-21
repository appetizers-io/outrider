package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/appetizers-io/outrider/internal/proc"
	"github.com/appetizers-io/outrider/internal/watch"
)

func TestCrewSessionPromptUsesExplicitConfigAndVerifiedFork(t *testing.T) {
	for _, author := range []string{"me", "other"} {
		t.Run(author, func(t *testing.T) {
			run := func(_ context.Context, c proc.Cmd) (proc.Result, error) {
				args := strings.Join(c.Args, " ")
				switch {
				case args == "gh api user":
					return proc.Result{Stdout: `{"login":"me"}`}, nil
				case strings.HasPrefix(args, "gh pr view"):
					return proc.Result{Stdout: `{"author":{"login":"` + author + `"},"title":"Repair API","baseRefName":"main","headRefName":"fix/api","state":"OPEN"}`}, nil
				case args == "gh api repos/me/fork":
					return proc.Result{Stdout: `{"fork":true,"owner":{"login":"me"},"parent":{"full_name":"o/r"}}`}, nil
				default:
					t.Fatalf("unexpected command %s", args)
					return proc.Result{}, nil
				}
			}
			cmd := crewCmd(watch.Deps{Run: run})
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetArgs([]string{"session-prompt"})
			cmd.SetIn(strings.NewReader(`{"item":{"repo":"o/r","pr":1,"trigger":"mention","scope":["https://github.com/o/r/pull/1#issuecomment-2"]},"remote":"upstream","origin":"me/fork","branch":"review/local","settings":{"prompts":{"extra":"others guidance"},"overrides":[{"match":[{"prs":"own"}],"prompts":{"extra":"own guidance"}}]}}`))
			require.NoError(t, cmd.Execute())
			var result map[string]string
			require.NoError(t, json.Unmarshal(out.Bytes(), &result))
			p := result["prompt"]
			require.Contains(t, p, "Repair API")
			require.Contains(t, p, "issuecomment-2")
			if author == "me" {
				require.Contains(t, p, "OWN PR")
				require.Contains(t, p, "own guidance")
			} else {
				require.Contains(t, p, "REVIEW WITH EVIDENCE")
				require.Contains(t, p, "me/fork")
				require.Contains(t, p, "others guidance")
			}
		})
	}
}
func TestCrewImportPreservesPollingAndTaskPolicies(t *testing.T) {
	cmd := crewCmd(watch.Deps{})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"import-settings", "--repo", "o/r"})
	cmd.SetIn(strings.NewReader("interval_seconds: 10\nlookback_hours: 4\ncandidate_limit: 12\npush: never\ngithub_writes: never\nothers_prs: {allow_push: false, review_forks: [me/fork], sandbox: read-only}\n"))
	require.NoError(t, cmd.Execute())
	var result struct {
		Polling  map[string]int
		Settings map[string]any
		Skipped  []string
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &result))
	require.Equal(t, map[string]int{"interval_seconds": 10, "lookback_hours": 4, "candidate_limit": 12}, result.Polling)
	require.Equal(t, "never", result.Settings["push"])
	require.Equal(t, "never", result.Settings["github_writes"])
	require.Contains(t, result.Skipped, "others_prs.sandbox")
	require.NotContains(t, result.Skipped, "lookback_hours")
	require.Equal(t, []any{"me/fork"}, result.Settings["others_prs"].(map[string]any)["review_forks"])
}
