package guard

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// RunDraft saves local Markdown without granting general Edit/Write permission.
// It has no target argument: policy fixes the session and filename.
func RunDraft(stdin io.Reader, stderr io.Writer) int {
	self, err := os.Executable()
	if err == nil {
		err = saveDraft(self, stdin)
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "outrider draft:", err)
		return 1
	}
	return 0
}

func saveDraft(self string, stdin io.Reader) error {
	p, err := loadPolicy(self)
	if err != nil {
		return err
	}
	dir := filepath.Clean(filepath.Join(filepath.Dir(self), ".."))
	if p.Review == nil || p.Review.Comments != "draft" || filepath.Clean(p.Review.Outbox) != filepath.Join(dir, "outbox") {
		return fmt.Errorf("this session has no writable draft outbox")
	}
	raw, err := io.ReadAll(io.LimitReader(stdin, (1<<20)+1))
	if err != nil {
		return err
	}
	if len(raw) > 1<<20 {
		return fmt.Errorf("draft exceeds 1 MiB")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat("outbox")
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("outbox must be a real directory")
	}
	outbox, err := root.OpenRoot("outbox")
	if err != nil {
		return err
	}
	defer func() { _ = outbox.Close() }()
	if info, err := outbox.Lstat("comments.md"); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("draft target must not be a symlink")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Rename a new file instead of truncating the destination: even a raced
	// symlink or hard link cannot make this writer overwrite session metadata.
	temp := ".comments-" + rand.Text() + ".tmp"
	file, err := outbox.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = outbox.Remove(temp) }()
	_, err = file.Write(raw)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return outbox.Rename(temp, "comments.md")
}
