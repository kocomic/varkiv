package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"
)

func (s *Store) CreateGame(ctx context.Context, in NewGame) (Game, error) {
	if strings.TrimSpace(in.DefaultTitle) == "" || strings.TrimSpace(in.Platform) == "" {
		return Game{}, errors.New("default_title and platform are required")
	}
	if in.ID == "" {
		in.ID = NewID()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Game{}, err
	}
	defer tx.Rollback()
	now := nowText()
	if _, err = tx.ExecContext(ctx, `INSERT INTO games(id,default_title,platform,created_at,updated_at) VALUES(?,?,?,?,?)`, in.ID, strings.TrimSpace(in.DefaultTitle), strings.TrimSpace(in.Platform), now, now); err != nil {
		return Game{}, err
	}
	if err = putTitles(ctx, tx, "game", in.ID, in.Titles); err != nil {
		return Game{}, err
	}
	if err = tx.Commit(); err != nil {
		return Game{}, err
	}
	return s.GetGame(ctx, in.ID, "")
}

func (s *Store) UpdateGame(ctx context.Context, id string, in NewGame) (Game, error) {
	if strings.TrimSpace(in.DefaultTitle) == "" || strings.TrimSpace(in.Platform) == "" {
		return Game{}, errors.New("default_title and platform are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Game{}, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE games SET default_title=?, platform=?, updated_at=? WHERE id=?`, strings.TrimSpace(in.DefaultTitle), strings.TrimSpace(in.Platform), nowText(), id)
	if err != nil {
		return Game{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Game{}, sql.ErrNoRows
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM localized_titles WHERE owner_type='game' AND owner_id=?`, id); err != nil {
		return Game{}, err
	}
	if err = putTitles(ctx, tx, "game", id, in.Titles); err != nil {
		return Game{}, err
	}
	if err = tx.Commit(); err != nil {
		return Game{}, err
	}
	return s.GetGame(ctx, id, "")
}

func putTitles(ctx context.Context, tx *sql.Tx, ownerType, ownerID string, titles map[string]string) error {
	for locale, title := range titles {
		locale, title = strings.TrimSpace(locale), strings.TrimSpace(title)
		if locale == "" || title == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO localized_titles(owner_type,owner_id,locale,title) VALUES(?,?,?,?)`, ownerType, ownerID, locale, title); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) AddEdition(ctx context.Context, in NewEdition) (Edition, error) {
	if in.GameID == "" || strings.TrimSpace(in.DefaultTitle) == "" {
		return Edition{}, errors.New("game_id and default_title are required")
	}
	if in.ID == "" {
		in.ID = NewID()
	}
	if in.EditionType == "" {
		in.EditionType = "original"
	}
	if !validEditionType(in.EditionType) {
		return Edition{}, errors.New("invalid edition_type")
	}
	langs, _ := json.Marshal(in.Languages)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Edition{}, err
	}
	defer tx.Rollback()
	now := nowText()
	_, err = tx.ExecContext(ctx, `INSERT INTO editions(id,game_id,default_title,edition_type,version,languages_json,author,save_namespace,serial,product_code,title_id,sort_order,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		in.ID, in.GameID, strings.TrimSpace(in.DefaultTitle), in.EditionType, in.Version, string(langs), in.Author, in.ID, in.Serial, in.ProductCode, in.TitleID, in.SortOrder, now, now)
	if err != nil {
		return Edition{}, err
	}
	if err = putTitles(ctx, tx, "edition", in.ID, in.Titles); err != nil {
		return Edition{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE games SET primary_edition_id=CASE WHEN primary_edition_id='' THEN ? ELSE primary_edition_id END, updated_at=? WHERE id=?`, in.ID, now, in.GameID); err != nil {
		return Edition{}, err
	}
	if err = tx.Commit(); err != nil {
		return Edition{}, err
	}
	w, err := s.GetGame(ctx, in.GameID, "")
	if err != nil {
		return Edition{}, err
	}
	for _, e := range w.Editions {
		if e.ID == in.ID {
			return e, nil
		}
	}
	return Edition{}, sql.ErrNoRows
}

// AddEditionWithArtifact creates a manually entered edition and its first
// verified file relation in one transaction. It is used only after the server
// has resolved the file inside an authorized library root and calculated its
// content hash; a missing or hashless relation is never persisted.
func (s *Store) AddEditionWithArtifact(ctx context.Context, in NewEdition, artifact NewArtifact) (Edition, error) {
	if in.GameID == "" || strings.TrimSpace(in.DefaultTitle) == "" {
		return Edition{}, errors.New("game_id and default_title are required")
	}
	if in.ID == "" {
		in.ID = NewID()
	}
	if in.EditionType == "" {
		in.EditionType = "original"
	}
	if !validEditionType(in.EditionType) {
		return Edition{}, errors.New("invalid edition_type")
	}
	if artifact.EditionID == "" {
		artifact.EditionID = in.ID
	}
	if artifact.EditionID != in.ID || strings.TrimSpace(artifact.Path) == "" || artifact.Missing || strings.TrimSpace(artifact.SHA256) == "" {
		return Edition{}, errors.New("first artifact must be an existing hashed file or directory for this edition")
	}
	if artifact.ID == "" {
		artifact.ID = NewID()
	}
	if artifact.Role == "" {
		artifact.Role = "rom"
	}
	artifact.Role = strings.ToLower(strings.TrimSpace(artifact.Role))
	if err := validateArtifactFields(artifact.Role, artifact.DiscIndex, artifact.Size); err != nil {
		return Edition{}, err
	}
	if artifact.StorageKind == "" {
		artifact.StorageKind = "library"
	}
	if artifact.StorageKind != "library" && artifact.StorageKind != "managed" {
		return Edition{}, errors.New("storage_kind must be library or managed")
	}
	if artifact.SourcePath == "" && artifact.StorageKind == "library" {
		artifact.SourcePath = artifact.Path
	}
	if artifact.OriginalName == "" {
		artifact.OriginalName = filepath.Base(filepath.FromSlash(artifact.Path))
	}
	langs, _ := json.Marshal(in.Languages)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Edition{}, err
	}
	defer tx.Rollback()
	now := nowText()
	if _, err = tx.ExecContext(ctx, `INSERT INTO editions(id,game_id,default_title,edition_type,version,languages_json,author,save_namespace,serial,product_code,title_id,sort_order,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		in.ID, in.GameID, strings.TrimSpace(in.DefaultTitle), in.EditionType, in.Version, string(langs), in.Author, in.ID, in.Serial, in.ProductCode, in.TitleID, in.SortOrder, now, now); err != nil {
		return Edition{}, err
	}
	if err = putTitles(ctx, tx, "edition", in.ID, in.Titles); err != nil {
		return Edition{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE games SET primary_edition_id=CASE WHEN primary_edition_id='' THEN ? ELSE primary_edition_id END, updated_at=? WHERE id=?`, in.ID, now, in.GameID); err != nil {
		return Edition{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO artifacts(id,edition_id,path,role,disc_index,size,sha256,missing,created_at,updated_at,storage_kind,source_path,original_name) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		artifact.ID, artifact.EditionID, artifact.Path, artifact.Role, artifact.DiscIndex, artifact.Size, artifact.SHA256, 0, now, now, artifact.StorageKind, artifact.SourcePath, artifact.OriginalName); err != nil {
		return Edition{}, err
	}
	if err = tx.Commit(); err != nil {
		return Edition{}, err
	}
	return s.GetEdition(ctx, in.ID, "")
}

func validEditionType(value string) bool {
	switch value {
	case "original", "translation", "hack", "revision", "homebrew", "other":
		return true
	default:
		return false
	}
}

func validateArtifactFields(role string, discIndex int, size int64) error {
	if !ValidArtifactRole(role) {
		return errors.New("artifact role must be rom, disc, executable, patch, dlc, update, or other")
	}
	if discIndex < 0 || discIndex > 64 {
		return errors.New("artifact disc_index must be between 0 and 64")
	}
	if size < 0 {
		return errors.New("artifact size cannot be negative")
	}
	return nil
}

func (s *Store) GetEdition(ctx context.Context, id, locale string) (Edition, error) {
	var gameID string
	if err := s.db.QueryRowContext(ctx, `SELECT game_id FROM editions WHERE id=?`, id).Scan(&gameID); err != nil {
		return Edition{}, err
	}
	w, err := s.GetGame(ctx, gameID, locale)
	if err != nil {
		return Edition{}, err
	}
	for _, e := range w.Editions {
		if e.ID == id {
			return e, nil
		}
	}
	return Edition{}, sql.ErrNoRows
}

func (s *Store) UpdateEdition(ctx context.Context, id string, in NewEdition) (Edition, error) {
	if strings.TrimSpace(in.DefaultTitle) == "" {
		return Edition{}, errors.New("default_title is required")
	}
	if in.EditionType == "" {
		in.EditionType = "original"
	}
	if !validEditionType(in.EditionType) {
		return Edition{}, errors.New("invalid edition_type")
	}
	langs, _ := json.Marshal(in.Languages)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Edition{}, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE editions SET default_title=?,edition_type=?,version=?,languages_json=?,author=?,serial=?,product_code=?,title_id=?,sort_order=?,updated_at=? WHERE id=?`,
		strings.TrimSpace(in.DefaultTitle), in.EditionType, strings.TrimSpace(in.Version), string(langs), strings.TrimSpace(in.Author), strings.TrimSpace(in.Serial), strings.TrimSpace(in.ProductCode), strings.TrimSpace(in.TitleID), in.SortOrder, nowText(), id)
	if err != nil {
		return Edition{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Edition{}, sql.ErrNoRows
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM localized_titles WHERE owner_type='edition' AND owner_id=?`, id); err != nil {
		return Edition{}, err
	}
	if err = putTitles(ctx, tx, "edition", id, in.Titles); err != nil {
		return Edition{}, err
	}
	if err = tx.Commit(); err != nil {
		return Edition{}, err
	}
	return s.GetEdition(ctx, id, "")
}

func (s *Store) MoveEdition(ctx context.Context, editionID, targetGameID string) (Edition, error) {
	if editionID == "" || targetGameID == "" {
		return Edition{}, errors.New("edition_id and target_game_id are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Edition{}, err
	}
	defer tx.Rollback()
	var sourceGameID, sourcePlatform, targetPlatform string
	if err = tx.QueryRowContext(ctx, `SELECT e.game_id,g.platform FROM editions e JOIN games g ON g.id=e.game_id WHERE e.id=?`, editionID).Scan(&sourceGameID, &sourcePlatform); err != nil {
		return Edition{}, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT platform FROM games WHERE id=?`, targetGameID).Scan(&targetPlatform); err != nil {
		return Edition{}, err
	}
	if sourcePlatform != targetPlatform {
		return Edition{}, ErrPlatformMismatch
	}
	if sourceGameID == targetGameID {
		_ = tx.Rollback()
		return s.GetEdition(ctx, editionID, "")
	}
	now := nowText()
	if _, err = tx.ExecContext(ctx, `UPDATE editions SET game_id=?,updated_at=? WHERE id=?`, targetGameID, now, editionID); err != nil {
		return Edition{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE games SET primary_edition_id=CASE WHEN primary_edition_id=? THEN COALESCE((SELECT id FROM editions WHERE game_id=? ORDER BY sort_order,lower(default_title),id LIMIT 1),'') ELSE primary_edition_id END,updated_at=? WHERE id=?`, editionID, sourceGameID, now, sourceGameID); err != nil {
		return Edition{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE games SET primary_edition_id=CASE WHEN primary_edition_id='' THEN ? ELSE primary_edition_id END,updated_at=? WHERE id=?`, editionID, now, targetGameID); err != nil {
		return Edition{}, err
	}
	if err = tx.Commit(); err != nil {
		return Edition{}, err
	}
	return s.GetEdition(ctx, editionID, "")
}

func (s *Store) SetPrimaryEdition(ctx context.Context, gameID, editionID string) error {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM editions WHERE id=? AND game_id=?`, editionID, gameID).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return sql.ErrNoRows
	}
	_, err := s.db.ExecContext(ctx, `UPDATE games SET primary_edition_id=?,updated_at=? WHERE id=?`, editionID, nowText(), gameID)
	return err
}

func (s *Store) MergeGames(ctx context.Context, targetGameID, sourceGameID string) (Game, error) {
	return s.mergeGames(ctx, targetGameID, sourceGameID, "", false)
}

func (s *Store) DeleteEdition(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var gameID string
	if err = tx.QueryRowContext(ctx, `SELECT game_id FROM editions WHERE id=?`, id).Scan(&gameID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM localized_titles WHERE owner_type='edition' AND owner_id=?`, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM editions WHERE id=?`, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE games SET primary_edition_id=CASE WHEN primary_edition_id=? THEN COALESCE((SELECT id FROM editions WHERE game_id=? ORDER BY sort_order,lower(default_title),id LIMIT 1),'') ELSE primary_edition_id END,updated_at=? WHERE id=?`, id, gameID, nowText(), gameID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeleteGame(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM localized_titles WHERE owner_type='edition' AND owner_id IN (SELECT id FROM editions WHERE game_id=?)`, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM localized_titles WHERE owner_type='game' AND owner_id=?`, id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM games WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

func (s *Store) AddArtifact(ctx context.Context, in NewArtifact) (Artifact, error) {
	if in.EditionID == "" || strings.TrimSpace(in.Path) == "" {
		return Artifact{}, errors.New("edition_id and path are required")
	}
	if in.ID == "" {
		in.ID = NewID()
	}
	if in.Role == "" {
		in.Role = "rom"
	}
	in.Role = strings.ToLower(strings.TrimSpace(in.Role))
	if err := validateArtifactFields(in.Role, in.DiscIndex, in.Size); err != nil {
		return Artifact{}, err
	}
	if in.StorageKind == "" {
		in.StorageKind = "library"
	}
	if in.StorageKind != "library" && in.StorageKind != "managed" {
		return Artifact{}, errors.New("storage_kind must be library or managed")
	}
	if in.SourcePath == "" && in.StorageKind == "library" {
		in.SourcePath = in.Path
	}
	if in.OriginalName == "" {
		in.OriginalName = filepath.Base(filepath.FromSlash(in.Path))
	}
	now := nowText()
	_, err := s.db.ExecContext(ctx, `INSERT INTO artifacts(id,edition_id,path,role,disc_index,size,sha256,missing,created_at,updated_at,storage_kind,source_path,original_name) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		in.ID, in.EditionID, in.Path, in.Role, in.DiscIndex, in.Size, in.SHA256, boolInt(in.Missing), now, now, in.StorageKind, in.SourcePath, in.OriginalName)
	if err != nil {
		return Artifact{}, err
	}
	return Artifact{ID: in.ID, EditionID: in.EditionID, Path: in.Path, StorageKind: in.StorageKind, SourcePath: in.SourcePath, OriginalName: in.OriginalName, Role: in.Role, DiscIndex: in.DiscIndex, Size: in.Size, SHA256: in.SHA256, Missing: in.Missing}, nil
}

func (s *Store) UpdateArtifact(ctx context.Context, id string, in NewArtifact) (Artifact, error) {
	if strings.TrimSpace(in.Path) == "" {
		return Artifact{}, errors.New("path is required")
	}
	if in.Role == "" {
		in.Role = "rom"
	}
	in.Role = strings.ToLower(strings.TrimSpace(in.Role))
	if err := validateArtifactFields(in.Role, in.DiscIndex, in.Size); err != nil {
		return Artifact{}, err
	}
	if in.StorageKind == "" {
		in.StorageKind = "library"
	}
	if in.StorageKind != "library" && in.StorageKind != "managed" {
		return Artifact{}, errors.New("storage_kind must be library or managed")
	}
	if in.OriginalName == "" {
		in.OriginalName = filepath.Base(filepath.FromSlash(in.Path))
	}
	res, err := s.db.ExecContext(ctx, `UPDATE artifacts SET path=?,role=?,disc_index=?,size=?,sha256=?,missing=?,storage_kind=?,source_path=?,original_name=?,updated_at=? WHERE id=?`,
		strings.TrimSpace(in.Path), in.Role, in.DiscIndex, in.Size, in.SHA256, boolInt(in.Missing), in.StorageKind, in.SourcePath, in.OriginalName, nowText(), id)
	if err != nil {
		return Artifact{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Artifact{}, sql.ErrNoRows
	}
	var a Artifact
	var missing int
	err = s.db.QueryRowContext(ctx, `SELECT id,edition_id,path,storage_kind,source_path,original_name,role,disc_index,size,sha256,missing FROM artifacts WHERE id=?`, id).Scan(&a.ID, &a.EditionID, &a.Path, &a.StorageKind, &a.SourcePath, &a.OriginalName, &a.Role, &a.DiscIndex, &a.Size, &a.SHA256, &missing)
	a.Missing = missing != 0
	return a, err
}

func (s *Store) DeleteArtifact(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM artifacts WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (s *Store) ArtifactByPath(ctx context.Context, path string) (Artifact, error) {
	var a Artifact
	var missing int
	err := s.db.QueryRowContext(ctx, `SELECT id,edition_id,path,storage_kind,source_path,original_name,role,disc_index,size,sha256,missing FROM artifacts WHERE path=?`, path).Scan(&a.ID, &a.EditionID, &a.Path, &a.StorageKind, &a.SourcePath, &a.OriginalName, &a.Role, &a.DiscIndex, &a.Size, &a.SHA256, &missing)
	a.Missing = missing != 0
	return a, err
}

func (s *Store) ArtifactBySourcePath(ctx context.Context, path string) (Artifact, error) {
	var a Artifact
	var missing int
	err := s.db.QueryRowContext(ctx, `SELECT id,edition_id,path,storage_kind,source_path,original_name,role,disc_index,size,sha256,missing FROM artifacts WHERE source_path=? LIMIT 1`, path).Scan(&a.ID, &a.EditionID, &a.Path, &a.StorageKind, &a.SourcePath, &a.OriginalName, &a.Role, &a.DiscIndex, &a.Size, &a.SHA256, &missing)
	a.Missing = missing != 0
	return a, err
}

func (s *Store) ArtifactBySHA256(ctx context.Context, hash string) (Artifact, error) {
	var a Artifact
	var missing int
	err := s.db.QueryRowContext(ctx, `SELECT id,edition_id,path,storage_kind,source_path,original_name,role,disc_index,size,sha256,missing FROM artifacts WHERE sha256=? AND sha256<>'' LIMIT 1`, strings.TrimSpace(hash)).Scan(&a.ID, &a.EditionID, &a.Path, &a.StorageKind, &a.SourcePath, &a.OriginalName, &a.Role, &a.DiscIndex, &a.Size, &a.SHA256, &missing)
	a.Missing = missing != 0
	return a, err
}

func (s *Store) GetArtifact(ctx context.Context, id string) (Artifact, error) {
	var a Artifact
	var missing int
	err := s.db.QueryRowContext(ctx, `SELECT id,edition_id,path,storage_kind,source_path,original_name,role,disc_index,size,sha256,missing FROM artifacts WHERE id=?`, id).Scan(&a.ID, &a.EditionID, &a.Path, &a.StorageKind, &a.SourcePath, &a.OriginalName, &a.Role, &a.DiscIndex, &a.Size, &a.SHA256, &missing)
	a.Missing = missing != 0
	return a, err
}

func (s *Store) ListGames(ctx context.Context, locale string) ([]Game, error) {
	page, err := s.ReadGames(ctx, GameReadQuery{Locale: locale})
	return page.Items, err
}

func (s *Store) GetGame(ctx context.Context, id, locale string) (Game, error) {
	var w Game
	w.Editions = []Edition{}
	var created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT id,default_title,platform,primary_edition_id,created_at,updated_at FROM games WHERE id=?`, id).Scan(&w.ID, &w.DefaultTitle, &w.Platform, &w.PrimaryEditionID, &created, &updated)
	if err != nil {
		return Game{}, err
	}
	w.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	w.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	w.Titles, err = s.loadTitles(ctx, "game", id)
	if err != nil {
		return Game{}, err
	}
	w.DisplayTitle = resolveTitle(locale, w.DefaultTitle, w.Titles)
	w.Media, err = s.ListMedia(ctx, id, "", "")
	if err != nil {
		return Game{}, err
	}
	erows, err := s.db.QueryContext(ctx, `SELECT id,default_title,edition_type,version,languages_json,author,save_namespace,serial,product_code,title_id,sort_order FROM editions WHERE game_id=? ORDER BY sort_order, lower(default_title), id`, id)
	if err != nil {
		return Game{}, err
	}
	type editionRow struct {
		edition   Edition
		languages string
	}
	var pending []editionRow
	for erows.Next() {
		var e Edition
		var langs string
		if err := erows.Scan(&e.ID, &e.DefaultTitle, &e.EditionType, &e.Version, &langs, &e.Author, &e.SaveNamespace, &e.Serial, &e.ProductCode, &e.TitleID, &e.SortOrder); err != nil {
			erows.Close()
			return Game{}, err
		}
		e.GameID = id
		pending = append(pending, editionRow{edition: e, languages: langs})
	}
	if err := erows.Err(); err != nil {
		erows.Close()
		return Game{}, err
	}
	erows.Close()
	for _, row := range pending {
		e := row.edition
		_ = json.Unmarshal([]byte(row.languages), &e.Languages)
		if e.Languages == nil {
			e.Languages = []string{}
		}
		e.Titles, err = s.loadTitles(ctx, "edition", e.ID)
		if err != nil {
			return Game{}, err
		}
		e.DisplayTitle = resolveTitle(locale, e.DefaultTitle, e.Titles)
		e.Artifacts, err = s.loadArtifacts(ctx, e.ID)
		if err != nil {
			return Game{}, err
		}
		e.Media, err = s.ListMedia(ctx, "", e.ID, "")
		if err != nil {
			return Game{}, err
		}
		w.Editions = append(w.Editions, e)
	}
	return w, nil
}

func (s *Store) loadTitles(ctx context.Context, ownerType, ownerID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT locale,title FROM localized_titles WHERE owner_type=? AND owner_id=?`, ownerType, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	titles := map[string]string{}
	for rows.Next() {
		var l, t string
		if err := rows.Scan(&l, &t); err != nil {
			return nil, err
		}
		titles[l] = t
	}
	return titles, rows.Err()
}

func (s *Store) loadArtifacts(ctx context.Context, editionID string) ([]Artifact, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,path,storage_kind,source_path,original_name,role,disc_index,size,sha256,missing FROM artifacts WHERE edition_id=? ORDER BY disc_index,path`, editionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Artifact{}
	for rows.Next() {
		var a Artifact
		var m int
		a.EditionID = editionID
		if err := rows.Scan(&a.ID, &a.Path, &a.StorageKind, &a.SourcePath, &a.OriginalName, &a.Role, &a.DiscIndex, &a.Size, &a.SHA256, &m); err != nil {
			return nil, err
		}
		a.Missing = m != 0
		out = append(out, a)
	}
	return out, rows.Err()
}

func ValidMediaKind(kind string) bool {
	switch kind {
	case "cover", "box_front", "box_back", "box_spine", "logo", "screenshot", "title_screen", "background", "fanart", "marquee", "bezel", "manual", "video", "music", "cartridge", "poster", "banner", "tile", "other":
		return true
	}
	return false
}

func ValidMediaContentStatus(status string) bool {
	switch status {
	case "unverified", "available", "missing", "changed", "unsafe":
		return true
	default:
		return false
	}
}

func (s *Store) AddMedia(ctx context.Context, in NewMediaAsset) (MediaAsset, error) {
	if (in.GameID == "") == (in.EditionID == "") {
		return MediaAsset{}, errors.New("exactly one of game_id or edition_id is required")
	}
	in.Kind = strings.ToLower(strings.TrimSpace(in.Kind))
	if !ValidMediaKind(in.Kind) {
		return MediaAsset{}, errors.New("invalid media kind")
	}
	if in.SortOrder < 0 {
		return MediaAsset{}, errors.New("sort_order must be zero or greater")
	}
	in.Locale = strings.TrimSpace(in.Locale)
	if len(in.Locale) > 64 || strings.ContainsAny(in.Locale, "\r\n\x00") {
		return MediaAsset{}, errors.New("media locale must be at most 64 characters without control characters")
	}
	if in.StorageKind == "" {
		in.StorageKind = "managed"
	}
	if in.StorageKind != "library" && in.StorageKind != "managed" {
		return MediaAsset{}, errors.New("storage_kind must be library or managed")
	}
	if strings.TrimSpace(in.Path) == "" {
		return MediaAsset{}, errors.New("media path is required")
	}
	if in.ID == "" {
		in.ID = NewID()
	}
	if in.OriginalName == "" {
		in.OriginalName = filepath.Base(filepath.FromSlash(in.Path))
	}
	if in.MIMEType == "" {
		in.MIMEType = "application/octet-stream"
	}
	if in.SourceType == "" {
		in.SourceType = "upload"
	}
	if in.ContentStatus == "" {
		in.ContentStatus = "unverified"
	}
	if !ValidMediaContentStatus(in.ContentStatus) {
		return MediaAsset{}, errors.New("invalid media content_status")
	}
	created := nowText()
	if in.ContentStatus == "available" && in.ContentCheckedAt == "" {
		in.ContentCheckedAt = created
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO media_assets(id,game_id,edition_id,kind,storage_kind,path,source_path,original_name,mime_type,size,sha256,locale,source_type,sort_order,content_status,content_checked_at,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		in.ID, nullString(in.GameID), nullString(in.EditionID), in.Kind, in.StorageKind, in.Path, in.SourcePath, in.OriginalName, in.MIMEType, in.Size, in.SHA256, in.Locale, in.SourceType, in.SortOrder, in.ContentStatus, in.ContentCheckedAt, created)
	if err != nil {
		return MediaAsset{}, err
	}
	return s.GetMedia(ctx, in.ID)
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func scanMedia(scanner interface{ Scan(...any) error }) (MediaAsset, error) {
	var item MediaAsset
	var gameID, editionID sql.NullString
	var created string
	err := scanner.Scan(&item.ID, &gameID, &editionID, &item.Kind, &item.StorageKind, &item.Path, &item.SourcePath, &item.OriginalName, &item.MIMEType, &item.Size, &item.SHA256, &item.Locale, &item.SourceType, &item.SortOrder, &item.ContentStatus, &item.ContentCheckedAt, &created)
	item.GameID, item.EditionID = gameID.String, editionID.String
	item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return item, err
}

func (s *Store) GetMedia(ctx context.Context, id string) (MediaAsset, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,game_id,edition_id,kind,storage_kind,path,source_path,original_name,mime_type,size,sha256,locale,source_type,sort_order,content_status,content_checked_at,created_at FROM media_assets WHERE id=?`, id)
	return scanMedia(row)
}

func (s *Store) ListMedia(ctx context.Context, gameID, editionID, kind string) ([]MediaAsset, error) {
	query := `SELECT id,game_id,edition_id,kind,storage_kind,path,source_path,original_name,mime_type,size,sha256,locale,source_type,sort_order,content_status,content_checked_at,created_at FROM media_assets WHERE 1=1`
	args := []any{}
	if gameID != "" {
		query += ` AND game_id=?`
		args = append(args, gameID)
	}
	if editionID != "" {
		query += ` AND edition_id=?`
		args = append(args, editionID)
	}
	if kind != "" {
		query += ` AND kind=?`
		args = append(args, strings.ToLower(strings.TrimSpace(kind)))
	}
	query += ` ORDER BY kind,sort_order,created_at,id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []MediaAsset{}
	for rows.Next() {
		item, scanErr := scanMedia(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) UpdateMediaMetadata(ctx context.Context, id string, in MediaMetadataUpdate) (MediaAsset, error) {
	in.Kind = strings.ToLower(strings.TrimSpace(in.Kind))
	if !ValidMediaKind(in.Kind) {
		return MediaAsset{}, errors.New("invalid media kind")
	}
	if in.SortOrder < 0 {
		return MediaAsset{}, errors.New("sort_order must be zero or greater")
	}
	in.Locale = strings.TrimSpace(in.Locale)
	if len(in.Locale) > 64 || strings.ContainsAny(in.Locale, "\r\n\x00") {
		return MediaAsset{}, errors.New("media locale must be at most 64 characters without control characters")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE media_assets SET kind=?,locale=?,sort_order=? WHERE id=?`, in.Kind, in.Locale, in.SortOrder, id)
	if err != nil {
		return MediaAsset{}, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return MediaAsset{}, sql.ErrNoRows
	}
	return s.GetMedia(ctx, id)
}

func (s *Store) UpdateMediaContentStatuses(ctx context.Context, updates []MediaContentStatusUpdate) error {
	seen := make(map[string]struct{}, len(updates))
	for _, update := range updates {
		if strings.TrimSpace(update.ID) == "" {
			return errors.New("media id is required")
		}
		if !ValidMediaContentStatus(update.ContentStatus) {
			return errors.New("invalid media content_status")
		}
		if strings.TrimSpace(update.ContentCheckedAt) == "" {
			return errors.New("media content_checked_at is required")
		}
		if _, exists := seen[update.ID]; exists {
			return errors.New("duplicate media status update")
		}
		seen[update.ID] = struct{}{}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, update := range updates {
		result, updateErr := tx.ExecContext(ctx, `UPDATE media_assets SET content_status=?,content_checked_at=? WHERE id=?`, update.ContentStatus, update.ContentCheckedAt, update.ID)
		if updateErr != nil {
			return updateErr
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return sql.ErrNoRows
		}
	}
	return tx.Commit()
}

func (s *Store) DeleteMedia(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM media_assets WHERE id=?`, id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func resolveTitle(locale, fallback string, titles map[string]string) string {
	if locale != "" {
		if v := titles[locale]; v != "" {
			return v
		}
		if i := strings.IndexAny(locale, "-_"); i > 0 {
			if v := titles[locale[:i]]; v != "" {
				return v
			}
		}
	}
	return fallback
}
