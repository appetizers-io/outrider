// Package config is the YAML configuration.
//
// The structs below declare the keys; their `jsonschema` tags give the
// constraints and descriptions, from which github.com/invopop/jsonschema
// generates the JSON Schema (`outrider config schema`). Loading
// validates a file against that schema (github.com/santhosh-tekuri/jsonschema)
// and then decodes it onto Default() with yaml.v3 KnownFields.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	// EnvVar names the config file when --config is not given.
	EnvVar = "OUTRIDER_CONFIG"
	// LegacyEnvVar is EnvVar from before the rename to outrider; still read, with a warning.
	LegacyEnvVar = "LLM_REVIEW_AGENT_CONFIG"
	// SchemaID is the $id of the generated JSON Schema.
	SchemaID = "https://github.com/appetizers-io/outrider/config.schema.json"
)

// ReadOnly is the sandbox in which a session can't change anything.
const ReadOnly = "read-only"

// JevBackendEnv are the variables that give jev-use a backend.
var JevBackendEnv = []string{"TYPESAFE_API_KEY", "OPENROUTER_API_KEY", "AI_GATEWAY_API_KEY", "JEV_BACKEND"}

// Config is the outrider configuration. Every key is optional;
// command-line flags override this file.
type Config struct {
	OwnerName       *string `yaml:"owner_name" jsonschema:"minLength=1,nullable" jsonschema_description:"How prompts refer to you. null: first name from your GitHub profile, else your login."`
	Mode            string  `yaml:"mode" jsonschema:"enum=supervised,enum=autonomous,default=supervised" jsonschema_description:"supervised: Claude sessions get deny rules for risky tools and, when its classifier is available, the tool_gate on every call; other people's PRs are review only. autonomous: no tool gating, and pushing to other people's PRs follows others_prs.allow_push (default: allowed)."`
	PRSettings      `yaml:",inline"`
	Launcher        string      `yaml:"launcher" jsonschema:"enum=auto,enum=terminal,enum=tmux,default=auto" jsonschema_description:"auto: macOS: the terminal in a desktop (Aqua) session, else tmux. Linux: the terminal when a display is present and a terminal resolves, else tmux. Windows: always the terminal."`
	Terminal        Terminal    `yaml:"terminal" jsonschema_description:"Terminal app for launcher: terminal. auto: detected once at startup. A name forces that app. A command list with a {cmd} placeholder runs anything else, e.g. [alacritty, -e, '{cmd}']."`
	IntervalSeconds int         `yaml:"interval_seconds" jsonschema:"minimum=10,default=60" jsonschema_description:"Pause between polls. Doubles on consecutive failures, up to 15 minutes."`
	LookbackHours   int         `yaml:"lookback_hours" jsonschema:"minimum=1,default=168" jsonschema_description:"Notification window, and how long an unseen candidate PR is kept."`
	MaxAgents       int         `yaml:"max_agents" jsonschema:"minimum=1,default=1" jsonschema_description:"Agent sessions allowed to run at once."`
	CandidateLimit  int         `yaml:"candidate_limit" jsonschema:"minimum=1,default=50" jsonschema_description:"Most recently seen non-owned PRs checked for the opt-in reaction per poll."`
	StaleLockHours  float64     `yaml:"stale_lock_hours" jsonschema:"exclusiveMinimum=0,default=12" jsonschema_description:"A session lock older than this is dead."`
	Repos           Repos       `yaml:"repos"`
	Classifiers     Classifiers `yaml:"classifiers" jsonschema_description:"Named decision backends. 'jev' is always defined; add kind: command entries for local classifiers."`
	Overrides       []Override  `yaml:"overrides,omitempty" jsonschema_description:"Layers over the keys above for some PRs: every entry whose match selects a PR applies, in file order, later entries win. Only per-PR keys."`
}

// PRSettings are the keys that can differ per PR; overrides set them for
// the PRs they match.
type PRSettings struct {
	Workflows     []Workflow  `yaml:"workflows,omitempty" jsonschema_description:"Ordered agent workflows selected by PR metadata. First matching activity workflow wins; conditional workflows watch discovered PRs."`
	Push          *string     `yaml:"push" jsonschema:"enum=ask,enum=never,enum=allow,nullable" jsonschema_description:"Pushes in sessions that may push (your own PRs, others' PRs with others_prs.allow_push). ask: every git push opens a dialog and runs only after you click Push (without a dialog it is refused). never: commits stay local. allow: no question. null: ask in supervised mode, allow in autonomous mode."`
	GitHubWrites  *string     `yaml:"github_writes" jsonschema:"enum=ask,enum=never,enum=allow,nullable" jsonschema_description:"Comments, review comments and replies, reviews and reactions the agent posts with gh on the session's PR. ask: each one opens a dialog showing the text and runs only after you click Post (without a dialog it is refused). never: GitHub stays read-only. allow: no question. null: ask in supervised mode, allow in autonomous mode."`
	Sandbox       string      `yaml:"sandbox" jsonschema:"enum=off,enum=read-only,default=off" jsonschema_description:"read-only: every session runs in the agent's own OS sandbox (Claude Code: sandbox settings and deny rules; Codex: --sandbox read-only, approvals never): no file writes, no commits, no pushes, no GitHub posts, no network. The PR context is fetched into the session dir first. push and github_writes must be never or unset. Where the sandbox is unavailable, sessions are refused. off: no sandbox."`
	NetworkAccess *bool       `yaml:"network_access" jsonschema:"nullable" jsonschema_description:"Session override for the agent network sandbox. true: allow outbound hosts without network approval prompts; false: block sandboxed outbound access; null: inherit the owner settings. Codex: sandbox_workspace_write.network_access. Claude: sandbox network domain rules, only effective when its Bash sandbox is enabled. Does not enable a sandbox or change tool approvals, push or post guards. true conflicts with read-only sessions."`
	Agent         string      `yaml:"agent" jsonschema:"enum=codex,enum=claude,default=codex" jsonschema_description:"Coding agent CLI to launch."`
	IgnoreAuthors []string    `yaml:"ignore_authors" jsonschema:"minLength=1" jsonschema_description:"Login globs (* and ?) whose activity alone never triggers a session, e.g. netlify[bot] or *[bot]."`
	Triggers      Triggers    `yaml:"triggers"`
	OthersPRs     OthersPRs   `yaml:"others_prs"`
	LaunchCheck   LaunchCheck `yaml:"launch_check"`
	ToolGate      ToolGate    `yaml:"tool_gate"`
	Prompts       Prompts     `yaml:"prompts"`
}

// Repos limits which repositories are watched.
type Repos struct {
	Include []string `yaml:"include" jsonschema:"minLength=1" jsonschema_description:"owner/repo globs (*, ?, [abc], [!abc], {a,b}) or GitHub URLs. Empty: the checkout you run in, else every repo."`
	Exclude []string `yaml:"exclude" jsonschema:"minLength=1" jsonschema_description:"owner/repo globs or GitHub URLs that never trigger."`
}

// OwnPRs are notifications on PRs you authored.
type OwnPRs struct {
	Enabled bool `yaml:"enabled" jsonschema:"default=true"`
	Check   bool `yaml:"check" jsonschema:"default=true" jsonschema_description:"Run the launch_check classifier before starting an agent (only when one is configured and available)."`
}

// OnChange relaunches when reviews or comments on an opted-in PR change.
type OnChange struct {
	Check             bool `yaml:"check" jsonschema:"default=true" jsonschema_description:"Run the launch_check classifier before starting an agent (only when one is configured and available)."`
	IgnoreOwnActivity bool `yaml:"ignore_own_activity" jsonschema:"default=true" jsonschema_description:"Changes made only by you (or by ignore_authors) never relaunch."`
}

// OptIn is the opt-in trigger: your reaction on someone else's PR opts it
// in; sessions cover the whole PR.
type OptIn struct {
	Enabled  bool     `yaml:"enabled" jsonschema:"default=true"`
	Reaction string   `yaml:"reaction" jsonschema:"enum=+1,enum=-1,enum=laugh,enum=confused,enum=heart,enum=hooray,enum=rocket,enum=eyes,default=eyes" jsonschema_description:"GitHub reaction that opts a PR in. Removing it stops the watch."`
	Where    []string `yaml:"where" jsonschema:"enum=description,enum=comment,enum=review,enum=review_comment,minItems=1,uniqueItems=true" jsonschema_description:"Where the reaction counts."`
	OnChange OnChange `yaml:"on_change"`
}

// ReviewReplies is the reply trigger: someone replies in a review thread you
// took part in.
type ReviewReplies struct {
	Enabled          bool   `yaml:"enabled" jsonschema:"default=true"`
	Check            bool   `yaml:"check" jsonschema:"default=true" jsonschema_description:"Run the launch_check classifier before starting an agent (only when one is configured and available)."`
	Scope            string `yaml:"scope" jsonschema:"enum=thread,enum=pr,default=thread" jsonschema_description:"thread: the session handles only the replied threads. pr: the whole PR."`
	FreshWithinHours *int   `yaml:"fresh_within_hours" jsonschema:"minimum=1,nullable" jsonschema_description:"Only replies this recent trigger. null: lookback_hours."`
}

// Mentions is the mention trigger: someone @mentions your login on a PR that
// is neither yours nor opted in (those already relaunch for any new activity).
type Mentions struct {
	Enabled          bool   `yaml:"enabled" jsonschema:"default=true"`
	Check            bool   `yaml:"check" jsonschema:"default=true" jsonschema_description:"Run the launch_check classifier before starting an agent (only when one is configured and available)."`
	Scope            string `yaml:"scope" jsonschema:"enum=comment,enum=pr,default=comment" jsonschema_description:"comment: the session handles only the mentioning comments. pr: the whole PR."`
	FreshWithinHours *int   `yaml:"fresh_within_hours" jsonschema:"minimum=1,nullable" jsonschema_description:"Only mentions this recent trigger. null: lookback_hours."`
}

// Triggers decide which events start a session.
type Triggers struct {
	OwnPRs        OwnPRs        `yaml:"own_prs"`
	OptIn         OptIn         `yaml:"opt_in"`
	ReviewReplies ReviewReplies `yaml:"review_replies"`
	Mentions      Mentions      `yaml:"mentions"`
}

// LaunchCheck asks a classifier whether new activity is worth an agent session.
type LaunchCheck struct {
	Classifier *string `yaml:"classifier" jsonschema:"minLength=1,nullable" jsonschema_description:"Name from classifiers; null: always launch."`
	SkipBelow  float64 `yaml:"skip_below" jsonschema:"minimum=0,maximum=1,default=0.5" jsonschema_description:"Skip when the classifier is confident the probability that something is actionable is below this."`
}

// ToolGate checks every tool call in supervised agent sessions (PreToolUse hook).
type ToolGate struct {
	Classifier         *string  `yaml:"classifier" jsonschema:"minLength=1,nullable" jsonschema_description:"Name from classifiers; null: no tool gate."`
	Matcher            string   `yaml:"matcher" jsonschema:"minLength=1" jsonschema_description:"Tools the gate sees."`
	Threshold          *float64 `yaml:"threshold" jsonschema:"minimum=0,maximum=1,nullable" jsonschema_description:"Confidence below which the gate asks instead of deciding. null: the classifier's own default."`
	Rules              []string `yaml:"rules" jsonschema:"minLength=1" jsonschema_description:"Your own rules for every tool call, e.g. never modify generated/."`
	IncludePromptExtra bool     `yaml:"include_prompt_extra" jsonschema_description:"Also enforce prompts.extra through the gate."`
}

// OthersPRs are sessions on PRs someone else authored.
type OthersPRs struct {
	AllowPush   *bool     `yaml:"allow_push" jsonschema:"nullable" jsonschema_description:"false: review only. The agent must not edit, commit or push, and git push is blocked in the session. true: it may push fixes to the PR branch (fast-forward only). null: false in supervised mode, true in autonomous mode."`
	Sandbox     *string   `yaml:"sandbox" jsonschema:"enum=off,enum=read-only,nullable" jsonschema_description:"sandbox for sessions on PRs someone else authored, e.g. read-only while your own PRs run unsandboxed. read-only needs allow_push false or unset. null: sandbox."`
	ReviewForks *[]string `yaml:"review_forks" jsonschema:"minLength=1,nullable" jsonschema_description:"Your own forks that review-only sessions may push evidence to (failing tests, repro scripts), by exact owner/repo name, e.g. me/repo; globs work, but a wildcard also matches repos that aren't forks. Such sessions may edit and commit locally; git push goes only to branches under review/ of a listed repo that is neither the PR's head nor its base repo, and follows push. Pushing workflow files needs the workflow token scope. null: auto, the checkout's origin when --remote watches another remote and origin is your fork of the watched repo. []: review only, no pushes."`
}

// Forks is review_forks, nil when unset (auto) or empty.
func (o OthersPRs) Forks() []string {
	if o.ReviewForks == nil {
		return nil
	}
	return *o.ReviewForks
}

// Prompts adds to every agent prompt.
type Prompts struct {
	Extra string `yaml:"extra" jsonschema_description:"Appended to every agent prompt, e.g. house rules for tests or commits."`
}

// Default is the configuration without a file.
func Default() Config {
	return Config{
		Mode:            "supervised",
		Launcher:        "auto",
		Terminal:        Terminal{Name: "auto"},
		IntervalSeconds: 60,
		LookbackHours:   168,
		MaxAgents:       1,
		CandidateLimit:  50,
		StaleLockHours:  12,
		Repos:           Repos{Include: []string{}, Exclude: []string{}},
		Classifiers:     Classifiers{"jev": DefaultJev()},
		PRSettings: PRSettings{
			Sandbox:       "off",
			Agent:         "codex",
			IgnoreAuthors: []string{},
			Triggers: Triggers{
				OwnPRs: OwnPRs{Enabled: true, Check: true},
				OptIn: OptIn{
					Enabled:  true,
					Reaction: "eyes",
					Where:    []string{"description", "comment", "review", "review_comment"},
					OnChange: OnChange{Check: true, IgnoreOwnActivity: true},
				},
				ReviewReplies: ReviewReplies{Enabled: true, Check: true, Scope: "thread"},
				Mentions:      Mentions{Enabled: true, Check: true, Scope: "comment"},
			},
			LaunchCheck: LaunchCheck{Classifier: new("jev"), SkipBelow: 0.5},
			ToolGate: ToolGate{
				Classifier: new("jev"),
				Matcher:    "Bash|Write|Edit|NotebookEdit",
				Rules:      []string{},
			},
		},
	}
}

// AllowPushToOthers tells whether sessions on someone else's PR may push.
func (c *Config) AllowPushToOthers() bool {
	if c.OthersPRs.AllowPush != nil {
		return *c.OthersPRs.AllowPush
	}
	return c.Mode == "autonomous"
}

// SandboxFor is the sandbox of a session on your own PR or someone else's.
func (c *Config) SandboxFor(own bool) string {
	if !own && c.OthersPRs.Sandbox != nil {
		return *c.OthersPRs.Sandbox
	}
	return c.Sandbox
}

// PushMode is push with its mode-dependent default.
func (c *Config) PushMode() string {
	if c.Push != nil {
		return *c.Push
	}
	if c.Sandbox == ReadOnly {
		return "never"
	}
	if c.Mode == "supervised" {
		return "ask"
	}
	return "allow"
}

// GitHubWritesMode is github_writes with its mode-dependent default.
func (c *Config) GitHubWritesMode() string {
	if c.GitHubWrites != nil {
		return *c.GitHubWrites
	}
	if c.Sandbox == ReadOnly {
		return "never"
	}
	if c.Mode == "supervised" {
		return "ask"
	}
	return "allow"
}

// Error is an unreadable or invalid config file.
type Error struct{ msg string }

func (e *Error) Error() string { return e.msg }

// BaseDir is $XDG_CONFIG_HOME, else ~/.config.
func BaseDir() string {
	if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
		return base
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config")
}

// DefaultPath is where the config is found without --config or $OUTRIDER_CONFIG.
func DefaultPath() string { return filepath.Join(BaseDir(), "outrider", "config.yaml") }

// Find returns --config, then $OUTRIDER_CONFIG (or the old
// $LLM_REVIEW_AGENT_CONFIG), then the default path
// when it exists; "" means no config file.
func Find(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if p := os.Getenv(EnvVar); p != "" {
		return p
	}
	if p := os.Getenv(LegacyEnvVar); p != "" {
		slog.Warn("$" + LegacyEnvVar + " is deprecated, use $" + EnvVar)
		return p
	}
	p := DefaultPath()
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

// Describe names a config source for messages.
func Describe(path string) string {
	if path == "" {
		return "no config file, built-in defaults"
	}
	return path
}

// Load reads the config at path, or returns the defaults when path is "".
func Load(path string) (Config, error) {
	if path == "" {
		return Default(), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		var pe *os.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		return Config{}, &Error{fmt.Sprintf("cannot read config %s: %v", path, err)}
	}
	return Parse(data, path)
}

// JevBackendConfigured tells whether a jev-use backend key is in the environment.
func JevBackendConfigured() bool {
	return slices.ContainsFunc(JevBackendEnv, func(k string) bool { return os.Getenv(k) != "" })
}

// crossCheck validates what spans several keys, which the schema can't.
func (c *Config) crossCheck() []string {
	errs := c.checkWorkflows()
	if c.NetworkAccess != nil && *c.NetworkAccess && (c.SandboxFor(true) == ReadOnly || c.SandboxFor(false) == ReadOnly) {
		errs = append(errs, "network_access: true conflicts with read-only sessions, which have no network (set false or remove it)")
	}
	if _, ok := c.Classifiers["jev"]; !ok {
		// keep the built-in available when only others are added
		c.Classifiers["jev"] = DefaultJev()
	}
	for _, globs := range []struct {
		key      string
		patterns []string
		compile  func([]string) (Globs, error)
	}{
		{"repos.include", c.Repos.Include, RepoGlobs},
		{"repos.exclude", c.Repos.Exclude, RepoGlobs},
		{"ignore_authors", c.IgnoreAuthors, LoginGlobs},
	} {
		if _, err := globs.compile(globs.patterns); err != nil {
			errs = append(errs, globs.key+": "+err.Error())
		}
	}
	for _, key := range []struct {
		name  string
		value *string
	}{{"push", c.Push}, {"github_writes", c.GitHubWrites}} {
		if c.Sandbox == ReadOnly && key.value != nil && *key.value != "never" {
			errs = append(errs, fmt.Sprintf("%s: '%s' conflicts with sandbox: read-only, which never pushes or posts (set never or remove it)", key.name, *key.value))
		}
	}
	if _, err := RepoGlobs(c.OthersPRs.Forks()); err != nil {
		errs = append(errs, "others_prs.review_forks: "+err.Error())
	}
	if c.SandboxFor(false) == ReadOnly && len(c.OthersPRs.Forks()) > 0 {
		errs = append(errs, "others_prs.review_forks conflicts with a read-only sandbox for others' PRs, which can't push (remove one)")
	}
	if c.SandboxFor(false) == ReadOnly && c.OthersPRs.AllowPush != nil && *c.OthersPRs.AllowPush {
		errs = append(errs, "others_prs.allow_push: true conflicts with a read-only sandbox for others' PRs (set false or remove it)")
	}
	for _, role := range []struct {
		name, needs string
		classifier  *string
	}{
		{"launch_check", "launch_command", c.LaunchCheck.Classifier},
		{"tool_gate", "hook_command", c.ToolGate.Classifier},
	} {
		if role.classifier == nil {
			continue
		}
		name := *role.classifier
		cl, ok := c.Classifiers[name]
		if !ok {
			errs = append(errs, fmt.Sprintf("%s.classifier: unknown classifier '%s' (defined: %s)",
				role.name, name, strings.Join(c.Classifiers.Names(), ", ")))
			continue
		}
		if cl.Kind == KindCommand {
			cmd := cl.LaunchCommand
			if role.needs == "hook_command" {
				cmd = cl.HookCommand
			}
			if cmd == nil {
				errs = append(errs, fmt.Sprintf("%s.classifier: '%s' has no %s", role.name, name, role.needs))
			}
		}
	}
	return errs
}
