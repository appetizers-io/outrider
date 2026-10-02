package watch

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/appetizers-io/outrider/internal/config"
	"github.com/appetizers-io/outrider/internal/proc"
	"github.com/appetizers-io/outrider/internal/session"
)

func TestResolveReviewForks(t *testing.T) {
	const (
		pushURL = "git remote get-url --push --all -- origin"
		lookup  = "gh api repos/me/r"
		forkOf  = `{"full_name": "me/r", "fork": true, "parent": {"full_name": "o/r"}}`
	)
	fork := map[string]string{pushURL: "git@github.com:me/r.git\n", lookup: forkOf}
	upstream := session.Local{Path: "/src/r", Remote: "upstream"}
	for _, tc := range []struct {
		name    string
		config  string
		repo    string // the watched repo; "" outside a checkout
		loc     session.Local
		login   string
		outputs map[string]string // commands not listed fail
		want    string
		failed  bool
		calls   []string // the commands run, in order
	}{
		{name: "all conditions met", outputs: fork, want: "me/r (auto: origin)", calls: []string{pushURL, lookup}},
		{name: "login differs in case", login: "Me", outputs: fork, want: "me/r (auto: origin)"},
		{name: "remote is origin", loc: session.Local{Path: "/src/r", Remote: "origin"}, outputs: fork, want: "off (watching origin itself)"},
		{name: "no local checkout", repo: "-", outputs: fork, want: "off (not in a local checkout)"},
		{
			name: "origin owned by someone else", outputs: map[string]string{pushURL: "https://github.com/bob/r.git\n"},
			want: "off (origin bob/r is not your fork)", calls: []string{pushURL},
		},
		{
			name: "not a fork", outputs: map[string]string{pushURL: fork[pushURL], lookup: `{"full_name": "me/r", "fork": false}`},
			want: "off (origin me/r is not a fork)",
		},
		{
			name: "fork of a different parent", outputs: map[string]string{pushURL: fork[pushURL], lookup: `{"full_name": "me/r", "fork": true, "parent": {"full_name": "x/r"}}`},
			want: "off (origin me/r is a fork of x/r, not of o/r)",
		},
		{
			name: "origin moved to someone else", outputs: map[string]string{pushURL: fork[pushURL], lookup: `{"full_name": "bob/r", "fork": true, "parent": {"full_name": "o/r"}}`},
			want: "off (origin me/r is now bob/r on GitHub)",
		},
		{
			name: "two push URLs", outputs: map[string]string{pushURL: "git@github.com:me/r.git\nhttps://github.com/me/r.git\n"},
			want: "off (origin has 2 push URLs)",
		},
		{name: "origin not on GitHub", outputs: map[string]string{pushURL: "https://gitlab.com/me/r.git\n"}, want: "off (origin is not a GitHub repo"},
		{name: "no origin", outputs: map[string]string{}, want: "off (cannot read origin's push URL", failed: true},
		{name: "lookup fails", outputs: map[string]string{pushURL: fork[pushURL]}, want: "off (cannot look up origin me/r on GitHub", failed: true},
		{name: "unknown user", login: "-", outputs: fork, want: "off (GitHub user unknown)", failed: true},
		{name: "explicit list overrides", config: "others_prs: {review_forks: [me-evidence/*]}", outputs: fork, want: "me-evidence/* (config)"},
		{name: "[] disables", config: "others_prs: {review_forks: []}", outputs: fork, want: "off (review_forks: [])"},
		{name: "read-only others", config: "others_prs: {sandbox: read-only}", outputs: fork, want: "off (others' PRs run read-only)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := require.New(t)
			cfg, err := config.Parse([]byte(tc.config+"\n"), "c.yaml")
			r.NoError(err)
			repo, loc, login := "o/r", upstream, "me"
			if tc.repo == "-" {
				repo, loc = "", session.Local{}
			}
			if tc.loc.Remote != "" {
				loc = tc.loc
			}
			if tc.login == "-" {
				login = ""
			} else if tc.login != "" {
				login = tc.login
			}
			var calls []string
			d := Deps{Run: func(_ context.Context, c proc.Cmd) (proc.Result, error) {
				a := strings.Join(c.Args, " ")
				calls = append(calls, a)
				if out, ok := tc.outputs[a]; ok {
					return proc.Result{Stdout: out}, nil
				}
				return proc.Result{Code: 1}, &proc.Error{Args: c.Args, Code: 1, Stderr: "failed"}
			}}
			got := ResolveReviewForks(t.Context(), &cfg, repo, loc, login, d)
			r.True(strings.HasPrefix(got.String(), tc.want), got.String())
			r.Equal(tc.failed, got.Failed)
			if tc.calls != nil {
				r.Equal(tc.calls, calls)
			}
		})
	}
}
