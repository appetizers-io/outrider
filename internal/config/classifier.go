package config

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/invopop/jsonschema"
	"github.com/kballard/go-shellquote"
	"go.yaml.in/yaml/v3"
)

// Classifier kinds.
const (
	KindJev     = "jev"
	KindCommand = "command"
)

// JevClassifier is Jev via jev-use: `<command> judge` for launch checks,
// `<command> hook gate` as the tool gate.
type JevClassifier struct {
	Kind                string     `yaml:"kind" jsonschema:"default=jev" jsonschema_extras:"const=jev"`
	Enabled             JevEnabled `yaml:"enabled" jsonschema_description:"auto: on when the command is found and a Jev backend is configured (TYPESAFE_API_KEY, OPENROUTER_API_KEY, AI_GATEWAY_API_KEY, JEV_BACKEND)."`
	Command             *string    `yaml:"command" jsonschema:"minLength=1,nullable" jsonschema_description:"null: jev-use on PATH, else npx -y jev-use@0.8.0."`
	ConfidenceThreshold *float64   `yaml:"confidence_threshold" jsonschema:"minimum=0,maximum=1,nullable" jsonschema_description:"Launch check: below this confidence Jev escalates, which launches. null: jev-use's defaults (0.5 reported, 0.4 estimated)."`
	TimeoutSeconds      int        `yaml:"timeout_seconds" jsonschema:"minimum=1,default=60" jsonschema_description:"A slower launch check launches anyway."`
}

// CommandClassifier is any local classifier.
//
// launch_command gets a JSON request on stdin and prints
// {"launch": bool} or {"probability": 0..1}, optionally with "reason".
// hook_command is a Claude Code / Codex PreToolUse hook; the session policy
// is in $OUTRIDER_POLICY_FILE and $OUTRIDER_GATE_TEXT.
type CommandClassifier struct {
	Kind           string  `yaml:"kind" jsonschema:"required" jsonschema_extras:"const=command"`
	Enabled        bool    `yaml:"enabled" jsonschema:"default=true"`
	LaunchCommand  *string `yaml:"launch_command" jsonschema:"minLength=1,nullable" jsonschema_description:"Command for launch checks: JSON request on stdin, {\"launch\": bool} or {\"probability\": 0..1} on stdout. null: can't do them."`
	HookCommand    *string `yaml:"hook_command" jsonschema:"minLength=1,nullable" jsonschema_description:"Claude Code / Codex PreToolUse hook command. null: can't gate tools."`
	TimeoutSeconds int     `yaml:"timeout_seconds" jsonschema:"minimum=1,default=30" jsonschema_description:"A slower launch check launches anyway."`
}

// JevEnabled is "auto", "true" or "false".
type JevEnabled string

// JevEnabled values.
const (
	EnabledAuto  JevEnabled = "auto"
	EnabledTrue  JevEnabled = "true"
	EnabledFalse JevEnabled = "false"
)

// UnmarshalYAML accepts auto or a boolean.
func (e *JevEnabled) UnmarshalYAML(n *yaml.Node) error {
	if n.Tag == "!!str" && n.Value == "auto" {
		*e = EnabledAuto
		return nil
	}
	var b bool
	if err := n.Decode(&b); err != nil {
		return fmt.Errorf("enabled: %w", err)
	}
	*e = JevEnabled(fmt.Sprint(b))
	return nil
}

// JSONSchema is "auto", true or false.
func (JevEnabled) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Enum: []any{"auto", true, false}, Default: "auto"}
}

// MarshalYAML writes auto or a boolean.
func (e JevEnabled) MarshalYAML() (any, error) {
	if e == EnabledAuto {
		return "auto", nil
	}
	return e == EnabledTrue, nil
}

// Classifier is a JevClassifier or a CommandClassifier, told apart by kind.
type Classifier struct {
	Kind    string
	Jev     JevClassifier
	Command CommandClassifier

	// Common view of the kind's fields.
	LaunchCommand *string
	HookCommand   *string
}

// DefaultJev is the built-in jev classifier.
func DefaultJev() Classifier {
	return Classifier{Kind: KindJev, Jev: JevClassifier{Kind: KindJev, Enabled: EnabledAuto, TimeoutSeconds: 60}}
}

func defaultCommand() CommandClassifier {
	return CommandClassifier{Kind: KindCommand, Enabled: true, TimeoutSeconds: 30}
}

// nodeKind is the kind key of a classifier mapping ("jev" when missing).
func nodeKind(n *yaml.Node) string {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == "kind" {
			return n.Content[i+1].Value
		}
	}
	return KindJev
}

// UnmarshalYAML decodes the kind's struct with its defaults.
func (c *Classifier) UnmarshalYAML(n *yaml.Node) error {
	switch kind := nodeKind(n); kind {
	case KindJev:
		*c = DefaultJev()
		if err := n.Decode(&c.Jev); err != nil {
			return fmt.Errorf("jev classifier: %w", err)
		}
	case KindCommand:
		*c = Classifier{Kind: KindCommand, Command: defaultCommand()}
		if err := n.Decode(&c.Command); err != nil {
			return fmt.Errorf("command classifier: %w", err)
		}
		c.LaunchCommand, c.HookCommand = c.Command.LaunchCommand, c.Command.HookCommand
	default:
		return fmt.Errorf("kind must be 'jev' or 'command', not %q", kind)
	}
	return nil
}

// JSONSchema is one of the kinds (their definitions are added by Schema).
func (Classifier) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{OneOf: []*jsonschema.Schema{
		{Ref: "#/$defs/JevClassifier"},
		{Ref: "#/$defs/CommandClassifier"},
	}}
}

// MarshalYAML writes the kind's struct.
func (c Classifier) MarshalYAML() (any, error) {
	if c.Kind == KindCommand {
		return c.Command, nil
	}
	return c.Jev, nil
}

// Disabled tells whether the classifier is switched off.
func (c Classifier) Disabled() bool {
	if c.Kind == KindCommand {
		return !c.Command.Enabled
	}
	return c.Jev.Enabled == EnabledFalse
}

// Classifiers are the named decision backends.
type Classifiers map[string]Classifier

// Names are the classifier names, sorted.
func (cs Classifiers) Names() []string { return slices.Sorted(maps.Keys(cs)) }

// Terminal names the terminal app sessions open in: "auto", a known name, or
// a command list with a {cmd} placeholder.
type Terminal struct {
	Name    string   // set unless Command is
	Command []string // custom command
}

// Placeholder is replaced by the session command in a custom terminal command.
const Placeholder = "{cmd}"

// TerminalNames are the terminal apps outrider knows how to open.
var TerminalNames = []string{"auto", "terminal-app", "iterm", "ghostty", "wezterm", "kitty", "windows-terminal", "x-terminal-emulator", "cmd"}

// UnmarshalYAML accepts a name or a command list.
func (t *Terminal) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.SequenceNode {
		*t = Terminal{}
		if err := n.Decode(&t.Command); err != nil {
			return fmt.Errorf("terminal: %w", err)
		}
		return nil
	}
	*t = Terminal{}
	if err := n.Decode(&t.Name); err != nil {
		return fmt.Errorf("terminal: %w", err)
	}
	return nil
}

// JSONSchema is a known name or a command list containing {cmd}.
func (Terminal) JSONSchema() *jsonschema.Schema {
	names := make([]any, len(TerminalNames))
	for i, n := range TerminalNames {
		names[i] = n
	}
	minItems := uint64(1)
	return &jsonschema.Schema{
		Default: "auto",
		AnyOf: []*jsonschema.Schema{
			{Type: "string", Enum: names},
			{
				Type: "array", MinItems: &minItems,
				Items:    &jsonschema.Schema{Type: "string", MinLength: &minItems},
				Contains: &jsonschema.Schema{Type: "string", Pattern: `\{cmd\}`},
			},
		},
	}
}

// MarshalYAML writes the name or the command list.
func (t Terminal) MarshalYAML() (any, error) {
	if t.Command != nil {
		return t.Command, nil
	}
	return t.Name, nil
}

// String is the name or the command list, for logs.
func (t Terminal) String() string {
	if t.Command != nil {
		return "[" + strings.Join(t.Command, " ") + "]"
	}
	return t.Name
}

// ParseTerminal reads --terminal: a name, or a shell-quoted command with {cmd}.
func ParseTerminal(s string) Terminal {
	if strings.Contains(s, Placeholder) {
		words, _ := shellquote.Split(s) // unbalanced quotes: no words, which the schema refuses
		return Terminal{Command: words}
	}
	return Terminal{Name: s}
}
