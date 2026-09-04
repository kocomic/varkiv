package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"varkiv/internal/catalog"
)

func TestHashPackCLIExportPreviewImport(t *testing.T) {
	ctx := context.Background()
	sourceDB := filepath.Join(t.TempDir(), "source.db")
	store, err := catalog.Open(sourceDB)
	if err != nil {
		t.Fatal(err)
	}
	game, err := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "CLI Fixture", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	edition, err := store.AddEdition(ctx, catalog.NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddArtifact(ctx, catalog.NewArtifact{EditionID: edition.ID, Path: "private/fixture.gba", Role: "rom", Size: 2048, SHA256: strings.Repeat("e", 64)}); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	packPath := filepath.Join(t.TempDir(), "fixture.hashpack")
	if err = exportHashPack([]string{"--db", sourceDB, "--out", packPath, "--source-id", "cli.fixture", "--name", "CLI Fixture", "--license", "CC0-1.0", "--release", "1"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(packPath)
	if err != nil || info.Size() == 0 || info.Mode().Perm() != 0o600 {
		t.Fatalf("pack output=%#v err=%v", info, err)
	}
	destinationDB := filepath.Join(t.TempDir(), "destination.db")
	destination, err := catalog.Open(destinationDB)
	if err != nil {
		t.Fatal(err)
	}
	_ = destination.Close()
	if err = previewHashPack([]string{"--db", destinationDB, "--from", packPath}); err != nil {
		t.Fatal(err)
	}
	if err = importHashPack([]string{"--db", destinationDB, "--from", packPath}); err != nil {
		t.Fatal(err)
	}
	verified, err := catalog.Open(destinationDB)
	if err != nil {
		t.Fatal(err)
	}
	defer verified.Close()
	sources, err := verified.ListHashSources(ctx)
	if err != nil || len(sources) != 1 || sources[0].RecordCount != 1 {
		t.Fatalf("imported sources=%#v err=%v", sources, err)
	}
	games, err := verified.ListGames(ctx, "")
	if err != nil || len(games) != 0 {
		t.Fatalf("hash-only CLI import created games=%#v err=%v", games, err)
	}
}
