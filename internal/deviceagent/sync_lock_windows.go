//go:build windows

package deviceagent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func acquireSyncLock(configPath string) (func(), error) {
	absolute, err := filepath.Abs(configPath)
	if err != nil {
		return nil, err
	}
	lockPath := absolute + ".sync.lock"
	if info, statErr := os.Lstat(lockPath); statErr == nil && !info.Mode().IsRegular() {
		return nil, errors.New("agent sync lock must be an exact regular file")
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return nil, statErr
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open agent sync lock: %w", err)
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		if err != nil {
			return nil, fmt.Errorf("inspect agent sync lock: %w", err)
		}
		return nil, errors.New("agent sync lock must be an exact regular file")
	}
	var overlapped windows.Overlapped
	err = windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
	if err != nil {
		_ = file.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrSyncInProgress
		}
		return nil, fmt.Errorf("lock agent sync: %w", err)
	}
	return func() {
		_ = windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped)
		_ = file.Close()
	}, nil
}
