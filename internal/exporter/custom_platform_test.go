package exporter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"varkiv/internal/catalog"
)

func TestESDEExportUsesCustomFrontendSystemDirectory(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	romData := []byte("fixture-handheld-rom")
	if err := os.MkdirAll(filepath.Join(root, "fixture-handheld"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "fixture-handheld", "demo.opk"), romData, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err = store.CreateCustomPlatform(ctx, catalog.NewCustomPlatform{ID: "fixture-handheld", Name: "Fixture Handheld", Category: "handheld", Extensions: []string{".opk"}, ESDESystems: []string{"fixture-handheld-es"}}); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(romData)
	if _, _, err = store.ImportGame(ctx, catalog.ImportedGame{Platform: "fixture-handheld", DefaultTitle: "Demo", EditionTitle: "Demo", EditionType: "original", Artifacts: []catalog.NewArtifact{{Path: "fixture-handheld/demo.opk", Role: "rom", Size: int64(len(romData)), SHA256: hex.EncodeToString(digest[:])}}}); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if count, exportErr := ExportESDE(ctx, store, root, out, "en"); exportErr != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, exportErr)
	}
	if _, err = os.Stat(filepath.Join(out, "gamelists", "fixture-handheld-es", "gamelist.xml")); err != nil {
		t.Fatalf("custom ES-DE directory missing: %v", err)
	}
	if _, err = os.Stat(filepath.Join(out, "gamelists", "fixture-handheld", "gamelist.xml")); !os.IsNotExist(err) {
		t.Fatalf("canonical platform directory should not shadow custom ES-DE system: %v", err)
	}
}
