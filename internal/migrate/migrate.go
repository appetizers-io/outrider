// Package migrate moves the directories of llm-review-agent, outrider's old
// name, to the new name once.
package migrate

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const oldName, newName = "llm-review-agent", "outrider"

// Move is an old llm-review-agent directory and its outrider name.
type Move struct{ Old, New string }

// Moves are the config dir (under configBase: $XDG_CONFIG_HOME or
// ~/.config), ~/.cache and ~/.local/state, old and new.
func Moves(home, configBase string) []Move {
	var out []Move
	for _, base := range []string{configBase, filepath.Join(home, ".cache"), filepath.Join(home, ".local", "state")} {
		out = append(out, Move{filepath.Join(base, oldName), filepath.Join(base, newName)})
	}
	return out
}

// Dirs moves the Moves, each only when the new one doesn't exist yet and the
// old one does.
func Dirs(home, configBase string, log *slog.Logger) {
	for _, m := range Moves(home, configBase) {
		old, cur := m.Old, m.New
		if _, err := os.Stat(cur); err == nil {
			continue
		}
		if _, err := os.Stat(old); err != nil {
			continue
		}
		// git may have recorded the worktrees through a resolved symlink
		// (e.g. /var -> /private/var on macOS)
		oldReal, err := filepath.EvalSymlinks(old)
		if err != nil {
			oldReal = old
		}
		if err := os.Rename(old, cur); err != nil {
			log.Warn(fmt.Sprintf("could not move %s to %s: %v", old, cur, err))
			continue
		}
		log.Info(fmt.Sprintf("moved %s to %s (renamed to outrider)", old, cur))
		repairWorktrees([]string{old, oldReal}, cur, log)
	}
}

// repairWorktrees re-links the PR worktrees in a moved cache: git records
// them by absolute path, also for the clones that moved along.
func repairWorktrees(olds []string, cur string, log *slog.Logger) {
	dotgits, _ := filepath.Glob(filepath.Join(cur, "worktrees", "*", "*", ".git"))
	for _, dotgit := range dotgits {
		raw, err := os.ReadFile(dotgit)
		if err != nil {
			continue // a directory: not a linked worktree
		}
		admin := strings.TrimSpace(strings.TrimPrefix(string(raw), "gitdir:"))
		admin = filepath.ToSlash(admin)
		for _, old := range olds {
			if oldSlash := filepath.ToSlash(old); strings.HasPrefix(admin, oldSlash+"/") {
				admin = filepath.ToSlash(cur) + strings.TrimPrefix(admin, oldSlash) // a cached clone moved too
				break
			}
		}
		i := strings.LastIndex(admin, "/worktrees/")
		if i < 0 {
			continue
		}
		main, wt := filepath.Dir(filepath.FromSlash(admin[:i])), filepath.Dir(dotgit)
		if out, err := exec.Command("git", "-C", main, "worktree", "repair", wt).CombinedOutput(); err != nil {
			log.Warn(fmt.Sprintf("could not re-link worktree %s: %v: %s", wt, err, strings.TrimSpace(string(out))))
		}
	}
}
