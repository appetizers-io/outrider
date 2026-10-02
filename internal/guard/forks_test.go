package guard

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGitHubRepo(t *testing.T) {
	for _, tc := range []struct{ url, want string }{
		{"https://github.com/me/fork", "me/fork"},
		{"https://github.com/Me/Fork.git", "me/fork"},
		{"https://x-access-token:t@github.com/me/fork/", "me/fork"},
		{"ssh://git@github.com/me/fork.git", "me/fork"},
		{"git@github.com:me/fork.git", "me/fork"},
		{"git@GitHub.com:me/fork", "me/fork"},
	} {
		got, err := GitHubRepo(tc.url)
		require.NoError(t, err, tc.url)
		require.Equal(t, tc.want, got, tc.url)
	}
	for _, url := range []string{
		"https://gitlab.com/me/fork", "http://github.com/me/fork", "git://github.com/me/fork",
		"https://github.com.evil.io/me/fork", "https://github.com/me", "https://github.com/me/fork/extra",
		"https://github.com/me/..", "../fork", "/tmp/fork.git", "file:///tmp/fork", "ext::sh -c x",
		"outrider-push-blocked://github.com/me/fork", "",
	} {
		_, err := GitHubRepo(url)
		require.Error(t, err, url)
	}
}

var forkURLs = map[string][]string{
	"fork":                       {"git@github.com:me/fork.git"},
	"fork2":                      {"https://github.com/me/fork2"},
	"origin":                     {"https://github.com/base/repo"},
	"head":                       {"git@github.com:bob/repo.git"},
	"other":                      {"https://github.com/someone/else"},
	"mine":                       {"https://github.com/me/repo"}, // matches me/* but is the head below
	"twice":                      {"https://github.com/me/fork", "https://github.com/base/repo"},
	"gitlab":                     {"https://gitlab.com/me/fork"},
	"pushurl":                    {"https://github.com/base/repo"}, // url me/fork, pushurl base/repo
	"https://github.com/me/fork": {"https://github.com/me/fork"},
}

func fakeResolve(repo string) ([]string, error) {
	if urls, ok := forkURLs[repo]; ok {
		return urls, nil
	}
	return nil, os.ErrNotExist
}

func reviewForks(head string) *ReviewForks {
	rf, err := ReviewForksFromEnv(envMap{
		EnvReviewForks: "me/*\n Other/Exact", EnvHeadRepo: head, EnvRepo: "Base/Repo",
	}.get)
	if err != nil {
		panic(err)
	}
	return rf
}

type envMap map[string]string

func (m envMap) get(k string) string { return m[k] }

func TestReviewForksFromEnv(t *testing.T) {
	r := require.New(t)
	rf, err := ReviewForksFromEnv(envMap{}.get)
	r.NoError(err)
	r.Nil(rf) // no review forks: the usual git guard
	_, err = ReviewForksFromEnv(envMap{EnvReviewForks: "me/[x"}.get)
	r.Error(err)
	rf = reviewForks("Bob/Repo")
	r.Equal("bob/repo", rf.Head)
	r.Equal("base/repo", rf.Base)
	r.True(rf.Forks.Match("other/exact"))
}

func TestReviewForkPushDecision(t *testing.T) {
	trap := []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=" + trapKey, "GIT_CONFIG_VALUE_0=https://"}
	allowed := []struct {
		name string
		args []string
		env  []string
		dest string
	}{
		{"remote name, scp url", []string{"push", "fork", "HEAD:review/pr-7"}, nil, "github.com/me/fork"},
		{"https remote", []string{"push", "-u", "fork2", "review/pr-7"}, nil, "github.com/me/fork2"},
		{"url given directly", []string{"push", "https://github.com/me/fork", "HEAD:refs/heads/x"}, nil, "github.com/me/fork"},
		{"force-with-lease", []string{"push", "--force-with-lease", "fork", "HEAD:x"}, nil, "github.com/me/fork"},
		{"force-with-lease=ref", []string{"push", "--force-with-lease=x:abc", "--", "fork", "HEAD:x"}, nil, "github.com/me/fork"},
		{"the session's own push trap", []string{"push", "fork", "HEAD:x"}, trap, "github.com/me/fork"},
	}
	for _, tc := range allowed {
		t.Run("allowed/"+tc.name, func(t *testing.T) {
			r := require.New(t)
			d := reviewForks("bob/repo").Decide(tc.args, "allow", tc.env, fakeResolve)
			r.Empty(d.Deny)
			r.False(d.Ask)
			r.Equal(tc.dest, d.Dest)
			d = reviewForks("bob/repo").Decide(tc.args, "ask", tc.env, fakeResolve)
			r.Empty(d.Deny)
			r.True(d.Ask)
			r.Contains(reviewForks("bob/repo").Decide(tc.args, "never", tc.env, fakeResolve).Deny, "Pushing is off")
		})
	}

	refused := []struct {
		name, head string
		args       []string
		env        []string
		why        string
	}{
		{"base repo", "bob/repo", []string{"push", "origin", "HEAD:x"}, nil, "head or base repo"},
		{"head repo", "bob/repo", []string{"push", "head", "HEAD:x"}, nil, "head or base repo"},
		{"head repo matching a fork glob", "me/repo", []string{"push", "mine", "HEAD:x"}, nil, "head or base repo"},
		{"base url given directly", "bob/repo", []string{"push", "https://github.com/base/repo", "HEAD:x"}, nil, "Cannot resolve"},
		{"other repo", "bob/repo", []string{"push", "other", "HEAD:x"}, nil, "not one of the review forks"},
		{"not github", "bob/repo", []string{"push", "gitlab", "HEAD:x"}, nil, "not a github.com"},
		{"pushurl redirect", "bob/repo", []string{"push", "pushurl", "HEAD:x"}, nil, "head or base repo"},
		{"two push urls", "bob/repo", []string{"push", "twice", "HEAD:x"}, nil, "2 URLs"},
		{"unknown remote", "bob/repo", []string{"push", "nope", "HEAD:x"}, nil, "Cannot resolve"},
		{"head unknown", "", []string{"push", "fork", "HEAD:x"}, nil, "unknown"},
		{"no refspec", "bob/repo", []string{"push", "fork"}, nil, "Name the remote"},
		{"no remote", "bob/repo", []string{"push"}, nil, "Name the remote"},
		{"--mirror", "bob/repo", []string{"push", "--mirror", "fork", "x"}, nil, "--mirror"},
		{"--all", "bob/repo", []string{"push", "--all", "fork", "x"}, nil, "--all"},
		{"--branches", "bob/repo", []string{"push", "--branches", "fork", "x"}, nil, "--branches"},
		{"--tags", "bob/repo", []string{"push", "--tags", "fork", "x"}, nil, "--tags"},
		{"--delete", "bob/repo", []string{"push", "--delete", "fork", "x"}, nil, "--delete"},
		{"-d", "bob/repo", []string{"push", "-d", "fork", "x"}, nil, "-d"},
		{":ref deletes", "bob/repo", []string{"push", "fork", ":x"}, nil, "deletes or force-pushes"},
		{"+ref forces", "bob/repo", []string{"push", "fork", "+HEAD:x"}, nil, "deletes or force-pushes"},
		{"--force", "bob/repo", []string{"push", "--force", "fork", "x"}, nil, "--force"},
		{"-f", "bob/repo", []string{"push", "-f", "fork", "x"}, nil, "-f"},
		{"combined -uf", "bob/repo", []string{"push", "-uf", "fork", "x"}, nil, "-uf"},
		{"abbreviated --forc", "bob/repo", []string{"push", "--forc", "fork", "x"}, nil, "--forc"},
		{"--prune", "bob/repo", []string{"push", "--prune", "fork", "x"}, nil, "--prune"},
		{"--repo", "bob/repo", []string{"push", "--repo=origin", "fork", "x"}, nil, "--repo"},
		{"--receive-pack", "bob/repo", []string{"push", "--receive-pack=x", "fork", "x"}, nil, "--receive-pack"},
		{"-u=x", "bob/repo", []string{"push", "-u=x", "fork", "x"}, nil, "-u=x"},
		{"-c pushurl", "bob/repo", []string{"-c", "remote.fork.pushurl=https://github.com/base/repo", "push", "fork", "x"}, nil, "global options"},
		{"--config-env", "bob/repo", []string{"--config-env=remote.fork.pushurl=E", "push", "fork", "x"}, nil, "global options"},
		{"-C", "bob/repo", []string{"-C", "/other", "push", "fork", "x"}, nil, "global options"},
		{"--git-dir", "bob/repo", []string{"--git-dir=/x/.git", "push", "fork", "x"}, nil, "global options"},
		{"send-pack", "bob/repo", []string{"send-pack", "git@github.com:me/fork.git", "x"}, nil, "global options"},
		{"alias", "bob/repo", []string{"p"}, nil, "aliases"},
		{"GIT_CONFIG_PARAMETERS", "bob/repo", []string{"push", "fork", "x"},
			[]string{"GIT_CONFIG_PARAMETERS='remote.fork.pushurl'='https://github.com/base/repo'"}, "GIT_CONFIG_PARAMETERS"},
		{"extra GIT_CONFIG_KEY", "bob/repo", []string{"push", "fork", "x"},
			append(slices.Clone(trap), "GIT_CONFIG_KEY_1=remote.fork.pushurl"), "GIT_CONFIG_KEY_1"},
		{"GIT_CONFIG_GLOBAL", "bob/repo", []string{"push", "fork", "x"}, []string{"GIT_CONFIG_GLOBAL=/tmp/x"}, "GIT_CONFIG_GLOBAL"},
		{"GIT_DIR", "bob/repo", []string{"push", "fork", "x"}, []string{"GIT_DIR=/x/.git"}, "GIT_DIR"},
	}
	for _, tc := range refused {
		t.Run("refused/"+tc.name, func(t *testing.T) {
			for _, mode := range []string{"allow", "ask"} {
				d := reviewForks(tc.head).Decide(tc.args, mode, tc.env, fakeResolve)
				require.Contains(t, d.Deny, tc.why)
				require.False(t, d.Ask)
			}
		})
	}
}

// gitRepo is a throwaway repository with the given config; the global config
// is a file in it, so the owner's own ~/.gitconfig plays no part.
func gitRepo(t *testing.T, global string, config ...[2]string) (dir string, env []string) {
	t.Helper()
	dir = t.TempDir()
	g := filepath.Join(dir, "global.gitconfig")
	require.NoError(t, os.WriteFile(g, []byte(global), 0o600))
	env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "GIT_CONFIG_GLOBAL=" + g, "GIT_CONFIG_NOSYSTEM=1"}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir, cmd.Env = dir, env
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	run("init", "-q")
	for _, kv := range config {
		run("config", "--add", kv[0], kv[1])
	}
	return dir, env
}

// The resolution layer against real git: remote names, URLs, pushurl,
// insteadOf and pushInsteadOf, and config in other files.
func TestResolvePushWithRealGit(t *testing.T) {
	realGit, err := exec.LookPath("git")
	require.NoError(t, err)
	dir, env := gitRepo(t, "[remote \"https://github.com/me/global\"]\n\tpushurl = https://github.com/base/repo\n",
		[2]string{"remote.fork.url", "git@github.com:me/fork.git"},
		[2]string{"remote.redirect.url", "https://github.com/me/fork"},
		[2]string{"remote.redirect.pushurl", "https://github.com/base/repo"},
		[2]string{"remote.both.url", "https://github.com/me/fork"},
		[2]string{"remote.both.pushurl", "https://github.com/me/fork"},
		[2]string{"remote.both.pushurl", "https://github.com/base/repo"},
		[2]string{"remote.rewritten.url", "https://example.com/me/fork"},
		[2]string{"url.https://github.com/base/.insteadOf", "https://example.com/me/"},
		[2]string{"remote.pushrewritten.url", "https://github.com/me/pushfork"},
		[2]string{"url.https://github.com/base/repo.pushInsteadOf", "https://github.com/me/pushfork"},
		[2]string{"url.https://github.com/base/repo.pushInsteadOf", "https://github.com/me/trap"},
	)
	t.Chdir(dir)
	resolve := resolvePush(realGit, env)
	rf := reviewForks("bob/repo")
	for _, tc := range []struct {
		repo, dest, deny string
	}{
		{repo: "fork", dest: "github.com/me/fork"},
		{repo: "https://github.com/me/fork", dest: "github.com/me/fork"},
		{repo: "git@github.com:me/fork.git", dest: "github.com/me/fork"},
		{repo: "redirect", deny: "head or base repo"},                // pushurl
		{repo: "both", deny: "2 URLs"},                               // two pushurls
		{repo: "rewritten", deny: "github.com/base/fork is not one"}, // insteadOf
		{repo: "pushrewritten", deny: "head or base repo"},           // pushInsteadOf
		{repo: "https://github.com/me/trap", deny: "changed by the git config url."},
		{repo: "https://github.com/me/global", deny: "changed by the git config remote."},
		{repo: "https://github.com/base/repo", deny: "head or base repo"},
		{repo: "upstream", deny: "not a github.com https or ssh URL"}, // no such remote: a URL
	} {
		d := rf.Decide([]string{"push", tc.repo, "HEAD:x"}, "allow", nil, resolve)
		if tc.deny != "" {
			require.Contains(t, d.Deny, tc.deny, tc.repo)
			continue
		}
		require.Empty(t, d.Deny, tc.repo)
		require.Equal(t, tc.dest, d.Dest, tc.repo)
	}
}

// The whole guard with real git for resolution: the dialog shows where the
// push goes, a denied push never runs, and a redirect is refused before
// anything is asked.
func TestReviewForkGuardShowsTheDestination(t *testing.T) {
	r := require.New(t)
	realGit, err := exec.LookPath("git")
	r.NoError(err)
	dir, env := gitRepo(t, "", [2]string{"remote.fork.url", "git@github.com:me/fork.git"})
	t.Chdir(dir)
	sess := map[string]string{
		EnvRealGit: realGit, EnvPush: "ask", EnvReviewForks: "me/*", EnvHeadRepo: "bob/repo", EnvRepo: "base/repo",
		"GUARD_ANSWER": "Deny",
	}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if k != "GIT_CONFIG_GLOBAL" { // refused for review-fork pushes; HOME isolates instead
			sess[k] = v
		}
	}
	res := runGuard(t, "git", sess, "push", "fork", "HEAD:review/pr-7")
	r.Equal(1, res.code)
	r.Contains(res.stderr, "did not approve")
	r.Contains(res.dialog, "to: github.com/me/fork\nrefs: HEAD:review/pr-7\n")

	res = runGuard(t, "git", sess, "-c", "remote.fork.pushurl=https://github.com/base/repo", "push", "fork", "HEAD:x")
	r.Equal(1, res.code)
	r.Contains(res.stderr, "global options")
	r.Empty(res.dialog)

	res = runGuard(t, "git", sess, "status")
	r.Equal(0, res.code, res.stderr) // everything else runs the real git
}
