package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"varkiv/internal/catalog"
)

func TestCustomPlatformExtensionAndAliasDriveDiscovery(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	roms := filepath.Join(root, "roms")
	if err := os.MkdirAll(roms, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(roms, "Demo.opk"), []byte("custom-rom"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(roms, "Ignored.gba"), []byte("other-rom"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err = store.CreateCustomPlatform(ctx, catalog.NewCustomPlatform{ID: "fixture-handheld", Name: "Fixture Handheld", Category: "handheld", Aliases: []string{"fixture-hh"}, Extensions: []string{".opk"}}); err != nil {
		t.Fatal(err)
	}
	registry, err := store.PlatformRegistry(ctx)
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := DiscoverWithRegistry(ctx, store, root, roms, "fixture-hh", registry)
	if err != nil || len(candidates) != 1 || candidates[0].Game.Platform != "fixture-handheld" || candidates[0].Game.DefaultTitle != "Demo" {
		t.Fatalf("candidates=%#v err=%v", candidates, err)
	}
}
