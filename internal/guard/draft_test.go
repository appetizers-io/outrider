package guard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDraftWriterCannotEscapeOutboxOrPost(t *testing.T) {
	dir := t.TempDir()
	outbox := filepath.Join(dir, "outbox")
	require.NoError(t, os.Mkdir(outbox, 0o700))
	self := filepath.Join(dir, "bin", "outrider-draft")
	policy := Policy{Repo: "o/r", PR: 7, GitHubWrites: "never", Guard: Runtime{Git: self, GH: self}, Review: &DraftPolicy{Comments: "draft", Outbox: outbox}}
	write := func() {
		raw, err := json.Marshal(policy)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "policy.json"), raw, 0o600))
	}
	write()
	require.NoError(t, saveDraft(self, strings.NewReader("local draft")))
	raw, err := os.ReadFile(filepath.Join(outbox, "comments.md"))
	require.NoError(t, err)
	require.Equal(t, "local draft", string(raw))
	require.ErrorContains(t, saveDraft(self, strings.NewReader(strings.Repeat("x", (1<<20)+1))), "exceeds")
	policy.Review.Outbox = t.TempDir()
	write()
	require.ErrorContains(t, saveDraft(self, strings.NewReader("escape")), "no writable")
	policy.Review.Outbox = outbox
	write()
	// A hard link to policy is regular, but atomic replacement must leave the
	// linked metadata unchanged instead of truncating it.
	require.NoError(t, os.Remove(filepath.Join(outbox, "comments.md")))
	require.NoError(t, os.Link(filepath.Join(dir, "policy.json"), filepath.Join(outbox, "comments.md")))
	before, err := os.ReadFile(filepath.Join(dir, "policy.json"))
	require.NoError(t, err)
	require.NoError(t, saveDraft(self, strings.NewReader("replacement draft")))
	after, err := os.ReadFile(filepath.Join(dir, "policy.json"))
	require.NoError(t, err)
	require.Equal(t, before, after)
	if runtime.GOOS != "windows" {
		require.NoError(t, os.Remove(filepath.Join(outbox, "comments.md")))
		outside := filepath.Join(t.TempDir(), "secret")
		require.NoError(t, os.WriteFile(outside, []byte("unchanged"), 0o600))
		require.NoError(t, os.Symlink(outside, filepath.Join(outbox, "comments.md")))
		require.Error(t, saveDraft(self, strings.NewReader("escape")))
		raw, err = os.ReadFile(outside)
		require.NoError(t, err)
		require.Equal(t, "unchanged", string(raw))
	}
}
