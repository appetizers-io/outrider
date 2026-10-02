package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

const layered = `
ignore_authors: ["netlify[bot]"]
overrides:
  - match: [{prs: others}]
    ignore_authors: ["*[bot]"]
  - match:
      - {repo: "my-org/*", prs: others}
      - {url: "https://github.com/upstream-org/repo.git"}
    launch_check: {skip_below: 0.7}
    triggers: {opt_in: {where: [comment]}}
  - match: [{repo: my-org/api}]
    ignore_authors: []
    prompts: {extra: api rules}
`

func TestOverridesMatch(t *testing.T) {
	c, err := Parse([]byte(layered), "c.yaml")
	require.NoError(t, err)
	for _, tc := range []struct {
		name string
		repo string
		own  bool
		want []string
	}{
		{name: "own PR elsewhere", repo: "a/b", own: true},
		{name: "others' PR elsewhere", repo: "a/b", want: []string{"overrides[0] (prs: others)"}},
		{name: "glob and prs both match", repo: "my-org/web", want: []string{
			"overrides[0] (prs: others)",
			"overrides[1] (repo: my-org/*, prs: others | url: https://github.com/upstream-org/repo.git)",
		}},
		{name: "glob matches, prs does not", repo: "my-org/web", own: true},
		{name: "url, any PR", repo: "Upstream-Org/repo", own: true, want: []string{
			"overrides[1] (repo: my-org/*, prs: others | url: https://github.com/upstream-org/repo.git)",
		}},
		{name: "all three", repo: "my-org/api", want: []string{
			"overrides[0] (prs: others)",
			"overrides[1] (repo: my-org/*, prs: others | url: https://github.com/upstream-org/repo.git)",
			"overrides[2] (repo: my-org/api)",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, c.Applied(tc.repo, tc.own))
		})
	}
}

func TestOverridesLayerInFileOrder(t *testing.T) {
	c, err := Parse([]byte(layered), "c.yaml")
	require.NoError(t, err)
	for _, tc := range []struct {
		name    string
		repo    string
		own     bool
		ignore  []string
		skip    float64
		where   []string
		extra   string
		applied int
	}{
		{name: "global", repo: "a/b", own: true, ignore: []string{"netlify[bot]"}, skip: 0.5, where: Default().Triggers.OptIn.Where},
		{name: "list replaces", repo: "a/b", ignore: []string{"*[bot]"}, skip: 0.5, where: Default().Triggers.OptIn.Where},
		{name: "nested key keeps its siblings", repo: "my-org/web", ignore: []string{"*[bot]"}, skip: 0.7, where: []string{"comment"}},
		{name: "later wins", repo: "my-org/api", ignore: []string{}, skip: 0.7, where: []string{"comment"}, extra: "api rules"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := c.For(tc.repo, tc.own)
			require.Equal(t, tc.ignore, got.IgnoreAuthors)
			require.InDelta(t, tc.skip, got.LaunchCheck.SkipBelow, 0)
			require.Equal(t, tc.where, got.Triggers.OptIn.Where)
			require.Equal(t, tc.extra, got.Prompts.Extra)
			require.Equal(t, "jev", *got.LaunchCheck.Classifier)
			require.Nil(t, got.Overrides)
		})
	}
	// layers never write through to the config they are applied to
	require.Equal(t, []string{"netlify[bot]"}, c.IgnoreAuthors)
	require.InDelta(t, 0.5, c.LaunchCheck.SkipBelow, 0)
	require.Len(t, c.Overrides, 3)
}

func TestOverridePointersAreCopies(t *testing.T) {
	c, err := Parse([]byte("overrides: [{match: [{prs: own}], launch_check: {classifier: mine}}]\n"+
		"classifiers: {mine: {kind: command, launch_command: x}}\n"), "c.yaml")
	require.NoError(t, err)
	require.Equal(t, "mine", *c.For("a/b", true).LaunchCheck.Classifier)
	require.Equal(t, "jev", *c.LaunchCheck.Classifier)
}

func TestOverridesAcceptOnlyPerPRKeys(t *testing.T) {
	for _, tc := range []struct {
		name  string
		entry string
		want  string
	}{
		{name: "interval_seconds", entry: "match: [{prs: own}], interval_seconds: 30", want: "additional properties 'interval_seconds' not allowed"},
		{name: "max_agents", entry: "match: [{prs: own}], max_agents: 2", want: "additional properties 'max_agents' not allowed"},
		{name: "launcher", entry: "match: [{prs: own}], launcher: tmux", want: "additional properties 'launcher' not allowed"},
		{name: "terminal", entry: "match: [{prs: own}], terminal: auto", want: "additional properties 'terminal' not allowed"},
		{name: "classifiers", entry: "match: [{prs: own}], classifiers: {}", want: "additional properties 'classifiers' not allowed"},
		{name: "repos", entry: "match: [{prs: own}], repos: {include: [a/b]}", want: "additional properties 'repos' not allowed"},
		{name: "candidate_limit", entry: "match: [{prs: own}], candidate_limit: 5", want: "additional properties 'candidate_limit' not allowed"},
		{name: "lookback_hours", entry: "match: [{prs: own}], lookback_hours: 5", want: "additional properties 'lookback_hours' not allowed"},
		{name: "stale_lock_hours", entry: "match: [{prs: own}], stale_lock_hours: 5", want: "additional properties 'stale_lock_hours' not allowed"},
		{name: "mode", entry: "match: [{prs: own}], mode: autonomous", want: "additional properties 'mode' not allowed"},
		{name: "nested overrides", entry: "match: [{prs: own}], overrides: []", want: "additional properties 'overrides' not allowed"},
		{name: "no match", entry: "agent: claude", want: "missing property 'match'"},
		{name: "empty match", entry: "match: []", want: "minItems: got 0, want 1"},
		{name: "unknown attribute", entry: "match: [{repos: a/b}]", want: "additional properties 'repos' not allowed"},
		{name: "bad prs", entry: "match: [{prs: mine}]", want: "/overrides/0/match/0/prs"},
		{name: "PR URL", entry: "match: [{url: 'https://github.com/a/b/pull/1'}]", want: "/overrides/0/match/0/url"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte("overrides: [{"+tc.entry+"}]\n"), "c.yaml")
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestOverridesAreCrossChecked(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config string
		want   string
	}{
		{
			name:   "an override against the global config",
			config: "sandbox: read-only\noverrides: [{match: [{prs: own}], push: allow}]",
			want:   "overrides[0] (prs: own): push: 'allow' conflicts with sandbox: read-only",
		},
		{
			name: "overrides on others' PRs together",
			config: "overrides:\n- {match: [{prs: others}], others_prs: {sandbox: read-only}}\n" +
				"- {match: [{repo: a/*}], others_prs: {allow_push: true}}",
			want: "overrides on others' PRs: others_prs.allow_push: true conflicts",
		},
		{
			name:   "a broken glob",
			config: "overrides: [{match: [{repo: 'a/[b'}], agent: claude}]",
			want:   "overrides[0].match: bad glob",
		},
		{
			name:   "an unknown classifier",
			config: "overrides: [{match: [{prs: others}], tool_gate: {classifier: nope}}]",
			want:   "tool_gate.classifier: unknown classifier 'nope'",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.config), "c.yaml")
			require.ErrorContains(t, err, tc.want)
		})
	}
	// own and others' overrides never apply together
	_, err := Parse([]byte("overrides:\n- {match: [{prs: own}], sandbox: read-only}\n- {match: [{prs: others}], push: allow}"), "c.yaml")
	require.NoError(t, err)
}

func TestNoOverridesChangeNothing(t *testing.T) {
	// golden: without overrides, every PR gets the config byte for byte
	paths, err := filepath.Glob("../../docs/examples/configs/*.yaml")
	require.NoError(t, err)
	texts := map[string]string{"config.example.yaml": Example}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		require.NoError(t, err)
		texts[filepath.Base(p)] = string(b)
	}
	for name, text := range texts {
		c, err := Parse([]byte(text), name)
		require.NoError(t, err)
		if len(c.Overrides) > 0 {
			continue
		}
		want, err := yaml.Marshal(c)
		require.NoError(t, err)
		for _, own := range []bool{true, false} {
			scoped := c.For("my-org/repo", own)
			got, err := yaml.Marshal(scoped)
			require.NoError(t, err)
			require.Equal(t, string(want), string(got), name)
			require.Empty(t, c.Applied("my-org/repo", own))
		}
	}
}

func TestPinnedKeysBeatOverrides(t *testing.T) {
	c, err := Parse([]byte("overrides: [{match: [{prs: others}], agent: claude, sandbox: read-only}]"), "c.yaml")
	require.NoError(t, err)
	c.Pin("agent")
	c, err = Validate(c, "flags")
	require.NoError(t, err)
	got := c.For("a/b", false)
	require.Equal(t, "codex", got.Agent)
	require.Equal(t, ReadOnly, got.Sandbox)
}
