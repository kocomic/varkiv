package catalog

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestGameSummaryKeepsListMetadataAndAggregatesPrivateFileState(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	game, err := store.CreateGame(ctx, NewGame{
		DefaultTitle: "Hero", Platform: "gba", Titles: map[string]string{"zh-CN": "勇者"},
	})
	if err != nil {
		t.Fatal(err)
	}
	edition, err := store.AddEdition(ctx, NewEdition{
		GameID: game.ID, DefaultTitle: "Translated", EditionType: "translation",
		Languages: []string{"zh-CN"}, Titles: map[string]string{"zh-CN": "汉化版"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, artifact := range []NewArtifact{
		{EditionID: edition.ID, Path: "private/hero.gba", Role: "rom", StorageKind: "managed", SHA256: strings.Repeat("a", 64), Size: 1024},
		{EditionID: edition.ID, Path: "private/missing.ips", Role: "patch", StorageKind: "library", SHA256: strings.Repeat("b", 64), Missing: true},
	} {
		if _, err = store.AddArtifact(ctx, artifact); err != nil {
			t.Fatal(err)
		}
	}
	cover, err := store.AddMedia(ctx, NewMediaAsset{EditionID: edition.ID, Kind: "cover", Path: "private/cover.png", MIMEType: "image/png", SHA256: strings.Repeat("c", 64)})
	if err != nil {
		t.Fatal(err)
	}

	page, err := store.ReadGameSummaries(ctx, GameReadQuery{Locale: "zh-CN", Page: PageRequest{Limit: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("summary page = %#v", page)
	}
	got := page.Items[0]
	if got.DisplayTitle != "勇者" || len(got.Editions) != 1 || got.Editions[0].DisplayTitle != "汉化版" {
		t.Fatalf("localized summary = %#v", got)
	}
	stats := got.Editions[0].Artifacts
	if stats.Total != 2 || stats.Missing != 1 || stats.Hashed != 2 || stats.Usable != 1 || stats.Managed != 1 {
		t.Fatalf("artifact stats = %#v", stats)
	}
	if got.Cover == nil || got.Cover.ID != cover.ID || got.Cover.Kind != "cover" || got.Cover.MIMEType != "image/png" {
		t.Fatalf("cover summary = %#v", got.Cover)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"private/hero.gba", "private/cover.png", strings.Repeat("a", 64), `"artifacts"`} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("summary exposed detail %q: %s", private, encoded)
		}
	}
}

func TestSeriesSummaryUsesSummaryMembersAndDatabasePage(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	game, err := store.CreateGame(ctx, NewGame{DefaultTitle: "Hero Portable", Platform: "psp"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddEdition(ctx, NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original"}); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"Arc A", "Arc B"} {
		series, createErr := store.CreateSeries(ctx, NewSeries{DefaultTitle: title})
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, createErr = store.PutSeriesMember(ctx, series.ID, game.ID, NewSeriesMember{RelationType: "port", SortOrder: 10}); createErr != nil {
			t.Fatal(createErr)
		}
	}

	page, err := store.ReadSeriesSummaries(ctx, SeriesReadQuery{Search: "portable", Page: PageRequest{Limit: 1, Offset: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.Items) != 1 || page.Items[0].DefaultTitle != "Arc B" || len(page.Items[0].Members) != 1 {
		t.Fatalf("series summary page = %#v", page)
	}
	member := page.Items[0].Members[0]
	if member.Game.Platform != "psp" || len(member.Game.Editions) != 1 || member.RelationType != "port" {
		t.Fatalf("series member summary = %#v", member)
	}
}

func TestSummaryReadModelsRejectNegativePageBounds(t *testing.T) {
	store := testStore(t)
	if _, err := store.ReadGameSummaries(context.Background(), GameReadQuery{Page: PageRequest{Offset: -1}}); err == nil {
		t.Fatal("negative game summary offset accepted")
	}
	if _, err := store.ReadSeriesSummaries(context.Background(), SeriesReadQuery{Page: PageRequest{Limit: -1}}); err == nil {
		t.Fatal("negative series summary limit accepted")
	}
}

func TestFullAndSummaryProjectionsShareIdentityOrderAndPageBounds(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	var games []Game
	for index, item := range []NewGame{
		{DefaultTitle: "Alpha", Platform: "gba"},
		{DefaultTitle: "Beta Hero", Platform: "gba", Titles: map[string]string{"zh-CN": "乙英雄"}},
		{DefaultTitle: "Delta Hero", Platform: "gba"},
		{DefaultTitle: "Gamma Hero", Platform: "nds"},
	} {
		game, err := store.CreateGame(ctx, item)
		if err != nil {
			t.Fatal(err)
		}
		games = append(games, game)
		if _, err = store.AddEdition(ctx, NewEdition{GameID: game.ID, DefaultTitle: "Edition", EditionType: "original", SortOrder: index}); err != nil {
			t.Fatal(err)
		}
	}
	for _, title := range []string{"Hero Arc A", "Hero Arc B", "Other Arc"} {
		series, err := store.CreateSeries(ctx, NewSeries{DefaultTitle: title})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.PutSeriesMember(ctx, series.ID, games[1].ID, NewSeriesMember{RelationType: "mainline"}); err != nil {
			t.Fatal(err)
		}
	}

	gameQuery := GameReadQuery{Locale: "zh-CN", Search: "hero", Platform: "gba", Page: PageRequest{Limit: 1, Offset: 1}}
	fullGames, err := store.ReadGames(ctx, gameQuery)
	if err != nil {
		t.Fatal(err)
	}
	summaryGames, err := store.ReadGameSummaries(ctx, gameQuery)
	if err != nil {
		t.Fatal(err)
	}
	if fullGames.Total != summaryGames.Total || fullGames.Limit != summaryGames.Limit || fullGames.Offset != summaryGames.Offset || len(fullGames.Items) != len(summaryGames.Items) || fullGames.Items[0].ID != summaryGames.Items[0].ID {
		t.Fatalf("game projections drifted: full=%#v summary=%#v", fullGames, summaryGames)
	}

	seriesQuery := SeriesReadQuery{Search: "hero", Page: PageRequest{Limit: 1, Offset: 1}}
	fullSeries, err := store.ReadSeries(ctx, seriesQuery)
	if err != nil {
		t.Fatal(err)
	}
	summarySeries, err := store.ReadSeriesSummaries(ctx, seriesQuery)
	if err != nil {
		t.Fatal(err)
	}
	if fullSeries.Total != summarySeries.Total || fullSeries.Limit != summarySeries.Limit || fullSeries.Offset != summarySeries.Offset || len(fullSeries.Items) != len(summarySeries.Items) || fullSeries.Items[0].ID != summarySeries.Items[0].ID {
		t.Fatalf("series projections drifted: full=%#v summary=%#v", fullSeries, summarySeries)
	}
}
