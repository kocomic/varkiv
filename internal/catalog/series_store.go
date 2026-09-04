package catalog

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

func (s *Store) CreateSeries(ctx context.Context, in NewSeries) (Series, error) {
	return s.CreateSeriesMutation(ctx, SeriesMutation{ID: in.ID, DefaultTitle: in.DefaultTitle, Description: in.Description, Titles: in.Titles})
}

func (s *Store) CreateSeriesMutation(ctx context.Context, in SeriesMutation) (Series, error) {
	if err := validateSeriesMutation(in); err != nil {
		return Series{}, err
	}
	if in.ID == "" {
		in.ID = NewID()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Series{}, err
	}
	defer tx.Rollback()
	now := nowText()
	if _, err = tx.ExecContext(ctx, `INSERT INTO series(id,default_title,description,created_at,updated_at) VALUES(?,?,?,?,?)`, in.ID, strings.TrimSpace(in.DefaultTitle), strings.TrimSpace(in.Description), now, now); err != nil {
		return Series{}, err
	}
	if err = putSeriesTitles(ctx, tx, in.ID, in.Titles); err != nil {
		return Series{}, err
	}
	if in.Members != nil {
		if err = replaceSeriesMembers(ctx, tx, in.ID, *in.Members); err != nil {
			return Series{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return Series{}, err
	}
	return s.GetSeries(ctx, in.ID, "")
}

func (s *Store) UpdateSeries(ctx context.Context, id string, in NewSeries) (Series, error) {
	return s.UpdateSeriesMutation(ctx, id, SeriesMutation{DefaultTitle: in.DefaultTitle, Description: in.Description, Titles: in.Titles})
}

func (s *Store) UpdateSeriesMutation(ctx context.Context, id string, in SeriesMutation) (Series, error) {
	if strings.TrimSpace(id) == "" {
		return Series{}, errors.New("series_id is required")
	}
	if err := validateSeriesMutation(in); err != nil {
		return Series{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Series{}, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE series SET default_title=?,description=?,updated_at=? WHERE id=?`, strings.TrimSpace(in.DefaultTitle), strings.TrimSpace(in.Description), nowText(), id)
	if err != nil {
		return Series{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Series{}, sql.ErrNoRows
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM series_titles WHERE series_id=?`, id); err != nil {
		return Series{}, err
	}
	if err = putSeriesTitles(ctx, tx, id, in.Titles); err != nil {
		return Series{}, err
	}
	if in.Members != nil {
		if _, err = tx.ExecContext(ctx, `DELETE FROM series_members WHERE series_id=?`, id); err != nil {
			return Series{}, err
		}
		if err = replaceSeriesMembers(ctx, tx, id, *in.Members); err != nil {
			return Series{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return Series{}, err
	}
	return s.GetSeries(ctx, id, "")
}

func validateSeriesMutation(in SeriesMutation) error {
	if strings.TrimSpace(in.DefaultTitle) == "" {
		return errors.New("default_title is required")
	}
	if in.Members == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(*in.Members))
	for _, member := range *in.Members {
		member.GameID = strings.TrimSpace(member.GameID)
		if member.GameID == "" {
			return errors.New("member game_id is required")
		}
		if _, exists := seen[member.GameID]; exists {
			return errors.New("duplicate series member game_id")
		}
		seen[member.GameID] = struct{}{}
		if member.RelationType != "" && !validSeriesRelation(member.RelationType) {
			return errors.New("invalid relation_type")
		}
		if member.SortOrder < 0 {
			return errors.New("sort_order must be zero or greater")
		}
	}
	return nil
}

func replaceSeriesMembers(ctx context.Context, tx *sql.Tx, seriesID string, members []SeriesMemberMutation) error {
	for _, member := range members {
		member.GameID = strings.TrimSpace(member.GameID)
		if member.RelationType == "" {
			member.RelationType = "mainline"
		}
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM games WHERE id=?`, member.GameID).Scan(&exists); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO series_members(series_id,game_id,relation_type,sort_order) VALUES(?,?,?,?)`, seriesID, member.GameID, member.RelationType, member.SortOrder); err != nil {
			return err
		}
	}
	return nil
}

func putSeriesTitles(ctx context.Context, tx *sql.Tx, seriesID string, titles map[string]string) error {
	for locale, title := range titles {
		locale, title = strings.TrimSpace(locale), strings.TrimSpace(title)
		if locale == "" || title == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO series_titles(series_id,locale,title) VALUES(?,?,?)`, seriesID, locale, title); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ListSeries(ctx context.Context, locale string) ([]Series, error) {
	page, err := s.ReadSeries(ctx, SeriesReadQuery{Locale: locale})
	return page.Items, err
}

func (s *Store) GetSeries(ctx context.Context, id, locale string) (Series, error) {
	var item Series
	var created, updated string
	if err := s.db.QueryRowContext(ctx, `SELECT id,default_title,description,created_at,updated_at FROM series WHERE id=?`, id).Scan(&item.ID, &item.DefaultTitle, &item.Description, &created, &updated); err != nil {
		return Series{}, err
	}
	item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	item.Titles = map[string]string{}
	rows, err := s.db.QueryContext(ctx, `SELECT locale,title FROM series_titles WHERE series_id=?`, id)
	if err != nil {
		return Series{}, err
	}
	for rows.Next() {
		var lang, title string
		if err = rows.Scan(&lang, &title); err != nil {
			rows.Close()
			return Series{}, err
		}
		item.Titles[lang] = title
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return Series{}, err
	}
	rows.Close()
	item.DisplayTitle = resolveTitle(locale, item.DefaultTitle, item.Titles)
	memberRows, err := s.db.QueryContext(ctx, `SELECT game_id,relation_type,sort_order FROM series_members WHERE series_id=? ORDER BY sort_order,game_id`, id)
	if err != nil {
		return Series{}, err
	}
	type memberRow struct {
		gameID, relation string
		order            int
	}
	pending := []memberRow{}
	for memberRows.Next() {
		var row memberRow
		if err = memberRows.Scan(&row.gameID, &row.relation, &row.order); err != nil {
			memberRows.Close()
			return Series{}, err
		}
		pending = append(pending, row)
	}
	if err = memberRows.Err(); err != nil {
		memberRows.Close()
		return Series{}, err
	}
	memberRows.Close()
	item.Members = make([]SeriesMember, 0, len(pending))
	for _, row := range pending {
		work, getErr := s.GetGame(ctx, row.gameID, locale)
		if getErr != nil {
			return Series{}, getErr
		}
		item.Members = append(item.Members, SeriesMember{SeriesID: id, GameID: row.gameID, RelationType: row.relation, SortOrder: row.order, Game: work})
	}
	return item, nil
}

func validSeriesRelation(value string) bool {
	switch value {
	case "mainline", "port", "remake", "spinoff", "collection", "other":
		return true
	default:
		return false
	}
}

func (s *Store) PutSeriesMember(ctx context.Context, seriesID, gameID string, in NewSeriesMember) (Series, error) {
	if seriesID == "" || gameID == "" {
		return Series{}, errors.New("series_id and game_id are required")
	}
	if in.RelationType == "" {
		in.RelationType = "mainline"
	}
	if !validSeriesRelation(in.RelationType) {
		return Series{}, errors.New("invalid relation_type")
	}
	if in.SortOrder < 0 {
		return Series{}, errors.New("sort_order must be zero or greater")
	}
	if _, err := s.GetSeries(ctx, seriesID, ""); err != nil {
		return Series{}, err
	}
	if _, err := s.GetGame(ctx, gameID, ""); err != nil {
		return Series{}, err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO series_members(series_id,game_id,relation_type,sort_order) VALUES(?,?,?,?) ON CONFLICT(series_id,game_id) DO UPDATE SET relation_type=excluded.relation_type,sort_order=excluded.sort_order`, seriesID, gameID, in.RelationType, in.SortOrder)
	if err != nil {
		return Series{}, err
	}
	return s.GetSeries(ctx, seriesID, "")
}

func (s *Store) DeleteSeriesMember(ctx context.Context, seriesID, gameID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM series_members WHERE series_id=? AND game_id=?`, seriesID, gameID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DeleteSeries(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM series WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
