//go:build !windows

package bundler

import "os"

// replaceFile relies on same-directory POSIX rename replacement. It never
// pre-deletes the existing target; a failed rename leaves that target intact.
func replaceFile(source, target string) error {
	return os.Rename(source, target)
}
