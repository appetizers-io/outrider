// Package forgejo reads pull requests on a Forgejo (or Gitea) host, for a
// review rendered by `outrider render`. It implements forge.Reader in two
// ways, chosen per forge in the config:
//
//   - access: api, the REST API (/api/v1) with a token from an environment
//     variable or a file. The token is sent in the Authorization header only;
//     it never appears in a URL, a log line, an error or a written file.
//   - access: fj, the fj CLI (forgejo-cli) with its own login, so outrider
//     never sees a token. fj prints text, not JSON, so this mode gives less:
//     see FJ.
//
// Watching Forgejo activity, guarding writes and posting are not here.
package forgejo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/appetizers-io/outrider/internal/config"
	"github.com/appetizers-io/outrider/internal/forge"
	"github.com/appetizers-io/outrider/internal/github"
	"github.com/appetizers-io/outrider/internal/proc"
)

// Deps are what Open needs from the host.
type Deps struct {
	Run    proc.Runner         // runs fj
	Getenv func(string) string // reads token_env
	Home   string              // expands ~/ in token_file
	HTTP   *http.Client        // nil: a client with a 60 second timeout
}

// Reader is a Forgejo host as forge.Reader, plus what its context files hold.
type Reader interface {
	forge.Reader
	// ContextFiles are the files SaveContext writes, with what each holds.
	ContextFiles() [][2]string
}

// Open is the reader of a configured forge. With access: api it reads the
// token now, so a missing token fails here, naming where it was looked for.
func Open(f config.Forge, d Deps) (Reader, error) {
	if f.Kind != "forgejo" {
		return nil, fmt.Errorf("forge %s: unsupported kind %q", f.Host, f.Kind)
	}
	switch f.AccessMode() {
	case "fj":
		return &FJ{Host: f.Host, BaseURL: f.BaseURL(), Run: d.Run}, nil
	case "api":
		token, err := readToken(f, d)
		if err != nil {
			return nil, err
		}
		base, err := url.Parse(f.BaseURL())
		if err != nil || !f.TokenAllowed(base) {
			return nil, fmt.Errorf("forge %s: access: api would send the token over plain http; use https, or set insecure_http: true", f.Host)
		}
		client := &http.Client{Timeout: 60 * time.Second}
		if d.HTTP != nil {
			c := *d.HTTP
			client = &c
		}
		client.CheckRedirect = tokenRedirects(f, client.CheckRedirect)
		return &API{Host: f.Host, BaseURL: f.BaseURL(), HTTP: client, token: token}, nil
	}
	return nil, fmt.Errorf("forge %s: unsupported access %q", f.Host, f.Access)
}

// tokenRedirects refuses a redirect that would carry the token where the
// forge's URL may not: net/http forwards it to plain http on the same host.
func tokenRedirects(f config.Forge, next func(*http.Request, []*http.Request) error) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if !f.TokenAllowed(req.URL) {
			return fmt.Errorf("redirect to %s://%s refused: it would send the token over plain http", req.URL.Scheme, req.URL.Host)
		}
		if next != nil {
			return next(req, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
}

// readToken reads the token of an api forge. Errors name the variable or
// file, never what it holds.
func readToken(f config.Forge, d Deps) (string, error) {
	if f.TokenEnv != "" {
		if d.Getenv == nil {
			return "", fmt.Errorf("forge %s: cannot read $%s", f.Host, f.TokenEnv)
		}
		token := strings.TrimSpace(d.Getenv(f.TokenEnv))
		if token == "" {
			return "", fmt.Errorf("forge %s: $%s is empty or unset", f.Host, f.TokenEnv)
		}
		return token, nil
	}
	if f.TokenFile == "" {
		return "", fmt.Errorf("forge %s: access: api needs token_env or token_file", f.Host)
	}
	path := f.TokenFile
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		path = filepath.Join(d.Home, rest)
	}
	raw, err := os.ReadFile(path) //nolint:gosec // the owner's own config names the file
	if err != nil {
		var pe *os.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		return "", fmt.Errorf("forge %s: token_file %s: %w", f.Host, path, err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf("forge %s: token_file %s is empty", f.Host, path)
	}
	return token, nil
}

// repoName is owner/repo; anything else never reaches a URL or a command.
var repoName = regexp.MustCompile(`^[\w.-]+/[\w.-]+$`)

func checkRepo(repo string) error {
	if !repoName.MatchString(repo) || strings.Contains(repo, "..") {
		return fmt.Errorf("invalid repository %q: want owner/repo", repo)
	}
	return nil
}

// state is GitHub's PR state for a Forgejo one.
func state(open, merged bool) string {
	switch {
	case open:
		return "OPEN"
	case merged:
		return "MERGED"
	}
	return "CLOSED"
}

// failedStates are the commit status states that count as a failed check,
// in GitHub's spelling (github.FailingChecks).
var failedStates = []string{"FAILURE", "ERROR"}

// failing are the failed checks of a status rollup, never nil.
func failing(checks []github.Check) []github.Check {
	out := []github.Check{}
	for _, c := range checks {
		for _, s := range failedStates {
			if c.State == s {
				out = append(out, c)
			}
		}
	}
	return out
}

// Ping tells whether the forge at baseURL answers, without credentials, and
// its version when it tells one to anonymous callers. Any HTTP answer counts
// as reachable; an error means it did not answer.
func Ping(ctx context.Context, client *http.Client, baseURL string) (string, error) {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/api/v1/version", nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", errors.New(unwrapURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	var v struct {
		Version string `json:"version"`
	}
	if resp.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&v) == nil {
		return v.Version, nil
	}
	return "", nil
}

// Version is the server's version, asked with the token.
func (c *API) Version(ctx context.Context) (string, error) {
	v, err := getJSON[struct {
		Version string `json:"version"`
	}](ctx, c, "version", nil)
	return v.Version, err
}
