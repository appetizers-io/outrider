// Package golden compares test output with files under testdata/golden.
// Rewrite them with `go test <packages> -update` (or `task golden`) and review
// the diff.
package golden

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/golden")

// Dir is testdata/golden, seen from a package two levels below the repo root.
const Dir = "../../testdata/golden"

// Check compares got with the golden file name (relative to Dir), or writes
// it with -update.
func Check(t testing.TB, name, got string) {
	t.Helper()
	path := filepath.Join(Dir, filepath.FromSlash(name))
	if *update {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(got), 0o600))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "missing golden file; run task golden")
	require.Equal(t, string(want), got, "%s differs; run task golden and review the diff", name)
}
