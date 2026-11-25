package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestForges(t *testing.T) {
	r := require.New(t)
	c, err := Parse([]byte(`
forges:
  - {kind: forgejo, host: forge.example.com, token_env: FORGEJO_TOKEN}
  - {kind: forgejo, host: "forge.lan:3000", url: "http://forge.lan:3000/", access: fj}
repos:
  include: ["my-org/*", "forge.example.com/team/*"]
`), "c.yaml")
	r.NoError(err)
	f := c.Forge("FORGE.example.com")
	r.NotNil(f)
	r.Equal("api", f.AccessMode())
	r.Equal("https://forge.example.com", f.BaseURL())
	r.Equal("http://forge.lan:3000", c.Forge("forge.lan:3000").BaseURL())
	r.Nil(c.Forge("github.com"))
	// host-prefixed globs match host/owner/repo; GitHub globs keep their meaning
	g, err := RepoGlobs(c.Repos.Include)
	r.NoError(err)
	r.True(g.Match("forge.example.com/team/api"))
	r.True(g.Match("my-org/x"))
	r.False(g.Match("forge.example.com/my-org/x"))
}

func TestInvalidForges(t *testing.T) {
	for _, tc := range []struct{ yaml, message string }{
		{"forges: [{kind: gitlab, host: x.example, token_env: T}]", "/forges/0/kind"},
		{"forges: [{kind: forgejo, token_env: T}]", "/forges/0"},
		{"forges: [{kind: forgejo, host: https://x.example, token_env: T}]", "/forges/0/host"},
		{"forges: [{kind: forgejo, host: x.example/sub, token_env: T}]", "/forges/0/host"},
		{"forges: [{kind: forgejo, host: x.example, url: ftp://x.example, token_env: T}]", "/forges/0/url"},
		{"forges: [{kind: forgejo, host: x.example, url: 'https://me:pw@x.example', token_env: T}]", "forges[0].url: must be an http(s) URL without credentials"},
		{"forges: [{kind: forgejo, host: x.example, access: ssh}]", "/forges/0/access"},
		{"forges: [{kind: forgejo, host: x.example, token: abc}]", "/forges/0"},
		{"forges: [{kind: forgejo, host: x.example}]", "forges[0]: access: api needs exactly one of token_env and token_file"},
		{"forges: [{kind: forgejo, host: x.example, token_env: T, token_file: f}]", "forges[0]: access: api needs exactly one"},
		{"forges: [{kind: forgejo, host: x.example, token_env: 'A-B'}]", "/forges/0/token_env"},
		{"forges: [{kind: forgejo, host: x.example, access: fj, token_env: T}]", "forges[0]: access: fj uses fj's own login"},
		{"forges: [{kind: forgejo, host: github.com, token_env: T}]", "forges[0]: github.com is read through gh"},
		{"forges: [{kind: forgejo, host: x.example, token_env: T}, {kind: forgejo, host: X.example, access: fj}]", "forges[1]: host X.example is configured twice"},
	} {
		t.Run(tc.yaml, func(t *testing.T) {
			_, err := Parse([]byte(tc.yaml), "c.yaml")
			require.ErrorContains(t, err, tc.message)
		})
	}
}
