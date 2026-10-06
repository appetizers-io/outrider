package github

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/appetizers-io/outrider/internal/proc"
)

func TestReviewContextPinsBaseAndPrefetchesOneParentLevel(t *testing.T) {
	base := strings.Repeat("a", 40)
	var calls [][]string
	client := &Client{Run: func(_ context.Context, cmd proc.Cmd) (proc.Result, error) {
		calls = append(calls, cmd.Args)
		switch {
		case cmd.Args[0] == "git" && cmd.Args[1] == "ls-tree":
			return proc.Result{Stdout: "docs/spec.md\x00src/app.go\x00"}, nil
		case cmd.Args[0] == "git" && cmd.Args[1] == "show":
			require.Equal(t, base+":docs/spec.md", cmd.Args[2])
			return proc.Result{Stdout: "BASE SPEC"}, nil
		case cmd.Args[1] == "pr":
			return proc.Result{Stdout: `{"baseRefOid":"` + base + `","body":"Fixes #4; related other/repo#8 and https://github.com/o/r/issues/4", "closingIssuesReferences":[{"url":"https://github.com/o/r/issues/4"}]}`}, nil
		case strings.HasSuffix(cmd.Args[2], "/parent"):
			return proc.Result{Stdout: `{"html_url":"https://github.com/o/r/issues/1"}`}, nil
		default:
			return proc.Result{Stdout: `{"number":4,"body":"Issue text"}`}, nil
		}
	}}
	dir := t.TempDir()
	require.NoError(t, client.SaveReviewContext(t.Context(), "o/r", 7, "/checkout", true, []string{"docs/**"}, dir))
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	require.NoError(t, err)
	var manifest reviewManifest
	require.NoError(t, json.Unmarshal(raw, &manifest))
	require.Equal(t, base, manifest.Base)
	require.Len(t, manifest.Issues, 3)
	require.Len(t, manifest.Docs, 1)
	require.Contains(t, manifest.Docs[0].URL, "/blob/"+base+"/docs/spec.md")
	require.NotContains(t, calls, []string{"gh", "api", "repos/o/r/issues/1/parent"})
	raw, err = os.ReadFile(filepath.Join(dir, manifest.Docs[0].File))
	require.NoError(t, err)
	require.Equal(t, "BASE SPEC", string(raw))
}

func TestReviewContextCapsIssueCountAndSize(t *testing.T) {
	refs := ""
	for i := 1; i <= 30; i++ {
		refs += " #" + strconv.Itoa(i)
	}
	body, _ := json.Marshal(map[string]any{"baseRefOid": strings.Repeat("a", 40), "body": refs})
	client := &Client{Run: func(_ context.Context, cmd proc.Cmd) (proc.Result, error) {
		if cmd.Args[1] == "pr" {
			return proc.Result{Stdout: string(body)}, nil
		}
		if strings.HasSuffix(cmd.Args[2], "/parent") {
			return proc.Result{Stdout: `{}`}, nil
		}
		raw, _ := json.Marshal(map[string]any{"body": strings.Repeat("x", MaxContextFile+100)})
		return proc.Result{Stdout: string(raw)}, nil
	}}
	dir := t.TempDir()
	require.NoError(t, client.SaveReviewContext(t.Context(), "o/r", 7, "", true, nil, dir))
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	require.NoError(t, err)
	var manifest reviewManifest
	require.NoError(t, json.Unmarshal(raw, &manifest))
	require.Len(t, manifest.Issues, maxReviewItems)
	total := 0
	for _, source := range manifest.Issues {
		if source.File != "" {
			info, err := os.Stat(filepath.Join(dir, source.File))
			require.NoError(t, err)
			total += int(info.Size())
		}
	}
	require.LessOrEqual(t, total, maxReviewBytes)
}
