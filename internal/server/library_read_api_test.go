package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"varkiv/internal/catalog"
)

func TestGameCollectionFiltersBeforeDatabasePage(t *testing.T) {
	_, handler, _ := testServer(t)
	for _, item := range []catalog.NewGame{
		{DefaultTitle: "Alpha Advance", Platform: "gba"},
		{DefaultTitle: "Beta Advance", Platform: "gba"},
		{DefaultTitle: "Advance DS", Platform: "nds"},
		{DefaultTitle: "Unrelated", Platform: "gba"},
	} {
		jsonRequest(t, handler, http.MethodPost, "/api/v1/games", item, &catalog.Game{})
	}

	var page struct {
		Data       []catalog.Game `json:"data"`
		Pagination pagination     `json:"pagination"`
	}
	jsonRequest(t, handler, http.MethodGet, "/api/v1/games?q=advance&platform=gba&limit=1&offset=1", nil, &page)
	if page.Pagination.Total != 2 || page.Pagination.Limit != 1 || page.Pagination.Offset != 1 || len(page.Data) != 1 || page.Data[0].DefaultTitle != "Beta Advance" {
		t.Fatalf("database page = %#v", page)
	}
}

func TestGameSummaryProjectionOmitsArtifactAndMediaStorageDetails(t *testing.T) {
	store, handler, _ := testServer(t)
	ctx := context.Background()
	game, err := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Summary Hero", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	edition, err := store.AddEdition(ctx, catalog.NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original"})
	if err != nil {
		t.Fatal(err)
	}
	privateHash := strings.Repeat("a", 64)
	if _, err = store.AddArtifact(ctx, catalog.NewArtifact{EditionID: edition.ID, Path: "private/hero.gba", Role: "rom", SHA256: privateHash, Size: 1024}); err != nil {
		t.Fatal(err)
	}
	cover, err := store.AddMedia(ctx, catalog.NewMediaAsset{GameID: game.ID, Kind: "cover", Path: "private/hero.png", MIMEType: "image/png", SHA256: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/games?projection=summary&limit=10", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("summary status=%d body=%s", response.Code, response.Body.String())
	}
	var page struct {
		Data []catalog.GameSummary `json:"data"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 1 || page.Data[0].Cover == nil || page.Data[0].Cover.ID != cover.ID || page.Data[0].Editions[0].Artifacts.Total != 1 {
		t.Fatalf("summary body = %#v", page)
	}
	for _, private := range []string{"private/hero.gba", "private/hero.png", privateHash, `"artifacts"`, `"media"`} {
		if strings.Contains(response.Body.String(), private) {
			t.Fatalf("summary exposed %q: %s", private, response.Body.String())
		}
	}

	full := httptest.NewRecorder()
	handler.ServeHTTP(full, httptest.NewRequest(http.MethodGet, "/api/v1/games?projection=full&limit=10", nil))
	if full.Code != http.StatusOK || !strings.Contains(full.Body.String(), "private/hero.gba") {
		t.Fatalf("full projection lost detail: %d %s", full.Code, full.Body.String())
	}
}

func TestSeriesSummaryProjectionAndInvalidProjectionContract(t *testing.T) {
	store, handler, _ := testServer(t)
	ctx := context.Background()
	game, _ := store.CreateGame(ctx, catalog.NewGame{DefaultTitle: "Portable Hero", Platform: "psp"})
	edition, _ := store.AddEdition(ctx, catalog.NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original"})
	_, _ = store.AddArtifact(ctx, catalog.NewArtifact{EditionID: edition.ID, Path: "private/portable.iso", Role: "disc", SHA256: strings.Repeat("c", 64)})
	series, _ := store.CreateSeries(ctx, catalog.NewSeries{DefaultTitle: "Hero Arc"})
	_, _ = store.PutSeriesMember(ctx, series.ID, game.ID, catalog.NewSeriesMember{RelationType: "port"})

	var page struct {
		Data []catalog.SeriesSummary `json:"data"`
	}
	jsonRequest(t, handler, http.MethodGet, "/api/v1/series?projection=summary&limit=10", nil, &page)
	if len(page.Data) != 1 || len(page.Data[0].Members) != 1 || page.Data[0].Members[0].Game.Editions[0].Artifacts.Total != 1 {
		t.Fatalf("series summary = %#v", page)
	}

	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, httptest.NewRequest(http.MethodGet, "/api/v1/games?projection=compact", nil))
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "invalid_argument") || !strings.Contains(invalid.Body.String(), "projection must be full or summary") {
		t.Fatalf("invalid projection = %d %s", invalid.Code, invalid.Body.String())
	}
}

func TestSeriesCollectionFiltersBeforeDatabasePage(t *testing.T) {
	_, handler, _ := testServer(t)
	var gba, nds catalog.Game
	jsonRequest(t, handler, http.MethodPost, "/api/v1/games", catalog.NewGame{DefaultTitle: "Hero Advance", Platform: "gba"}, &gba)
	jsonRequest(t, handler, http.MethodPost, "/api/v1/games", catalog.NewGame{DefaultTitle: "Hero DS", Platform: "nds"}, &nds)
	for index, game := range []catalog.Game{gba, nds} {
		var series catalog.Series
		jsonRequest(t, handler, http.MethodPost, "/api/v1/series", catalog.NewSeries{DefaultTitle: []string{"Arc One", "Arc Two"}[index]}, &series)
		jsonRequest(t, handler, http.MethodPut, "/api/v1/series/"+series.ID+"/members/"+game.ID, catalog.NewSeriesMember{RelationType: "mainline", SortOrder: 10}, &series)
	}

	var page struct {
		Data       []catalog.Series `json:"data"`
		Pagination pagination       `json:"pagination"`
	}
	jsonRequest(t, handler, http.MethodGet, "/api/v1/series?q=hero&limit=1&offset=1", nil, &page)
	if page.Pagination.Total != 2 || len(page.Data) != 1 || page.Data[0].DefaultTitle != "Arc Two" || page.Data[0].Members[0].Game.Platform != "nds" {
		t.Fatalf("series database page = %#v", page)
	}
}
