package catalog

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// ArtifactStats contains only the aggregate file state needed by library and
// sync overviews. Paths, hashes, and individual Artifact records remain on the
// Game detail resource.
type ArtifactStats struct {
	Total   int `json:"total"`
	Missing int `json:"missing"`
	Hashed  int `json:"hashed"`
	Usable  int `json:"usable"`
	Managed int `json:"managed"`
}

type EditionSummary struct {
	ID            string            `json:"id"`
	GameID        string            `json:"game_id"`
	DefaultTitle  string            `json:"default_title"`
	DisplayTitle  string            `json:"display_title"`
	EditionType   string            `json:"edition_type"`
	Version       string            `json:"version,omitempty"`
	Languages     []string          `json:"languages"`
	Author        string            `json:"author,omitempty"`
	SaveNamespace string            `json:"save_namespace"`
	Serial        string            `json:"serial,omitempty"`
	ProductCode   string            `json:"product_code,omitempty"`
	TitleID       string            `json:"title_id,omitempty"`
	SortOrder     int               `json:"sort_order"`
	Titles        map[string]string `json:"titles"`
	Artifacts     ArtifactStats     `json:"artifact_stats"`
}

// MediaSummary is an opaque image reference for thumbnail delivery. Storage
// paths, source paths, hashes, and ownership internals stay on Game detail.
type MediaSummary struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	MIMEType string `json:"mime_type"`
}

type GameSummary struct {
	ID               string            `json:"id"`
	DefaultTitle     string            `json:"default_title"`
	DisplayTitle     string            `json:"display_title"`
	Platform         string            `json:"platform"`
	PrimaryEditionID string            `json:"primary_edition_id,omitempty"`
	Titles           map[string]string `json:"titles"`
	Editions         []EditionSummary  `json:"editions"`
	Cover            *MediaSummary     `json:"cover,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

type SeriesMemberSummary struct {
	SeriesID     string      `json:"series_id"`
	GameID       string      `json:"game_id"`
	RelationType string      `json:"relation_type"`
	SortOrder    int         `json:"sort_order"`
	Game         GameSummary `json:"game"`
}

type SeriesSummary struct {
	ID           string                `json:"id"`
	DefaultTitle string                `json:"default_title"`
	DisplayTitle string                `json:"display_title"`
	Description  string                `json:"description,omitempty"`
	Titles       map[string]string     `json:"titles"`
	Members      []SeriesMemberSummary `json:"members"`
	CreatedAt    time.Time             `json:"created_at"`
	UpdatedAt    time.Time             `json:"updated_at"`
}

func (s *Store) ReadGameSummaries(ctx context.Context, query GameReadQuery) (Page[GameSummary], error) {
	where, args := gameReadWhere(query)
	return readCatalogPage(ctx, s.db, query.Page,
		`SELECT COUNT(*) FROM games g`+where,
		`SELECT g.id FROM games g`+where+` ORDER BY lower(g.default_title),g.id`,
		args, query.Locale, loadGameSummariesByIDs)
}

func (s *Store) ReadSeriesSummaries(ctx context.Context, query SeriesReadQuery) (Page[SeriesSummary], error) {
	where, args := seriesReadWhere(query)
	return readCatalogPage(ctx, s.db, query.Page,
		`SELECT COUNT(*) FROM series s`+where,
		`SELECT s.id FROM series s`+where+` ORDER BY lower(s.default_title),s.id`,
		args, query.Locale, loadSeriesSummariesByIDs)
}

func loadGameSummariesByIDs(ctx context.Context, db catalogQueryer, ids []string, locale string) ([]GameSummary, error) {
	if len(ids) == 0 {
		return []GameSummary{}, nil
	}
	games := make(map[string]*GameSummary, len(ids))
	editions := map[string]*EditionSummary{}
	for _, id := range ids {
		games[id] = &GameSummary{ID: id, Titles: map[string]string{}, Editions: []EditionSummary{}}
	}
	for _, batch := range stringBatches(ids, readBatchSize) {
		if err := loadGameSummaryRows(ctx, db, batch, games); err != nil {
			return nil, err
		}
		if err := loadGameSummaryTitles(ctx, db, batch, games); err != nil {
			return nil, err
		}
		if err := loadEditionSummaries(ctx, db, batch, games, editions); err != nil {
			return nil, err
		}
	}
	editionIDs := make([]string, 0, len(editions))
	for id := range editions {
		editionIDs = append(editionIDs, id)
	}
	for _, batch := range stringBatches(editionIDs, readBatchSize) {
		if err := loadEditionSummaryTitles(ctx, db, batch, editions); err != nil {
			return nil, err
		}
	}
	if err := loadSummaryCovers(ctx, db, ids, editionIDs, games, editions); err != nil {
		return nil, err
	}
	items := make([]GameSummary, 0, len(ids))
	for _, id := range ids {
		game := games[id]
		game.DisplayTitle = resolveTitle(locale, game.DefaultTitle, game.Titles)
		for index := range game.Editions {
			edition := &game.Editions[index]
			edition.DisplayTitle = resolveTitle(locale, edition.DefaultTitle, edition.Titles)
		}
		items = append(items, *game)
	}
	return items, nil
}

func loadGameSummaryRows(ctx context.Context, db catalogQueryer, ids []string, target map[string]*GameSummary) error {
	rows, err := db.QueryContext(ctx, `SELECT id,default_title,platform,primary_edition_id,created_at,updated_at FROM games WHERE id IN (`+placeholders(len(ids))+`)`, stringsToAny(ids)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var item GameSummary
		var created, updated string
		if err = rows.Scan(&item.ID, &item.DefaultTitle, &item.Platform, &item.PrimaryEditionID, &created, &updated); err != nil {
			return err
		}
		game := target[item.ID]
		game.DefaultTitle, game.Platform, game.PrimaryEditionID = item.DefaultTitle, item.Platform, item.PrimaryEditionID
		game.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		game.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	}
	return rows.Err()
}

func loadGameSummaryTitles(ctx context.Context, db catalogQueryer, ids []string, target map[string]*GameSummary) error {
	rows, err := db.QueryContext(ctx, `SELECT owner_id,locale,title FROM localized_titles WHERE owner_type='game' AND owner_id IN (`+placeholders(len(ids))+`)`, stringsToAny(ids)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, language, title string
		if err = rows.Scan(&id, &language, &title); err != nil {
			return err
		}
		target[id].Titles[language] = title
	}
	return rows.Err()
}

func loadEditionSummaries(ctx context.Context, db catalogQueryer, gameIDs []string, games map[string]*GameSummary, editions map[string]*EditionSummary) error {
	rows, err := db.QueryContext(ctx, `
		SELECT e.id,e.game_id,e.default_title,e.edition_type,e.version,e.languages_json,e.author,e.save_namespace,e.serial,e.product_code,e.title_id,e.sort_order,
		       COUNT(a.id),COALESCE(SUM(CASE WHEN a.missing=1 THEN 1 ELSE 0 END),0),
		       COALESCE(SUM(CASE WHEN a.sha256<>'' THEN 1 ELSE 0 END),0),
		       COALESCE(SUM(CASE WHEN a.missing=0 AND a.sha256<>'' THEN 1 ELSE 0 END),0),
		       COALESCE(SUM(CASE WHEN a.storage_kind='managed' THEN 1 ELSE 0 END),0)
		FROM editions e LEFT JOIN artifacts a ON a.edition_id=e.id
		WHERE e.game_id IN (`+placeholders(len(gameIDs))+`)
		GROUP BY e.id,e.game_id,e.default_title,e.edition_type,e.version,e.languages_json,e.author,e.save_namespace,e.serial,e.product_code,e.title_id,e.sort_order
		ORDER BY e.game_id,e.sort_order,lower(e.default_title),e.id`, stringsToAny(gameIDs)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var item EditionSummary
		var languages string
		if err = rows.Scan(&item.ID, &item.GameID, &item.DefaultTitle, &item.EditionType, &item.Version, &languages, &item.Author, &item.SaveNamespace, &item.Serial, &item.ProductCode, &item.TitleID, &item.SortOrder, &item.Artifacts.Total, &item.Artifacts.Missing, &item.Artifacts.Hashed, &item.Artifacts.Usable, &item.Artifacts.Managed); err != nil {
			return err
		}
		_ = json.Unmarshal([]byte(languages), &item.Languages)
		if item.Languages == nil {
			item.Languages = []string{}
		}
		item.Titles = map[string]string{}
		games[item.GameID].Editions = append(games[item.GameID].Editions, item)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for _, gameID := range gameIDs {
		for index := range games[gameID].Editions {
			item := &games[gameID].Editions[index]
			editions[item.ID] = item
		}
	}
	return nil
}

func loadEditionSummaryTitles(ctx context.Context, db catalogQueryer, ids []string, target map[string]*EditionSummary) error {
	rows, err := db.QueryContext(ctx, `SELECT owner_id,locale,title FROM localized_titles WHERE owner_type='edition' AND owner_id IN (`+placeholders(len(ids))+`)`, stringsToAny(ids)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, language, title string
		if err = rows.Scan(&id, &language, &title); err != nil {
			return err
		}
		target[id].Titles[language] = title
	}
	return rows.Err()
}

func loadSummaryCovers(ctx context.Context, db catalogQueryer, gameIDs, editionIDs []string, games map[string]*GameSummary, editions map[string]*EditionSummary) error {
	clauses := []string{`game_id IN (` + placeholders(len(gameIDs)) + `)`}
	args := stringsToAny(gameIDs)
	if len(editionIDs) > 0 {
		clauses = append(clauses, `edition_id IN (`+placeholders(len(editionIDs))+`)`)
		args = append(args, stringsToAny(editionIDs)...)
	}
	rows, err := db.QueryContext(ctx, `SELECT id,COALESCE(game_id,''),COALESCE(edition_id,''),kind,mime_type FROM media_assets WHERE (`+strings.Join(clauses, ` OR `)+`) AND kind IN ('cover','box_front','poster','tile','screenshot') AND lower(mime_type) LIKE 'image/%' ORDER BY sort_order,created_at,id`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	type selected struct {
		item  MediaSummary
		score int
	}
	chosen := map[string]selected{}
	for rows.Next() {
		var item MediaSummary
		var gameID, editionID string
		if err = rows.Scan(&item.ID, &gameID, &editionID, &item.Kind, &item.MIMEType); err != nil {
			return err
		}
		ownerPenalty := 0
		if gameID == "" {
			edition := editions[editionID]
			if edition == nil {
				continue
			}
			gameID, ownerPenalty = edition.GameID, 1
		}
		score := mediaKindRank(item.Kind)*2 + ownerPenalty
		if current, exists := chosen[gameID]; !exists || score < current.score {
			chosen[gameID] = selected{item: item, score: score}
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for gameID, cover := range chosen {
		item := cover.item
		games[gameID].Cover = &item
	}
	return nil
}

func mediaKindRank(kind string) int {
	for index, candidate := range []string{"cover", "box_front", "poster", "tile", "screenshot"} {
		if kind == candidate {
			return index
		}
	}
	return 100
}

func loadSeriesSummariesByIDs(ctx context.Context, db catalogQueryer, ids []string, locale string) ([]SeriesSummary, error) {
	if len(ids) == 0 {
		return []SeriesSummary{}, nil
	}
	series := make(map[string]*SeriesSummary, len(ids))
	for _, id := range ids {
		series[id] = &SeriesSummary{ID: id, Titles: map[string]string{}, Members: []SeriesMemberSummary{}}
	}
	gameIDs, seenGames := []string{}, map[string]bool{}
	for _, batch := range stringBatches(ids, readBatchSize) {
		args := stringsToAny(batch)
		rows, err := db.QueryContext(ctx, `SELECT id,default_title,description,created_at,updated_at FROM series WHERE id IN (`+placeholders(len(batch))+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var item SeriesSummary
			var created, updated string
			if err = rows.Scan(&item.ID, &item.DefaultTitle, &item.Description, &created, &updated); err != nil {
				rows.Close()
				return nil, err
			}
			target := series[item.ID]
			target.DefaultTitle, target.Description = item.DefaultTitle, item.Description
			target.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
			target.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
		rows, err = db.QueryContext(ctx, `SELECT series_id,locale,title FROM series_titles WHERE series_id IN (`+placeholders(len(batch))+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, language, title string
			if err = rows.Scan(&id, &language, &title); err != nil {
				rows.Close()
				return nil, err
			}
			series[id].Titles[language] = title
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
		rows, err = db.QueryContext(ctx, `SELECT series_id,game_id,relation_type,sort_order FROM series_members WHERE series_id IN (`+placeholders(len(batch))+`) ORDER BY series_id,sort_order,game_id`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var member SeriesMemberSummary
			if err = rows.Scan(&member.SeriesID, &member.GameID, &member.RelationType, &member.SortOrder); err != nil {
				rows.Close()
				return nil, err
			}
			series[member.SeriesID].Members = append(series[member.SeriesID].Members, member)
			if !seenGames[member.GameID] {
				seenGames[member.GameID] = true
				gameIDs = append(gameIDs, member.GameID)
			}
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	games, err := loadGameSummariesByIDs(ctx, db, gameIDs, locale)
	if err != nil {
		return nil, err
	}
	gamesByID := make(map[string]GameSummary, len(games))
	for _, game := range games {
		gamesByID[game.ID] = game
	}
	items := make([]SeriesSummary, 0, len(ids))
	for _, id := range ids {
		item := series[id]
		item.DisplayTitle = resolveTitle(locale, item.DefaultTitle, item.Titles)
		for index := range item.Members {
			item.Members[index].Game = gamesByID[item.Members[index].GameID]
		}
		items = append(items, *item)
	}
	return items, nil
}
