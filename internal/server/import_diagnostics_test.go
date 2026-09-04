package server

import (
	"testing"

	"varkiv/internal/platforms"
)

func TestContainerMatchesPlatformUsesExactCollectionRules(t *testing.T) {
	registry, err := platforms.NewRegistry(platforms.All())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		platform string
		match    bool
	}{
		{name: "POKE MINI.7z.tkzlm", platform: "pokemini", match: true},
		{name: "FC hack.7z.tkzlm", platform: "nes", match: true},
		{name: "GBA.zip.001", platform: "gba", match: true},
		{name: "GBA.zip.001", platform: "gb", match: false},
		{name: "private-platform.7z.tkzlm", platform: "pokemini", match: false},
	} {
		if got := containerMatchesPlatform(registry, test.platform, test.name); got != test.match {
			t.Errorf("containerMatchesPlatform(%q, %q) = %v, want %v", test.platform, test.name, got, test.match)
		}
	}
}
