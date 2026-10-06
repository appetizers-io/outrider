package guard

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/appetizers-io/outrider/internal/approve"

	"github.com/stretchr/testify/require"
)

func TestMissingOrInvalidPolicyFailsClosed(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "bin", "git")
	_, err := loadPolicy(self)
	require.ErrorContains(t, err, "session policy")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "policy.json"), []byte(`{"repo":"o/r","pr":7}`), 0o600))
	_, err = loadPolicy(self)
	require.ErrorContains(t, err, "incomplete")
}

func TestApprovalRestoresWatcherDisplay(t *testing.T) {
	t.Setenv("DISPLAY", ":attacker")
	t.Setenv("XAUTHORITY", "/attacker")
	ask := trustedAsk(func(_, _, _ string) (approve.Decision, error) {
		require.Equal(t, ":owner", os.Getenv("DISPLAY"))
		require.Equal(t, "/owner", os.Getenv("XAUTHORITY"))
		return approve.Approved, nil
	}, map[string]string{"DISPLAY": ":owner", "XAUTHORITY": "/owner"})
	decision, err := ask("title", "body", "Push")
	require.NoError(t, err)
	require.Equal(t, approve.Approved, decision)
}
