package guard

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The test binary doubles as the guard (GUARD_AS) and as the real gh/git
// (FAKE_REAL) for the tests that run the whole thing.
func TestMain(m *testing.M) {
	if as := os.Getenv("GUARD_AS"); as != "" {
		_ = os.Unsetenv("GUARD_AS")
		ask := func(title, body, ok string) bool {
			if f := os.Getenv("GUARD_DIALOG"); f != "" {
				_ = os.WriteFile(f, []byte(title+"\n"+body), 0o600)
			}
			return os.Getenv("GUARD_ANSWER") == ok
		}
		run := RunGH
		if as == "git" {
			run = RunGit
		}
		os.Exit(run(os.Args[1:], os.Getenv, ask, os.Stderr))
	}
	if os.Getenv("FAKE_REAL") != "" {
		a := os.Args[1:]
		if len(a) >= 3 && a[len(a)-3] == "config" && a[len(a)-2] == "--get" {
			fmt.Println(map[string]string{"alias.p": "push origin", "alias.shipit": "!git push", "alias.st": "status"}[a[len(a)-1]])
			os.Exit(0)
		}
		fmt.Printf("count=%s\n", os.Getenv("GIT_CONFIG_COUNT"))
		fmt.Println(strings.Join(append([]string{"REAL"}, a...), "\n"))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func gh(mode string) GHSession { return GHSession{Mode: mode, Repo: "o/r", PR: "7"} }

func TestReadsPassThrough(t *testing.T) {
	for _, args := range [][]string{
		{"pr", "view", "1"},
		{"pr", "diff", "1"},
		{"pr", "checkout", "1"},
		{"run", "view", "123", "--log-failed"},
		{"api", "repos/o/r/pulls/1/comments"},
		{"api", "-X", "GET", "search/issues", "-f", "q=is:pr"},
		{"api", "graphql", "-f", "query=query { viewer { login } }"},
		{"search", "prs", "foo"},
		{"browse"},
	} {
		for _, mode := range []string{"never", "ask", "allow"} {
			d := DecideGH(args, GHSession{Mode: mode})
			require.Empty(t, d.Deny, args)
			require.False(t, d.Write, args)
		}
	}
}

func TestWritesAreBlockedWithoutASession(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"pr", "comment", "1", "-b", "x"},
		{"pr", "review", "1", "--approve"},
		{"pr", "merge", "1"},
		{"pr", "close", "1"},
		{"pr"},
		{"issue", "comment", "1"},
		{"repo", "delete"},
		{"api", "-X", "POST", "repos/o/r/issues/1/comments"},
		{"api", "--method=PATCH", "repos/o/r/pulls/1"},
		{"api", "-XDELETE", "repos/o/r/issues/comments/1/reactions/2"},
		{"api", "repos/o/r/issues/1/comments", "-f", "body=hi"},
		{"api", "repos/o/r/issues/1/comments", "--raw-field=body=hi"},
		{"api", "repos/o/r/issues/1/comments", "--input", "body.json"},
		{"api", "graphql", "-f", "query=mutation { addReaction }"},
		{"api", "graphql", "-F", "query=@q.graphql"},
	} {
		d := DecideGH(args, GHSessionFromEnv(func(string) string { return "" }))
		require.Contains(t, d.Deny, "blocked", args)
	}
}

var posts = [][]string{
	{"pr", "comment", "7", "-b", "hi"},
	{"pr", "comment", "https://github.com/o/r/pull/7", "--body=hi"},
	{"pr", "review", "7", "--comment", "-b", "hi"},
	{"pr", "review", "7", "-R", "o/r", "--approve"},
	{"pr", "review", "7", "-R", "https://github.com/O/R.git", "-c", "-bhi"},
	{"issue", "comment", "7", "-b", "hi"},
	{"api", "repos/o/r/pulls/7/comments", "-f", "body=hi", "-F", "line=3"},
	{"api", "/repos/O/R/pulls/7/comments/11/replies", "-f", "body=hi"},
	{"api", "-X", "POST", "repos/o/r/pulls/7/reviews", "-f", "event=COMMENT"},
	{"api", "repos/o/r/issues/7/comments", "-f", "body=hi"},
	{"api", "-X", "PATCH", "repos/o/r/issues/comments/9", "-f", "body=hi"},
	{"api", "repos/o/r/pulls/comments/9/reactions", "-f", "content=+1"},
	{"api", "-XDELETE", "repos/o/r/issues/comments/9/reactions/2"},
}

var neverPosts = [][]string{
	{"pr", "merge", "7"},
	{"pr", "close", "7"},
	{"pr", "edit", "7", "--add-label", "x"},
	{"pr", "comment", "8", "-b", "hi"}, // another PR
	{"pr", "comment", "7", "-R", "o/other", "-b", "hi"},
	{"pr", "comment", "-b", "hi"}, // PR from the branch: not explicit
	{"pr", "comment", "7", "--web"},
	{"pr", "comment", "7", "--body-file", "-"},
	{"pr", "comment", "07", "-b", "hi"},
	{"api", "repos/o/r/pulls/8/comments", "-f", "body=hi"},
	{"api", "repos/o/other/pulls/7/comments", "-f", "body=hi"},
	{"api", "-X", "PUT", "repos/o/r/pulls/7/merge"},
	{"api", "-X", "PATCH", "repos/o/r/pulls/7", "-f", "state=closed"},
	{"api", "-X", "DELETE", "repos/o/r/issues/comments/9"},
	{"api", "repos/o/r/issues/7/labels", "-f", "labels[]=x"},
	{"api", "repos/{owner}/{repo}/pulls/7/comments", "-f", "body=hi"},
	{"api", "graphql", "-f", "query=mutation { addComment }"},
	{"api", "user", "-X", "POST"},
	{"-R", "o/r", "pr", "comment", "7", "-b", "hi"},
}

func TestPostsAreBlockedWhenWritesAreOff(t *testing.T) {
	for _, args := range posts {
		d := DecideGH(args, gh("never"))
		require.Contains(t, d.Deny, "read-only", args)
	}
}

func TestPostsOnTheSessionPRAreWrites(t *testing.T) {
	for _, mode := range []string{"ask", "allow"} {
		for _, args := range posts {
			d := DecideGH(args, gh(mode))
			require.Empty(t, d.Deny, args)
			require.True(t, d.Write, args)
		}
	}
}

func TestOtherWritesStayBlocked(t *testing.T) {
	for _, mode := range []string{"ask", "allow"} {
		for _, args := range neverPosts {
			d := DecideGH(args, gh(mode))
			require.Contains(t, d.Deny, "blocked", args)
			require.Contains(t, d.Deny, "PR o/r#7", args)
		}
	}
}

func TestPostTextIsShown(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()
	t.Chdir(dir)
	r.NoError(os.WriteFile("comment.md", []byte("Please handle the error here."), 0o600))
	args := []string{"api", "repos/o/r/pulls/7/comments", "-f", "path=store.go", "-F", "line=618", "-F", "body=@comment.md"}
	d := DecideGH(args, gh("ask"))
	r.Empty(d.Deny)
	r.Equal("path: store.go\nline: 618\nbody: Please handle the error here.", d.Text)
	dialog := PostDialog("PR o/r#7", args, d.Text)
	r.Contains(dialog, "PR o/r#7 wants to post to GitHub:")
	r.Contains(dialog, "gh api repos/o/r/pulls/7/comments")
	r.Contains(dialog, "body: Please handle the error here.")

	d = DecideGH([]string{"pr", "review", "7", "--request-changes", "-F", "missing.md"}, gh("ask"))
	r.Contains(d.Deny, "cannot read missing.md")
	d = DecideGH([]string{"pr", "review", "7", "-r", "-b", "fix it"}, gh("ask"))
	r.Equal("-r\nfix it", d.Text)
}

func TestPostDialogIsTrimmed(t *testing.T) {
	body := PostDialog("", []string{"api", strings.Repeat("x", 400)}, strings.Repeat("y", 1600))
	require.Contains(t, body, "agent session wants to post")
	require.Contains(t, body, " ...\n\n")
	require.Contains(t, body, "[... 100 more characters]")
}

func TestGitSubcommand(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"push"}, "push"},
		{[]string{"-C", "/repo", "push"}, "push"},
		{[]string{"-c", "user.name=x", "push", "--force"}, "push"},
		{[]string{"--git-dir=/repo/.git", "push"}, "push"},
		{[]string{"--version"}, ""},
	} {
		got, _ := Subcommand(tc.args)
		require.Equal(t, tc.want, got, tc.args)
	}
}

func fakeAlias(global []string, name string) string {
	for i, g := range global {
		if g == "-c" && i+1 < len(global) && strings.HasPrefix(global[i+1], "alias."+name+"=") {
			return strings.TrimPrefix(global[i+1], "alias."+name+"=")
		}
	}
	return map[string]string{"p": "push origin", "shipit": "!git push", "st": "status"}[name]
}

func TestGitPushBlockedInReviewOnlySession(t *testing.T) {
	for _, args := range [][]string{
		{"push"},
		{"push", "origin", "HEAD:feature"},
		{"-C", "/repo", "push"},
		{"-c", "user.name=x", "push", "--force"},
		{"--git-dir=/repo/.git", "push"},
		{"p"},      // alias to push
		{"shipit"}, // shell alias running push
		{"-c", "alias.x=push", "x"},
	} {
		for _, mode := range []string{"review-only", ""} { // unset: review only
			d := DecideGit(args, mode, fakeAlias)
			require.Contains(t, d.Deny, "review only, never push", args)
		}
	}
}

func TestGitReadsPassThrough(t *testing.T) {
	for _, args := range [][]string{{"status"}, {"st"}, {"log", "--oneline"}, {"-C", "/repo", "diff"}, {"fetch"}, {"--version"}} {
		for _, mode := range []string{"review-only", "never", "ask", "allow"} {
			require.Equal(t, GitDecision{}, DecideGit(args, mode, fakeAlias), args)
		}
	}
}

func TestGitPushModes(t *testing.T) {
	require.Equal(t, GitDecision{}, DecideGit([]string{"push"}, "allow", fakeAlias))
	for _, args := range [][]string{{"push"}, {"p"}, {"-C", "/repo", "push"}} {
		require.Contains(t, DecideGit(args, "never", fakeAlias).Deny, "Pushing is off", args)
		require.Contains(t, DecideGit(args, "bogus", fakeAlias).Deny, "Pushing is off", args)
		require.Equal(t, GitDecision{Ask: true}, DecideGit(args, "ask", fakeAlias), args)
	}
}

func TestApprovedPushEnvDropsTheTrap(t *testing.T) {
	env := approvedPushEnv([]string{"PATH=/x", "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=k", "GIT_CONFIG_VALUE_0=v", "LLM_REVIEW_AGENT_PUSH=ask"})
	require.Equal(t, []string{"PATH=/x", "LLM_REVIEW_AGENT_PUSH=allow"}, env)
}

// --- the whole guard, run as a process ---------------------------------------

type result struct {
	code           int
	stdout, stderr string
	dialog         string
}

func runGuard(t *testing.T, as string, env map[string]string, args ...string) result {
	t.Helper()
	dir := t.TempDir()
	dialog := filepath.Join(dir, "dialog")
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), "GUARD_AS="+as, "FAKE_REAL=1", "GUARD_DIALOG="+dialog,
		EnvRealGH+"="+os.Args[0], EnvRealGit+"="+os.Args[0])
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	}
	d, _ := os.ReadFile(dialog)
	return result{code, stdout.String(), stderr.String(), string(d)}
}

func session(mode, answer string) map[string]string {
	return map[string]string{EnvGHWrites: mode, EnvRepo: "o/r", EnvPR: "7", "GUARD_ANSWER": answer}
}

func TestGHGuardRunsTheRealGH(t *testing.T) {
	r := require.New(t)
	res := runGuard(t, "gh", session("never", ""), "pr", "view", "7")
	r.Equal(0, res.code, res.stderr)
	r.Contains(res.stdout, "REAL\npr\nview\n7")

	res = runGuard(t, "gh", session("allow", ""), "pr", "comment", "7", "-b", "hi")
	r.Equal(0, res.code, res.stderr)
	r.Contains(res.stdout, "REAL\npr\ncomment")
	r.Empty(res.dialog) // allow: no question
}

func TestGHAskPostsOnlyAfterApproval(t *testing.T) {
	r := require.New(t)
	res := runGuard(t, "gh", session("ask", "Post"), "pr", "comment", "7", "-b", "hi there")
	r.Equal(0, res.code, res.stderr)
	r.Contains(res.stdout, "REAL")
	r.Contains(res.dialog, "llm-review-agent: post to GitHub?")
	r.Contains(res.dialog, "hi there")

	for _, answer := range []string{"Deny", ""} { // denied, or no dialog available
		res = runGuard(t, "gh", session("ask", answer), "pr", "comment", "7", "-b", "hi")
		r.Equal(1, res.code)
		r.Contains(res.stderr, "did not approve")
		r.NotContains(res.stdout, "REAL")
	}

	res = runGuard(t, "gh", session("ask", "Deny"), "api", "repos/o/r/pulls/7/comments")
	r.Equal(0, res.code) // reads never ask
	r.Empty(res.dialog)
}

func TestGitGuardAsksAndDropsTheTrap(t *testing.T) {
	r := require.New(t)
	trap := map[string]string{EnvPush: "ask", "GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "url.x://.pushInsteadOf"}
	trap["GUARD_ANSWER"] = "Push"
	res := runGuard(t, "git", trap, "push", "origin")
	r.Equal(0, res.code, res.stderr)
	r.Contains(res.stdout, "count=\nREAL\npush\norigin") // GIT_CONFIG_* dropped for the real push
	r.Contains(res.dialog, "git push origin")

	trap["GUARD_ANSWER"] = "Deny"
	res = runGuard(t, "git", trap, "p")
	r.Equal(1, res.code)
	r.Contains(res.stderr, "did not approve")
	r.NotContains(res.stdout, "REAL")

	res = runGuard(t, "git", trap, "status")
	r.Equal(0, res.code)
	r.Contains(res.stdout, "count=1\n") // the push trap stays for everything else
	r.Empty(res.dialog)

	res = runGuard(t, "git", map[string]string{EnvPush: "review-only"}, "shipit")
	r.Equal(1, res.code)
	r.Contains(res.stderr, "review only, never push")
}
