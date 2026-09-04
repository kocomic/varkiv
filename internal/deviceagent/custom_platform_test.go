package deviceagent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"varkiv/internal/platforms"
)

func TestDeviceInventoryUsesServerCustomPlatformRegistry(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Demo.opk"), []byte("custom-rom"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Ignored.gba"), []byte("other-rom"), 0o600); err != nil {
		t.Fatal(err)
	}
	items := platforms.All()
	items = append(items, platforms.Platform{ID: "fixture-handheld", Name: "Fixture Handheld", Vendor: "Custom", Category: "handheld", Aliases: []string{"fixture-hh"}, Extensions: []string{".opk"}, BIOS: "varies", Runtime: "native", SuggestedEmulators: map[string][]string{}, Enabled: true})
	registry, err := platforms.NewRegistry(items)
	if err != nil {
		t.Fatal(err)
	}
	config := Config{ROMRoots: map[string]string{"fixture-hh": root}, ROMCache: map[string]ROMCacheEntry{}}
	inventory, names, err := enumerateROMInventoryWithRegistry(context.Background(), &config, registry)
	if err != nil || len(inventory) != 1 || inventory[0].PlatformID != "fixture-handheld" || len(names) != 1 || names[0].Stem != "Demo" {
		t.Fatalf("inventory=%#v names=%#v err=%v", inventory, names, err)
	}
}
