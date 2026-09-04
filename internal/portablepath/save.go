package portablepath

import (
	"errors"
	"path"
	"strings"
	"unicode/utf8"
)

const (
	maxSaveLogicalBytes  = 1024
	MaxSaveRevisionFiles = 4096
	MaxSaveRevisionBytes = int64(240 << 20)
)

var errUnsafeSaveLogicalPath = errors.New("save logical path is not portable")

// CleanSaveLogical validates a revision path independently of the host OS.
// A path accepted on Linux must remain relative and creatable on Windows,
// Android SAF, and handheld filesystems when that revision is restored later.
func CleanSaveLogical(value string) (string, error) {
	if value == "" || value != strings.TrimSpace(value) || len(value) > maxSaveLogicalBytes || !utf8.ValidString(value) {
		return "", errUnsafeSaveLogicalPath
	}
	normalized := strings.ReplaceAll(value, `\`, "/")
	if strings.HasPrefix(normalized, "/") || strings.HasSuffix(normalized, "/") || strings.Contains(normalized, "//") || path.Clean(normalized) != normalized {
		return "", errUnsafeSaveLogicalPath
	}
	for _, part := range strings.Split(normalized, "/") {
		if part == "" || part == "." || part == ".." || len([]byte(part)) > 255 || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || strings.ContainsAny(part, `<>:"|?*`) || windowsReserved(part) {
			return "", errUnsafeSaveLogicalPath
		}
		for _, character := range part {
			if character < 0x20 || character == 0x7f {
				return "", errUnsafeSaveLogicalPath
			}
		}
	}
	return normalized, nil
}

func windowsReserved(value string) bool {
	base := strings.ToUpper(strings.TrimSuffix(value, path.Ext(value)))
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" {
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) {
		return base[3] >= '1' && base[3] <= '9'
	}
	return false
}
