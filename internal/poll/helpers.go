package poll

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

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

// fnmatch translates a shell glob (*, ?, [seq], [!seq]) to a regexp, like
// Python's fnmatch: * also matches "/".
func fnmatch(pattern string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; c {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		case '[':
			end := strings.IndexByte(pattern[i+1:], ']')
			if end < 0 {
				b.WriteString(`\[`)
				continue
			}
			class := pattern[i+1 : i+1+end]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			b.WriteString("[" + strings.ReplaceAll(class, `\`, `\\`) + "]")
			i += end + 1
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	rx, err := regexp.Compile("(?s)" + b.String())
	if err != nil {
		return regexp.MustCompile("^" + regexp.QuoteMeta(pattern) + "$")
	}
	return rx
}

// RepoOK tells whether a repo passes the include and exclude globs.
func RepoOK(repo string, include, exclude []string) bool {
	match := func(p string) bool { return fnmatch(p).MatchString(repo) }
	if len(include) > 0 && !slices.ContainsFunc(include, match) {
		return false
	}
	return !slices.ContainsFunc(exclude, match)
}

// LoginGlob matches logins case-insensitively with * and ? only: logins like
// "netlify[bot]" contain brackets, which fnmatch would read as a class.
func LoginGlob(pattern string) *regexp.Regexp {
	rx := regexp.QuoteMeta(pattern)
	rx = strings.ReplaceAll(rx, `\*`, ".*")
	rx = strings.ReplaceAll(rx, `\?`, ".")
	return regexp.MustCompile("(?i)^(?:" + rx + ")$")
}

// IgnoredAuthor tells whether ignore_authors matches the login.
func IgnoredAuthor(user string, ignore []string) bool {
	return slices.ContainsFunc(ignore, func(p string) bool { return LoginGlob(p).MatchString(user) })
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

// pyJSON encodes a string like Python's json.dumps (ensure_ascii).
func pyJSON(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r < 0x20 || r > 0x7f:
			if r > 0xffff {
				r1, r2 := utf16.EncodeRune(r)
				fmt.Fprintf(&b, `\u%04x\u%04x`, r1, r2)
			} else {
				fmt.Fprintf(&b, `\u%04x`, r)
			}
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func jsonOrNull(s *string) string {
	if s == nil {
		return "null"
	}
	return pyJSON(*s)
}

// Fingerprint identifies the state of a PR's reviews and comments. The
// encoding must stay byte-identical to the Python version: stored
// fingerprints are compared across versions, and a change would relaunch
// every watched PR.
func Fingerprint(items []github.Activity) string {
	parts := make([]string, len(items))
	for i, x := range items {
		id := "null"
		if x.ID != nil {
			id = strconv.FormatInt(*x.ID, 10)
		}
		parts[i] = "[" + strings.Join([]string{id, jsonOrNull(x.State), jsonOrNull(x.UpdatedAt),
			jsonOrNull(x.SubmittedAt), jsonOrNull(x.User)}, ",") + "]"
	}
	sum := sha256.Sum256([]byte("[" + strings.Join(parts, ",") + "]"))
	return hex.EncodeToString(sum[:])
}

func isWord(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// Mentions tells whether text @mentions login: not inside a word, an email
// address or a path, and not a longer login.
func Mentions(text, login string) bool {
	at := "@" + login
	for i := 0; i+len(at) <= len(text); i++ {
		if text[i] != '@' || !strings.EqualFold(text[i:i+len(at)], at) {
			continue
		}
		if prev, _ := utf8.DecodeLastRuneInString(text[:i]); i > 0 && (isWord(prev) || prev == '@' || prev == '/') {
			continue
		}
		if next, _ := utf8.DecodeRuneInString(text[i+len(at):]); i+len(at) < len(text) && (isWord(next) || next == '-') {
			continue
		}
		return true
	}
	return false
}
