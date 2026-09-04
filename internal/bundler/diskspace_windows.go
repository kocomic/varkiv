//go:build windows

package bundler

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

var getDiskFreeSpaceExW = syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")

func filesystemAvailableBytes(path string) (uint64, error) {
	existing, err := nearestExistingDirectory(path)
	if err != nil {
		return 0, err
	}
	pointer, err := syscall.UTF16PtrFromString(existing)
	if err != nil {
		return 0, err
	}
	var available, total, totalFree uint64
	result, _, callErr := getDiskFreeSpaceExW.Call(
		uintptr(unsafe.Pointer(pointer)),
		uintptr(unsafe.Pointer(&available)),
		uintptr(unsafe.Pointer(&total)),
		uintptr(unsafe.Pointer(&totalFree)),
	)
	if result == 0 {
		return 0, callErr
	}
	return available, nil
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
