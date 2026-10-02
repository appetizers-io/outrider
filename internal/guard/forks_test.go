package guard

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
		{"https remote, branch name", []string{"push", "-u", "fork2", "review/pr-7"}, nil, "github.com/me/fork2"},
		{"url given directly", []string{"push", "https://github.com/me/fork", "HEAD:refs/heads/review/x"}, nil, "github.com/me/fork"},
		{"force-with-lease", []string{"push", "--force-with-lease", "fork", "HEAD:review/x"}, nil, "github.com/me/fork"},
		{"force-with-lease=ref", []string{"push", "--force-with-lease=x:abc", "--", "fork", "HEAD:review/x"}, nil, "github.com/me/fork"},
		{"the session's own push trap", []string{"push", "fork", "HEAD:review/x"}, trap, "github.com/me/fork"},
	}
	for _, tc := range allowed {
		t.Run("allowed/"+tc.name, func(t *testing.T) {
			r := require.New(t)
			d := reviewForks("bob/repo").Decide(tc.args, "allow", tc.env, fakeResolve)
			r.Empty(d.Deny)
			r.False(d.Ask)
			r.Equal(tc.dest, d.Dest)
			r.Equal(forkURLs[tc.args[len(tc.args)-2]][0], d.URL)
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
		{"base repo", "bob/repo", []string{"push", "origin", "HEAD:review/x"}, nil, "head or base repo"},
		{"head repo", "bob/repo", []string{"push", "head", "HEAD:review/x"}, nil, "head or base repo"},
		{"head repo matching a fork glob", "me/repo", []string{"push", "mine", "HEAD:review/x"}, nil, "head or base repo"},
		{"base url given directly", "bob/repo", []string{"push", "https://github.com/base/repo", "HEAD:review/x"}, nil, "Cannot resolve"},
		{"other repo", "bob/repo", []string{"push", "other", "HEAD:review/x"}, nil, "not one of the review forks"},
		{"not github", "bob/repo", []string{"push", "gitlab", "HEAD:review/x"}, nil, "not a github.com"},
		{"pushurl redirect", "bob/repo", []string{"push", "pushurl", "HEAD:review/x"}, nil, "head or base repo"},
		{"two push urls", "bob/repo", []string{"push", "twice", "HEAD:review/x"}, nil, "2 URLs"},
		{"unknown remote", "bob/repo", []string{"push", "nope", "HEAD:review/x"}, nil, "Cannot resolve"},
		{"head unknown", "", []string{"push", "fork", "HEAD:review/x"}, nil, "unknown"},
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
		// security review B1: the transport and helpers git reads at push time
		{"GIT_SSH_COMMAND", "bob/repo", []string{"push", "fork", "HEAD:review/x"}, []string{"GIT_SSH_COMMAND=ssh-evil"}, "GIT_SSH_COMMAND"},
		{"GIT_SSH", "bob/repo", []string{"push", "fork", "HEAD:review/x"}, []string{"GIT_SSH=ssh-evil"}, "GIT_SSH"},
		{"GIT_SSH_VARIANT", "bob/repo", []string{"push", "fork", "HEAD:review/x"}, []string{"GIT_SSH_VARIANT=simple"}, "GIT_SSH_VARIANT"},
		{"GIT_EXEC_PATH", "bob/repo", []string{"push", "fork", "HEAD:review/x"}, []string{"GIT_EXEC_PATH=/evil"}, "GIT_EXEC_PATH"},
		{"GIT_PROXY_COMMAND", "bob/repo", []string{"push", "fork", "HEAD:review/x"}, []string{"GIT_PROXY_COMMAND=evil"}, "GIT_PROXY_COMMAND"},
		// security review B3: only branches under review/
		{"main", "bob/repo", []string{"push", "fork", "HEAD:main"}, nil, "under review/"},
		{"refs/heads/main", "bob/repo", []string{"push", "fork", "HEAD:refs/heads/main"}, nil, "under review/"},
		{"branch name", "bob/repo", []string{"push", "fork", "main"}, nil, "under review/"},
		{"tag", "bob/repo", []string{"push", "fork", "HEAD:refs/tags/review/x"}, nil, "under review/"},
		{"review/ alone", "bob/repo", []string{"push", "fork", "HEAD:review/"}, nil, "under review/"},
		{"wildcard", "bob/repo", []string{"push", "fork", "refs/heads/*:refs/heads/review/*"}, nil, "under review/"},
		{"one bad refspec", "bob/repo", []string{"push", "fork", "HEAD:review/x", "HEAD:main"}, nil, "under review/"},
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

func TestReviewRefsAreQualified(t *testing.T) {
	for ref, want := range map[string]string{
		"HEAD:review/x":                "HEAD:refs/heads/review/x",
		"review/pr-7":                  "review/pr-7:refs/heads/review/pr-7",
		"abc123:refs/heads/review/a/b": "abc123:refs/heads/review/a/b",
		"HEAD:main":                    "",
		"HEAD:refs/remotes/review/x":   "",
		"HEAD:review/x:y":              "",
		"HEAD:review/../main":          "",
		"HEAD:review/^x":               "",
		":review/x":                    "",
	} {
		require.Equal(t, want, reviewRef(ref), ref)
	}
}

func TestPinnedPushRunsAgainstTheResolvedURL(t *testing.T) {
	d := GitDecision{URL: "git@github.com:me/fork.git", Refs: []string{"HEAD:refs/heads/review/x"}, Flags: []string{"-u"}}
	require.Equal(t, []string{"-c", "core.hooksPath=" + os.DevNull, "-c", "core.sshCommand=ssh", "push", "--no-verify",
		"-u", "--", "git@github.com:me/fork.git", "HEAD:refs/heads/review/x"}, PinnedPush(d))
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
		d := rf.Decide([]string{"push", tc.repo, "HEAD:review/x"}, "allow", nil, resolve)
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
		EnvRealGit: realGit, EnvPush: "review-only", EnvReviewForks: "me/*", EnvReviewForksPush: "ask",
		EnvHeadRepo: "bob/repo", EnvRepo: "base/repo",
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
	r.Contains(res.dialog, "to: github.com/me/fork\nrefs: HEAD:refs/heads/review/pr-7\n")

	res = runGuard(t, "git", sess, "-c", "remote.fork.pushurl=https://github.com/base/repo", "push", "fork", "HEAD:review/x")
	r.Equal(1, res.code)
	r.Contains(res.stderr, "global options")
	r.Empty(res.dialog)

	res = runGuard(t, "git", sess, "status")
	r.Equal(0, res.code, res.stderr) // everything else runs the real git
}

// writeExec writes an executable shell script.
func writeScript(t *testing.T, path, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o700)) //nolint:gosec // a test script
}

// Security review B1 and B2, end to end: a review-fork push the guard allowed
// for the fork must not land anywhere else, whatever git reads at push time
// (ssh command, hooks, a remote helper), and a session that lost
// $OUTRIDER_REVIEW_FORKS pushes nowhere. Local bare repos stand in for
// GitHub behind a fake ssh that serves git@github.com:<owner>/<repo>.
func TestReviewForkPushCannotBeRedirected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts stand in for ssh, hooks and helpers")
	}
	realGit, err := exec.LookPath("git")
	require.NoError(t, err)
	root := t.TempDir()
	remotes, bin, work := filepath.Join(root, "remotes"), filepath.Join(root, "bin"), filepath.Join(root, "work")
	base := filepath.Join(remotes, "base", "repo.git")
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command(realGit, args...)
		cmd.Dir, cmd.Env = dir, []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root}
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
		return string(out)
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitconfig"),
		[]byte("[user]\n\tname = t\n\temail = t@example.com\n[commit]\n\tgpgsign = false\n"), 0o600))
	for _, repo := range []string{"me/fork.git", "base/repo.git"} {
		require.NoError(t, os.MkdirAll(filepath.Join(remotes, repo), 0o700))
		git(filepath.Join(remotes, repo), "init", "-q", "--bare")
	}
	require.NoError(t, os.MkdirAll(bin, 0o700))
	require.NoError(t, os.MkdirAll(work, 0o700))
	// ssh git@github.com "git-receive-pack 'me/fork.git'" serves remotes/me/fork.git;
	// ssh-evil serves the base repo whatever was asked for
	serve := `[ "$1" = -G ] && exit 1
for a; do last=$a; done
path=$(printf '%s' "$last" | sed "s/^[^']*'\\(.*\\)'$/\\1/")
exec ` + realGit + ` receive-pack "` + remotes + `/PATH"
`
	writeScript(t, filepath.Join(bin, "ssh"), strings.Replace(serve, "PATH", "$path", 1))
	writeScript(t, filepath.Join(bin, "ssh-evil"), strings.Replace(serve, "PATH", "base/repo.git", 1))
	toBase := realGit + ` push -q --no-verify "` + base + `" HEAD:refs/heads/pwned
`
	writeScript(t, filepath.Join(bin, "git-remote-evil"), toBase+"exit 1\n")
	require.NoError(t, os.MkdirAll(filepath.Join(work, "hooks"), 0o700))
	git(work, "init", "-q")
	writeScript(t, filepath.Join(work, "hooks", "pre-push"), toBase)
	writeScript(t, filepath.Join(work, ".git", "hooks", "pre-push"), toBase)
	git(work, "commit", "-q", "--allow-empty", "-m", "evidence")
	git(work, "remote", "add", "fork", "git@github.com:me/fork.git")
	t.Chdir(work)

	sess := func(extra ...string) map[string]string {
		m := map[string]string{
			"PATH": bin + string(os.PathListSeparator) + os.Getenv("PATH"), "HOME": root,
			EnvRealGit: realGit, EnvPush: "review-only", EnvReviewForks: "me/fork", EnvReviewForksPush: "allow",
			EnvHeadRepo: "bob/repo", EnvRepo: "base/repo",
		}
		for i := 0; i+1 < len(extra); i += 2 {
			m[extra[i]] = extra[i+1]
		}
		return m
	}
	baseRefs := func() string { return git(base, "for-each-ref", "--format=%(refname)") }
	forkRefs := func() string {
		return git(filepath.Join(remotes, "me", "fork.git"), "for-each-ref", "--format=%(refname)")
	}

	for _, tc := range []struct {
		name   string
		config [2]string // repo config set for this case
		env    []string
		deny   string // "": pushed, and only to the fork
	}{
		{name: "plain", env: nil},
		{name: "GIT_SSH_COMMAND", env: []string{"GIT_SSH_COMMAND", "ssh-evil"}, deny: "Unset GIT_SSH_COMMAND"},
		{name: "GIT_SSH", env: []string{"GIT_SSH", "ssh-evil"}, deny: "Unset GIT_SSH"},
		{name: "core.sshCommand", config: [2]string{"core.sshCommand", "ssh-evil"}},
		{name: "core.hooksPath pre-push", config: [2]string{"core.hooksPath", "hooks"}},
		{name: ".git/hooks/pre-push"},
		{name: "remote vcs helper", config: [2]string{"remote.fork.vcs", "evil"}, deny: "remote helper"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := require.New(t)
			if tc.config[0] != "" {
				git(work, "config", tc.config[0], tc.config[1])
				defer git(work, "config", "--unset", tc.config[0])
			}
			ref := "review/" + strings.NewReplacer(" ", "-", ".", "-", "/", "-").Replace(tc.name)
			res := runGuard(t, "git", sess(tc.env...), "push", "fork", "HEAD:"+ref)
			if tc.deny != "" {
				r.Equal(1, res.code)
				r.Contains(res.stderr, tc.deny)
			} else {
				r.Equal(0, res.code, res.stderr)
				r.Contains(forkRefs(), "refs/heads/"+ref)
			}
			r.Empty(baseRefs())
		})
	}

	// security review B2: without $OUTRIDER_REVIEW_FORKS the session is review only
	s := sess()
	delete(s, EnvReviewForks)
	res := runGuard(t, "git", s, "push", base, "HEAD:refs/heads/viaunset")
	require.Equal(t, 1, res.code)
	require.Contains(t, res.stderr, "review only, never push")
	require.Empty(t, baseRefs())
}
