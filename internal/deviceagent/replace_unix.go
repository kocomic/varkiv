//go:build !windows

package deviceagent

import "os"

func replaceFile(source, target string) error { return os.Rename(source, target) }
