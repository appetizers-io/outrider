// Package doctor checks the local setup: the tools, logins and settings
// outrider needs, each with the fix for what is missing. It only reads: no
// writes, no migration, no GitHub writes, no agent session.
//
// The checks resolve the launcher, terminal, classifiers and sandbox with the
// same functions the watcher uses at startup, so both agree.
package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/appetizers-io/outrider/internal/approve"
	"github.com/appetizers-io/outrider/internal/classifier"
	"github.com/appetizers-io/outrider/internal/config"
	"github.com/appetizers-io/outrider/internal/github"
	"github.com/appetizers-io/outrider/internal/migrate"
	"github.com/appetizers-io/outrider/internal/poll"
	"github.com/appetizers-io/outrider/internal/proc"
	"github.com/appetizers-io/outrider/internal/session"
	"github.com/appetizers-io/outrider/internal/watch"
)

// Status is how a check went. Only Fail makes doctor exit non-zero.
type Status string

// The statuses, from good to bad.
const (
	OK   Status = "ok"
	Info Status = "info"
	Warn Status = "warn"
	Fail Status = "fail"
)

// Result is one check: what it found and, unless ok, how to fix it.
type Result struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail"`
	Fix    string `json:"fix"`
}

// Input is what the checks look at.
type Input struct {
	Settings  watch.Settings // the defaults when the config is invalid
	ConfigErr error          // why the config could not be loaded
	Deps      watch.Deps
	Dialogs   approve.Platform
}

// requiredScope is the gh token scope outrider reads GitHub with: private
// PRs, and the notifications API accepts it in place of notifications.
const requiredScope = "repo"

var checks = []func(context.Context, *Input) Result{
	checkConfig, checkGitHub, checkGit, checkAgent, checkOtherAgent, checkLauncher, checkDialogs,
	checkLaunchCheck, checkToolGate, checkSandbox, checkCheckout, checkFiles, checkLeftovers,
}

// Run runs every check.
func Run(ctx context.Context, in Input) []Result {
	out := make([]Result, 0, len(checks))
	for _, check := range checks {
		out = append(out, check(ctx, &in))
	}
	return out
}

// Failed tells whether any check failed.
func Failed(rs []Result) bool {
	return slices.ContainsFunc(rs, func(r Result) bool { return r.Status == Fail })
}

// WriteText prints one line per check, the fix below anything not ok.
func WriteText(w io.Writer, rs []Result) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, r := range rs {
		lines := strings.Split(r.Detail, "\n")
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", r.Status, r.Name, lines[0])
		for _, l := range lines[1:] {
			_, _ = fmt.Fprintf(tw, "\t\t%s\n", l)
		}
		if r.Fix != "" {
			_, _ = fmt.Fprintf(tw, "\t\tfix: %s\n", r.Fix)
		}
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// WriteJSON prints the results as a JSON list of {name, status, detail, fix}.
func WriteJSON(w io.Writer, rs []Result) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rs); err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	return nil
}

func checkConfig(_ context.Context, in *Input) Result {
	r := Result{Name: "config"}
	if in.ConfigErr != nil {
		r.Status = Fail
		r.Detail = in.ConfigErr.Error() + "\n(the checks below use the built-in defaults)"
		r.Fix = "correct the file (see docs/configuration.md) and run `outrider config check`; " +
			"`outrider config generate -o FILE` writes a valid starting point"
		return r
	}
	cfg := &in.Settings.Cfg
	mode, _ := watch.SandboxMode(cfg)
	r.Status = OK
	r.Detail = fmt.Sprintf("%s (mode %s, push %s, github_writes %s, %s)", config.Describe(in.Settings.ConfigSource),
		cfg.Mode, cfg.PushMode(), cfg.GitHubWritesMode(), mode)
	return r
}

// authStatus is the part of `gh auth status --json hosts` doctor reads; the
// token is never decoded.
type authStatus struct {
	Hosts map[string][]authAccount `json:"hosts"`
}

type authAccount struct {
	State       string `json:"state"`
	Error       string `json:"error"`
	Active      bool   `json:"active"`
	TokenSource string `json:"tokenSource"`
	Scopes      string `json:"scopes"`
}

// scopes splits gh's scope list ("gist, 'repo'") into names.
func scopes(s string) []string {
	var out []string
	for _, f := range strings.Split(s, ",") {
		if f = strings.Trim(strings.TrimSpace(f), `'"`); f != "" {
			out = append(out, f)
		}
	}
	return out
}

func checkGitHub(ctx context.Context, in *Input) Result {
	d := in.Deps
	r := Result{Name: "github", Status: Fail}
	if _, err := d.LookPath("gh"); err != nil {
		r.Detail, r.Fix = "gh not found on PATH", "install the GitHub CLI: https://cli.github.com"
		return r
	}
	host := d.Getenv("GH_HOST")
	if host == "" {
		host = "github.com"
	}
	login := "gh auth login --hostname " + host
	st, err := github.JSON[authStatus](ctx, &github.Client{Run: d.Run}, "auth", "status", "--active", "--hostname", host, "--json", "hosts")
	if err != nil {
		r.Detail, r.Fix = err.Error(), "update gh (`gh auth status --json` is needed), then "+login
		return r
	}
	i := slices.IndexFunc(st.Hosts[host], func(a authAccount) bool { return a.Active })
	if i < 0 || st.Hosts[host][i].State != "success" {
		r.Detail, r.Fix = "gh is not logged in to "+host, login
		if i >= 0 && st.Hosts[host][i].Error != "" {
			r.Detail += ": " + st.Hosts[host][i].Error
		}
		return r
	}
	acct := st.Hosts[host][i]
	user, err := (&github.Client{Run: d.Run}).Me(ctx)
	if err != nil {
		r.Detail, r.Fix = "cannot read the GitHub user: "+err.Error(), "check `gh api user` and your network"
		return r
	}
	have := scopes(acct.Scopes)
	r.Detail = fmt.Sprintf("%s as %s (prompts call you %s), token from %s", host, user.Login, watch.Owner(&in.Settings.Cfg, user), acct.TokenSource)
	if len(have) == 0 {
		r.Status = Warn
		r.Detail += ", no OAuth scopes to check (e.g. a fine-grained token)"
		r.Fix = "make sure the token can read notifications and the watched repos' pull requests"
		return r
	}
	r.Detail += ", scopes " + strings.Join(have, ", ")
	if !slices.Contains(have, requiredScope) {
		r.Detail += "; missing " + requiredScope
		r.Fix = "gh auth refresh --hostname " + host + " --scopes " + requiredScope
		return r
	}
	if len(in.Settings.Cfg.OthersPRs.ReviewForks) > 0 && !slices.Contains(have, "workflow") {
		// GitHub refuses a push that adds or changes .github/workflows without it
		r.Status = Warn
		r.Detail += "; missing workflow, so review sessions can't push CI workflows to others_prs.review_forks"
		r.Fix = "gh auth refresh --hostname " + host + " --scopes workflow"
		return r
	}
	r.Status = OK
	return r
}

// version is the first line a tool prints for --version.
func version(ctx context.Context, d watch.Deps, args ...string) (string, error) {
	res, err := d.Run(ctx, proc.Cmd{Args: args})
	if err != nil {
		return "", err //nolint:wrapcheck // a *proc.Error names the command already
	}
	line, _, _ := strings.Cut(strings.TrimSpace(res.Stdout), "\n")
	return line, nil
}

func checkGit(ctx context.Context, in *Input) Result {
	r := Result{Name: "git", Status: Fail}
	if _, err := in.Deps.LookPath("git"); err != nil {
		r.Detail, r.Fix = "git not found on PATH", "install git: https://git-scm.com/downloads"
		return r
	}
	v, err := version(ctx, in.Deps, "git", "--version")
	if err != nil {
		r.Detail, r.Fix = err.Error(), "reinstall git"
		return r
	}
	r.Status, r.Detail = OK, v
	return r
}

var agentInstall = map[string]string{
	"claude": "install Claude Code: https://docs.claude.com/en/docs/claude-code",
	"codex":  "install Codex: https://github.com/openai/codex",
}

func agentResult(ctx context.Context, d watch.Deps, agent string) (string, error) {
	path, err := d.LookPath(agent)
	if err != nil {
		return "", fmt.Errorf("%s not found on PATH", agent)
	}
	v, err := version(ctx, d, agent, "--version")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s %s (%s)", agent, v, path), nil
}

func checkAgent(ctx context.Context, in *Input) Result {
	agent := in.Settings.Cfg.Agent
	detail, err := agentResult(ctx, in.Deps, agent)
	if err != nil {
		return Result{Name: "agent", Status: Fail, Detail: err.Error(), Fix: agentInstall[agent] + ", or set agent in the config"}
	}
	return Result{Name: "agent", Status: OK, Detail: detail + ", the configured agent"}
}

func checkOtherAgent(ctx context.Context, in *Input) Result {
	other := "claude"
	if in.Settings.Cfg.Agent == "claude" {
		other = "codex"
	}
	detail, err := agentResult(ctx, in.Deps, other)
	if err != nil {
		detail = err.Error()
	}
	return Result{Name: "other agent", Status: Info, Detail: detail + ", not configured"}
}

func checkLauncher(ctx context.Context, in *Input) Result {
	cfg, d := &in.Settings.Cfg, in.Deps
	launcher, terminal := watch.ResolveLauncher(ctx, cfg, d)
	r := Result{Name: "launcher", Status: Fail, Detail: fmt.Sprintf("%s (launcher: %s), terminal %s", launcher, cfg.Launcher, terminal)}
	tools, err := watch.LauncherTools(launcher, terminal, d)
	if err != nil {
		r.Detail += ": " + err.Error()
		r.Fix = "set terminal in the config (see docs/terminals.md), or the launcher that works here"
		return r
	}
	for _, tool := range tools {
		if _, err := d.LookPath(tool); err != nil {
			r.Detail += ": " + tool + " not found on PATH"
			r.Fix = "install " + tool + ", or set launcher (see docs/terminals.md)"
			return r
		}
	}
	r.Status = OK
	return r
}

func checkDialogs(ctx context.Context, in *Input) Result {
	cfg := &in.Settings.Cfg
	modes := fmt.Sprintf("push %s, github_writes %s", cfg.PushMode(), cfg.GitHubWritesMode())
	r := Result{Name: "dialogs"}
	if cfg.PushMode() != "ask" && cfg.GitHubWritesMode() != "ask" {
		r.Status, r.Detail = Info, "not needed ("+modes+")"
		return r
	}
	never := "or set push and github_writes to never or allow"
	backend, err := in.Dialogs.Backend()
	switch {
	case err != nil:
		r.Status, r.Detail = Warn, "no approval dialog here: ask will deny ("+modes+")"
		r.Fix = map[string]string{
			"darwin":  "/usr/bin/osascript is missing; " + never,
			"windows": "Windows PowerShell is missing; " + never,
		}[in.Deps.GOOS]
		if r.Fix == "" {
			r.Fix = "install zenity or kdialog and run in a desktop session ($DISPLAY or $WAYLAND_DISPLAY), " + never
		}
	case !watch.Desktop(ctx, in.Deps):
		r.Status, r.Detail = Warn, backend+", but no desktop session: ask will deny ("+modes+")"
		r.Fix = "run outrider in your desktop session, " + never
	default:
		r.Status, r.Detail = OK, backend+" ("+modes+")"
	}
	return r
}

func checkLaunchCheck(_ context.Context, in *Input) Result {
	cfg := &in.Settings.Cfg
	r, note, _, _ := watch.Classifiers(cfg, in.Deps.LookPath)
	return classifierResult("launch check", cfg, cfg.LaunchCheck.Classifier, r, note)
}

func checkToolGate(_ context.Context, in *Input) Result {
	cfg := &in.Settings.Cfg
	_, _, r, note := watch.Classifiers(cfg, in.Deps.LookPath)
	name := cfg.ToolGate.Classifier
	if cfg.Mode == "autonomous" {
		name = nil
	}
	return classifierResult("tool gate", cfg, name, r, note)
}

// classifierResult judges a role: off by choice is info, off because the
// classifier can't run is a warning.
func classifierResult(role string, cfg *config.Config, name *string, r *classifier.Resolved, note string) Result {
	switch {
	case r != nil:
		return Result{Name: role, Status: OK, Detail: "on (" + note + ")"}
	case name == nil || cfg.Classifiers[*name].Disabled():
		return Result{Name: role, Status: Info, Detail: "off (" + note + ")"}
	}
	fix := "point classifiers." + *name + " at an executable (~ is expanded); see docs/classifiers.md"
	if cfg.Classifiers[*name].Kind == config.KindJev {
		fix = "install jev-use (or npx) and export a backend key (" + strings.Join(config.JevBackendEnv, ", ") +
			"); see docs/classifiers.md"
	}
	return Result{Name: role, Status: Warn, Detail: "off (" + note + ")", Fix: fix}
}

func checkSandbox(ctx context.Context, in *Input) Result {
	cfg, d := &in.Settings.Cfg, in.Deps
	mode, on := watch.SandboxMode(cfg)
	if !on {
		return Result{Name: "sandbox", Status: Info, Detail: strings.TrimPrefix(mode, "sandbox: ")}
	}
	mode = strings.TrimPrefix(mode, "sandbox: ")
	if err := session.SandboxSupport(ctx, cfg.Agent, d.GOOS, watch.CodexHome(d), d.LookPath, d.Run); err != nil {
		return Result{
			Name: "sandbox", Status: Fail, Detail: mode + ": " + err.Error() + "; sandboxed sessions are refused",
			Fix: "install or update what is named above, or set sandbox and others_prs.sandbox to off (see docs/safety.md)",
		}
	}
	return Result{Name: "sandbox", Status: OK, Detail: mode + " (" + session.SandboxNote(cfg.Agent) + ")"}
}

func checkCheckout(ctx context.Context, in *Input) Result {
	repo, loc, err := watch.LocalCheckout(ctx, &in.Settings, in.Deps)
	switch {
	case err != nil:
		return Result{Name: "checkout", Status: Fail, Detail: err.Error(), Fix: "run inside the checkout and check `git remote -v`"}
	case repo == "":
		return Result{Name: "checkout", Status: Info, Detail: "not inside a GitHub checkout; repos come from repos.include"}
	}
	return Result{Name: "checkout", Status: Info, Detail: fmt.Sprintf("%s at %s (remote %s)", repo, loc.Path, loc.Remote)}
}

// creatable tells whether dir exists as a directory, or whether its nearest
// existing parent is one, so it can be created.
func creatable(dir string) error {
	for p := dir; ; p = filepath.Dir(p) {
		st, err := os.Stat(p)
		switch {
		case err == nil && st.IsDir():
			return nil
		case err == nil:
			return fmt.Errorf("%s is not a directory", p)
		case !errors.Is(err, os.ErrNotExist):
			return fmt.Errorf("%s: %w", p, err)
		case filepath.Dir(p) == p:
			return fmt.Errorf("%s: %w", dir, err)
		}
	}
}

func checkFiles(_ context.Context, in *Input) Result {
	home := in.Deps.Home
	state := watch.StatePath(home)
	dirs := []string{filepath.Dir(config.DefaultPath()), watch.CacheRoot(home), filepath.Dir(state)}
	for _, dir := range dirs {
		if err := creatable(dir); err != nil {
			return Result{Name: "files", Status: Warn, Detail: err.Error(), Fix: "remove or rename what is in the way of " + dir}
		}
	}
	if _, err := poll.LoadState(state); err != nil {
		return Result{Name: "files", Status: Warn, Detail: err.Error(), Fix: "start once with --reset-state to forget the broken state"}
	}
	return Result{Name: "files", Status: OK, Detail: strings.Join(dirs, ", ")}
}

func checkLeftovers(_ context.Context, in *Input) Result {
	d := in.Deps
	r := Result{Name: "llm-review-agent", Status: OK, Detail: "nothing left of the old name"}
	var found, fixes []string
	for _, m := range migrate.Moves(d.Home, config.BaseDir()) {
		if _, err := os.Stat(m.Old); err != nil {
			continue
		}
		if _, err := os.Stat(m.New); err != nil {
			found = append(found, m.Old+" (moved to "+m.New+" on the next start)")
			continue
		}
		found = append(found, m.Old+" (left over next to "+m.New+")")
		fixes = append(fixes, "remove "+m.Old+" once nothing in it is needed")
	}
	if path, err := d.LookPath("llm-review-agent"); err == nil {
		found = append(found, "binary "+path)
		fixes = append(fixes, "remove "+path)
	}
	if d.Getenv(config.LegacyEnvVar) != "" {
		found = append(found, "$"+config.LegacyEnvVar)
		fixes = append(fixes, "rename $"+config.LegacyEnvVar+" to $"+config.EnvVar)
	}
	switch {
	case len(fixes) > 0:
		r.Status, r.Fix = Warn, strings.Join(fixes, "; ")
	case len(found) > 0:
		r.Status = Info
	}
	if len(found) > 0 {
		r.Detail = strings.Join(found, ", ")
	}
	return r
}
