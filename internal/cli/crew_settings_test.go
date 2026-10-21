package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/appetizers-io/outrider/internal/config"
	"github.com/appetizers-io/outrider/internal/watch"
)

func TestCrewSettingsYAMLRoundTrip(t *testing.T) {
	input := "triggers:\n  mentions:\n    enabled: false\nworkflows:\n  - name: inspect\n    match: [{}]\n    steps: [Inspect APIs]\n"
	convert := func(raw string) map[string]any {
		t.Helper()
		cmd := crewCmd(watch.Deps{})
		var out bytes.Buffer
		cmd.SetIn(strings.NewReader(raw))
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs([]string{"settings"})
		require.NoError(t, cmd.Execute())
		var response map[string]any
		require.NoError(t, json.Unmarshal(out.Bytes(), &response))
		return response
	}
	first := convert(input)
	encoded, err := json.Marshal(first["settings"])
	require.NoError(t, err)
	second := convert(string(encoded))
	require.Equal(t, first["settings"], second["settings"])
	require.Equal(t, first["settings"], convert(second["yaml"].(string))["settings"])
}

func TestCrewSettingsRejectsUnsupportedAndInvalidInput(t *testing.T) {
	for _, input := range []string{"model: custom", "triggers: {mentions: {scope: wrong}}", "workflows: [{name: incomplete}]", "[not, a, mapping]", "triggers: [", strings.Repeat(" ", 131073)} {
		t.Run(input[:min(len(input), 30)], func(t *testing.T) {
			cmd := crewCmd(watch.Deps{})
			cmd.SetIn(strings.NewReader(input))
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs([]string{"settings"})
			require.Error(t, cmd.Execute())
		})
	}
}

func TestCrewSettingsShowsEffectiveTriggerDefaults(t *testing.T) {
	cmd := crewCmd(watch.Deps{})
	var out bytes.Buffer
	cmd.SetIn(strings.NewReader("{}"))
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"settings"})
	require.NoError(t, cmd.Execute())
	var response struct {
		Settings struct {
			Triggers map[string]map[string]any `json:"triggers"`
		} `json:"settings"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &response))
	for _, key := range []string{"own_prs", "opt_in", "review_replies", "mentions"} {
		require.Equal(t, true, response.Settings.Triggers[key]["enabled"])
	}
	require.Equal(t, "eyes", response.Settings.Triggers["opt_in"]["reaction"])
}

func TestCrewImportResolvesRepoOverridesAndRequiresOwnershipChoice(t *testing.T) {
	source := `agent: claude
launcher: tmux
triggers:
  opt_in: {reaction: heart}
overrides:
  - match: [{repo: "o/r"}]
    triggers: {mentions: {enabled: false}}
  - match: [{repo: "o/r", prs: own}]
    prompts: {extra: "Own PR instructions"}
`
	run := func(repo, profile string) (map[string]any, error) {
		cmd := crewCmd(watch.Deps{})
		var out bytes.Buffer
		cmd.SetIn(strings.NewReader(source))
		cmd.SetOut(&out)
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs([]string{"import-settings", "--repo", repo, "--profile", profile})
		err := cmd.Execute()
		var result map[string]any
		if err == nil {
			require.NoError(t, json.Unmarshal(out.Bytes(), &result))
		}
		return result, err
	}
	_, err := run("o/r", "common")
	require.ErrorContains(t, err, "different own-PR and other-PR settings")
	result, err := run("o/r", "own")
	require.NoError(t, err)
	settings := result["settings"].(map[string]any)
	require.NotContains(t, settings, "agent")
	require.NotContains(t, settings, "launcher")
	triggers := settings["triggers"].(map[string]any)
	require.Equal(t, "heart", triggers["opt_in"].(map[string]any)["reaction"])
	require.Equal(t, false, triggers["mentions"].(map[string]any)["enabled"])
	require.Equal(t, "Own PR instructions", settings["prompts"].(map[string]any)["extra"])
	require.Contains(t, result["skipped"], "agent")
	result, err = run("o/other", "common")
	require.NoError(t, err)
	require.Equal(t, true, result["settings"].(map[string]any)["triggers"].(map[string]any)["mentions"].(map[string]any)["enabled"])
}

func TestCrewDefaultConfigUsesCLILocationsWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv(config.EnvVar, "")
	t.Setenv(config.LegacyEnvVar, "")
	read := func() map[string]any {
		t.Helper()
		cmd := crewCmd(watch.Deps{})
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs([]string{"default-config"})
		require.NoError(t, cmd.Execute())
		var response map[string]any
		require.NoError(t, json.Unmarshal(out.Bytes(), &response))
		return response
	}
	missing := read()
	require.Equal(t, false, missing["found"])
	require.Equal(t, config.DefaultPath(), missing["path"])
	require.NoFileExists(t, config.DefaultPath())
	require.NoError(t, os.MkdirAll(filepath.Dir(config.DefaultPath()), 0o700))
	require.NoError(t, os.WriteFile(config.DefaultPath(), []byte("# my config\ntriggers: {}\n"), 0o600))
	require.Equal(t, "# my config\ntriggers: {}\n", read()["content"])
	legacy := filepath.Join(dir, "legacy.yaml")
	require.NoError(t, os.WriteFile(legacy, []byte("# legacy"), 0o600))
	t.Setenv(config.LegacyEnvVar, legacy)
	require.Equal(t, legacy, read()["path"])
	explicit := filepath.Join(dir, "custom.yaml")
	require.NoError(t, os.WriteFile(explicit, []byte("# custom"), 0o600))
	t.Setenv(config.EnvVar, explicit)
	result := read()
	require.Equal(t, explicit, result["path"])
	require.Equal(t, "# custom", result["content"])
	contents, err := os.ReadFile(config.DefaultPath())
	require.NoError(t, err)
	require.Equal(t, "# my config\ntriggers: {}\n", string(contents))
}
