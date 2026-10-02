package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// isolate keeps the developer's real config away.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg"))
	t.Setenv(EnvVar, "")
	t.Setenv(LegacyEnvVar, "")
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
	// schema errors name the JSON pointer of the bad value
	for _, tc := range []struct{ yaml, message string }{
		{"agent: gpt", "/agent"},
		{"max_agents: 0", "/max_agents"},
		{"typo: 1", "typo"},
		{`classifiers: {jev: {enabled: "yes"}}`, "/classifiers/jev"},
		{"classifiers: {jev: {enabled: maybe}}", "/classifiers/jev"},
		{"launch_check: {skip_below: 2}", "/launch_check/skip_below"},
		{"tool_gate: {classifier: nope}", "tool_gate.classifier: unknown classifier 'nope' (defined: jev)"},
		{"classifiers: {l: {kind: command, hook_command: x}}\nlaunch_check: {classifier: l}", "launch_check.classifier: 'l' has no launch_command"},
		{"classifiers: {l: {kind: command, launch_command: x}}\ntool_gate: {classifier: l}", "tool_gate.classifier: 'l' has no hook_command"},
		{"classifiers: {l: {kind: magic}}", "/classifiers/l"},
		{"classifiers: {l: {launch_command: x}}", "/classifiers/l"}, // kind: command is required
		{"classifiers: {jev: {hook_command: x}}", "/classifiers/jev"},
		{"classifiers: {l: {kind: command, enabled: auto}}", "/classifiers/l"},
		{"triggers: {opt_in: {reaction: eyez}}", "/triggers/opt_in/reaction"},
		{"triggers: {opt_in: {where: [comment, comment]}}", "/triggers/opt_in/where"},
		{"triggers: {opt_in: {where: [nowhere]}}", "/triggers/opt_in/where/0"},
		{"triggers: {opt_in: {where: []}}", "/triggers/opt_in/where"},
		{"triggers: {review_replies: {fresh_within_hours: 0}}", "/triggers/review_replies/fresh_within_hours"},
		{"ignore_authors: bot", "/ignore_authors"},
		{`ignore_authors: [""]`, "/ignore_authors/0"},
		{`max_agents: "3"`, "/max_agents"},
		{"max_agents: 3.5", "/max_agents"},
		{"agent: 3", "/agent"},
		{"stale_lock_hours: 0", "/stale_lock_hours"},
		{"owner_name: ''", "/owner_name"},
		{"repos: null", "/repos"},
		{"- not\n- a\n- mapping", "got array, want object"},
		{"terminal: konsole2", "/terminal"},
		{"terminal: [alacritty, -e]", "/terminal"},
		// security review F1: a broken glob must not silently match nothing
		{`repos: {exclude: ["org/[abc"]}`, "repos.exclude: bad glob \"org/[abc\""},
		{`repos: {exclude: ["org/{a,b"]}`, "repos.exclude: bad glob \"org/{a,b\""},
		{`repos: {include: ["org/x\\"]}`, "repos.include: bad glob"},
	} {
		t.Run(tc.yaml, func(t *testing.T) {
			_, err := Parse([]byte(tc.yaml), "c.yaml")
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.message)
			require.True(t, strings.HasPrefix(err.Error(), "c.yaml is invalid"), err.Error())
		})
	}
}

func TestBooleansAreTrueAndFalse(t *testing.T) {
	// YAML 1.2: yes/no/on/off are strings, not booleans
	_, err := Parse([]byte("triggers: {own_prs: {enabled: no}}\n"), "c.yaml")
	require.ErrorContains(t, err, "/triggers/own_prs/enabled")
	c, err := Parse([]byte("triggers: {own_prs: {enabled: false}}\n"), "c.yaml")
	require.NoError(t, err)
	require.False(t, c.Triggers.OwnPRs.Enabled)
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
		"config.schema.json is stale; regenerate with `task schema`")
}

func TestSchemaDocumentsDefaults(t *testing.T) {
	var s map[string]any
	require.NoError(t, json.Unmarshal([]byte(SchemaText()), &s))
	r := require.New(t)
	r.Equal(SchemaID, s["$id"])
	r.Equal(false, s["additionalProperties"])
	r.Equal("outrider configuration", s["title"])
	defs := s["$defs"].(map[string]any)
	onChange := defs["OnChange"].(map[string]any)["properties"].(map[string]any)
	r.Equal(true, onChange["ignore_own_activity"].(map[string]any)["default"])
	optIn := defs["OptIn"].(map[string]any)["properties"].(map[string]any)
	r.Equal("eyes", optIn["reaction"].(map[string]any)["default"])
	r.Contains(defs, "JevClassifier")
	r.Contains(defs, "CommandClassifier")
}

func TestExampleConfigIsExactlyTheDefaults(t *testing.T) {
	c, err := Parse([]byte(Example), "config.example.yaml")
	require.NoError(t, err)
	require.Equal(t, Default(), c)
}

// keys lists every key path of YAML data.
func keys(prefix string, v any) []string {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	var out []string
	for k, sub := range m {
		out = append(out, prefix+k)
		out = append(out, keys(prefix+k+".", sub)...)
	}
	return out
}

func TestExampleConfigShowsEveryKey(t *testing.T) {
	var example, defaults any
	require.NoError(t, yaml.Unmarshal([]byte(Example), &example))
	shown, err := yaml.Marshal(Default())
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(shown, &defaults))
	want, got := keys("", defaults), keys("", example)
	slices.Sort(want)
	slices.Sort(got)
	require.Equal(t, want, got)
	for _, k := range []string{"kind", "enabled", "launch_command", "hook_command", "timeout_seconds"} {
		require.Contains(t, Example, "#   "+k+":") // the commented local classifier
	}
}

func TestConfigWithAllKeysLoads(t *testing.T) {
	// a config that sets every key the way users write them; it must keep loading
	text, err := os.ReadFile("../../testdata/config-all-keys.yaml")
	require.NoError(t, err)
	c, err := Parse(text, "config-all-keys.yaml")
	require.NoError(t, err)
	require.Equal(t, "Matthias", *c.OwnerName)
	require.Equal(t, []string{"open-component-model/*"}, c.Repos.Include)
	require.Equal(t, []string{"never modify generated/ or vendor/"}, c.ToolGate.Rules)
	require.Equal(t, []string{"alacritty", "-e", "{cmd}"}, c.Terminal.Command)
	// it really sets every key
	var data, defaults any
	require.NoError(t, yaml.Unmarshal(text, &data))
	shown, err := yaml.Marshal(Default())
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(shown, &defaults))
	missing := slices.DeleteFunc(keys("", defaults), func(k string) bool { return slices.Contains(keys("", data), k) })
	require.Empty(t, missing)
}

func TestValidateChecksBuiltConfigs(t *testing.T) {
	c := Default()
	c.MaxAgents = 0
	_, err := Validate(c, "flags")
	require.ErrorContains(t, err, "flags is invalid")
	require.ErrorContains(t, err, "/max_agents")
	c = Default()
	c.Agent = "claude"
	got, err := Validate(c, "flags")
	require.NoError(t, err)
	require.Equal(t, c, got)
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
		{"sandbox: read-only", false, "never", "never"},
		{"mode: autonomous\nsandbox: read-only\npush: never", true, "never", "never"},
		{"others_prs: {sandbox: read-only}\npush: allow", false, "allow", "ask"},
	} {
		c, err := Parse([]byte(tc.text), "c.yaml")
		require.NoError(t, err)
		require.Equal(t, tc.allowOthers, c.AllowPushToOthers(), tc.text)
		require.Equal(t, tc.push, c.PushMode(), tc.text)
		require.Equal(t, tc.writes, c.GitHubWritesMode(), tc.text)
	}
}

func TestExistingConfigsLoadUnchanged(t *testing.T) {
	// a config from an earlier version, every key set
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

func TestSandbox(t *testing.T) {
	for _, tc := range []struct {
		text       string
		own, other string
	}{
		{"", "off", "off"},
		{"sandbox: off", "off", "off"},
		{"sandbox: read-only", "read-only", "read-only"},
		{"others_prs: {sandbox: read-only}", "off", "read-only"},
		{"sandbox: read-only\nothers_prs: {sandbox: off}", "read-only", "off"},
	} {
		r := require.New(t)
		c, err := Parse([]byte(tc.text), "c.yaml")
		r.NoError(err, tc.text)
		r.Equal(tc.own, c.SandboxFor(true), tc.text)
		r.Equal(tc.other, c.SandboxFor(false), tc.text)
	}
}

func TestReadOnlySandboxRefusesConflictingKeys(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{"sandbox: read-only\npush: allow", "push: 'allow' conflicts with sandbox: read-only"},
		{"sandbox: read-only\npush: ask", "push: 'ask' conflicts"},
		{"sandbox: read-only\ngithub_writes: ask", "github_writes: 'ask' conflicts"},
		{"others_prs: {sandbox: read-only, allow_push: true}", "others_prs.allow_push: true conflicts"},
		{"others_prs: {sandbox: read-only, review_forks: [me/*]}", "others_prs.review_forks conflicts"},
		{"sandbox: read-only\nothers_prs: {review_forks: [me/*]}", "others_prs.review_forks conflicts"},
		{"others_prs: {review_forks: ['me/[x']}", "others_prs.review_forks: bad glob"},
		{"sandbox: read-only\nothers_prs: {allow_push: true}", "others_prs.allow_push: true conflicts"},
		{"sandbox: on", "sandbox"},
	} {
		_, err := Parse([]byte(tc.text), "c.yaml")
		require.ErrorContains(t, err, tc.want, tc.text)
	}
	_, err := Parse([]byte("sandbox: read-only\npush: never\ngithub_writes: never\nothers_prs: {allow_push: false}"), "c.yaml")
	require.NoError(t, err)
}
