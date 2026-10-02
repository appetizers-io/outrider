package migrate

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	require.NoError(t, err, string(out))
	return string(out)
}

// messages are the msg fields of JSON log lines.
func messages(t *testing.T, log string) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(log), "\n") {
		var rec struct{ Msg string }
		require.NoError(t, json.Unmarshal([]byte(line), &rec), line)
		out = append(out, rec.Msg)
	}
	return out
}

func TestDirsMovesOldDirsOnce(t *testing.T) {
	r := require.New(t)
	home := t.TempDir()
	config := filepath.Join(home, ".config")
	oldConfig := filepath.Join(config, "llm-review-agent", "config.yaml")
	oldState := filepath.Join(home, ".local", "state", "llm-review-agent", "state.json")
	for _, f := range []string{oldConfig, oldState} {
		r.NoError(os.MkdirAll(filepath.Dir(f), 0o700))
		r.NoError(os.WriteFile(f, []byte("x"), 0o600))
	}
	// a cached clone with a PR worktree, as the launcher makes them
	clone := filepath.Join(home, ".cache", "llm-review-agent", "repos", "o__r")
	wt := filepath.Join(home, ".cache", "llm-review-agent", "worktrees", "o__r", "pr-1")
	r.NoError(os.MkdirAll(clone, 0o700))
	git(t, clone, "init", "-q")
	git(t, clone, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "x")
	r.NoError(os.MkdirAll(filepath.Dir(wt), 0o700))
	git(t, clone, "worktree", "add", "-q", "--detach", wt)

	var log bytes.Buffer
	Dirs(home, config, slog.New(slog.NewJSONHandler(&log, nil)))

	r.FileExists(filepath.Join(config, "outrider", "config.yaml"))
	r.FileExists(filepath.Join(home, ".local", "state", "outrider", "state.json"))
	r.NoDirExists(filepath.Join(config, "llm-review-agent"))
	r.NoDirExists(filepath.Join(home, ".cache", "llm-review-agent"))
	r.Contains(messages(t, log.String()), "moved "+filepath.Join(config, "llm-review-agent")+" to "+filepath.Join(config, "outrider")+" (renamed to outrider)")
	// the moved worktree still works
	git(t, filepath.Join(home, ".cache", "outrider", "worktrees", "o__r", "pr-1"), "status", "--porcelain")

	// a second start changes nothing; an existing new dir wins over an old one
	r.NoError(os.MkdirAll(filepath.Join(config, "llm-review-agent"), 0o700))
	log.Reset()
	Dirs(home, config, slog.New(slog.NewJSONHandler(&log, nil)))
	r.Empty(log.String())
	r.DirExists(filepath.Join(config, "llm-review-agent"))
}
