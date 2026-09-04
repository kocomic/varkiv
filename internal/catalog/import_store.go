package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

func (s *Store) ImportGame(ctx context.Context, in ImportedGame) (Game, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Game{}, false, err
	}
	defer tx.Rollback()
	gameID, created, err := importGameTx(ctx, tx, in)
	if err != nil || !created {
		return Game{}, created, err
	}
	if err = tx.Commit(); err != nil {
		return Game{}, false, err
	}
	work, err := s.GetGame(ctx, gameID, "")
	return work, true, err
}

// ImportGamesAtomic commits every game in one SQLite transaction. A duplicate
// or invalid item aborts the complete batch; callers can therefore clean up
// staged managed files without leaving partial metadata behind.
func (s *Store) ImportGamesAtomic(ctx context.Context, games []ImportedGame) error {
	return s.ImportGamesAndCustomPlatformsAtomic(ctx, games, nil)
}

func importGameTx(ctx context.Context, tx *sql.Tx, in ImportedGame) (string, bool, error) {
	if strings.TrimSpace(in.DefaultTitle) == "" || strings.TrimSpace(in.Platform) == "" {
		return "", false, errors.New("default_title and platform are required")
	}
	editionTitle := in.EditionTitle
	if strings.TrimSpace(editionTitle) == "" {
		editionTitle = in.DefaultTitle
	}
	editionTitle = strings.TrimSpace(editionTitle)
	if in.EditionType == "" {
		in.EditionType = "original"
	}
	if !validEditionType(in.EditionType) {
		return "", false, errors.New("invalid edition_type")
	}
	editionTitles := in.EditionTitles
	if editionTitles == nil {
		editionTitles = in.Titles
	}
	if in.GameID == "" {
		in.GameID = NewID()
	}
	if in.EditionID == "" {
		in.EditionID = NewID()
	}

	// Duplicate detection and every metadata insert share one transaction. A
	// failed artifact or media row therefore cannot leave a half-imported game.
	for _, a := range in.Artifacts {
		if strings.TrimSpace(a.Path) == "" {
			return "", false, errors.New("artifact path is required")
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM artifacts WHERE path=? OR (?<>'' AND source_path=?) OR (?<>'' AND sha256=?)`, a.Path, a.SourcePath, a.SourcePath, a.SHA256, a.SHA256).Scan(&count); err != nil {
			return "", false, err
		}
		if count > 0 {
			return in.GameID, false, nil
		}
	}

	var existingPlatform string
	err := tx.QueryRowContext(ctx, `SELECT platform FROM games WHERE id=?`, in.GameID).Scan(&existingPlatform)
	switch {
	case err == nil && existingPlatform != strings.TrimSpace(in.Platform):
		return "", false, ErrPlatformMismatch
	case err == nil:
		// Append this edition to the stable logical work.
	case errors.Is(err, sql.ErrNoRows):
		now := nowText()
		if _, err = tx.ExecContext(ctx, `INSERT INTO games(id,default_title,platform,created_at,updated_at) VALUES(?,?,?,?,?)`, in.GameID, strings.TrimSpace(in.DefaultTitle), strings.TrimSpace(in.Platform), now, now); err != nil {
			return "", false, err
		}
		if err = putTitles(ctx, tx, "game", in.GameID, in.Titles); err != nil {
			return "", false, err
		}
	default:
		return "", false, err
	}

	langs, _ := json.Marshal(in.Languages)
	now := nowText()
	if _, err = tx.ExecContext(ctx, `INSERT INTO editions(id,game_id,default_title,edition_type,version,languages_json,author,save_namespace,serial,product_code,title_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		in.EditionID, in.GameID, editionTitle, in.EditionType, in.Version, string(langs), in.Author, in.EditionID, in.Serial, in.ProductCode, in.TitleID, now, now); err != nil {
		return "", false, err
	}
	if err = putTitles(ctx, tx, "edition", in.EditionID, editionTitles); err != nil {
		return "", false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE games SET primary_edition_id=CASE WHEN primary_edition_id='' THEN ? ELSE primary_edition_id END,updated_at=? WHERE id=?`, in.EditionID, now, in.GameID); err != nil {
		return "", false, err
	}

	for _, a := range in.Artifacts {
		if a.ID == "" {
			a.ID = NewID()
		}
		if a.Role == "" {
			a.Role = "rom"
		}
		a.Role = strings.ToLower(strings.TrimSpace(a.Role))
		if err = validateArtifactFields(a.Role, a.DiscIndex, a.Size); err != nil {
			return "", false, err
		}
		if a.StorageKind == "" {
			a.StorageKind = "library"
		}
		if a.StorageKind != "library" && a.StorageKind != "managed" {
			return "", false, errors.New("storage_kind must be library or managed")
		}
		if a.SourcePath == "" && a.StorageKind == "library" {
			a.SourcePath = a.Path
		}
		if a.OriginalName == "" {
			a.OriginalName = filepath.Base(filepath.FromSlash(a.Path))
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO artifacts(id,edition_id,path,role,disc_index,size,sha256,missing,created_at,updated_at,storage_kind,source_path,original_name) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			a.ID, in.EditionID, a.Path, a.Role, a.DiscIndex, a.Size, a.SHA256, boolInt(a.Missing), now, now, a.StorageKind, a.SourcePath, a.OriginalName); err != nil {
			return "", false, fmt.Errorf("add artifact %s: %w", a.Path, err)
		}
	}
	for _, media := range in.Media {
		if media.GameID == "" && media.EditionID == "" {
			media.EditionID = in.EditionID
		}
		if (media.GameID == "") == (media.EditionID == "") {
			return "", false, errors.New("exactly one of game_id or edition_id is required")
		}
		media.Kind = strings.ToLower(strings.TrimSpace(media.Kind))
		if !ValidMediaKind(media.Kind) {
			return "", false, errors.New("invalid media kind")
		}
		if media.StorageKind == "" {
			media.StorageKind = "managed"
		}
		if media.StorageKind != "library" && media.StorageKind != "managed" {
			return "", false, errors.New("storage_kind must be library or managed")
		}
		if strings.TrimSpace(media.Path) == "" {
			return "", false, errors.New("media path is required")
		}
		if media.ID == "" {
			media.ID = NewID()
		}
		if media.OriginalName == "" {
			media.OriginalName = filepath.Base(filepath.FromSlash(media.Path))
		}
		if media.MIMEType == "" {
			media.MIMEType = "application/octet-stream"
		}
		if media.SourceType == "" {
			media.SourceType = "frontend-import"
		}
		contentStatus := media.ContentStatus
		if contentStatus == "" {
			contentStatus = "available"
		}
		if !ValidMediaContentStatus(contentStatus) {
			return "", false, errors.New("invalid imported media content_status")
		}
		checkedAt := media.ContentCheckedAt
		if contentStatus == "available" && checkedAt == "" {
			checkedAt = now
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO media_assets(id,game_id,edition_id,kind,storage_kind,path,source_path,original_name,mime_type,size,sha256,locale,source_type,sort_order,content_status,content_checked_at,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			media.ID, nullString(media.GameID), nullString(media.EditionID), media.Kind, media.StorageKind, media.Path, media.SourcePath, media.OriginalName, media.MIMEType, media.Size, media.SHA256, media.Locale, media.SourceType, media.SortOrder, contentStatus, checkedAt, now); err != nil {
			return "", false, fmt.Errorf("add media %s: %w", media.Path, err)
		}
	}
	for _, hint := range in.RuntimeHints {
		hint.EditionID = in.EditionID
		if err = insertRuntimeImportHintTx(ctx, tx, hint); err != nil {
			return "", false, fmt.Errorf("add runtime import hint: %w", err)
		}
	}
	for _, membership := range in.SeriesMemberships {
		membership.Series.ID = strings.TrimSpace(membership.Series.ID)
		membership.Series.DefaultTitle = strings.TrimSpace(membership.Series.DefaultTitle)
		membership.RelationType = strings.ToLower(strings.TrimSpace(membership.RelationType))
		if membership.RelationType == "" {
			membership.RelationType = "mainline"
		}
		if membership.Series.ID == "" || membership.Series.DefaultTitle == "" || !validSeriesRelation(membership.RelationType) {
			return "", false, errors.New("imported series requires id, default_title, and a valid relation_type")
		}
		var existingTitle string
		seriesErr := tx.QueryRowContext(ctx, `SELECT default_title FROM series WHERE id=?`, membership.Series.ID).Scan(&existingTitle)
		switch {
		case errors.Is(seriesErr, sql.ErrNoRows):
			if _, err = tx.ExecContext(ctx, `INSERT INTO series(id,default_title,description,created_at,updated_at) VALUES(?,?,?,?,?)`, membership.Series.ID, membership.Series.DefaultTitle, strings.TrimSpace(membership.Series.Description), now, now); err != nil {
				return "", false, fmt.Errorf("add imported series: %w", err)
			}
			if err = putSeriesTitles(ctx, tx, membership.Series.ID, membership.Series.Titles); err != nil {
				return "", false, fmt.Errorf("add imported series titles: %w", err)
			}
		case seriesErr != nil:
			return "", false, seriesErr
		}
		// Existing user relationships are preserved. A neutral manifest can add
		// a missing relation but never silently rewrite one.
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO series_members(series_id,game_id,relation_type,sort_order) VALUES(?,?,?,?)`, membership.Series.ID, in.GameID, membership.RelationType, membership.SortOrder); err != nil {
			return "", false, fmt.Errorf("add imported series membership: %w", err)
		}
	}
	return in.GameID, true, nil
}
