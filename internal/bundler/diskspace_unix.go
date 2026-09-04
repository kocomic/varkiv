//go:build !windows

package bundler

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func filesystemAvailableBytes(path string) (uint64, error) {
	existing, err := nearestExistingDirectory(path)
	if err != nil {
		return 0, err
	}
	var stats syscall.Statfs_t
	if err = syscall.Statfs(existing, &stats); err != nil {
		return 0, err
	}
	if stats.Bsize <= 0 {
		return 0, fmt.Errorf("filesystem returned an invalid block size")
	}
	return uint64(stats.Bavail) * uint64(stats.Bsize), nil
}

func nearestExistingDirectory(path string) (string, error) {
	current := filepath.Clean(path)
	for {
		info, err := os.Stat(current)
		if err == nil {
			if !info.IsDir() {
				return "", fmt.Errorf("%s is not a directory", current)
			}
			return current, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no existing ancestor for %s", path)
		}
		current = parent
	}
}
