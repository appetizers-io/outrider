package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsolationValidation(t *testing.T) {
	for _, tc := range []struct{ name, text, want string }{
		{"default", "", ""},
		{"opt-in", "mode: autonomous\nisolation: {enabled: true}\n", ""},
		{"supervised", "isolation: {enabled: true}\n", "requires mode: autonomous"},
		{"dialog", "mode: autonomous\npush: ask\nisolation: {enabled: true}\n", "does not support host approval dialogs"},
		{"posts", "mode: autonomous\ngithub_writes: ask\nisolation: {enabled: true}\n", "does not support host approval dialogs"},
		{"read-only", "mode: autonomous\nsandbox: read-only\nisolation: {enabled: true}\n", "conflicts with sandbox: read-only"},
		{"relative mount", "mode: autonomous\nisolation: {enabled: true, read_only: [relative]}\n", "absolute paths"},
		{"relative guard", "mode: autonomous\nisolation: {enabled: true, guard_binary: relative}\n", "absolute path"},
		{"override", "mode: autonomous\noverrides: [{match: [{repo: org/repo}], isolation: {enabled: true}}]\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.text), "test.yaml")
			if tc.want == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.want)
			}
		})
	}
}
