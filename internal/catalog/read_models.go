package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// PageRequest describes an offset page at the catalog boundary. A zero Limit
// deliberately means "all rows" for internal exporters and the deprecated
// unversioned API; public HTTP handlers enforce their own bounded limits.
type PageRequest struct {
	Limit  int
	Offset int
}

// Page is the stable result contract shared by catalog read models. Mutation
// repositories intentionally do not return this type.
type Page[T any] struct {
	Items  []T
	Total  int
	Limit  int
	Offset int
}

type GameReadQuery struct {
	Locale   string
	Search   string
	Platform string
	Page     PageRequest
}

type SeriesReadQuery struct {
	Locale string
	Search string
	Page   PageRequest
}

type catalogQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

const readBatchSize = 200

// ReadGames executes filtering and pagination in SQLite, then hydrates the
// selected page in bounded batches. Query count therefore depends on page
// count, not on the number of Games or Editions inside a page.
func (s *Store) ReadGames(ctx context.Context, query GameReadQuery) (Page[Game], error) {
	where, args := gameReadWhere(query)
	return readCatalogPage(ctx, s.db, query.Page,
		`SELECT COUNT(*) FROM games g`+where,
		`SELECT g.id FROM games g`+where+` ORDER BY lower(g.default_title),g.id`,
		args, query.Locale, loadGamesByIDs)
}

// ReadSeries uses the same bounded read-model pipeline as ReadGames. Member
// Games are hydrated once per page even when they occur in several Series.
func (s *Store) ReadSeries(ctx context.Context, query SeriesReadQuery) (Page[Series], error) {
	where, args := seriesReadWhere(query)
	return readCatalogPage(ctx, s.db, query.Page,
		`SELECT COUNT(*) FROM series s`+where,
		`SELECT s.id FROM series s`+where+` ORDER BY lower(s.default_title),s.id`,
		args, query.Locale, loadSeriesByIDs)
}

type catalogPageLoader[T any] func(context.Context, catalogQueryer, []string, string) ([]T, error)

// readCatalogPage keeps every collection projection on one read-only SQLite
// snapshot. Count, stable ID selection, and projection hydration therefore
// cannot drift independently as new list projections are added.
func readCatalogPage[T any](ctx context.Context, db *sql.DB, page PageRequest, countQuery, idQuery string, args []any, locale string, load catalogPageLoader[T]) (Page[T], error) {
	if err := validatePageRequest(page); err != nil {
		return Page[T]{}, err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Page[T]{}, err
	}
	defer tx.Rollback()
	var total int
	if err = tx.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return Page[T]{}, err
	}
	ids, err := readPageIDs(ctx, tx, idQuery, args, page)
	if err != nil {
		return Page[T]{}, err
	}
	items, err := load(ctx, tx, ids, locale)
	if err != nil {
		return Page[T]{}, err
	}
	if err = tx.Commit(); err != nil {
		return Page[T]{}, err
	}
	return Page[T]{Items: items, Total: total, Limit: page.Limit, Offset: page.Offset}, nil
}

func gameReadWhere(query GameReadQuery) (string, []any) {
	clauses := []string{}
	args := []any{}
	if platform := strings.TrimSpace(query.Platform); platform != "" {
		clauses = append(clauses, `g.platform=?`)
		args = append(args, platform)
	}
	if search := strings.ToLower(strings.TrimSpace(query.Search)); search != "" {
		clauses = append(clauses, `(
			instr(lower(g.default_title),?)>0 OR instr(lower(g.platform),?)>0 OR
			EXISTS (SELECT 1 FROM localized_titles lt WHERE lt.owner_type='game' AND lt.owner_id=g.id AND instr(lower(lt.title),?)>0) OR
			EXISTS (SELECT 1 FROM editions e WHERE e.game_id=g.id AND (
				instr(lower(e.default_title),?)>0 OR instr(lower(e.edition_type),?)>0 OR
				instr(lower(e.version),?)>0 OR instr(lower(e.author),?)>0 OR
				EXISTS (SELECT 1 FROM localized_titles et WHERE et.owner_type='edition' AND et.owner_id=e.id AND instr(lower(et.title),?)>0)
			))
		)`)
		for range 8 {
			args = append(args, search)
		}
	}
	if len(clauses) == 0 {
		return "", args
	}
	return ` WHERE ` + strings.Join(clauses, ` AND `), args
}

func seriesReadWhere(query SeriesReadQuery) (string, []any) {
	search := strings.ToLower(strings.TrimSpace(query.Search))
	if search == "" {
		return "", nil
	}
	where := ` WHERE
		instr(lower(s.default_title),?)>0 OR instr(lower(s.description),?)>0 OR
		EXISTS (SELECT 1 FROM series_titles st WHERE st.series_id=s.id AND instr(lower(st.title),?)>0) OR
		EXISTS (
			SELECT 1 FROM series_members sm JOIN games g ON g.id=sm.game_id
			WHERE sm.series_id=s.id AND (
				instr(lower(g.default_title),?)>0 OR instr(lower(g.platform),?)>0 OR
				instr(lower(sm.relation_type),?)>0 OR
				EXISTS (SELECT 1 FROM localized_titles lt WHERE lt.owner_type='game' AND lt.owner_id=g.id AND instr(lower(lt.title),?)>0)
			)
		)`
	args := make([]any, 7)
	for index := range args {
		args[index] = search
	}
	return where, args
}

func readPageIDs(ctx context.Context, db catalogQueryer, query string, args []any, page PageRequest) ([]string, error) {
	if page.Limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(append([]any{}, args...), page.Limit, page.Offset)
	} else if page.Offset > 0 {
		query += ` LIMIT -1 OFFSET ?`
		args = append(append([]any{}, args...), page.Offset)
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func validatePageRequest(page PageRequest) error {
	if page.Limit < 0 {
		return errors.New("page limit must be zero or greater")
	}
	if page.Offset < 0 {
		return errors.New("page offset must be zero or greater")
	}
	return nil
}

func loadGamesByIDs(ctx context.Context, db catalogQueryer, ids []string, locale string) ([]Game, error) {
	if len(ids) == 0 {
		return []Game{}, nil
	}
	gamesByID := make(map[string]*Game, len(ids))
	editionsByID := map[string]*Edition{}
	for _, id := range ids {
		gamesByID[id] = &Game{ID: id, Titles: map[string]string{}, Editions: []Edition{}, Media: []MediaAsset{}}
	}

	for _, batch := range stringBatches(ids, readBatchSize) {
		if err := loadGameRows(ctx, db, batch, gamesByID); err != nil {
			return nil, err
		}
		if err := loadGameTitles(ctx, db, batch, gamesByID); err != nil {
			return nil, err
		}
		if err := loadGameMedia(ctx, db, batch, gamesByID); err != nil {
			return nil, err
		}
		if err := loadEditions(ctx, db, batch, gamesByID, editionsByID); err != nil {
			return nil, err
		}
	}

	editionIDs := make([]string, 0, len(editionsByID))
	for id := range editionsByID {
		editionIDs = append(editionIDs, id)
	}
	for _, batch := range stringBatches(editionIDs, readBatchSize) {
		if err := loadEditionTitles(ctx, db, batch, editionsByID); err != nil {
			return nil, err
		}
		if err := loadEditionArtifacts(ctx, db, batch, editionsByID); err != nil {
			return nil, err
		}
		if err := loadEditionMedia(ctx, db, batch, editionsByID); err != nil {
			return nil, err
		}
	}

	items := make([]Game, 0, len(ids))
	for _, id := range ids {
		game := gamesByID[id]
		game.DisplayTitle = resolveTitle(locale, game.DefaultTitle, game.Titles)
		for index := range game.Editions {
			edition := &game.Editions[index]
			edition.DisplayTitle = resolveTitle(locale, edition.DefaultTitle, edition.Titles)
		}
		items = append(items, *game)
	}
	return items, nil
}

func loadGameRows(ctx context.Context, db catalogQueryer, ids []string, target map[string]*Game) error {
	rows, err := db.QueryContext(ctx, `SELECT id,default_title,platform,primary_edition_id,created_at,updated_at FROM games WHERE id IN (`+placeholders(len(ids))+`)`, stringsToAny(ids)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var item Game
		var created, updated string
		if err = rows.Scan(&item.ID, &item.DefaultTitle, &item.Platform, &item.PrimaryEditionID, &created, &updated); err != nil {
			return err
		}
		game := target[item.ID]
		if game == nil {
			return fmt.Errorf("catalog read model returned unexpected game %q", item.ID)
		}
		game.DefaultTitle = item.DefaultTitle
		game.Platform = item.Platform
		game.PrimaryEditionID = item.PrimaryEditionID
		game.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		game.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	}
	return rows.Err()
}

func loadGameTitles(ctx context.Context, db catalogQueryer, ids []string, target map[string]*Game) error {
	rows, err := db.QueryContext(ctx, `SELECT owner_id,locale,title FROM localized_titles WHERE owner_type='game' AND owner_id IN (`+placeholders(len(ids))+`)`, stringsToAny(ids)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, locale, title string
		if err = rows.Scan(&id, &locale, &title); err != nil {
			return err
		}
		target[id].Titles[locale] = title
	}
	return rows.Err()
}

func loadGameMedia(ctx context.Context, db catalogQueryer, ids []string, target map[string]*Game) error {
	rows, err := db.QueryContext(ctx, mediaSelect+` WHERE game_id IN (`+placeholders(len(ids))+`) ORDER BY game_id,kind,sort_order,created_at,id`, stringsToAny(ids)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		item, scanErr := scanMedia(rows)
		if scanErr != nil {
			return scanErr
		}
		target[item.GameID].Media = append(target[item.GameID].Media, item)
	}
	return rows.Err()
}

func loadEditions(ctx context.Context, db catalogQueryer, gameIDs []string, games map[string]*Game, editions map[string]*Edition) error {
	rows, err := db.QueryContext(ctx, `SELECT id,game_id,default_title,edition_type,version,languages_json,author,save_namespace,serial,product_code,title_id,sort_order FROM editions WHERE game_id IN (`+placeholders(len(gameIDs))+`) ORDER BY game_id,sort_order,lower(default_title),id`, stringsToAny(gameIDs)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var item Edition
		var languages string
		if err = rows.Scan(&item.ID, &item.GameID, &item.DefaultTitle, &item.EditionType, &item.Version, &languages, &item.Author, &item.SaveNamespace, &item.Serial, &item.ProductCode, &item.TitleID, &item.SortOrder); err != nil {
			return err
		}
		_ = json.Unmarshal([]byte(languages), &item.Languages)
		if item.Languages == nil {
			item.Languages = []string{}
		}
		item.Titles = map[string]string{}
		item.Artifacts = []Artifact{}
		item.Media = []MediaAsset{}
		games[item.GameID].Editions = append(games[item.GameID].Editions, item)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	// Populate pointers only after every append in this batch. Taking addresses
	// while appending would let a slice reallocation leave stale pointers.
	for _, gameID := range gameIDs {
		for index := range games[gameID].Editions {
			item := &games[gameID].Editions[index]
			editions[item.ID] = item
		}
	}
	return nil
}

func loadEditionTitles(ctx context.Context, db catalogQueryer, ids []string, target map[string]*Edition) error {
	rows, err := db.QueryContext(ctx, `SELECT owner_id,locale,title FROM localized_titles WHERE owner_type='edition' AND owner_id IN (`+placeholders(len(ids))+`)`, stringsToAny(ids)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, locale, title string
		if err = rows.Scan(&id, &locale, &title); err != nil {
			return err
		}
		target[id].Titles[locale] = title
	}
	return rows.Err()
}

func loadEditionArtifacts(ctx context.Context, db catalogQueryer, ids []string, target map[string]*Edition) error {
	rows, err := db.QueryContext(ctx, `SELECT id,edition_id,path,storage_kind,source_path,original_name,role,disc_index,size,sha256,missing FROM artifacts WHERE edition_id IN (`+placeholders(len(ids))+`) ORDER BY edition_id,disc_index,path`, stringsToAny(ids)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var item Artifact
		var missing int
		if err = rows.Scan(&item.ID, &item.EditionID, &item.Path, &item.StorageKind, &item.SourcePath, &item.OriginalName, &item.Role, &item.DiscIndex, &item.Size, &item.SHA256, &missing); err != nil {
			return err
		}
		item.Missing = missing != 0
		target[item.EditionID].Artifacts = append(target[item.EditionID].Artifacts, item)
	}
	return rows.Err()
}

func loadEditionMedia(ctx context.Context, db catalogQueryer, ids []string, target map[string]*Edition) error {
	rows, err := db.QueryContext(ctx, mediaSelect+` WHERE edition_id IN (`+placeholders(len(ids))+`) ORDER BY edition_id,kind,sort_order,created_at,id`, stringsToAny(ids)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		item, scanErr := scanMedia(rows)
		if scanErr != nil {
			return scanErr
		}
		target[item.EditionID].Media = append(target[item.EditionID].Media, item)
	}
	return rows.Err()
}

func loadSeriesByIDs(ctx context.Context, db catalogQueryer, ids []string, locale string) ([]Series, error) {
	if len(ids) == 0 {
		return []Series{}, nil
	}
	seriesByID := make(map[string]*Series, len(ids))
	for _, id := range ids {
		seriesByID[id] = &Series{ID: id, Titles: map[string]string{}, Members: []SeriesMember{}}
	}
	memberGameIDs := []string{}
	seenGame := map[string]bool{}

	for _, batch := range stringBatches(ids, readBatchSize) {
		args := stringsToAny(batch)
		rows, err := db.QueryContext(ctx, `SELECT id,default_title,description,created_at,updated_at FROM series WHERE id IN (`+placeholders(len(batch))+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var item Series
			var created, updated string
			if err = rows.Scan(&item.ID, &item.DefaultTitle, &item.Description, &created, &updated); err != nil {
				rows.Close()
				return nil, err
			}
			series := seriesByID[item.ID]
			if series == nil {
				rows.Close()
				return nil, fmt.Errorf("catalog read model returned unexpected series %q", item.ID)
			}
			series.DefaultTitle = item.DefaultTitle
			series.Description = item.Description
			series.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
			series.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
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
			seriesByID[id].Titles[language] = title
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
			var member SeriesMember
			if err = rows.Scan(&member.SeriesID, &member.GameID, &member.RelationType, &member.SortOrder); err != nil {
				rows.Close()
				return nil, err
			}
			seriesByID[member.SeriesID].Members = append(seriesByID[member.SeriesID].Members, member)
			if !seenGame[member.GameID] {
				seenGame[member.GameID] = true
				memberGameIDs = append(memberGameIDs, member.GameID)
			}
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	memberGames, err := loadGamesByIDs(ctx, db, memberGameIDs, locale)
	if err != nil {
		return nil, err
	}
	gamesByID := make(map[string]Game, len(memberGames))
	for _, game := range memberGames {
		gamesByID[game.ID] = game
	}
	items := make([]Series, 0, len(ids))
	for _, id := range ids {
		item := seriesByID[id]
		item.DisplayTitle = resolveTitle(locale, item.DefaultTitle, item.Titles)
		for index := range item.Members {
			item.Members[index].Game = gamesByID[item.Members[index].GameID]
		}
		items = append(items, *item)
	}
	return items, nil
}

func placeholders(count int) string {
	if count < 1 {
		panic("catalog read model requires at least one placeholder")
	}
	return strings.TrimRight(strings.Repeat("?,", count), ",")
}

func stringsToAny(values []string) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}

func stringBatches(values []string, size int) [][]string {
	if size < 1 {
		panic(fmt.Sprintf("invalid catalog read batch size %d", size))
	}
	result := make([][]string, 0, (len(values)+size-1)/size)
	for start := 0; start < len(values); start += size {
		result = append(result, values[start:min(start+size, len(values))])
	}
	return result
}

const mediaSelect = `SELECT id,game_id,edition_id,kind,storage_kind,path,source_path,original_name,mime_type,size,sha256,locale,source_type,sort_order,content_status,content_checked_at,created_at FROM media_assets`
