package poll

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/dlclark/regexp2"
	"github.com/gobwas/glob"

	"github.com/appetizers-io/llm-review-agent/internal/github"
)

var (
	repoPrefix = regexp.MustCompile(`^(https?://|git@|ssh://git@)github\.com[/:]`)
	// RepoName is an owner/repo without wildcards.
	RepoName = regexp.MustCompile(`^[\w.-]+/[\w.-]+$`)
)

// RepoPattern accepts owner/repo globs as well as GitHub URLs.
func RepoPattern(x string) string {
	x = repoPrefix.ReplaceAllString(strings.TrimSpace(x), "")
	return strings.TrimSuffix(strings.TrimRight(x, "/"), ".git")
}

// RepoOK tells whether a repo passes the include and exclude globs.
func RepoOK(repo string, include, exclude []string) bool {
	// shell globs like Python's fnmatch: * also matches "/", [seq] and [!seq] work
	match := func(p string) bool {
		g, err := glob.Compile(p)
		return err == nil && g.Match(repo)
	}
	if len(include) > 0 && !slices.ContainsFunc(include, match) {
		return false
	}
	return !slices.ContainsFunc(exclude, match)
}

// loginLiterals are glob characters that are literal in logins like "netlify[bot]".
var loginLiterals = strings.NewReplacer("[", `\[`, "]", `\]`, "{", `\{`, "}", `\}`, `\`, `\\`)

// LoginGlob matches logins case-insensitively with * and ? only.
func LoginGlob(pattern string, login string) bool {
	g, err := glob.Compile(loginLiterals.Replace(strings.ToLower(pattern)))
	return err == nil && g.Match(strings.ToLower(login))
}

// IgnoredAuthor tells whether ignore_authors matches the login.
func IgnoredAuthor(user string, ignore []string) bool {
	return slices.ContainsFunc(ignore, func(p string) bool { return LoginGlob(p, user) })
}

// OnlyNoise tells whether nothing in items needs me: all by ignored authors
// (or by me when mineToo).
func OnlyNoise(items []github.Activity, login string, ignore []string, mineToo bool) bool {
	for _, x := range items {
		mine := mineToo && strings.EqualFold(x.UserLogin(), login)
		if !mine && !IgnoredAuthor(x.UserLogin(), ignore) {
			return false
		}
	}
	return true
}

// NewerThan is the activity newer than t, oldest first (all of it, capped to
// the last ten, when never judged).
func NewerThan(items []github.Activity, t string) []github.Activity {
	ordered := slices.Clone(items)
	slices.SortStableFunc(ordered, func(a, b github.Activity) int { return cmp.Compare(a.At, b.At) })
	if t == "" {
		return ordered[max(0, len(ordered)-10):]
	}
	var out []github.Activity
	for _, x := range ordered {
		if x.At > t {
			out = append(out, x)
		}
	}
	return out
}

// asciiJSON is JSON with every non-ASCII character escaped, like Python's
// json.dumps (ensure_ascii, the default).
func asciiJSON(v any) (string, error) {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", fmt.Errorf("encode: %w", err)
	}
	var out strings.Builder
	for _, r := range strings.TrimSuffix(b.String(), "\n") {
		switch {
		case r < utf8.RuneSelf:
			out.WriteRune(r)
		case r > 0xffff:
			r1, r2 := utf16.EncodeRune(r)
			fmt.Fprintf(&out, `\u%04x\u%04x`, r1, r2)
		default:
			fmt.Fprintf(&out, `\u%04x`, r)
		}
	}
	return out.String(), nil
}

// Fingerprint identifies the state of a PR's reviews and comments. The
// encoding must stay byte-identical to the Python version: stored
// fingerprints are compared across versions, and a change would relaunch
// every watched PR.
func Fingerprint(items []github.Activity) string {
	data := make([][]any, len(items))
	for i, x := range items {
		data[i] = []any{x.ID, x.State, x.UpdatedAt, x.SubmittedAt, x.User}
	}
	raw, err := asciiJSON(data)
	if err != nil {
		panic(err) // ints, strings and nulls always encode
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// Mentions tells whether text @mentions login: not inside a word, an email
// address or a path, and not a longer login. The Python version's regexp,
// with lookbehind (regexp2: Go's regexp has none).
func Mentions(text, login string) bool {
	rx := regexp2.MustCompile(`(?<![\w@/])@`+regexp2.Escape(login)+`(?![\w-])`, regexp2.IgnoreCase)
	ok, err := rx.MatchString(text)
	return err == nil && ok
}
