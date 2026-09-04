package buildinfo

import (
	"regexp"
	"testing"
)

func TestVersionIsCanonicalPreviewSemver(t *testing.T) {
	if !regexp.MustCompile(`^0\.[0-9]+\.[0-9]+-preview\.[0-9]+$`).MatchString(Version) {
		t.Fatalf("invalid embedded version %q", Version)
	}
}
