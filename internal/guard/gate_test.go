package guard

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGateNeverOverridesPermissionsWithAllow(t *testing.T) {
	for _, decision := range []string{"allow", "deny", "ask"} {
		raw, err := gateOutput([]byte(`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"` + decision + `","permissionDecisionReason":"test"}}`))
		require.NoError(t, err)
		var result map[string]any
		require.NoError(t, json.Unmarshal(raw, &result))
		hook := result["hookSpecificOutput"].(map[string]any)
		if decision == "allow" {
			require.NotContains(t, hook, "permissionDecision")
		} else {
			require.Equal(t, decision, hook["permissionDecision"])
		}
	}
	_, err := gateOutput([]byte("broken response"))
	require.Error(t, err)
}
