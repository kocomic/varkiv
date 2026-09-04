package importer

import (
	"os"
	"path/filepath"
	"testing"

	"varkiv/internal/platforms"
)

func TestESDERuntimeHintsResolveCustomSystemAlias(t *testing.T) {
	root := t.TempDir()
	romDir := filepath.Join(root, "roms")
	if err := os.MkdirAll(romDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(romDir, "Demo.opk"), []byte("custom-rom"), 0o600); err != nil {
		t.Fatal(err)
	}
	gamelist := filepath.Join(root, "gamelist.xml")
	if err := os.WriteFile(gamelist, []byte(`<gameList><game><path>./roms/Demo.opk</path><name>Demo</name></game></gameList>`), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := filepath.Join(root, "es_systems.xml")
	if err := os.WriteFile(runtime, []byte(`<systemList><system><name>fixture-handheld-es</name><command label="Runner">runner %ROM%</command></system></systemList>`), 0o600); err != nil {
		t.Fatal(err)
	}
	items := platforms.All()
	items = append(items, platforms.Platform{ID: "fixture-handheld", Name: "Fixture Handheld", Vendor: "Custom", Category: "handheld", Aliases: []string{"fixture-hh"}, Extensions: []string{".opk"}, ESDESystems: []string{"fixture-handheld-es"}, BIOS: "varies", Runtime: "native", SuggestedEmulators: map[string][]string{}, Enabled: true})
	registry, err := platforms.NewRegistry(items)
	if err != nil {
		t.Fatal(err)
	}
	games, err := PreviewESDEWithRuntimeRegistry(root, gamelist, runtime, "fixture-handheld", "en", registry)
	if err != nil || len(games) != 1 || games[0].Platform != "fixture-handheld" || len(games[0].RuntimeHints) != 1 || games[0].RuntimeHints[0].SourceKind != "esde-system" {
		t.Fatalf("games=%#v err=%v", games, err)
	}
}
