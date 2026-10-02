package session

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/appetizers-io/outrider/internal/proc"
)

// LaunchGrace is how long a session's runner may take to stamp its pid.
const LaunchGrace = 60 * time.Second

// LockMeta is the content of a session lock.
type LockMeta struct {
	Repo    string  `json:"repo"`
	PR      int     `json:"pr"`
	Started float64 `json:"started"` // unix seconds
	Tmux    *string `json:"tmux"`
	PID     *int    `json:"pid"` // null until the runner started
}

// lockPath is the lock of a PR's session.
func lockPath(root, repo string, n int) string {
	return filepath.Join(root, "locks", strings.ReplaceAll(repo, "/", "__")+"__"+itoa(n)+".lock")
}

// Locks are the locks of live sessions; stale ones (dead runner, runner
// that never started, gone tmux session, older than staleHours) are removed.
func Locks(ctx context.Context, root string, staleHours float64, run proc.Runner, log *slog.Logger) []string {
	dir := filepath.Join(root, "locks")
	_ = os.MkdirAll(dir, 0o700)
	paths, _ := filepath.Glob(filepath.Join(dir, "*.lock"))
	cutoff := time.Now().Add(-time.Duration(staleHours * float64(time.Hour)))
	var out []string
	for _, p := range paths {
		var meta map[string]any
		if raw, err := os.ReadFile(p); err == nil {
			_ = json.Unmarshal(raw, &meta)
		}
		gone := false
		if tmux, _ := meta["tmux"].(string); tmux != "" {
			_, err := run(ctx, proc.Cmd{Args: []string{"tmux", "has-session", "-t", "=" + tmux}})
			gone = err != nil
		}
		pid, isNum := meta["pid"].(float64)
		_, hasPID := meta["pid"]
		switch {
		case isNum:
			gone = gone || !alive(int(pid))
		case hasPID: // null: the runner never stamped it (locks from before pid stamping have no key)
			started, _ := meta["started"].(float64)
			gone = gone || time.Since(time.Unix(int64(started), 0)) > LaunchGrace
		}
		st, err := os.Stat(p)
		if gone || (err == nil && st.ModTime().Before(cutoff)) {
			log.Info("removing stale lock " + filepath.Base(p))
			_ = os.Remove(p)
			continue
		}
		out = append(out, p)
	}
	return out
}
