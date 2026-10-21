package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

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
