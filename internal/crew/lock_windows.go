package crew

import (
	"fmt"
	"os"
	"path/filepath"
)

// Windows scans use a directory lock; an interrupted process can leave it behind.
func scanLock(dir string) (func(), error) {
	path := filepath.Join(dir, "scan.lock")
	if err := os.Mkdir(path, 0o700); err != nil {
		return nil, fmt.Errorf("another scan is running (after a crash, remove %s): %w", path, err)
	}
	return func() { _ = os.Remove(path) }, nil
}

// Windows does not support directory fsync via os.File.Sync. Snapshot files
// are flushed, but the standalone scanner cannot promise power-loss durability.
func syncDirectory(_ string) error { return nil }
