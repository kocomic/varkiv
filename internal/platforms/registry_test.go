package platforms

import "testing"

func TestRegistryHasStableUniquePresets(t *testing.T) {
	items := All()
	if len(items) != 72 {
		t.Fatalf("expected the curated through-Switch catalog of 72 platforms, got %d", len(items))
	}
	seen := map[string]bool{}
	for _, item := range items {
		if item.ID == "" || item.Name == "" || item.NameZH == "" || seen[item.ID] {
			t.Fatalf("invalid or duplicate preset: %#v", item)
		}
		seen[item.ID] = true
		if len(item.Extensions) == 0 || len(item.ESDESystems) == 0 {
			t.Fatalf("preset lacks frontend/file mapping: %#v", item)
		}
		for _, target := range []string{"windows", "android", "handheld_linux"} {
			if _, ok := item.SuggestedEmulators[target]; !ok {
				t.Fatalf("preset lacks %s emulator decision: %#v", target, item)
			}
		}
	}
}

func TestCuratedThroughSwitchCoverage(t *testing.T) {
	for _, id := range []string{
		"amiga", "amigacd32", "amstradcpc", "apple2", "atari8bit", "atarist", "atomiswave",
		"c64", "cdi", "colecovision", "famicomdisk", "fmtowns", "intellivision", "jaguarcd",
		"msx", "msx2", "n64dd", "pc88", "pc98", "pico8", "sg1000", "supergrafx", "switch",
		"vectrex", "x68000", "xbox", "xbox360", "zxspectrum",
	} {
		if item, ok := Resolve(id); !ok || item.ID != id {
			t.Fatalf("required platform %q is missing: %#v, %v", id, item, ok)
		}
	}
	for _, beyondBoundary := range []string{"ps4", "ps5", "xboxone", "xboxseries"} {
		if item, ok := Resolve(beyondBoundary); ok {
			t.Fatalf("platform %q exceeds the curated Switch-era boundary: %#v", beyondBoundary, item)
		}
	}
}

func TestRegistryPreservesSuggestedEmulatorsWithoutSharingMutableMaps(t *testing.T) {
	first, ok := Resolve("ngpc")
	if !ok {
		t.Fatal("NGPC preset is missing")
	}
	for _, target := range []string{"windows", "android", "handheld_linux"} {
		if got := first.SuggestedEmulators[target]; len(got) != 1 || got[0] != "RetroArch · Beetle NeoPop" {
			t.Fatalf("NGPC %s suggestions = %#v", target, got)
		}
	}

	first.SuggestedEmulators["windows"][0] = "mutated"
	first.SuggestedEmulators["android"] = nil
	second, ok := Resolve("ngpc")
	if !ok || second.SuggestedEmulators["windows"][0] != "RetroArch · Beetle NeoPop" || len(second.SuggestedEmulators["android"]) != 1 {
		t.Fatalf("registry returned shared suggestion state: %#v", second.SuggestedEmulators)
	}
}

func TestSwitchSuggestionsStayInsideAuditedTargets(t *testing.T) {
	item, ok := Resolve("switch")
	if !ok {
		t.Fatal("Switch preset is missing")
	}
	for _, target := range []string{"windows", "handheld_linux"} {
		if got := item.SuggestedEmulators[target]; len(got) != 1 || got[0] != "Eden" {
			t.Fatalf("Switch %s suggestions = %#v", target, got)
		}
	}
	if got := item.SuggestedEmulators["android"]; len(got) != 0 {
		t.Fatalf("Switch Android suggestions must remain empty until audited: %#v", got)
	}
}

func TestAliasesAndNativeOnlyBoundaries(t *testing.T) {
	for alias, want := range map[string]string{"atari800": "atari8bit", "cdimono1": "cdi", "fc": "nes", "fds": "famicomdisk", "genesis": "megadrive", "ps1": "psx", "n3ds": "3ds", "sg-1000": "sg1000", "x360": "xbox360"} {
		item, ok := Resolve(alias)
		if !ok || item.ID != want {
			t.Fatalf("Resolve(%q) = %#v, %v", alias, item, ok)
		}
	}
	for _, id := range []string{"ps2", "ps3", "3ds", "switch", "xbox", "xbox360"} {
		item, ok := Resolve(id)
		if !ok || item.Runtime != "native" {
			t.Fatalf("%s must remain native-only: %#v", id, item)
		}
	}
}

func TestPegasusCollectionDirectoriesResolveWithoutCreatingPlatforms(t *testing.T) {
	for directory, want := range map[string]string{
		"FC hack":            "nes",
		"SFC-MSU1":           "snes",
		"MD hack(picodrive)": "megadrive",
		"DC hack":            "dreamcast",
		"PS2 hack":           "ps2",
		"WII Ware":           "wii",
		"FBNEO ACT V":        "arcade",
		"MAME FTG hack":      "arcade",
	} {
		item, ok := ResolveCollectionDirectory(directory)
		if !ok || item.ID != want {
			t.Fatalf("ResolveCollectionDirectory(%q) = %#v, %v", directory, item, ok)
		}
	}
}
