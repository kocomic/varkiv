package catalog

import "testing"

func TestSelectLaunchArtifactNeverUsesAuxiliaryResources(t *testing.T) {
	artifacts := []Artifact{
		{Path: "patches/translation.ips", Role: "patch"},
		{Path: "updates/update.pkg", Role: "update"},
		{Path: "discs/disc-2.bin", Role: "disc", DiscIndex: 2},
		{Path: "game/game.m3u", Role: "rom"},
		{Path: "game/game.exe", Role: "executable"},
	}
	selected := SelectLaunchArtifact(artifacts)
	if selected == nil || selected.Path != "game/game.m3u" {
		t.Fatalf("selected=%#v", selected)
	}
	artifacts[3].Missing = true
	selected = SelectLaunchArtifact(artifacts)
	if selected == nil || selected.Role != "executable" {
		t.Fatalf("fallback selected=%#v", selected)
	}
	artifacts[4].Missing = true
	selected = SelectLaunchArtifact(artifacts)
	if selected == nil || selected.DiscIndex != 2 {
		t.Fatalf("disc fallback selected=%#v", selected)
	}
}

func TestSelectLaunchArtifactRejectsAuxiliaryOnlyEdition(t *testing.T) {
	if selected := SelectLaunchArtifact([]Artifact{{Path: "patch.ips", Role: "patch"}, {Path: "dlc.pkg", Role: "dlc"}}); selected != nil {
		t.Fatalf("auxiliary resource became launch target: %#v", selected)
	}
}
