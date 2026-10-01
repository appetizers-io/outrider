package config

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/gobwas/glob"
)

var repoPrefix = regexp.MustCompile(`^(https?://|git@|ssh://git@)github\.com[/:]`)

// RepoPattern accepts owner/repo globs as well as GitHub URLs.
func RepoPattern(x string) string {
	x = repoPrefix.ReplaceAllString(strings.TrimSpace(x), "")
	return strings.TrimSuffix(strings.TrimRight(x, "/"), ".git")
}

// Globs are compiled patterns; they match when any of them does.
type Globs []*glob.Pattern

// Match tells whether any glob matches s.
func (g Globs) Match(s string) bool {
	for _, p := range g {
		if p.Match(s) {
			return true
		}
	}
	return false
}

// RepoGlobs compiles owner/repo globs (or GitHub URLs): * also matches "/",
// ?, [seq], [!seq] and {a,b} work. A broken pattern is an error, never a
// pattern that silently matches nothing.
func RepoGlobs(patterns []string) (Globs, error) {
	out := make(Globs, 0, len(patterns))
	for _, p := range patterns {
		g, err := glob.Compile(RepoPattern(p))
		if err != nil {
			return nil, fmt.Errorf("bad glob %q: %w", p, err)
		}
		out = append(out, g)
	}
	return out, nil
}

// loginLiterals are glob characters that are literal in logins like "netlify[bot]".
var loginLiterals = strings.NewReplacer("[", `\[`, "]", `\]`, "{", `\{`, "}", `\}`, `\`, `\\`)

// LoginGlobs compiles login globs: * and ? only, case-insensitive. Match
// them against lower-cased logins.
func LoginGlobs(patterns []string) (Globs, error) {
	out := make(Globs, 0, len(patterns))
	for _, p := range patterns {
		g, err := glob.Compile(loginLiterals.Replace(strings.ToLower(p)))
		if err != nil {
			return nil, fmt.Errorf("bad glob %q: %w", p, err)
		}
		out = append(out, g)
	}
	return out, nil
}

// MatchLogin tells whether login globs match a login, ignoring case.
func (g Globs) MatchLogin(login string) bool { return g.Match(strings.ToLower(login)) }
