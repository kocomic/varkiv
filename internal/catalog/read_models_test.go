package catalog

import (
	"context"
	"fmt"
	"testing"
)

func TestReadGamesFiltersBeforePagingAndHydratesSelectedRows(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	alpha, err := store.CreateGame(ctx, NewGame{DefaultTitle: "Alpha", Platform: "gba", Titles: map[string]string{"zh-CN": "阿尔法"}})
	if err != nil {
		t.Fatal(err)
	}
	alphaEdition, err := store.AddEdition(ctx, NewEdition{GameID: alpha.ID, DefaultTitle: "Original", EditionType: "original", Languages: []string{"en"}, Author: "Studio Alpha", Titles: map[string]string{"zh-CN": "原版"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddArtifact(ctx, NewArtifact{EditionID: alphaEdition.ID, Path: "gba/alpha.gba", Role: "rom", Size: 1024}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddMedia(ctx, NewMediaAsset{GameID: alpha.ID, Kind: "cover", Path: "media/alpha.png", MIMEType: "image/png"}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddMedia(ctx, NewMediaAsset{EditionID: alphaEdition.ID, Kind: "screenshot", Path: "media/alpha-shot.png", MIMEType: "image/png"}); err != nil {
		t.Fatal(err)
	}

	for _, item := range []NewGame{
		{DefaultTitle: "Beta", Platform: "gba"},
		{DefaultTitle: "Gamma", Platform: "nds"},
		{DefaultTitle: "Omega", Platform: "gba", Titles: map[string]string{"ja": "スタジオ作品"}},
	} {
		if _, err = store.CreateGame(ctx, item); err != nil {
			t.Fatal(err)
		}
	}

	page, err := store.ReadGames(ctx, GameReadQuery{Locale: "zh-CN", Platform: "gba", Page: PageRequest{Limit: 1, Offset: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 || page.Limit != 1 || page.Offset != 1 || len(page.Items) != 1 || page.Items[0].DefaultTitle != "Beta" {
		t.Fatalf("filtered page = %#v", page)
	}

	localized, err := store.ReadGames(ctx, GameReadQuery{Locale: "zh-CN", Search: "阿尔法", Page: PageRequest{Limit: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if localized.Total != 1 || len(localized.Items) != 1 {
		t.Fatalf("localized search = %#v", localized)
	}
	got := localized.Items[0]
	if got.DisplayTitle != "阿尔法" || len(got.Editions) != 1 || got.Editions[0].DisplayTitle != "原版" || len(got.Editions[0].Artifacts) != 1 || len(got.Media) != 1 || len(got.Editions[0].Media) != 1 {
		t.Fatalf("hydrated game = %#v", got)
	}

	editionMatch, err := store.ReadGames(ctx, GameReadQuery{Search: "studio alpha", Page: PageRequest{Limit: 10}})
	if err != nil || editionMatch.Total != 1 || editionMatch.Items[0].ID != alpha.ID {
		t.Fatalf("edition search = %#v err=%v", editionMatch, err)
	}
}

func TestReadSeriesHydratesSharedMembersOncePerPageContract(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	shared, err := store.CreateGame(ctx, NewGame{DefaultTitle: "Shared Hero", Platform: "snes", Titles: map[string]string{"ja": "共有勇者"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddEdition(ctx, NewEdition{GameID: shared.ID, DefaultTitle: "Japanese", EditionType: "original", Languages: []string{"ja"}}); err != nil {
		t.Fatal(err)
	}
	for index, title := range []string{"First Arc", "Second Arc"} {
		series, createErr := store.CreateSeries(ctx, NewSeries{DefaultTitle: title, Titles: map[string]string{"zh-CN": fmt.Sprintf("第%d篇", index+1)}})
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, createErr = store.PutSeriesMember(ctx, series.ID, shared.ID, NewSeriesMember{RelationType: "mainline", SortOrder: 10}); createErr != nil {
			t.Fatal(createErr)
		}
	}

	page, err := store.ReadSeries(ctx, SeriesReadQuery{Locale: "ja", Search: "snes", Page: PageRequest{Limit: 1, Offset: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.Items) != 1 || page.Items[0].DefaultTitle != "Second Arc" {
		t.Fatalf("series page = %#v", page)
	}
	if len(page.Items[0].Members) != 1 || page.Items[0].Members[0].Game.DisplayTitle != "共有勇者" || len(page.Items[0].Members[0].Game.Editions) != 1 {
		t.Fatalf("series member hydration = %#v", page.Items[0].Members)
	}
}

func TestReadModelsRejectNegativePageBounds(t *testing.T) {
	store := testStore(t)
	if _, err := store.ReadGames(context.Background(), GameReadQuery{Page: PageRequest{Limit: -1}}); err == nil {
		t.Fatal("negative game page limit accepted")
	}
	if _, err := store.ReadSeries(context.Background(), SeriesReadQuery{Page: PageRequest{Offset: -1}}); err == nil {
		t.Fatal("negative series page offset accepted")
	}
}
