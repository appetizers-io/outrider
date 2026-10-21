package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/appetizers-io/outrider/internal/proc"
	"github.com/appetizers-io/outrider/internal/watch"
)

func TestCrewManualCaptureUsesOwnershipWorkflowAndDeduplicates(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(source, []byte("repos: {include: [o/r]}\nworkflows:\n  - name: technical-review\n    match: [{prs: others, events: [opt_in]}]\n    steps: [Inspect all code]\n"), 0600))
	run := func(_ context.Context, c proc.Cmd) (proc.Result, error) {
		require.Equal(t, "gh", c.Args[0])
		args := strings.Join(c.Args, " ")
		switch {
		case args == "gh api user":
			return proc.Result{Stdout: `{"login":"me"}`}, nil
		case strings.Contains(args, "pr view"):
			return proc.Result{Stdout: `{"headRefOid":"abc","state":"OPEN","title":"Fix API","author":{"login":"other"},"statusCheckRollup":[]}`}, nil
		case strings.Contains(args, "pr diff"):
			return proc.Result{Stdout: "diff"}, nil
		case strings.Contains(args, "/comments?"):
			return proc.Result{Stdout: `[[]]`}, nil
		default:
			t.Fatalf("unexpected command %s", args)
			return proc.Result{}, nil
		}
	}
	var ids []string
	for range 2 {
		cmd := crewCmd(watch.Deps{Run: run})
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs([]string{"capture", "--repo", "o/r", "--pr", "7", "--config", source, "--state-dir", dir})
		require.NoError(t, cmd.Execute())
		var item struct {
			ID       string
			Workflow string
		}
		require.NoError(t, json.Unmarshal(out.Bytes(), &item))
		require.Equal(t, "technical-review", item.Workflow)
		ids = append(ids, item.ID)
	}
	require.Equal(t, ids[0], ids[1])
	paths, err := filepath.Glob(filepath.Join(dir, "queue", "*.json"))
	require.NoError(t, err)
	require.Len(t, paths, 1)
	raw, err := os.ReadFile(paths[0])
	require.NoError(t, err)
	require.Contains(t, string(raw), "manual opt-in from Crew")
}

func TestCrewManualCaptureRequiresExplicitConfigAndSubscription(t *testing.T) {
	cmd := crewCmd(watch.Deps{})
	cmd.SetArgs([]string{"capture", "--repo", "o/r", "--pr", "1", "--state-dir", t.TempDir()})
	require.ErrorContains(t, cmd.Execute(), "--config")
	dir := t.TempDir()
	source := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(source, []byte("repos: {include: [other/repo]}"), 0600))
	cmd = crewCmd(watch.Deps{})
	cmd.SetArgs([]string{"capture", "--repo", "o/r", "--pr", "1", "--config", source, "--state-dir", dir})
	require.ErrorContains(t, cmd.Execute(), "add the repository")
}
