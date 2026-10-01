package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// isolate keeps the developer's real config away.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg"))
	t.Setenv(EnvVar, "")
	return dir
}

func TestDefaults(t *testing.T) {
	r := require.New(t)
	c := Default()
	r.Equal("codex", c.Agent)
	r.True(c.Triggers.OptIn.OnChange.IgnoreOwnActivity)
	r.Equal("thread", c.Triggers.ReviewReplies.Scope)
	r.Equal(KindJev, c.Classifiers["jev"].Kind)
	r.Equal(EnabledAuto, c.Classifiers["jev"].Jev.Enabled)
	r.Equal("jev", *c.LaunchCheck.Classifier)
	r.InDelta(0.5, c.LaunchCheck.SkipBelow, 0)
	r.Equal("jev", *c.ToolGate.Classifier)
	r.Equal("auto", c.Terminal.Name)
	r.Equal("ask", c.PushMode())
	r.Equal("ask", c.GitHubWritesMode())
	r.False(c.AllowPushToOthers())
}

func TestEmptyFileIsDefaults(t *testing.T) {
	for _, text := range []string{"", "# nothing yet\n", "null\n"} {
		c, err := Parse([]byte(text), "c.yaml")
		require.NoError(t, err)
		require.Equal(t, Default(), c)
	}
}

func TestInvalid(t *testing.T) {
	for _, tc := range []struct{ yaml, message string }{
		{"agent: gpt", "agent: Input should be 'codex' or 'claude'"},
		{"max_agents: 0", "max_agents: Input should be greater than or equal to 1"},
		{"typo: 1", "typo: Extra inputs are not permitted"},
		{`classifiers: {jev: {enabled: "yes"}}`, "classifiers.jev.enabled: Input should be 'auto', True or"},
		{"classifiers: {jev: {enabled: maybe}}", "classifiers.jev.enabled: Input should be 'auto', True or"},
		{"launch_check: {skip_below: 2}", "launch_check.skip_below: Input should be less than or"},
		{"tool_gate: {classifier: nope}", "tool_gate.classifier: unknown classifier 'nope' (defined: jev)"},
		{"classifiers: {l: {kind: command, hook_command: x}}\nlaunch_check: {classifier: l}", "launch_check.classifier: 'l' has no launch_command"},
		{"classifiers: {l: {kind: command, launch_command: x}}\ntool_gate: {classifier: l}", "tool_gate.classifier: 'l' has no hook_command"},
		{"classifiers: {l: {kind: magic}}", "classifiers.l: kind must be"},
		{"classifiers: {jev: {hook_command: x}}", "classifiers.jev.hook_command: Extra inputs are not permitted"},
		{"classifiers: {l: {kind: command, enabled: auto}}", "classifiers.l.enabled: Input should be a valid boolean"},
		{"triggers: {opt_in: {reaction: eyez}}", "triggers.opt_in.reaction: Input should be"},
		{"triggers: {opt_in: {where: [comment, comment]}}", "triggers.opt_in.where: entries must be unique"},
		{"triggers: {opt_in: {where: [nowhere]}}", "triggers.opt_in.where.0: Input should be"},
		{"triggers: {opt_in: {where: []}}", "triggers.opt_in.where: List should"},
		{"triggers: {review_replies: {fresh_within_hours: 0}}", "triggers.review_replies.fresh_within_hours: Input should be greater than or equal to 1"},
		{"ignore_authors: bot", "ignore_authors: Input should be a valid list"},
		{`ignore_authors: [""]`, "ignore_authors.0: String should have at least 1 character"},
		{`max_agents: "3"`, "max_agents: Input should be a valid integer"},
		{"max_agents: 3.5", "max_agents: Input should be a valid integer"},
		{"agent: 3", "agent: Input should be a valid string"},
		{"stale_lock_hours: 0", "stale_lock_hours: Input should be greater than 0"},
		{"owner_name: ''", "owner_name: String should have at least 1 character"},
		{"repos: null", "repos: Input should be a valid dictionary"},
		{"- not\n- a\n- mapping", "Input should be a valid dictionary"},
		{"terminal: konsole2", "terminal: Input should be 'auto'"},
		{"terminal: [alacritty, -e]", "terminal: a command list needs a {cmd} placeholder"},
	} {
		t.Run(tc.yaml, func(t *testing.T) {
			_, err := Parse([]byte(tc.yaml), "c.yaml")
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.message)
			require.True(t, strings.HasPrefix(err.Error(), "c.yaml is invalid"), err.Error())
		})
	}
}

func TestYAML11BooleansStillLoad(t *testing.T) {
	// the Python version read YAML 1.1, where yes/no/on/off are booleans
	c, err := Parse([]byte("triggers: {own_prs: {enabled: no}}\nclassifiers: {jev: {enabled: yes}}\n"), "c.yaml")
	require.NoError(t, err)
	require.False(t, c.Triggers.OwnPRs.Enabled)
	require.Equal(t, EnabledTrue, c.Classifiers["jev"].Jev.Enabled)
}

func TestNestedKeysKeepTheirDefaults(t *testing.T) {
	r := require.New(t)
	c, err := Parse([]byte("triggers: {opt_in: {reaction: rocket}}\nclassifiers: {jev: {timeout_seconds: 5}}\n"), "c.yaml")
	r.NoError(err)
	r.Equal("rocket", c.Triggers.OptIn.Reaction)
	r.Equal(Default().Triggers.OptIn.Where, c.Triggers.OptIn.Where)
	r.True(c.Triggers.OptIn.OnChange.Check)
	r.Equal(5, c.Classifiers["jev"].Jev.TimeoutSeconds)
	r.Equal(EnabledAuto, c.Classifiers["jev"].Jev.Enabled)
}

func TestTerminal(t *testing.T) {
	r := require.New(t)
	c, err := Parse([]byte("terminal: iterm\n"), "c.yaml")
	r.NoError(err)
	r.Equal(Terminal{Name: "iterm"}, c.Terminal)
	c, err = Parse([]byte("terminal: [alacritty, -e, '{cmd}']\n"), "c.yaml")
	r.NoError(err)
	r.Equal(Terminal{Command: []string{"alacritty", "-e", "{cmd}"}}, c.Terminal)
	r.Equal(Terminal{Command: []string{"foot", "{cmd}"}}, ParseTerminal("foot {cmd}"))
	r.Equal(Terminal{Name: "kitty"}, ParseTerminal("kitty"))
}

func TestUnreadableAndBrokenFiles(t *testing.T) {
	dir := t.TempDir()
	_, err := Load(filepath.Join(dir, "missing.yaml"))
	require.ErrorContains(t, err, "cannot read config")
	p := filepath.Join(dir, "broken.yaml")
	require.NoError(t, os.WriteFile(p, []byte("agent: [claude\n"), 0o600))
	_, err = Load(p)
	require.ErrorContains(t, err, "not valid YAML")
}

func TestFindPrecedence(t *testing.T) {
	r := require.New(t)
	dir := isolate(t)
	r.Empty(Find("")) // nothing configured
	def := DefaultPath()
	r.NoError(os.MkdirAll(filepath.Dir(def), 0o700))
	r.NoError(os.WriteFile(def, []byte("{}"), 0o600))
	r.Equal(def, Find(""))
	t.Setenv(EnvVar, filepath.Join(dir, "env.yaml"))
	r.Equal(filepath.Join(dir, "env.yaml"), Find(""))
	r.Equal("cli.yaml", Find("cli.yaml"))
}

func TestCommittedSchemaIsUpToDate(t *testing.T) {
	committed, err := os.ReadFile("../../config.schema.json")
	require.NoError(t, err)
	require.Equal(t, SchemaText(), string(committed),
		"config.schema.json is stale; regenerate with `go run ./cmd/llm-review-agent config schema > config.schema.json`")
}

func TestSchemaDocumentsDefaults(t *testing.T) {
	var s map[string]any
	require.NoError(t, json.Unmarshal([]byte(SchemaText()), &s))
	r := require.New(t)
	r.Equal(SchemaID, s["$id"])
	r.Equal(false, s["additionalProperties"])
	r.Equal("llm-review-agent configuration", s["title"])
	onChange := s["$defs"].(map[string]any)["OnChange"].(map[string]any)["properties"].(map[string]any)
	r.Equal(true, onChange["ignore_own_activity"].(map[string]any)["default"])
	optIn := s["$defs"].(map[string]any)["OptIn"].(map[string]any)["properties"].(map[string]any)
	r.Equal("eyes", optIn["reaction"].(map[string]any)["default"])
}

func compiled(t *testing.T) *jsonschema.Schema {
	t.Helper()
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(SchemaText()))
	require.NoError(t, err)
	c := jsonschema.NewCompiler()
	require.NoError(t, c.AddResource("config.schema.json", doc))
	s, err := c.Compile("config.schema.json")
	require.NoError(t, err)
	return s
}

// asJSON turns YAML into the data an editor validates.
func asJSON(t *testing.T, text string) any {
	t.Helper()
	var data any
	require.NoError(t, yaml.Unmarshal([]byte(text), &data))
	raw, err := json.Marshal(data)
	require.NoError(t, err)
	v, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
	require.NoError(t, err)
	return v
}

func TestExampleConfigIsValidForTheLoaderAndTheSchema(t *testing.T) {
	text, err := os.ReadFile("../../config.example.yaml")
	require.NoError(t, err)
	_, err = Parse(text, "config.example.yaml")
	require.NoError(t, err)
	require.NoError(t, compiled(t).Validate(asJSON(t, string(text))))
}

func TestSchemaRejectsWhatTheLoaderRejects(t *testing.T) {
	// editors validating against the schema must agree with the tool
	s := compiled(t)
	for _, bad := range []string{
		"agent: gpt",
		"typo: 1",
		"triggers: {opt_in: {where: [comment, comment]}}",
		`classifiers: {jev: {enabled: "yes"}}`,
		"classifiers: {l: {kind: magic}}",
		"classifiers: {l: {launch_command: x}}",
		"interval_seconds: 5",
		"terminal: [alacritty, -e]",
		"terminal: nope",
	} {
		_, err := Parse([]byte(bad), "c.yaml")
		require.Error(t, err, bad)
		require.Error(t, s.Validate(asJSON(t, bad)), bad)
	}
	for _, good := range []string{
		"classifiers: {l: {kind: command, launch_command: x}}",
		"terminal: [alacritty, -e, '{cmd}']",
		"terminal: iterm",
		"push: null",
	} {
		_, err := Parse([]byte(good), "c.yaml")
		require.NoError(t, err, good)
		require.NoError(t, s.Validate(asJSON(t, good)), good)
	}
}

func TestLocalClassifierConfig(t *testing.T) {
	r := require.New(t)
	c, err := Parse([]byte(`
classifiers:
  local:
    kind: command
    launch_command: ~/bin/cls launch
    hook_command: ~/bin/cls hook
launch_check: {classifier: local}
tool_gate: {classifier: null}
`), "c.yaml")
	r.NoError(err)
	r.Equal([]string{"jev", "local"}, c.Classifiers.Names()) // jev stays defined
	r.Equal(KindCommand, c.Classifiers["local"].Kind)
	r.Equal("~/bin/cls launch", *c.Classifiers["local"].LaunchCommand)
	r.Equal(30, c.Classifiers["local"].Command.TimeoutSeconds)
	r.Nil(c.ToolGate.Classifier)
}

func TestDerivedModes(t *testing.T) {
	for _, tc := range []struct {
		text         string
		allowOthers  bool
		push, writes string
	}{
		{"", false, "ask", "ask"},
		{"mode: autonomous", true, "allow", "allow"},
		{"others_prs: {allow_push: true}", true, "ask", "ask"},
		{"mode: autonomous\nothers_prs: {allow_push: false}\npush: ask", false, "ask", "allow"},
		{"github_writes: never", false, "ask", "never"},
	} {
		c, err := Parse([]byte(tc.text), "c.yaml")
		require.NoError(t, err)
		require.Equal(t, tc.allowOthers, c.AllowPushToOthers(), tc.text)
		require.Equal(t, tc.push, c.PushMode(), tc.text)
		require.Equal(t, tc.writes, c.GitHubWritesMode(), tc.text)
	}
}

func fieldPaths(t reflect.Type, prefix string) []string {
	var out []string
	for f := range t.Fields() {
		name := yamlName(f)
		if isSection(f.Type) {
			out = append(out, fieldPaths(f.Type, prefix+name+".")...)
		} else {
			out = append(out, prefix+name)
		}
	}
	return out
}

func TestGeneratedConfigIsExactlyTheDefaults(t *testing.T) {
	text := Generate()
	c, err := Parse([]byte(text), "generated")
	require.NoError(t, err)
	require.Equal(t, Default(), c)
	require.NoError(t, compiled(t).Validate(asJSON(t, text))) // and editors agree
}

func TestGeneratedConfigDocumentsEveryOption(t *testing.T) {
	text := Generate()
	keys := map[string]bool{}
	for line := range strings.SplitSeq(text, "\n") {
		keys[strings.TrimLeft(strings.TrimSpace(strings.Split(line, ":")[0]), "# ")] = true
	}
	for _, path := range fieldPaths(reflect.TypeFor[Config](), "") {
		parts := strings.Split(path, ".")
		require.True(t, keys[parts[len(parts)-1]], path)
	}
	for f := range reflect.TypeFor[CommandClassifier]().Fields() { // the commented example
		require.Contains(t, text, "#   "+yamlName(f)+":")
	}
	require.Contains(t, text, "# (one of: supervised, autonomous)")
	require.Contains(t, text, "# (>= 10)")
}

func TestExistingConfigsLoadUnchanged(t *testing.T) {
	// a config written for the Python version, every key set
	text := `
owner_name: Matthias
mode: autonomous
push: never
github_writes: allow
agent: claude
launcher: tmux
interval_seconds: 30
lookback_hours: 2
max_agents: 3
candidate_limit: 10
stale_lock_hours: 1.5
repos: {include: ["o/*", "https://github.com/x/y"], exclude: [o/skip]}
ignore_authors: ["*[bot]"]
triggers:
  own_prs: {enabled: true, check: false}
  opt_in:
    enabled: true
    reaction: rocket
    where: [comment]
    on_change: {check: false, ignore_own_activity: false}
  review_replies: {enabled: false, check: true, scope: pr, fresh_within_hours: 4}
  mentions: {enabled: true, check: true, scope: pr, fresh_within_hours: 1}
others_prs: {allow_push: false}
classifiers:
  jev: {kind: jev, enabled: true, command: my-jev, confidence_threshold: 0.7, timeout_seconds: 10}
  local: {kind: command, enabled: true, launch_command: cls launch, hook_command: cls hook, timeout_seconds: 5}
launch_check: {classifier: local, skip_below: 0.3}
tool_gate: {classifier: jev, matcher: Bash, threshold: 0.8, rules: [no rm], include_prompt_extra: true}
prompts: {extra: "Run make test."}
`
	r := require.New(t)
	c, err := Parse([]byte(text), "old.yaml")
	r.NoError(err)
	r.Equal("Matthias", *c.OwnerName)
	r.InDelta(1.5, c.StaleLockHours, 0)
	r.Equal(4, *c.Triggers.ReviewReplies.FreshWithinHours)
	r.InDelta(0.7, *c.Classifiers["jev"].Jev.ConfidenceThreshold, 0)
	r.Equal("cls hook", *c.Classifiers["local"].HookCommand)
	r.InDelta(0.8, *c.ToolGate.Threshold, 0)
	r.False(*c.OthersPRs.AllowPush)

	// show's output loads back to the same config
	out, err := yaml.Marshal(c)
	r.NoError(err)
	again, err := Parse(out, "shown")
	r.NoError(err)
	r.Equal(c, again)
}
