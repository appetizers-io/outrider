package cli

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/appetizers-io/outrider/internal/forgejo/forgejotest"
	"github.com/appetizers-io/outrider/internal/watch"
)

const forgeToken = "render-secret-token-9f8e7d6c5b4a"

// renderSetup is a fake forge, a config that points at it and a machine
// whose environment holds the token.
func renderSetup(t *testing.T, token string) (watch.Deps, string, *forgejotest.Server) {
	t.Helper()
	isolate(t)
	srv := forgejotest.New(t, forgeToken)
	d := sandbox(t)
	d.Getenv = func(k string) string {
		if k == "FORGE_TOKEN" {
			return token
		}
		return os.Getenv(k)
	}
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(cfg, []byte(`
forges:
  - kind: forgejo
    host: forge.example
    url: `+srv.URL+`
    access: api
    token_env: FORGE_TOKEN
workflows:
  - name: pipeline-review
    match: [{repo: "forge.example/o/*"}]
    steps: [Read the linked issues., Review the diff.]
review:
  default: standard
  profiles:
    standard:
      context: {issues: true, docs: ["docs/**"]}
prompts:
  extra: House rules apply.
`), 0o600))
	return d, cfg, srv
}

func TestRenderWritesContextAndPrintsThePrompt(t *testing.T) {
	r := require.New(t)
	d, cfg, _ := renderSetup(t, forgeToken)
	dir := filepath.Join(t.TempDir(), "host-ctx")
	res := cli(t, d, "render", "--config", cfg, "--repo", "forge.example/o/r", "--pr", "7",
		"--workflow", "pipeline-review", "--context-dir", dir, "--prompt-context-dir", "/agent/ctx", "--log-level", "debug")
	r.Equal(0, res.code, res.stderr)
	var got struct {
		Prompt           string   `json:"prompt"`
		Own              bool     `json:"own"`
		Author           string   `json:"author"`
		Fork             bool     `json:"fork"`
		HeadMine         bool     `json:"head_mine"`
		BaseRef          string   `json:"base_ref"`
		HeadRef          string   `json:"head_ref"`
		Repo             string   `json:"repo"`
		Number           int      `json:"number"`
		Workflow         string   `json:"workflow"`
		Review           string   `json:"review"`
		ContextDir       string   `json:"context_dir"`
		PromptContextDir string   `json:"prompt_context_dir"`
		Files            []string `json:"files"`
	}
	r.NoError(json.Unmarshal([]byte(res.stdout), &got))
	r.False(got.Own)
	r.Equal("alice", got.Author)
	r.True(got.Fork) // the head is in alice/r-fork
	r.False(got.HeadMine)
	r.Equal("main", got.BaseRef)
	r.Equal("feat/x", got.HeadRef)
	r.Equal("forge.example/o/r", got.Repo)
	r.Equal(7, got.Number)
	r.Equal("pipeline-review", got.Workflow)
	r.Equal("standard", got.Review)
	r.Equal(dir, got.ContextDir)
	r.Equal("/agent/ctx", got.PromptContextDir)
	r.Equal([]string{"pr.json", "pr-files.txt", "pr.diff", "review-comments.json", "failing-checks.json", "review-context/"}, got.Files)

	p := got.Prompt
	r.True(strings.HasPrefix(p, "Babysit this Forgejo PR locally."), p)
	r.Contains(p, "READ-ONLY, OFFLINE")
	r.Contains(p, "fetched for you into /agent/ctx:")
	r.Contains(p, "/agent/ctx/review-context")
	r.NotContains(p, dir) // the agent sees the context elsewhere
	r.Contains(p, "WORKFLOW: pipeline-review")
	r.Contains(p, "1. Read the linked issues.")
	r.Contains(p, "House rules apply.")
	r.Contains(p, "FORGEJO: read-only in this session.")
	r.NotContains(p, "gh pr")
	r.NotContains(p, "GitHub")

	r.Equal(forgejotest.Diff, readFile(t, filepath.Join(dir, "pr.diff")))
	r.FileExists(filepath.Join(dir, "review-context", "manifest.json"))
	r.FileExists(filepath.Join(dir, "review-context", "doc-01.txt"))
}

func TestRenderRefusesUnknownInput(t *testing.T) {
	d, cfg, _ := renderSetup(t, forgeToken)
	dir := t.TempDir()
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--repo", "o/r", "--pr", "7"}, "want host/owner/repo"},
		{[]string{"--repo", "other.example/o/r", "--pr", "7"}, "no forge for host other.example"},
		{[]string{"--repo", "forge.example/o/r", "--pr", "0"}, "--pr must be"},
		{[]string{"--repo", "forge.example/o/r", "--pr", "7", "--workflow", "nope"}, `no workflow "nope"`},
		{[]string{"--repo", "forge.example/o/r", "--pr", "7", "--workspace", "writable"}, `--workspace "writable": want readonly or disposable`},
	} {
		res := cli(t, d, append([]string{"render", "--config", cfg, "--context-dir", dir}, c.args...)...)
		require.Equal(t, 1, res.code)
		require.Contains(t, res.stderr, c.want)
	}
}

// --workspace disposable lets the agent edit and test in its working
// directory; the default is the read-only prompt.
func TestRenderWorkspace(t *testing.T) {
	r := require.New(t)
	d, cfg, _ := renderSetup(t, forgeToken)
	prompt := func(args ...string) string {
		res := cli(t, d, append([]string{"render", "--config", cfg, "--repo", "forge.example/o/r", "--pr", "7",
			"--workflow", "pipeline-review", "--context-dir", t.TempDir(), "--prompt-context-dir", "/agent/ctx"}, args...)...)
		r.Equal(0, res.code, res.stderr)
		var got struct {
			Prompt string `json:"prompt"`
		}
		r.NoError(json.Unmarshal([]byte(res.stdout), &got))
		return got.Prompt
	}
	readonly := prompt()
	r.Equal(readonly, prompt("--workspace", "readonly"))
	r.Contains(readonly, "READ-ONLY, OFFLINE")
	r.NotContains(readonly, "DISPOSABLE")

	disposable := prompt("--workspace", "disposable")
	r.Contains(disposable, "DISPOSABLE WORKSPACE, OFFLINE")
	r.Contains(disposable, "you may create and edit files and run local tests and scripts in it.")
	r.Contains(disposable, "FORGEJO: read-only in this session.")
	r.NotContains(disposable, "READ-ONLY, OFFLINE")
}

// The prompt cites the agent's path, whatever OS renders it.
func TestAgentPathKeepsTheAgentsSeparator(t *testing.T) {
	for _, c := range []struct{ dir, want string }{
		{"/agent/ctx", "/agent/ctx/review-context"},
		{"/agent/ctx/", "/agent/ctx/review-context"},
		{"/", "/review-context"},
		{`C:\agent\ctx`, `C:\agent\ctx\review-context`},
		{`C:\agent\ctx\`, `C:\agent\ctx\review-context`},
		{"C:/agent/ctx", "C:/agent/ctx/review-context"},
	} {
		require.Equal(t, c.want, agentPath(c.dir, "review-context"), c.dir)
	}
}

// The token reaches the forge in the Authorization header and nothing
// else: not the output, the logs, the prompt, the context files or an error.
func TestRenderNeverShowsTheToken(t *testing.T) {
	r := require.New(t)
	d, cfg, srv := renderSetup(t, forgeToken)
	dir := t.TempDir()
	res := cli(t, d, "render", "--config", cfg, "--repo", "forge.example/o/r", "--pr", "7",
		"--workflow", "pipeline-review", "--context-dir", dir, "--log-level", "debug")
	r.Equal(0, res.code, res.stderr)
	r.NotEmpty(srv.Auth)
	r.NotContains(res.stdout, forgeToken)
	r.NotContains(res.stderr, forgeToken)
	files := 0
	r.NoError(filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
		r.NoError(err)
		if !e.IsDir() {
			files++
			r.NotContains(readFile(t, path), forgeToken, path)
		}
		return nil
	}))
	r.Positive(files)

	// a wrong token: the server echoes the header it got into its error
	d, cfg, srv = renderSetup(t, forgeToken+"-wrong")
	res = cli(t, d, "render", "--config", cfg, "--repo", "forge.example/o/r", "--pr", "7", "--context-dir", t.TempDir(), "--log-level", "debug")
	r.Equal(1, res.code)
	r.Contains(res.stderr, "401")
	r.Contains(res.stderr, "token is invalid: token [redacted]")
	r.NotContains(res.stderr, forgeToken)
	r.NotContains(res.stdout, forgeToken)
	r.Contains(srv.Auth[0], forgeToken+"-wrong") // it was sent, in the header

	// no token at all: the error names the variable only
	d, cfg, _ = renderSetup(t, "")
	res = cli(t, d, "render", "--config", cfg, "--repo", "forge.example/o/r", "--pr", "7", "--context-dir", t.TempDir())
	r.Equal(1, res.code)
	r.Contains(res.stderr, "$FORGE_TOKEN is empty or unset")
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(raw)
}
