// Package config is the YAML configuration.
//
// The structs below are the single source of truth: types, defaults (Default),
// constraints and descriptions (struct tags) are declared once. Loading
// validates with them, and the JSON Schema (`llm-review-agent config schema`)
// and the documented config (`config generate`) are generated from them.
//
// Tags besides `yaml`:
//
//	desc       description
//	enum       allowed values, comma separated
//	min, max   inclusive bounds; xmin: exclusive lower bound
//	minlen     at least this many characters (strings) or items (lists)
//	itemminlen at least this many characters per list item
//	unique     list entries must be unique
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	// EnvVar names the config file when --config is not given.
	EnvVar = "LLM_REVIEW_AGENT_CONFIG"
	// SchemaID is the $id of the generated JSON Schema.
	SchemaID = "https://github.com/appetizers-io/llm-review-agent/config.schema.json"
)

// JevBackendEnv are the variables that give jev-use a backend.
var JevBackendEnv = []string{"TYPESAFE_API_KEY", "OPENROUTER_API_KEY", "AI_GATEWAY_API_KEY", "JEV_BACKEND"}

// Config is the llm-review-agent configuration. Every key is optional;
// command-line flags override this file.
type Config struct {
	OwnerName       *string     `yaml:"owner_name" minlen:"1" desc:"How prompts refer to you. null: first name from your GitHub profile, else your login."`
	Mode            string      `yaml:"mode" enum:"supervised,autonomous" desc:"supervised: Claude sessions get deny rules for risky tools and, when its classifier is available, the tool_gate on every call; other people's PRs are review only. autonomous: no tool gating, and pushing to other people's PRs follows others_prs.allow_push (default: allowed)."`
	Push            *string     "yaml:\"push\" enum:\"ask,never,allow\" desc:\"Pushes in sessions that may push (your own PRs, others' PRs with others_prs.allow_push). ask: every `git push` opens a dialog and runs only after you click Push (macOS: osascript, Linux: zenity or kdialog, Windows: PowerShell; without one it is refused). never: commits stay local. allow: no question. null: ask in supervised mode, allow in autonomous mode.\""
	GitHubWrites    *string     "yaml:\"github_writes\" enum:\"ask,never,allow\" desc:\"Comments, review comments and replies, reviews and reactions the agent posts with `gh` on the session's PR, in every session. ask: each one opens a dialog showing the text and runs only after you click Post (without a dialog it is refused). never: GitHub stays read-only. allow: no question. Merging, closing, editing the PR and writes to other PRs are always refused. null: ask in supervised mode, allow in autonomous mode.\""
	Agent           string      `yaml:"agent" enum:"codex,claude" desc:"Coding agent CLI to launch."`
	Launcher        string      `yaml:"launcher" enum:"auto,terminal,tmux" desc:"auto: macOS: the terminal in a desktop (Aqua) session, else tmux. Linux: the terminal when a display is present and a terminal resolves, else tmux. Windows: always the terminal."`
	Terminal        Terminal    `yaml:"terminal" desc:"Terminal app for launcher: terminal. auto: detected once at startup ($TERM_PROGRAM, then terminal-specific variables, then $TERMINAL, then the platform default). A name forces that app. A command list with a {cmd} placeholder runs anything else, e.g. [alacritty, -e, '{cmd}']."`
	IntervalSeconds int         `yaml:"interval_seconds" min:"10" desc:"Pause between polls. Doubles on consecutive failures, up to 15 minutes."`
	LookbackHours   int         `yaml:"lookback_hours" min:"1" desc:"Notification window, and how long an unseen candidate PR is kept."`
	MaxAgents       int         `yaml:"max_agents" min:"1" desc:"Agent sessions allowed to run at once."`
	CandidateLimit  int         `yaml:"candidate_limit" min:"1" desc:"Most recently seen non-owned PRs checked for the opt-in reaction per poll."`
	StaleLockHours  float64     `yaml:"stale_lock_hours" xmin:"0" desc:"A session lock older than this is dead."`
	Repos           Repos       `yaml:"repos"`
	IgnoreAuthors   []string    `yaml:"ignore_authors" itemminlen:"1" desc:"Login globs whose activity alone never triggers a session, e.g. \"netlify[bot]\" or \"*[bot]\"."`
	Triggers        Triggers    `yaml:"triggers"`
	OthersPRs       OthersPRs   `yaml:"others_prs"`
	Classifiers     Classifiers "yaml:\"classifiers\" desc:\"Named decision backends. 'jev' is always defined; add `kind: command` entries for local classifiers.\""
	LaunchCheck     LaunchCheck `yaml:"launch_check"`
	ToolGate        ToolGate    `yaml:"tool_gate"`
	Prompts         Prompts     `yaml:"prompts"`
}

// Repos limits which repositories are watched.
type Repos struct {
	Include []string `yaml:"include" itemminlen:"1" desc:"owner/repo globs or GitHub URLs. Empty: the checkout you run in, else every repo."`
	Exclude []string `yaml:"exclude" itemminlen:"1" desc:"owner/repo globs or GitHub URLs that never trigger."`
}

// OwnPRs are notifications on PRs you authored.
type OwnPRs struct {
	Enabled bool `yaml:"enabled"`
	Check   bool `yaml:"check" desc:"Run the launch_check classifier before starting an agent (only when one is configured and available)."`
}

// OnChange relaunches when reviews or comments on an opted-in PR change.
type OnChange struct {
	Check             bool `yaml:"check" desc:"Run the launch_check classifier before starting an agent (only when one is configured and available)."`
	IgnoreOwnActivity bool `yaml:"ignore_own_activity" desc:"Changes made only by you (or by ignore_authors) never relaunch."`
}

// OptIn is the opt-in trigger: your reaction on someone else's PR opts it in; sessions cover the whole PR.
type OptIn struct {
	Enabled  bool     `yaml:"enabled"`
	Reaction string   `yaml:"reaction" enum:"+1,-1,laugh,confused,heart,hooray,rocket,eyes" desc:"GitHub reaction that opts a PR in. Removing it stops the watch."`
	Where    []string `yaml:"where" enum:"description,comment,review,review_comment" minlen:"1" unique:"true" desc:"Where the reaction counts."`
	OnChange OnChange `yaml:"on_change"`
}

// ReviewReplies is the reply trigger: someone replies in a review thread you took part in.
type ReviewReplies struct {
	Enabled          bool   `yaml:"enabled"`
	Check            bool   `yaml:"check" desc:"Run the launch_check classifier before starting an agent (only when one is configured and available)."`
	Scope            string `yaml:"scope" enum:"thread,pr" desc:"thread: the session handles only the replied threads. pr: the whole PR."`
	FreshWithinHours *int   `yaml:"fresh_within_hours" min:"1" desc:"Only replies this recent trigger. null: lookback_hours."`
}

// Mentions is the mention trigger: someone @mentions your login on a PR that is neither yours nor
// opted in (those already relaunch for any new activity).
type Mentions struct {
	Enabled          bool   `yaml:"enabled"`
	Check            bool   `yaml:"check" desc:"Run the launch_check classifier before starting an agent (only when one is configured and available)."`
	Scope            string `yaml:"scope" enum:"comment,pr" desc:"comment: the session handles only the mentioning comments. pr: the whole PR."`
	FreshWithinHours *int   `yaml:"fresh_within_hours" min:"1" desc:"Only mentions this recent trigger. null: lookback_hours."`
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
	Classifier *string `yaml:"classifier" minlen:"1" desc:"Name from classifiers; null: always launch."`
	SkipBelow  float64 `yaml:"skip_below" min:"0" max:"1" desc:"Skip when the classifier is confident the probability that something is actionable is below this."`
}

// ToolGate checks every tool call in supervised agent sessions (PreToolUse hook).
type ToolGate struct {
	Classifier         *string  `yaml:"classifier" minlen:"1" desc:"Name from classifiers; null: no tool gate."`
	Matcher            string   `yaml:"matcher" minlen:"1" desc:"Tools the gate sees."`
	Threshold          *float64 `yaml:"threshold" min:"0" max:"1" desc:"Confidence below which the gate asks instead of deciding. null: the classifier's own default."`
	Rules              []string `yaml:"rules" itemminlen:"1" desc:"Your own rules for every tool call, e.g. \"never modify generated/\"."`
	IncludePromptExtra bool     `yaml:"include_prompt_extra" desc:"Also enforce prompts.extra through the gate."`
}

// OthersPRs are sessions on PRs someone else authored.
type OthersPRs struct {
	AllowPush *bool "yaml:\"allow_push\" desc:\"false: review only. The agent must not edit, commit or push, and `git push` is blocked in the session. true: it may push fixes to the PR branch (fast-forward only). null: false in supervised mode, true in autonomous mode.\""
}

// Prompts adds to every agent prompt.
type Prompts struct {
	Extra string `yaml:"extra" desc:"Appended to every agent prompt, e.g. house rules for tests or commits."`
}

// Default is the configuration without a file.
func Default() Config {
	return Config{
		Mode:            "supervised",
		Agent:           "codex",
		Launcher:        "auto",
		Terminal:        Terminal{Name: "auto"},
		IntervalSeconds: 60,
		LookbackHours:   168,
		MaxAgents:       1,
		CandidateLimit:  50,
		StaleLockHours:  12,
		Repos:           Repos{Include: []string{}, Exclude: []string{}},
		IgnoreAuthors:   []string{},
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
		Classifiers: Classifiers{"jev": DefaultJev()},
		LaunchCheck: LaunchCheck{Classifier: new("jev"), SkipBelow: 0.5},
		ToolGate: ToolGate{
			Classifier: new("jev"),
			Matcher:    "Bash|Write|Edit|NotebookEdit",
			Rules:      []string{},
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

// PushMode is push with its mode-dependent default.
func (c *Config) PushMode() string {
	if c.Push != nil {
		return *c.Push
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
	if c.Mode == "supervised" {
		return "ask"
	}
	return "allow"
}

// Error is an unreadable or invalid config file.
type Error struct{ msg string }

func (e *Error) Error() string { return e.msg }

// DefaultPath is where the config is found without --config or $LLM_REVIEW_AGENT_CONFIG.
func DefaultPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "llm-review-agent", "config.yaml")
}

// Find returns --config, then $LLM_REVIEW_AGENT_CONFIG, then the default path
// when it exists; "" means no config file.
func Find(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if p := os.Getenv(EnvVar); p != "" {
		return p
	}
	p := DefaultPath()
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
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

// crossCheck validates what spans several keys.
func (c *Config) crossCheck() []string {
	var errs []string
	if _, ok := c.Classifiers["jev"]; !ok {
		// keep the built-in available when only others are added
		c.Classifiers["jev"] = DefaultJev()
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
	if msg := c.Terminal.check(); msg != "" {
		errs = append(errs, "terminal: "+msg)
	}
	return errs
}
