//go:build !windows

package deviceagent

import "os"

func secureConfigFile(path string) error { return os.Chmod(path, 0o600) }
