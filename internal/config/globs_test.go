package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func mustLogin(t *testing.T, p string) Globs {
	t.Helper()
	g, err := LoginGlobs([]string{p})
	require.NoError(t, err)
	return g
}

func TestRepoPattern(t *testing.T) {
	for _, tc := range [][2]string{
		{"owner/repo", "owner/repo"},
		{"https://github.com/owner/repo", "owner/repo"},
		{"https://github.com/owner/repo.git/", "owner/repo"},
		{"git@github.com:owner/repo.git", "owner/repo"},
		{"ssh://git@github.com/owner/repo.git", "owner/repo"},
		{"  owner/* ", "owner/*"},
	} {
		require.Equal(t, tc[1], RepoPattern(tc[0]), tc[0])
	}
}

func TestRepoGlobs(t *testing.T) {
	ok := func(repo string, include, exclude []string) bool {
		in, err := RepoGlobs(include)
		require.NoError(t, err)
		ex, err := RepoGlobs(exclude)
		require.NoError(t, err)
		return (len(in) == 0 || in.Match(repo)) && !ex.Match(repo)
	}
	require.True(t, ok("o/r", nil, nil))
	require.True(t, ok("o/r", []string{"o/*"}, nil))
	require.False(t, ok("x/r", []string{"o/*"}, nil))
	require.False(t, ok("o/r", []string{"o/*"}, []string{"o/r"}))
	require.True(t, ok("o/r", []string{"*"}, nil)) // * spans the slash, like fnmatch
	require.True(t, ok("o/r1", []string{"o/r[0-9]"}, nil))
	require.False(t, ok("o/r1", []string{"o/r[!0-9]"}, nil))
	require.True(t, ok("o/r.x", []string{"o/r.x"}, nil))
	require.False(t, ok("o/rax", []string{"o/r.x"}, nil))
	require.True(t, ok("org/b", []string{"org/{a,b}"}, nil)) // {a,b} alternation
	require.True(t, ok("o/r", []string{"https://github.com/o/r.git"}, nil))
}

func TestLoginGlob(t *testing.T) {
	for _, tc := range []struct {
		pattern, login string
		hit            bool
	}{
		{"*[bot]", "netlify[bot]", true},
		{"*[bot]", "robot", false}, // brackets are literal, not a class
		{"netlify[bot]", "Netlify[Bot]", true},
		{"coderabbit?i*", "coderabbitai[bot]", true},
		{"alice", "alice2", false},
	} {
		require.Equal(t, tc.hit, mustLogin(t, tc.pattern).MatchLogin(tc.login), tc)
	}
}

func TestBrokenGlobsAreErrors(t *testing.T) {
	for _, p := range []string{"org/[abc", "org/{a,b", `org/x\`} {
		_, err := RepoGlobs([]string{"o/*", p})
		require.ErrorContains(t, err, "bad glob", p)
	}
	// logins only know * and ?: brackets and braces are literal
	_, err := LoginGlobs([]string{"{bot", "x[", `y\`})
	require.NoError(t, err)
}
