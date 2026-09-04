//go:build windows

package bundler

import "golang.org/x/sys/windows"

// replaceFile uses the native replace-existing operation and never implements
// replacement as delete-then-rename.
func replaceFile(source, target string) error {
	sourcePointer, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	targetPointer, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(sourcePointer, targetPointer, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
