// Package session starts agent sessions: a worktree per PR, the prompt, the
// session policy, agent settings, the guards on PATH, and a terminal window
// or tmux session running `outrider session run`.
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
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/appetizers-io/outrider/internal/classifier"
	"github.com/appetizers-io/outrider/internal/config"
	"github.com/appetizers-io/outrider/internal/github"
	"github.com/appetizers-io/outrider/internal/guard"
	"github.com/appetizers-io/outrider/internal/proc"
	"github.com/appetizers-io/outrider/internal/shell"
)

func itoa(n int) string { return strconv.Itoa(n) }

// Local is a checkout of a watched repo: worktrees branch off it.
type Local struct {
	Path, Remote string
}

// Launcher starts agent sessions. Its fields are set once at startup.
type Launcher struct {
	Root         string // ~/.cache/outrider
	Self         string // this binary: the guards and the session runner
	Cfg          *config.Config
	ConfigSource string // "": built-in defaults
	Login, Owner string
	Launcher     string // terminal | tmux
	Terminal     Terminal
	LaunchCheck  *classifier.Resolved // Cfg's; a PR whose overrides name another classifier resolves it
	ToolGate     *classifier.Resolved
	Local        map[string]Local // owner/repo -> checkout
	DryRun       bool
	SandboxErrs  map[string]error // by agent: why its read-only sessions can't run here; missing: they can
	CodexHome    string           // the user's CODEX_HOME
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
	Event    string           // stable trigger kind used by workflow match
	Workflow *config.Workflow // explicit conditional workflow; nil selects by activity
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
	cfg, _ := l.scoped(r)
	if err := l.SandboxErrs[cfg.Agent]; l.sandboxed(r) && err != nil {
		// fail closed: never run a read-only session unsandboxed
		l.Log.Error(fmt.Sprintf("%s: refusing session: read-only sandbox unavailable: %v", key, err))
		return true, nil //nolint:nilerr // refused for good: handled, not retried
	}
	if check, _ := l.classifiers(&cfg); r.Gate != nil && check != nil && l.workflow(r, &cfg) == nil {
		items, err := r.Gate(ctx)
		if err != nil {
			return false, err
		}
		req := classifier.NewRequest(r.Repo, r.N, r.PR, r.Trigger, items, l.Owner, l.Login)
		launch, note := classifier.CheckLaunch(ctx, check, req, cfg.LaunchCheck.SkipBelow, l.Run)
		l.Log.Info(key + ": " + note)
		if !launch {
			l.Log.Info(fmt.Sprintf("%s: nothing actionable [%s]; not launching", key, r.Trigger))
			return true, nil
		}
	}
	l.Log.Info(fmt.Sprintf("matched %s: %s [%s]", key, r.PR.Title, r.Trigger))
	if w := l.workflow(r, &cfg); w != nil {
		l.Log.Info(key + ": workflow " + w.Name)
	}
	if cfg.Review != nil && !strings.EqualFold(r.PR.AuthorLogin(), l.Login) {
		name, rule := cfg.Review.Select(r.Repo, r.Event)
		l.Log.Info(key + ": review profile " + name + " (" + rule + ")")
	}
	if l.DryRun {
		return true, nil
	}
	return true, l.start(ctx, r, lock)
}

// Spec is everything `outrider session run` needs, saved as session.json.
type Spec struct {
	Review     *Review           `json:"review,omitempty"`
	Isolation  *IsolatedSpec     `json:"isolation,omitempty"`
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
	Review        *Review          `json:"review,omitempty"`
	Repo          string           `json:"repo"`
	PR            int              `json:"pr"`
	URL           string           `json:"url"`
	Author        string           `json:"author"`
	OwnPR         bool             `json:"own_pr"`
	Owner         string           `json:"owner"`
	Trigger       string           `json:"trigger"`
	Mode          string           `json:"mode"`
	ReviewOnly    bool             `json:"review_only"`
	PushAllowed   bool             `json:"push_allowed"`
	Push          string           `json:"push"`
	ReviewForks   []string         `json:"review_forks"` // review only, but pushes to these forks
	GitHubWrites  string           `json:"github_writes"`
	Scope         []string         `json:"scope"`
	DenyRules     []string         `json:"deny_rules"`
	NetworkAccess *bool            `json:"network_access,omitempty"`
	Sandbox       string           `json:"sandbox"`    // off | read-only
	PRContext     *string          `json:"pr_context"` // read-only: the prefetched PR context
	ToolGate      PolicyGate       `json:"tool_gate"`
	Config        *string          `json:"config"`
	Workflow      *config.Workflow `json:"workflow,omitempty"`
	Guard         guard.Runtime    `json:"guard"`
	Overrides     []string         `json:"overrides,omitempty"` // the config's overrides that apply, in order
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

func sessionDir(root, repo string, n int) string {
	return filepath.Join(root, "sessions", strings.ReplaceAll(repo, "/", "__"), "pr-"+itoa(n))
}

// scoped is the config of r's PR, with the overrides that match it, and
// whether it is your own PR.
func (l *Launcher) scoped(r Request) (config.Config, bool) {
	own := strings.EqualFold(r.PR.AuthorLogin(), l.Login)
	return l.Cfg.For(r.Repo, own), own
}

// classifiers are the launch check and the tool gate of cfg: the ones
// resolved at startup unless an override names other classifiers.
func (l *Launcher) classifiers(cfg *config.Config) (launchCheck, toolGate *classifier.Resolved) {
	same := func(a, b *string) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
	launchCheck, toolGate = l.LaunchCheck, l.ToolGate
	if !same(cfg.LaunchCheck.Classifier, l.Cfg.LaunchCheck.Classifier) {
		launchCheck, _ = classifier.ResolveRole(cfg, cfg.LaunchCheck.Classifier, l.LookPath)
	}
	if cfg.Mode != "autonomous" && !same(cfg.ToolGate.Classifier, l.Cfg.ToolGate.Classifier) {
		toolGate, _ = classifier.ResolveRole(cfg, cfg.ToolGate.Classifier, l.LookPath)
	}
	return launchCheck, toolGate
}

// sandboxed tells whether r's session runs in the read-only sandbox.
func (l *Launcher) sandboxed(r Request) bool {
	cfg, own := l.scoped(r)
	return cfg.SandboxFor(own) == config.ReadOnly
}

// Prepare writes the session files for a PR checked out at worktree; sb is
// nil unless the session is sandboxed.
func (l *Launcher) Prepare(r Request, worktree, lock string, sb *Sandbox) (Prepared, error) {
	dir := sessionDir(l.Root, r.Repo, r.N)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Prepared{}, fmt.Errorf("session dir: %w", err)
	}
	author := r.PR.AuthorLogin()
	cfg, own := l.scoped(r)
	_, toolGate := l.classifiers(&cfg)
	allowPush := cfg.AllowPushToOthers()
	reviewOnly := !own && !allowPush
	push := cfg.PushMode()
	ghWrites := cfg.GitHubWritesMode()
	if sb != nil {
		reviewOnly, ghWrites = true, "never"
	}
	if reviewOnly {
		push = "review-only"
	}
	// review with evidence: local commits, pushes only to the owner's forks;
	// forkPush is the push mode for those
	forks, forkPush := cfg.OthersPRs.Forks(), push
	if reviewOnly && sb == nil && len(forks) > 0 {
		push, forkPush = "review-forks", cfg.PushMode()
	} else {
		forks = nil
	}
	supervised := cfg.Mode == "supervised"
	gated := supervised && toolGate != nil
	mode := map[string]string{
		"review-only":  ", review only: git push blocked",
		"review-forks": ", review only: git push only to your review forks (" + forkPush + ")",
		"never":        ", git push blocked",
		"ask":          ", git push asks you first",
	}[push]
	ghNote, ok := map[string]string{
		"ask":   "gh posts to this PR ask you first",
		"allow": "gh may post to this PR",
	}[ghWrites]
	if !ok {
		ghNote = "gh is read-only"
	}
	if sb != nil {
		mode += ", read-only sandbox"
	}
	if supervised {
		by := "deny rules"
		if gated {
			by = toolGate.Name
		}
		mode += ", tools gated by " + by
	}
	review, err := l.review(r, &cfg, dir, sb != nil, worktree)
	if err != nil {
		return Prepared{}, err
	}
	policyFile := filepath.Join(dir, "policy.json")
	remote := ""
	if loc, ok := l.Local[r.Repo]; ok {
		remote = loc.Remote
	}
	var contextDir *string
	if sb != nil {
		contextDir = &sb.ContextDir
	}
	prompt, err := Prompt(PromptInput{
		Repo: r.Repo, N: r.N, PR: r.PR, Trigger: r.Trigger, Owner: l.Owner, Login: l.Login,
		Remote: remote, Scope: r.Scope, ScopeWhy: r.ScopeWhy, Extra: cfg.Prompts.Extra,
		AllowPush: allowPush, PolicyFile: policyFile, Push: forkPush, GHWrites: ghWrites, ContextDir: contextDir,
		Review: review, ReviewForks: forks, Workflow: l.workflow(r, &cfg),
	})
	if err != nil {
		return Prepared{}, err
	}
	promptFile := filepath.Join(dir, "prompt.txt")
	if err := os.WriteFile(promptFile, []byte(prompt), 0o600); err != nil {
		return Prepared{}, fmt.Errorf("write prompt: %w", err)
	}

	agentPath, err := l.LookPath(cfg.Agent)
	if err != nil {
		return Prepared{}, fmt.Errorf("%s is not installed", cfg.Agent)
	}
	ghPath, err1 := l.LookPath("gh")
	gitPath, err2 := l.LookPath("git")
	if err1 != nil || err2 != nil {
		return Prepared{}, errors.New("gh and git must be installed")
	}
	guardBin, err := l.installGuards(dir)
	if err != nil {
		return Prepared{}, err
	}

	rules := GateText(&cfg, r.Repo, r.N, author, l.Owner, own, push, ghWrites)
	rules += " Session metadata and guard binaries in " + dir + " are protected: never edit, replace, delete or redirect them."
	sandbox := "off"
	if sb != nil {
		rules += sandboxGateRule
		sandbox = config.ReadOnly
	}
	if w := l.workflow(r, &cfg); w != nil {
		rules += " Follow workflow " + w.Name + " in the listed order: " + strings.Join(w.Steps, "; ") + ". Existing session permissions still apply."
	}
	policy := Policy{
		Repo: r.Repo, PR: r.N, URL: r.PR.URL, Author: author, OwnPR: own, Owner: l.Owner,
		Trigger: r.Trigger, Mode: cfg.Mode, ReviewOnly: reviewOnly,
		PushAllowed: forkPush == "ask" || forkPush == "allow", Push: forkPush, ReviewForks: forks, GitHubWrites: ghWrites,
		Scope: r.Scope, DenyRules: []string{}, Sandbox: sandbox, PRContext: contextDir, NetworkAccess: cfg.NetworkAccess,
		ToolGate: PolicyGate{Matcher: cfg.ToolGate.Matcher, Threshold: cfg.ToolGate.Threshold, Rules: rules},
	}
	if len(r.Scope) == 0 {
		policy.Scope = nil
	}
	if (supervised || sb != nil) && cfg.Agent == "claude" {
		policy.DenyRules = DenyRules(push, l.GOOS, l.Root)
		if sb != nil {
			policy.DenyRules = append(policy.DenyRules, readOnlyDenyRules...)
		}
	}
	if gated {
		policy.ToolGate.Classifier = &toolGate.Name
	}
	if l.ConfigSource != "" {
		policy.Config = &l.ConfigSource
	}
	policy.Guard = guard.Runtime{Git: gitPath, GH: ghPath, Head: headRepo(r.PR), Display: map[string]string{}}
	policy.Guard.SSH, _ = l.LookPath("ssh")
	for _, key := range []string{"DISPLAY", "WAYLAND_DISPLAY", "XAUTHORITY", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS"} {
		policy.Guard.Display[key] = os.Getenv(key)
	}
	policy.Review = review
	policy.Workflow = l.workflow(r, &cfg)
	policy.Overrides = l.Cfg.Applied(r.Repo, own)
	if err := writeJSON(policyFile, policy); err != nil {
		return Prepared{}, err
	}

	hookEnv := map[string]string{"OUTRIDER_POLICY_FILE": policyFile}
	if gated {
		hookEnv = classifier.HookEnv(toolGate, rules, cfg.ToolGate.Threshold, policyFile)
	}
	if gated {
		policy.Guard.Hook, policy.Guard.HookEnv = toolGate.HookCmd, hookEnv
		if err := writeJSON(policyFile, policy); err != nil {
			return Prepared{}, err
		}
		toolGate = &classifier.Resolved{Name: toolGate.Name, HookCmd: []string{filepath.Join(guardBin, "outrider-gate"+exeSuffix(l.GOOS))}}
	}
	if review != nil && review.Outbox != "" {
		rules += " The only local draft exception is " + review.Outbox + "; this permits drafting, never PR edits or GitHub submission."
		policy.ToolGate.Rules = rules
		// Restrict review-only edits to the outbox without denying its own path.
		rootPattern := "/" + filepath.ToSlash(l.Root) + "/**"
		policy.DenyRules = slices.DeleteFunc(policy.DenyRules, func(rule string) bool { return rule == "Edit("+rootPattern+")" || rule == "Write("+rootPattern+")" })
		for i, rule := range policy.DenyRules {
			if rule == "Edit" || rule == "Write" {
				policy.DenyRules[i] = rule + "(/" + filepath.ToSlash(worktree) + "/**)"
			}
		}
		for _, path := range []string{"policy.json", "session.json", "prompt.txt", "claude-settings.json", "bin/**", "pr-context/**", "review-context/**"} {
			for _, tool := range []string{"Edit", "Write"} {
				policy.DenyRules = append(policy.DenyRules, tool+"(/"+filepath.ToSlash(filepath.Join(dir, path))+")")
			}
		}
		if gated {
			hookEnv["OUTRIDER_GATE_TEXT"] = rules
			if _, ok := hookEnv["JEV_GATE_STATE"]; ok {
				hookEnv["JEV_GATE_STATE"] = rules
			}
			policy.Guard.HookEnv = hookEnv
		}
		if err := writeJSON(policyFile, policy); err != nil {
			return Prepared{}, err
		}
	}
	name := fmt.Sprintf("PR %s#%d", r.Repo, r.N)
	agent := []string{agentPath}
	switch {
	case cfg.Agent == "claude":
		if reviewOnly && sb == nil {
			agent = append(agent, "--setting-sources", "user", "--strict-mcp-config")
		}
		// Remote Control lists the session on claude.ai and in Claude Desktop
		agent = append(agent, "--name", name, "--remote-control", name)
		if supervised || sb != nil || cfg.NetworkAccess != nil {
			settings := map[string]any{}
			if supervised || sb != nil {
				settings = ClaudeSettings(&cfg, push, l.GOOS, l.Root, toolGate, hookEnv)
				settings["permissions"] = map[string]any{"deny": policy.DenyRules}
			}
			if cfg.NetworkAccess != nil {
				network := map[string]any{"allowedDomains": []string{"*"}}
				if !*cfg.NetworkAccess {
					network = map[string]any{"deniedDomains": []string{"*"}, "strictAllowlist": true}
				}
				settings["sandbox"] = map[string]any{"network": network}
			}
			if sb != nil {
				settings["permissions"] = map[string]any{"deny": policy.DenyRules, "disableBypassPermissionsMode": "disable"}
				settings["sandbox"] = ClaudeSandbox(sb.DenyWrite)
			}
			if review != nil && review.Outbox != "" {
				sandboxSettings, _ := settings["sandbox"].(map[string]any)
				if sandboxSettings == nil {
					sandboxSettings = map[string]any{}
				}
				sandboxSettings["filesystem"] = map[string]any{"allowWrite": []string{review.Outbox}}
				settings["sandbox"] = sandboxSettings
				permissions := settings["permissions"].(map[string]any)
				path := "/" + filepath.ToSlash(review.Outbox) + "/**"
				permissions["allow"] = []string{"Write(" + path + ")", "Edit(" + path + ")"}
			}
			sf := filepath.Join(dir, "claude-settings.json")
			if err := writeJSON(sf, settings); err != nil {
				return Prepared{}, err
			}
			agent = append(agent, "--settings", sf)
			if sb != nil {
				agent = append(agent, ClaudeSandboxArgs()...)
			}
		}
	case sb != nil:
		agent = append(agent, CodexSandboxArgs(worktree, sb.Checkout)...)
	case cfg.NetworkAccess != nil:
		agent = append(agent, "-c", fmt.Sprintf("sandbox_workspace_write.network_access=%t", *cfg.NetworkAccess))
	}

	if review != nil {
		if review.Model != "" {
			if cfg.Agent == "claude" {
				agent = append(agent, "--model", review.Model)
			} else {
				agent = append(agent, "-m", review.Model)
			}
		}
		if review.Effort != "" {
			if cfg.Agent == "claude" {
				agent = append(agent, "--effort", review.Effort)
			} else {
				agent = append(agent, "-c", "model_reasoning_effort="+review.Effort)
			}
		}
	}
	env := map[string]string{
		guard.EnvRealGH:   ghPath,
		guard.EnvRealGit:  gitPath,
		guard.EnvPush:     forkPush,
		guard.EnvGHWrites: ghWrites,
		guard.EnvRepo:     r.Repo,
		guard.EnvPR:       itoa(r.N),
		guard.EnvSession:  name,
	}
	if forks != nil {
		// the guard stays review-only without the forks variable
		env[guard.EnvPush] = "review-only"
		env[guard.EnvReviewForks] = strings.Join(forks, "\n")
		env[guard.EnvReviewForksPush] = forkPush
		env[guard.EnvHeadRepo] = headRepo(r.PR)
	}
	if forkPush != "allow" || forks != nil {
		for k, v := range PushTrap() {
			env[k] = v
		}
	}
	for k, v := range hookEnv {
		env[k] = v
	}
	if sb != nil && sb.CodexHome != "" {
		env["CODEX_HOME"] = sb.CodexHome
	}
	var tmux *string
	if l.Launcher == "tmux" {
		tmux = new(regexp.MustCompile(`[^A-Za-z0-9_-]`).ReplaceAllString(fmt.Sprintf("pr-%s-%d", r.Repo, r.N), "-"))
	}
	spec := Spec{
		Review:     review,
		Lock:       lock,
		Meta:       LockMeta{Repo: r.Repo, PR: r.N, Started: float64(time.Now().UnixNano()) / 1e9, Tmux: tmux},
		Dir:        worktree,
		Env:        env,
		Path:       guardBin,
		Agent:      agent,
		PromptFile: promptFile,
		Header: []string{
			fmt.Sprintf("GitHub PR review agent: %s#%d", r.Repo, r.N),
			fmt.Sprintf("agent: %s (%s%s)", cfg.Agent, ghNote, mode),
			"",
		},
	}
	if review != nil {
		spec.Header = append(spec.Header, "review profile: "+review.Name+" ("+review.Rule+")")
		for _, note := range review.Notes {
			l.Log.Info(note)
		}
	}
	if err := writeJSON(filepath.Join(dir, "session.json"), spec); err != nil {
		return Prepared{}, err
	}
	return Prepared{Dir: dir, Spec: spec}, nil
}

// headRepo is the PR head's owner/repo, "" when GitHub doesn't say.
func headRepo(pr github.PR) string {
	if pr.HeadRepositoryOwner == nil || pr.HeadRepository == nil ||
		pr.HeadRepositoryOwner.Login == "" || pr.HeadRepository.Name == "" {
		return ""
	}
	return pr.HeadRepositoryOwner.Login + "/" + pr.HeadRepository.Name
}

// installGuards copies the binary into each session so os.Executable anchors
// the policy independently of PATH, argv[0], HOME and OUTRIDER_*.
func (l *Launcher) installGuards(dir string) (string, error) {
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		return "", fmt.Errorf("guard dir: %w", err)
	}
	for _, name := range []string{"gh", "git", "outrider-gate"} {
		if err := copyFile(l.Self, filepath.Join(bin, name+exeSuffix(l.GOOS))); err != nil {
			return "", err
		}
	}
	return bin, nil
}

func exeSuffix(goos string) string {
	if goos == "windows" {
		return ".exe"
	}
	return ""
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
	// A failed replacement must never leave a stale guard active.
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("copy guard: %w", err)
	}
	return nil
}

// start checks the PR out, writes the session and opens it.
func (l *Launcher) start(ctx context.Context, r Request, lock string) error {
	cfg, _ := l.scoped(r)
	if cfg.Isolation.Enabled {
		return l.startIsolated(ctx, r, lock, &cfg)
	}
	wt, err := l.worktree(ctx, r.Repo, r.N)
	if err != nil {
		return err
	}
	var sb *Sandbox
	if l.sandboxed(r) {
		if sb, err = l.prepareSandbox(ctx, r, wt); err != nil {
			return err
		}
	}
	review, err := l.review(r, &cfg, sessionDir(l.Root, r.Repo, r.N), sb != nil, wt)
	if err != nil {
		return err
	}
	if err := l.reviewContext(ctx, r, &cfg, review, wt, sessionDir(l.Root, r.Repo, r.N)); err != nil {
		return err
	}
	p, err := l.Prepare(r, wt, lock, sb)
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
	// the terminal app starts the runner with its own environment; tmux
	// sessions inherit the watcher's
	envFile := filepath.Join(p.Dir, watcherEnvFile)
	_ = os.Remove(envFile) // WriteFile keeps an existing file's mode
	if err := writeJSON(envFile, os.Environ()); err != nil {
		return err
	}
	args := l.Terminal.OpenCommand(l.GOOS, script, fmt.Sprintf("PR %s#%d", r.Repo, r.N), runner)
	if args == nil {
		return errors.New("no terminal to open the session in")
	}
	if _, err := l.Run(ctx, proc.Cmd{Args: args}); err != nil {
		_ = os.Remove(envFile)
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
	return p, writeExec(p, "#!/bin/sh\nexec "+shell.Join(runner...)+"\n")
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
