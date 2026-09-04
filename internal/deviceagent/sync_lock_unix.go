//go:build !windows

package deviceagent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func acquireSyncLock(configPath string) (func(), error) {
	absolute, err := filepath.Abs(configPath)
	if err != nil {
		return nil, err
	}
	lockPath := absolute + ".sync.lock"
	fd, err := unix.Open(lockPath, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open agent sync lock: %w", err)
	}
	file := os.NewFile(uintptr(fd), lockPath)
	closeOnError := func(source error) (func(), error) {
		_ = file.Close()
		return nil, source
	}
	info, err := file.Stat()
	if err != nil {
		return closeOnError(fmt.Errorf("inspect agent sync lock: %w", err))
	}
	if !info.Mode().IsRegular() {
		return closeOnError(errors.New("agent sync lock must be an exact regular file"))
	}
	if err = file.Chmod(0o600); err != nil {
		return closeOnError(fmt.Errorf("secure agent sync lock: %w", err))
	}
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return closeOnError(ErrSyncInProgress)
		}
		return closeOnError(fmt.Errorf("lock agent sync: %w", err))
	}
	return func() {
		_ = unix.Flock(fd, unix.LOCK_UN)
		_ = file.Close()
	}, nil
}
