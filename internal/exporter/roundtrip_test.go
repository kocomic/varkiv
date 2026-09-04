package exporter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"varkiv/internal/catalog"
	"varkiv/internal/importer"
)

func newStore(t *testing.T) *catalog.Store {
	t.Helper()
	s, err := catalog.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPegasusRoundTripPreservesGroupAndEditions(t *testing.T) {
	ctx := context.Background()
	library := t.TempDir()
	if err := os.MkdirAll(filepath.Join(library, "snes"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"original.sfc", "zh.sfc"} {
		if err := os.WriteFile(filepath.Join(library, "snes", name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s1 := newStore(t)
	w, err := s1.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Final Fantasy VI", Platform: "snes", Titles: map[string]string{"zh-CN": "最终幻想 VI"}})
	if err != nil {
		t.Fatal(err)
	}
	for i, item := range []struct{ title, kind, path string }{{"Original", "original", "snes/original.sfc"}, {"Chinese v1.2", "translation", "snes/zh.sfc"}} {
		e, err := s1.AddEdition(ctx, catalog.NewEdition{GameID: w.ID, DefaultTitle: item.title, EditionType: item.kind, SortOrder: i})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s1.AddArtifact(ctx, catalog.NewArtifact{EditionID: e.ID, Path: item.path}); err != nil {
			t.Fatal(err)
		}
	}
	out := t.TempDir()
	if _, err = ExportPegasus(ctx, s1, library, out, "zh-CN"); err != nil {
		t.Fatal(err)
	}
	s2 := newStore(t)
	metadata := filepath.Join(out, "snes", "metadata.pegasus.txt")
	if _, err = importer.ImportPegasus(ctx, s2, library, metadata, "snes", "zh-CN"); err != nil {
		t.Fatal(err)
	}
	games, err := s2.ListGames(ctx, "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if len(games) != 1 {
		t.Fatalf("games=%d", len(games))
	}
	if len(games[0].Editions) != 2 {
		t.Fatalf("editions=%d", len(games[0].Editions))
	}
	if games[0].DefaultTitle != "Final Fantasy VI" {
		t.Fatalf("work title=%q", games[0].DefaultTitle)
	}
}

func TestESDEPortableRoundTripRestoresNeutralManifestMetadataMediaAndSeries(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	for path, body := range map[string]string{
		"gba/advance.gba": "gba-rom", "nds/advance.nds": "nds-rom",
		"gba/translation.ips":     "patch-data",
		"media/advance-cover.png": "cover", "media/advance-manual.pdf": "manual",
	} {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	source := newStore(t)
	gameGBA, err := source.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Advance Wars", Platform: "gba", Titles: map[string]string{"zh-CN": "高级战争"}})
	if err != nil {
		t.Fatal(err)
	}
	editionGBA, err := source.AddEdition(ctx, catalog.NewEdition{GameID: gameGBA.ID, DefaultTitle: "Chinese v1.2", EditionType: "translation", Version: "1.2", Languages: []string{"zh-CN", "en"}, Author: "Test team", Serial: "AGB-AW", ProductCode: "TEST-GBA", TitleID: "gba-title", Titles: map[string]string{"zh-CN": "汉化版"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.AddArtifact(ctx, catalog.NewArtifact{EditionID: editionGBA.ID, Path: "gba/advance.gba", Role: "rom", SHA256: "gba-export-hash"}); err != nil {
		t.Fatal(err)
	}
	if _, err = source.AddArtifact(ctx, catalog.NewArtifact{EditionID: editionGBA.ID, Path: "gba/translation.ips", Role: "patch", OriginalName: "Translation v1.2.ips"}); err != nil {
		t.Fatal(err)
	}
	if _, err = source.AddMedia(ctx, catalog.NewMediaAsset{GameID: gameGBA.ID, Kind: "cover", StorageKind: "library", Path: "media/advance-cover.png", OriginalName: "Localized cover.png", MIMEType: "image/png", Locale: "zh-CN", SourceType: "test"}); err != nil {
		t.Fatal(err)
	}
	if _, err = source.AddMedia(ctx, catalog.NewMediaAsset{EditionID: editionGBA.ID, Kind: "manual", StorageKind: "library", Path: "media/advance-manual.pdf", MIMEType: "application/pdf", Locale: "en", SourceType: "test"}); err != nil {
		t.Fatal(err)
	}
	gameNDS, err := source.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Advance Wars: Dual Strike", Platform: "nds", Titles: map[string]string{"zh-CN": "高级战争：双重打击"}})
	if err != nil {
		t.Fatal(err)
	}
	editionNDS, err := source.AddEdition(ctx, catalog.NewEdition{GameID: gameNDS.ID, DefaultTitle: "Original", EditionType: "original", Languages: []string{"en"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.AddArtifact(ctx, catalog.NewArtifact{EditionID: editionNDS.ID, Path: "nds/advance.nds", Role: "rom", SHA256: "nds-export-hash"}); err != nil {
		t.Fatal(err)
	}
	series, err := source.CreateSeries(ctx, catalog.NewSeries{DefaultTitle: "Wars series", Titles: map[string]string{"zh-CN": "高级战争系列"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.PutSeriesMember(ctx, series.ID, gameGBA.ID, catalog.NewSeriesMember{RelationType: "mainline", SortOrder: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err = source.PutSeriesMember(ctx, series.ID, gameNDS.ID, catalog.NewSeriesMember{RelationType: "port", SortOrder: 20}); err != nil {
		t.Fatal(err)
	}
	if _, err = ExportESDEPortable(ctx, source, root, root, "zh-CN"); err != nil {
		t.Fatal(err)
	}
	gamelist, err := os.ReadFile(filepath.Join(root, "gamelists", "gba", "gamelist.xml"))
	if err != nil || strings.Contains(string(gamelist), "translation.ips") || !strings.Contains(string(gamelist), "advance.gba") {
		t.Fatalf("ES-DE launch path=%q err=%v", gamelist, err)
	}

	target := newStore(t)
	for _, platform := range []string{"gba", "nds"} {
		if _, err = importer.ImportESDE(ctx, target, root, filepath.Join(root, "gamelists", platform, "gamelist.xml"), platform, "zh-CN"); err != nil {
			t.Fatal(err)
		}
	}
	games, err := target.ListGames(ctx, "zh-CN")
	if err != nil || len(games) != 2 {
		t.Fatalf("games=%#v err=%v", games, err)
	}
	importedGBA, err := target.GetGame(ctx, gameGBA.ID, "zh-CN")
	if err != nil || importedGBA.DisplayTitle != "高级战争" || len(importedGBA.Editions) != 1 {
		t.Fatalf("GBA identity/titles=%#v err=%v", importedGBA, err)
	}
	gotEdition := importedGBA.Editions[0]
	if gotEdition.ID != editionGBA.ID || gotEdition.EditionType != "translation" || gotEdition.Version != "1.2" || gotEdition.Serial != "AGB-AW" || gotEdition.ProductCode != "TEST-GBA" || gotEdition.TitleID != "gba-title" || len(gotEdition.Languages) != 2 {
		t.Fatalf("edition metadata=%#v", gotEdition)
	}
	if len(importedGBA.Media) != 1 || importedGBA.Media[0].Kind != "cover" || importedGBA.Media[0].Locale != "zh-CN" || len(gotEdition.Media) != 1 || gotEdition.Media[0].Kind != "manual" {
		t.Fatalf("media game=%#v edition=%#v", importedGBA.Media, gotEdition.Media)
	}
	if len(gotEdition.Artifacts) != 2 || gotEdition.Artifacts[0].Role != "rom" || gotEdition.Artifacts[1].Role != "patch" || gotEdition.Artifacts[1].OriginalName != "Translation v1.2.ips" {
		t.Fatalf("artifact semantics=%#v", gotEdition.Artifacts)
	}
	seriesItems, err := target.ListSeries(ctx, "zh-CN")
	if err != nil || len(seriesItems) != 1 || seriesItems[0].ID != series.ID || len(seriesItems[0].Members) != 2 || seriesItems[0].Members[1].RelationType != "port" {
		t.Fatalf("series=%#v err=%v", seriesItems, err)
	}
}
