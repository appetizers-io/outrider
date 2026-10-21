//go:build !windows

package crew

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func scanLock(dir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, "scan.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("another crew scan is running: %w", err)
	}
	return func() { _ = f.Close() }, nil
}

func syncDirectory(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}
