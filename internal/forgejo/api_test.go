package forgejo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/appetizers-io/outrider/internal/config"
	"github.com/appetizers-io/outrider/internal/forgejo/forgejotest"
	"github.com/appetizers-io/outrider/internal/github"
)

const token = "fj-secret-token-0123456789"

func apiForge(srv *forgejotest.Server) config.Forge {
	return config.Forge{Kind: "forgejo", Host: "forge.example", URL: &srv.URL, Access: "api", TokenEnv: "FORGEJO_TOKEN"}
}

func openAPI(t *testing.T, srv *forgejotest.Server) Reader {
	t.Helper()
	r, err := Open(apiForge(srv), Deps{Getenv: func(k string) string {
		require.Equal(t, "FORGEJO_TOKEN", k)
		return token + "\n"
	}})
	require.NoError(t, err)
	return r
}

func read(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(raw)
}

func TestAPIMeRepoAndPRView(t *testing.T) {
	r := require.New(t)
	srv := forgejotest.New(t, token)
	c := openAPI(t, srv)

	me, err := c.Me(t.Context())
	r.NoError(err)
	r.Equal("bot", me.Login)
	r.Equal("Bot Example", me.Name)

	repo, err := c.Repo(t.Context(), "alice/r-fork")
	r.NoError(err)
	r.True(repo.Fork)
	r.Equal("o/r", repo.Parent.FullName)

	pr, err := c.PRView(t.Context(), "o/r", 7)
	r.NoError(err)
	r.True(pr.Open())
	r.Equal("OPEN", pr.State)
	r.Equal("alice", pr.AuthorLogin())
	r.Equal("Fix <main>", pr.Title)
	r.Equal(forgejotest.Web+"/o/r/pulls/7", pr.URL)
	r.Equal("feat/x", pr.HeadRefName)
	r.Equal("main", pr.BaseRefName)
	r.Equal("alice", pr.HeadRepositoryOwner.Login)
	r.Equal("r-fork", pr.HeadRepository.Name)
	r.True(*pr.MaintainerCanModify)
	r.Equal("bug", pr.Labels[0].Name)
	r.Equal(forgejotest.Body, pr.Body)
	r.Len(pr.StatusCheckRollup, 2)
	r.Equal([]string{"ci/build"}, github.FailingChecks(pr))

	for _, auth := range srv.Auth {
		r.Equal("token "+token, auth) // trimmed, in the header only
	}
	for _, req := range srv.Requests {
		r.NotContains(req, token)
	}
}

func TestAPIRefusesABadRepository(t *testing.T) {
	srv := forgejotest.New(t, token)
	c := openAPI(t, srv)
	for _, repo := range []string{"o", "o/r/x", "../x", "o/..", "o/r?x"} {
		_, err := c.PRView(t.Context(), repo, 1)
		require.ErrorContains(t, err, "invalid repository", repo)
	}
	require.Empty(t, srv.Requests)
}

func TestAPISaveContext(t *testing.T) {
	r := require.New(t)
	srv := forgejotest.New(t, token)
	c := openAPI(t, srv)
	dir := filepath.Join(t.TempDir(), "ctx")
	r.NoError(c.SaveContext(t.Context(), "o/r", 7, dir))

	names := []string{}
	for _, f := range c.ContextFiles() {
		names = append(names, f[0])
	}
	entries, err := os.ReadDir(dir)
	r.NoError(err)
	r.Len(entries, len(names))
	for _, name := range names {
		r.FileExists(filepath.Join(dir, name))
	}

	r.Equal(forgejotest.Diff, read(t, filepath.Join(dir, "pr.diff")))

	var pr prJSON
	r.NoError(json.Unmarshal([]byte(read(t, filepath.Join(dir, "pr.json"))), &pr))
	r.Equal(forgejotest.HeadSha, pr.HeadRefOid)
	r.Equal(forgejotest.BaseSha, pr.BaseRefOid)
	r.Len(pr.Files, 3) // all pages
	r.Equal("CHANGED", pr.Files[0].ChangeType)
	r.Len(pr.Commits, 1)
	r.Equal("fix: main", pr.Commits[0].MessageHeadline)
	r.Equal("Longer text.", pr.Commits[0].MessageBody)
	r.Len(pr.Reviews, 3)
	r.Equal("REQUEST_CHANGES", pr.Reviews[0].State)
	r.Len(pr.Comments, 1)
	r.Equal("Any news?", pr.Comments[0].Body)
	r.Contains(read(t, filepath.Join(dir, "pr.json")), `"Fix <main>"`) // readable, not <

	files := read(t, filepath.Join(dir, "pr-files.txt"))
	r.True(strings.HasPrefix(files, "3 changed files, +5 -1\n"), files)
	r.Contains(files, `"bad\nname"`) // quoted: no line of its own
	r.Equal(4, strings.Count(files, "\n"))

	var inline []map[string]any
	r.NoError(json.Unmarshal([]byte(read(t, filepath.Join(dir, "review-comments.json"))), &inline))
	r.Len(inline, 2) // the unpaged endpoint is asked once, not once per page
	r.Equal(1, srv.Count("repos/o/r/pulls/7/reviews/11/comments"))
	r.Zero(srv.Count("repos/o/r/pulls/7/reviews/12/comments"))

	var failing []map[string]any
	r.NoError(json.Unmarshal([]byte(read(t, filepath.Join(dir, "failing-checks.json"))), &failing))
	r.Len(failing, 1)
	r.Equal("ci/build", failing[0]["context"])
	r.Equal("https://ci.example/1", failing[0]["targetUrl"])
}

func TestAPISaveReviewContext(t *testing.T) {
	r := require.New(t)
	srv := forgejotest.New(t, token)
	c := openAPI(t, srv)
	dir := filepath.Join(t.TempDir(), "review")
	r.NoError(c.SaveReviewContext(t.Context(), "o/r", 7, "", true, []string{"docs/**"}, dir))

	var m reviewManifest
	r.NoError(json.Unmarshal([]byte(read(t, filepath.Join(dir, "manifest.json"))), &m))
	r.Equal(forgejotest.BaseSha, m.Base)
	urls := []string{}
	for _, i := range m.Issues {
		urls = append(urls, i.URL)
		r.Equal("fetched", i.Status)
	}
	// the forge's own web address, never the other host
	r.Equal([]string{
		forgejotest.Web + "/o/r/issues/4",
		forgejotest.Web + "/o/other/issues/5",
		forgejotest.Web + "/o/r/issues/3",
	}, urls)
	r.Contains(read(t, filepath.Join(dir, "issue-01.json")), "Spec for 4")
	r.Len(m.Docs, 1)
	r.Equal(forgejotest.Web+"/o/r/src/commit/"+forgejotest.BaseSha+"/docs/spec.md", m.Docs[0].URL)
	r.Equal("# Spec at base\n", read(t, filepath.Join(dir, m.Docs[0].File)))
}

func TestAPIIssueURLsOnTheConfiguredAddressCount(t *testing.T) {
	got := linkedIssues("http://forge.lan:3000/a/b/issues/9 https://forge.lan:3000/a/b/issues/10 http://forge.lan:30001/a/b/issues/11",
		"o/r", []string{"https://forge.example", "http://forge.lan:3000"})
	require.Equal(t, []issueRef{{"a/b", 9}, {"a/b", 10}}, got)
}
