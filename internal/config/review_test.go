package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReviewSelectionAndValidation(t *testing.T) {
	cfg, err := Parse([]byte(`review:
  default: deep
  rules:
    - repos: ["org/*"]
      triggers: [mentions]
      profile: quick
`), "config.yaml")
	require.NoError(t, err)
	name, rule := cfg.Review.Select("org/repo", "mentions")
	require.Equal(t, "quick", name)
	require.Contains(t, rule, "rules[0]")
	name, _ = cfg.Review.Select("org/repo", "opt_in")
	require.Equal(t, "deep", name)
	name, _ = cfg.Review.Select("org/repo", "on_change")
	require.Equal(t, "standard", name)
	_, err = Parse([]byte("review: {default: missing}"), "config.yaml")
	require.ErrorContains(t, err, "unknown review profile")
	_, err = Parse([]byte("sandbox: read-only\nreview: {profiles: {deep: {evidence: {tests: e2e}}}}"), "config.yaml")
	require.ErrorContains(t, err, "e2e")
}

func TestReviewOptInAndTrustedTestExecution(t *testing.T) {
	cfg := Default()
	require.Nil(t, cfg.Review)
	cfg, err := Parse([]byte(`review:
  default: deep
  trusted_authors: [alice]
  trusted_repos: ["org/trusted"]
`), "config.yaml")
	require.NoError(t, err)
	require.True(t, cfg.Review.Trusted("org/repo", "Alice"))
	require.True(t, cfg.Review.Trusted("org/trusted", "bob"))
	require.False(t, cfg.Review.Trusted("org/repo", "bob"))
}
