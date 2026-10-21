package crew

import (
	"fmt"
	"os"
	"path/filepath"
)

// Crew's app currently targets macOS/Linux; standalone scans also work on Windows.
func scanLock(dir string) (func(), error) {
	path := filepath.Join(dir, "scan.lock")
	if err := os.Mkdir(path, 0o700); err != nil {
		return nil, fmt.Errorf("another scan is running (after a crash, remove %s): %w", path, err)
	}
	return func() { _ = os.Remove(path) }, nil
}
