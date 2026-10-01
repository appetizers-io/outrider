// Package session starts agent sessions: a worktree per PR, the prompt, the
// session policy, agent settings, the guards on PATH, and a terminal window
// or tmux session running `llm-review-agent session run`.
package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/appetizers-io/llm-review-agent/internal/classifier"
	"github.com/appetizers-io/llm-review-agent/internal/config"
	"github.com/appetizers-io/llm-review-agent/internal/github"
	"github.com/appetizers-io/llm-review-agent/internal/guard"
	"github.com/appetizers-io/llm-review-agent/internal/proc"
	"github.com/appetizers-io/llm-review-agent/internal/shell"
)

func itoa(n int) string { return strconv.Itoa(n) }

// Local is a checkout of a watched repo: worktrees branch off it.
type Local struct {
	Path, Remote string
}

// Launcher starts agent sessions. Its fields are set once at startup.
type Launcher struct {
	Root         string // ~/.cache/llm-review-agent
	Self         string // this binary: the guards and the session runner
	Cfg          *config.Config
	ConfigSource string // "": built-in defaults
	Login, Owner string
	Agent        string
	Launcher     string // terminal | tmux
	Terminal     Terminal
	LaunchCheck  *classifier.Resolved
	ToolGate     *classifier.Resolved
	Local        map[string]Local // owner/repo -> checkout
	DryRun       bool
	GOOS         string
	Run          proc.Runner
	LookPath     func(string) (string, error)
	Log          *slog.Logger
}

// Request is an event that may start a session.
type Request struct {
	Repo     string
	N        int
	PR       github.PR
	Trigger  string
	Gate     func(context.Context) ([]github.Activity, error) // new activity to judge first; nil: launch unconditionally
	Scope    []string                                         // comment URLs the session is limited to; nil: the whole PR
	ScopeWhy string
}

// Launch handles the event: true once the agent started or the launch check
// said skip; false keeps the event pending.
func (l *Launcher) Launch(ctx context.Context, r Request) bool {
	ok, err := l.launch(ctx, r)
	if err != nil {
		l.Log.Warn(fmt.Sprintf("%s#%d: launch failed; keeping event pending: %v", r.Repo, r.N, err))
		return false
	}
	return ok
}

func (l *Launcher) launch(ctx context.Context, r Request) (bool, error) {
	key := fmt.Sprintf("%s#%d", r.Repo, r.N)
	lock := lockPath(l.Root, r.Repo, r.N)
	var live []string
	if !l.DryRun {
		live = Locks(ctx, l.Root, l.Cfg.StaleLockHours, l.Run, l.Log) // drops stale locks, this PR's too
	}
	if _, err := os.Stat(lock); err == nil {
		l.Log.Info(key + ": already running; keeping event pending")
		return false, nil
	}
	if !l.DryRun && len(live) >= l.Cfg.MaxAgents {
		l.Log.Info(fmt.Sprintf("%s: agent limit reached (%d); keeping event pending", key, l.Cfg.MaxAgents))
		return false, nil
	}
	if r.Gate != nil && l.LaunchCheck != nil {
		items, err := r.Gate(ctx)
		if err != nil {
			return false, err
		}
		req := classifier.NewRequest(r.Repo, r.N, r.PR, r.Trigger, items, l.Owner, l.Login)
		launch, note := classifier.CheckLaunch(ctx, l.LaunchCheck, req, l.Cfg.LaunchCheck.SkipBelow, l.Run)
		l.Log.Info(key + ": " + note)
		if !launch {
			l.Log.Info(fmt.Sprintf("%s: nothing actionable [%s]; not launching", key, r.Trigger))
			return true, nil
		}
	}
	l.Log.Info(fmt.Sprintf("matched %s: %s [%s]", key, r.PR.Title, r.Trigger))
	if l.DryRun {
		return true, nil
	}
	return true, l.start(ctx, r, lock)
}

// Spec is everything `llm-review-agent session run` needs, saved as session.json.
type Spec struct {
	Lock       string            `json:"lock"`
	Meta       LockMeta          `json:"meta"`
	Dir        string            `json:"dir"` // the worktree
	Env        map[string]string `json:"env"`
	Path       string            `json:"path"` // put first on PATH: the guards
	Agent      []string          `json:"agent"`
	PromptFile string            `json:"prompt_file"`
	Header     []string          `json:"header"`
}

func writeJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// Policy describes a session for the agent (policy.json) and the tool gate.
type Policy struct {
	Repo         string     `json:"repo"`
	PR           int        `json:"pr"`
	URL          string     `json:"url"`
	Author       string     `json:"author"`
	OwnPR        bool       `json:"own_pr"`
	Owner        string     `json:"owner"`
	Trigger      string     `json:"trigger"`
	Mode         string     `json:"mode"`
	ReviewOnly   bool       `json:"review_only"`
	PushAllowed  bool       `json:"push_allowed"`
	Push         string     `json:"push"`
	GitHubWrites string     `json:"github_writes"`
	Scope        []string   `json:"scope"`
	DenyRules    []string   `json:"deny_rules"`
	ToolGate     PolicyGate `json:"tool_gate"`
	Config       *string    `json:"config"`
}

// PolicyGate is the tool gate part of the policy.
type PolicyGate struct {
	Classifier *string  `json:"classifier"`
	Matcher    string   `json:"matcher"`
	Threshold  *float64 `json:"threshold"`
	Rules      string   `json:"rules"`
}

// Prepared is a session written to disk, ready to start.
type Prepared struct {
	Dir  string // the session directory
	Spec Spec
}

// Prepare writes the session files for a PR checked out at worktree.
func (l *Launcher) Prepare(r Request, worktree, lock string) (Prepared, error) {
	slug := strings.ReplaceAll(r.Repo, "/", "__")
	dir := filepath.Join(l.Root, "sessions", slug, "pr-"+itoa(r.N))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Prepared{}, fmt.Errorf("session dir: %w", err)
	}
	author := r.PR.AuthorLogin()
	own := strings.EqualFold(author, l.Login)
	allowPush := l.Cfg.AllowPushToOthers()
	reviewOnly := !own && !allowPush
	push := l.Cfg.PushMode()
	if reviewOnly {
		push = "review-only"
	}
	ghWrites := l.Cfg.GitHubWritesMode()
	supervised := l.Cfg.Mode == "supervised"
	gated := supervised && l.ToolGate != nil
	mode := map[string]string{
		"review-only": ", review only: git push blocked",
		"never":       ", git push blocked",
		"ask":         ", git push asks you first",
	}[push]
	ghNote, ok := map[string]string{
		"ask":   "gh posts to this PR ask you first",
		"allow": "gh may post to this PR",
	}[ghWrites]
	if !ok {
		ghNote = "gh is read-only"
	}
	if supervised {
		by := "deny rules"
		if gated {
			by = l.ToolGate.Name
		}
		mode += ", tools gated by " + by
	}
	policyFile := filepath.Join(dir, "policy.json")
	remote := ""
	if loc, ok := l.Local[r.Repo]; ok {
		remote = loc.Remote
	}
	prompt, err := Prompt(PromptInput{
		Repo: r.Repo, N: r.N, PR: r.PR, Trigger: r.Trigger, Owner: l.Owner, Login: l.Login,
		Remote: remote, Scope: r.Scope, ScopeWhy: r.ScopeWhy, Extra: l.Cfg.Prompts.Extra,
		AllowPush: allowPush, PolicyFile: policyFile, Push: push, GHWrites: ghWrites,
	})
	if err != nil {
		return Prepared{}, err
	}
	promptFile := filepath.Join(dir, "prompt.txt")
	if err := os.WriteFile(promptFile, []byte(prompt), 0o600); err != nil {
		return Prepared{}, fmt.Errorf("write prompt: %w", err)
	}

	agentPath, err := l.LookPath(l.Agent)
	if err != nil {
		return Prepared{}, fmt.Errorf("%s is not installed", l.Agent)
	}
	ghPath, err1 := l.LookPath("gh")
	gitPath, err2 := l.LookPath("git")
	if err1 != nil || err2 != nil {
		return Prepared{}, errors.New("gh and git must be installed")
	}
	guardBin, err := l.installGuards()
	if err != nil {
		return Prepared{}, err
	}

	rules := GateText(l.Cfg, r.Repo, r.N, author, l.Owner, own, push, ghWrites)
	policy := Policy{
		Repo: r.Repo, PR: r.N, URL: r.PR.URL, Author: author, OwnPR: own, Owner: l.Owner,
		Trigger: r.Trigger, Mode: l.Cfg.Mode, ReviewOnly: reviewOnly,
		PushAllowed: push == "ask" || push == "allow", Push: push, GitHubWrites: ghWrites,
		Scope: r.Scope, DenyRules: []string{},
		ToolGate: PolicyGate{Matcher: l.Cfg.ToolGate.Matcher, Threshold: l.Cfg.ToolGate.Threshold, Rules: rules},
	}
	if len(r.Scope) == 0 {
		policy.Scope = nil
	}
	if supervised && l.Agent == "claude" {
		policy.DenyRules = DenyRules(push, l.GOOS)
	}
	if gated {
		policy.ToolGate.Classifier = &l.ToolGate.Name
	}
	if l.ConfigSource != "" {
		policy.Config = &l.ConfigSource
	}
	if err := writeJSON(policyFile, policy); err != nil {
		return Prepared{}, err
	}

	hookEnv := map[string]string{"LLM_REVIEW_AGENT_POLICY_FILE": policyFile}
	if gated {
		hookEnv = classifier.HookEnv(l.ToolGate, rules, l.Cfg.ToolGate.Threshold, policyFile)
	}
	name := fmt.Sprintf("PR %s#%d", r.Repo, r.N)
	agent := []string{agentPath}
	if l.Agent == "claude" {
		// Remote Control lists the session on claude.ai and in Claude Desktop
		agent = append(agent, "--name", name, "--remote-control", name)
		if supervised {
			sf := filepath.Join(dir, "claude-settings.json")
			if err := writeJSON(sf, ClaudeSettings(l.Cfg, push, l.GOOS, l.ToolGate, hookEnv)); err != nil {
				return Prepared{}, err
			}
			agent = append(agent, "--settings", sf)
		}
	}

	env := map[string]string{
		guard.EnvRealGH:   ghPath,
		guard.EnvRealGit:  gitPath,
		guard.EnvPush:     push,
		guard.EnvGHWrites: ghWrites,
		guard.EnvRepo:     r.Repo,
		guard.EnvPR:       itoa(r.N),
		guard.EnvSession:  name,
	}
	if push != "allow" {
		for k, v := range PushTrap() {
			env[k] = v
		}
	}
	for k, v := range hookEnv {
		env[k] = v
	}
	var tmux *string
	if l.Launcher == "tmux" {
		tmux = new(regexp.MustCompile(`[^A-Za-z0-9_-]`).ReplaceAllString(fmt.Sprintf("pr-%s-%d", r.Repo, r.N), "-"))
	}
	spec := Spec{
		Lock:       lock,
		Meta:       LockMeta{Repo: r.Repo, PR: r.N, Started: float64(time.Now().UnixNano()) / 1e9, Tmux: tmux},
		Dir:        worktree,
		Env:        env,
		Path:       guardBin,
		Agent:      agent,
		PromptFile: promptFile,
		Header: []string{
			fmt.Sprintf("GitHub PR review agent: %s#%d", r.Repo, r.N),
			fmt.Sprintf("agent: %s (%s%s)", l.Agent, ghNote, mode),
			"",
		},
	}
	if err := writeJSON(filepath.Join(dir, "session.json"), spec); err != nil {
		return Prepared{}, err
	}
	return Prepared{Dir: dir, Spec: spec}, nil
}

// installGuards links gh and git in <root>/bin to this binary (copies on Windows).
func (l *Launcher) installGuards() (string, error) {
	bin := filepath.Join(l.Root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		return "", fmt.Errorf("guard dir: %w", err)
	}
	for _, name := range []string{"gh", "git"} {
		if l.GOOS == "windows" {
			if err := copyFile(l.Self, filepath.Join(bin, name+".exe")); err != nil {
				return "", err
			}
			continue
		}
		p := filepath.Join(bin, name)
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("replace guard %s: %w", p, err)
		}
		if err := os.Symlink(l.Self, p); err != nil {
			return "", fmt.Errorf("install guard %s: %w", p, err)
		}
	}
	return bin, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("copy guard: %w", err)
	}
	defer func() { _ = in.Close() }()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700) //nolint:gosec // the guard must be executable
	if err != nil {
		return fmt.Errorf("copy guard: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("copy guard: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("copy guard: %w", err)
	}
	// a running guard keeps its file open; then the old copy stays
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		if _, statErr := os.Stat(dst); statErr == nil {
			return nil
		}
		return fmt.Errorf("copy guard: %w", err)
	}
	return nil
}

// start checks the PR out, writes the session and opens it.
func (l *Launcher) start(ctx context.Context, r Request, lock string) error {
	wt, err := l.worktree(ctx, r.Repo, r.N)
	if err != nil {
		return err
	}
	p, err := l.Prepare(r, wt, lock)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(lock), 0o700); err != nil {
		return fmt.Errorf("lock dir: %w", err)
	}
	// the runner stamps its pid so Locks can tell a live agent from one
	// that never started
	if err := writeJSON(lock, p.Spec.Meta); err != nil {
		return err
	}
	if err := l.open(ctx, r, p); err != nil {
		_ = os.Remove(lock)
		return err
	}
	return nil
}

// open starts the runner in tmux or a terminal window.
func (l *Launcher) open(ctx context.Context, r Request, p Prepared) error {
	runner := []string{l.Self, "session", "run", p.Dir}
	if t := p.Spec.Meta.Tmux; t != nil {
		// a finished session may still wait for a keypress
		_, _ = l.Run(ctx, proc.Cmd{Args: []string{"tmux", "kill-session", "-t", "=" + *t}})
		args := append([]string{"tmux", "new-session", "-d", "-s", *t, "-c", p.Spec.Dir}, runner...)
		if _, err := l.Run(ctx, proc.Cmd{Args: args}); err != nil {
			return fmt.Errorf("tmux: %w", err)
		}
		l.Log.Info(fmt.Sprintf("%s#%d: attach with: tmux attach -t %s", r.Repo, r.N, *t))
		return nil
	}
	script, err := writeScript(p.Dir, l.GOOS, runner)
	if err != nil {
		return err
	}
	args := l.Terminal.OpenCommand(l.GOOS, script, fmt.Sprintf("PR %s#%d", r.Repo, r.N), runner)
	if args == nil {
		return errors.New("no terminal to open the session in")
	}
	if _, err := l.Run(ctx, proc.Cmd{Args: args}); err != nil {
		return fmt.Errorf("open terminal: %w", err)
	}
	return nil
}

// writeScript writes run-agent.command (run-agent.cmd on Windows), which
// starts the session runner; terminal apps that open files run it.
func writeScript(dir, goos string, runner []string) (string, error) {
	if goos == "windows" {
		p := filepath.Join(dir, "run-agent.cmd")
		quoted := make([]string, len(runner))
		for i, a := range runner {
			quoted[i] = `"` + a + `"`
		}
		return p, writeExec(p, "@echo off\r\n"+strings.Join(quoted, " ")+"\r\n")
	}
	p := filepath.Join(dir, "run-agent.command")
	return p, writeExec(p, "#!/bin/sh\nexec "+shell.Join(runner)+"\n")
}

func writeExec(p, text string) error {
	if err := os.WriteFile(p, []byte(text), 0o700); err != nil { //nolint:gosec // the terminal app runs it
		return fmt.Errorf("write %s: %w", p, err)
	}
	if err := os.Chmod(p, 0o700); err != nil { //nolint:gosec // the terminal app runs it
		return fmt.Errorf("chmod %s: %w", p, err)
	}
	return nil
}
