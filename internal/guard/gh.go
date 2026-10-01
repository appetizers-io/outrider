// Package guard holds the gh and git guards that come first on an agent
// session's PATH. The binary is multi-call: run as `gh` or `git`, main hands
// the arguments to RunGH or RunGit, which decide and then run the real binary.
//
// gh: reads pass through. $LLM_REVIEW_AGENT_GH_WRITES decides what
// comment-like writes on the session's PR do (comments, review comments and
// replies, reviews, reactions):
//
//	never: refused
//	ask:   a native dialog shows the command and text; only "Post" lets it through
//	allow: passes
//
// Every other GitHub write (merge, close, edits of the PR, labels, graphql
// mutations, other repos or PRs) is refused in every mode. Pushing goes
// through git, not gh.
package guard

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/kballard/go-shellquote"
)

// Environment contract between the session runner and the guards.
const (
	EnvRealGH   = "LLM_REVIEW_AGENT_REAL_GH"
	EnvRealGit  = "LLM_REVIEW_AGENT_REAL_GIT"
	EnvGHWrites = "LLM_REVIEW_AGENT_GH_WRITES"
	EnvPush     = "LLM_REVIEW_AGENT_PUSH"
	EnvRepo     = "LLM_REVIEW_AGENT_REPO"
	EnvPR       = "LLM_REVIEW_AGENT_PR"
	EnvSession  = "LLM_REVIEW_AGENT_SESSION"
)

var ghRead = map[string][]string{
	"pr":     {"view", "diff", "checks", "list", "status", "checkout"},
	"run":    {"view", "list", "watch", "download"},
	"repo":   {"view"},
	"auth":   {"status"},
	"issue":  {"view", "list"},
	"search": {"prs", "issues", "code", "commits"},
	"browse": nil, // any arguments
}

var ghWrites = map[string][]string{"pr": {"comment", "review"}, "issue": {"comment"}}

const n, id = `(?P<n>\d+)`, `\d+`

var apiWrites = []struct {
	method string
	path   *regexp.Regexp
}{
	{"POST", regexp.MustCompile(`^pulls/` + n + `/comments$`)},
	{"POST", regexp.MustCompile(`^pulls/` + n + `/comments/` + id + `/replies$`)},
	{"POST", regexp.MustCompile(`^pulls/` + n + `/reviews$`)},
	{"POST", regexp.MustCompile(`^pulls/` + n + `/reviews/` + id + `/events$`)},
	{"POST", regexp.MustCompile(`^issues/` + n + `/comments$`)},
	{"PATCH", regexp.MustCompile(`^(issues|pulls)/comments/` + id + `$`)},
	{"POST", regexp.MustCompile(`^issues/` + n + `/reactions$`)},
	{"POST", regexp.MustCompile(`^(issues|pulls)/comments/` + id + `/reactions$`)},
	{"DELETE", regexp.MustCompile(`^issues/` + n + `/reactions/` + id + `$`)},
	{"DELETE", regexp.MustCompile(`^(issues|pulls)/comments/` + id + `/reactions/` + id + `$`)},
}

var (
	apiValue = []string{"-X", "--method", "-f", "-F", "--field", "--raw-field", "-H",
		"--header", "--input", "-q", "--jq", "-t", "--template",
		"--hostname", "--cache", "-p", "--preview"}
	cliValue  = []string{"-b", "--body", "-F", "--body-file", "-R", "--repo"}
	repoPath  = regexp.MustCompile(`^repos/([^/]+/[^/]+)/(.+)$`)
	prURL     = regexp.MustCompile(`^https://github\.com/([^/]+/[^/]+)/(?:pull|issues)/(\d+)(?:[/#?].*)?$`)
	githubURL = regexp.MustCompile(`^(https://)?github\.com/`)
)

// GHSession is what the gh guard knows about its session.
type GHSession struct {
	Mode string // never | ask | allow
	Repo string // owner/repo, lower case
	PR   string
}

// GHSessionFromEnv reads the session from the environment.
func GHSessionFromEnv(getenv func(string) string) GHSession {
	mode := getenv(EnvGHWrites)
	if mode == "" {
		mode = "never"
	}
	return GHSession{Mode: mode, Repo: strings.ToLower(getenv(EnvRepo)), PR: getenv(EnvPR)}
}

func (s GHSession) writable() bool { return s.Mode == "ask" || s.Mode == "allow" }

func (s GHSession) target() string {
	repo, pr := s.Repo, s.PR
	if repo == "" {
		repo = "?"
	}
	if pr == "" {
		pr = "?"
	}
	return "PR " + repo + "#" + pr
}

// GHDecision is what to do with a gh invocation.
type GHDecision struct {
	Deny  string   // non-empty: refuse with this message
	Write bool     // a post on the session's PR
	Text  string   // what the post would say, for the dialog
	Args  []string // what to run the real gh with
}

// denied ends the decision; panicking keeps the ported control flow flat.
type denied struct{ msg string }

func (s GHSession) deny(args []string, why string) {
	panic(denied{s.denyMsg(args, why)})
}

func (s GHSession) denyMsg(args []string, why string) string {
	msg := "llm-review-agent guard: blocked `gh " + strings.Join(args, " ") + "` (" + why + "). "
	if s.writable() {
		msg += "Only comments, review comments and replies, reviews and reactions on " +
			s.target() + " can be posted, through plain `gh`; explain anything else locally instead."
	} else {
		msg += "GitHub is read-only here; explain it locally instead."
	}
	return msg
}

type opt struct {
	k, v string
	hasV bool
}

// options splits args into (flag, value) pairs and positionals.
func options(args, withValue []string) ([]opt, []string) {
	var opts []opt
	var pos []string
	for i := 0; i < len(args); i++ {
		x := args[i]
		switch {
		case strings.HasPrefix(x, "--") && strings.Contains(x, "="):
			k, v, _ := strings.Cut(x, "=")
			opts = append(opts, opt{k: k, v: v, hasV: true})
		case slices.Contains(withValue, x) && i+1 < len(args):
			opts = append(opts, opt{k: x, v: args[i+1], hasV: true})
			i++
		case len(x) > 2 && x[0] == '-' && x[1] != '-':
			// short flags packed after one dash, read like gh does:
			// -XPOST, -fbody=x, -iX PUT, -iXDELETE, -aRo/r
			for j := 1; j < len(x); j++ {
				f := "-" + x[j:j+1]
				if !slices.Contains(withValue, f) {
					opts = append(opts, opt{k: f})
					continue
				}
				switch rest := x[j+1:]; {
				case rest != "":
					opts = append(opts, opt{k: f, v: strings.TrimPrefix(rest, "="), hasV: true})
				case i+1 < len(args):
					i++
					opts = append(opts, opt{k: f, v: args[i], hasV: true})
				default:
					opts = append(opts, opt{k: f})
				}
				break
			}
		case strings.HasPrefix(x, "-"):
			opts = append(opts, opt{k: x})
		default:
			pos = append(pos, x)
		}
	}
	return opts, pos
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}

func (s GHSession) readFile(args []string, path string) string {
	if path == "-" {
		s.deny(args, "reading the text from stdin; use a file")
	}
	data, err := os.ReadFile(expandHome(path))
	if err != nil {
		reason := err.Error()
		var pe *os.PathError
		if errors.As(err, &pe) {
			reason = pe.Err.Error()
		}
		s.deny(args, "cannot read "+path+": "+reason)
	}
	return string(data)
}

func (s GHSession) onThisPR(args []string, repo *string, pr *string) {
	if s.Repo == "" || s.PR == "" {
		s.deny(args, "no session PR known")
	}
	if repo != nil && strings.ToLower(*repo) != s.Repo {
		s.deny(args, "only "+s.target()+" can be written to")
	}
	if pr != nil && *pr != s.PR {
		s.deny(args, "only "+s.target()+" can be written to")
	}
}

// apiCall decides `gh api`: (false, "") for a read, else the text a write would post.
func (s GHSession) apiCall(args []string) (bool, string) {
	opts, pos := options(args[1:], apiValue)
	method := ""
	var text []string
	for _, o := range opts {
		switch o.k {
		case "-X", "--method":
			method = strings.ToUpper(o.v)
		case "-f", "-F", "--field", "--raw-field":
			if !o.hasV {
				continue
			}
			key, val, _ := strings.Cut(o.v, "=")
			if (o.k == "-F" || o.k == "--field") && strings.HasPrefix(val, "@") {
				if slices.Contains(pos, "graphql") {
					s.deny(args, "graphql query from file")
				}
				val = s.readFile(args, val[1:])
			}
			text = append(text, key+": "+val)
		case "--input":
			v := o.v
			if v == "" {
				v = "-"
			}
			text = append(text, s.readFile(args, v))
		}
	}
	if slices.Contains(pos, "graphql") {
		// the query can also come from --input; any mention of a mutation is refused
		all := strings.ToLower(strings.Join(args, "\n") + "\n" + strings.Join(text, "\n"))
		if strings.Contains(all, "mutation") {
			s.deny(args, "graphql mutation")
		}
		return false, ""
	}
	if method == "" {
		method = "GET"
		if len(text) > 0 {
			method = "POST"
		}
	}
	if method == "GET" {
		return false, ""
	}
	endpoint := ""
	if len(pos) > 0 {
		endpoint = pos[0]
	}
	endpoint, _, _ = strings.Cut(strings.TrimLeft(endpoint, "/"), "?")
	m := repoPath.FindStringSubmatch(endpoint)
	if m == nil {
		s.deny(args, "non-GET api call")
	}
	for _, w := range apiWrites {
		hit := w.path.FindStringSubmatch(m[2])
		if hit == nil || method != w.method {
			continue
		}
		var pr *string
		if i := w.path.SubexpIndex("n"); i >= 0 {
			pr = &hit[i]
		}
		s.onThisPR(args, &m[1], pr)
		return true, strings.Join(text, "\n")
	}
	s.deny(args, "non-GET api call")
	return false, ""
}

func isDigits(s string) bool {
	return s != "" && strings.IndexFunc(s, func(r rune) bool { return r < '0' || r > '9' }) < 0
}

// cliCall decides `gh pr comment|review` and `gh issue comment`: the text it
// would post, and whether the command names no repo (gh would pick one from
// GH_REPO, the working directory or its resolved remote).
func (s GHSession) cliCall(args []string) (string, bool) {
	opts, pos := options(args[2:], cliValue)
	var repo *string
	var text []string
	for _, o := range opts {
		switch o.k {
		case "-R", "--repo":
			repo = &o.v
		case "-b", "--body":
			text = append(text, o.v)
		case "-F", "--body-file":
			v := o.v
			if v == "" {
				v = "-"
			}
			text = append(text, s.readFile(args, v))
		case "-w", "--web", "-e", "--editor", "--edit-last", "--delete-last":
			s.deny(args, o.k)
		}
	}
	if len(pos) != 1 {
		s.deny(args, "name the PR number explicitly")
	}
	ref := pos[0]
	if m := prURL.FindStringSubmatch(ref); m != nil {
		repo, ref = &m[1], m[2]
	}
	num := strings.TrimLeft(ref, "#")
	if !isDigits(num) {
		s.deny(args, "name the PR number explicitly")
	}
	noRepo := repo == nil
	if repo != nil {
		r := strings.TrimSuffix(githubURL.ReplaceAllString(*repo, ""), ".git")
		repo = &r
	}
	s.onThisPR(args, repo, &num)
	var kind []string
	for _, o := range opts {
		if slices.Contains([]string{"-a", "--approve", "-r", "--request-changes", "-c", "--comment"}, o.k) {
			kind = append(kind, o.k)
		}
	}
	out := strings.Join(text, "\n")
	if len(kind) > 0 {
		out = strings.Join(kind, " ") + "\n" + out
	}
	return out, noRepo
}

// DecideGH decides a gh invocation without running anything but file reads
// for the text of a post.
func DecideGH(args []string, s GHSession) (d GHDecision) {
	defer func() {
		if r := recover(); r != nil {
			den, ok := r.(denied)
			if !ok {
				panic(r)
			}
			d = GHDecision{Deny: den.msg}
		}
	}()
	if len(args) == 0 {
		s.deny(args, "no command")
	}
	switch {
	case args[0] == "api":
		d.Write, d.Text = s.apiCall(args)
	case len(args) > 1 && slices.Contains(ghWrites[args[0]], args[1]):
		if !s.writable() {
			s.deny(args, "subcommand not allowlisted")
		}
		var noRepo bool
		d.Text, noRepo = s.cliCall(args)
		d.Write = true
		if noRepo {
			d.Args = slices.Concat(args[:2], []string{"--repo", s.Repo}, args[2:])
		}
	default:
		subs, ok := ghRead[args[0]]
		if !ok {
			s.deny(args, "command not allowlisted")
		}
		if subs != nil && (len(args) < 2 || !slices.Contains(subs, args[1])) {
			s.deny(args, "subcommand not allowlisted")
		}
		if args[0] == "auth" && len(args) != 2 {
			s.deny(args, "only plain `gh auth status`; it must not print the token")
		}
	}
	if d.Write && !s.writable() {
		s.deny(args, "GitHub writes are off")
	}
	if d.Args == nil {
		d.Args = args
	}
	return d
}

// PostDialog is the approval dialog's text for a post.
func PostDialog(session string, args []string, text string) string {
	if session == "" {
		session = "agent session"
	}
	if n := utf8.RuneCountInString(text); n > 1500 {
		text = string([]rune(text)[:1500]) + fmt.Sprintf("\n[... %d more characters]", n-1500)
	}
	cmd := shellquote.Join(append([]string{"gh"}, args...)...)
	if utf8.RuneCountInString(cmd) > 300 {
		cmd = string([]rune(cmd)[:300]) + " ..."
	}
	body := session + " wants to post to GitHub:\n\n" + cmd
	if strings.TrimSpace(text) != "" {
		body += "\n\n" + text
	}
	return body
}
