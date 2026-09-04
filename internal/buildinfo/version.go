package buildinfo

import (
	_ "embed"
	"strings"
)

// rawVersion is the single runtime version source shared with the Android
// build and release checks. Keeping it embedded makes cross-compiled agents
// self-identifying without depending on a file beside the binary.
//
//go:embed VERSION
var rawVersion string

var Version = strings.TrimSpace(rawVersion)
