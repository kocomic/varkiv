package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"strings"
)

type mergeQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type mergeDigest struct {
	hash hash.Hash
}

func newMergeDigest(targetGameID, sourceGameID string) *mergeDigest {
	value := &mergeDigest{hash: sha256.New()}
	value.add("request", targetGameID, sourceGameID)
	return value
}

func (d *mergeDigest) add(kind string, fields ...any) {
	d.write(kind)
	for _, field := range fields {
		d.write(fmt.Sprintf("%T:%v", field, field))
	}
}

func (d *mergeDigest) write(value string) {
	_, _ = fmt.Fprintf(d.hash, "%d:", len(value))
	_, _ = io.WriteString(d.hash, value)
}

func (d *mergeDigest) sum() string {
	return hex.EncodeToString(d.hash.Sum(nil))
}

type mergeGameRecord struct {
	id, defaultTitle, platform, primaryEditionID, createdAt, updatedAt string
}

func readMergeGame(ctx context.Context, query mergeQueryer, id string) (mergeGameRecord, error) {
	var row mergeGameRecord
	err := query.QueryRowContext(ctx, `SELECT id,default_title,platform,primary_edition_id,created_at,updated_at FROM games WHERE id=?`, id).
		Scan(&row.id, &row.defaultTitle, &row.platform, &row.primaryEditionID, &row.createdAt, &row.updatedAt)
	return row, err
}

func validateMergeIDs(targetGameID, sourceGameID string) error {
	if strings.TrimSpace(targetGameID) == "" || strings.TrimSpace(sourceGameID) == "" || targetGameID == sourceGameID {
		return ErrInvalidGameMerge
	}
	return nil
}

// PreviewGameMerge returns a consistent, privacy-minimized view of the merge.
// The complete affected catalog graph is represented only by a SHA-256 digest;
// ROM paths and content hashes never leave the catalog layer through this API.
func (s *Store) PreviewGameMerge(ctx context.Context, targetGameID, sourceGameID, locale string) (GameMergePlan, error) {
	if err := validateMergeIDs(targetGameID, sourceGameID); err != nil {
		return GameMergePlan{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GameMergePlan{}, err
	}
	defer tx.Rollback()
	plan, err := buildGameMergePlan(ctx, tx, targetGameID, sourceGameID, locale)
	if err != nil {
		return GameMergePlan{}, err
	}
	if err = tx.Commit(); err != nil {
		return GameMergePlan{}, err
	}
	return plan, nil
}

// MergeGamesIfFingerprint recomputes the complete merge graph and compares it
// with the reviewed fingerprint inside the same transaction that performs the
// merge. A stale review therefore produces zero writes, including under a
// concurrent metadata, edition, media, or series update.
func (s *Store) MergeGamesIfFingerprint(ctx context.Context, targetGameID, sourceGameID, expectedFingerprint string) (Game, error) {
	if strings.TrimSpace(expectedFingerprint) == "" {
		return Game{}, fmt.Errorf("snapshot_fingerprint is required")
	}
	return s.mergeGames(ctx, targetGameID, sourceGameID, expectedFingerprint, true)
}

func (s *Store) mergeGames(ctx context.Context, targetGameID, sourceGameID, expectedFingerprint string, checked bool) (Game, error) {
	if err := validateMergeIDs(targetGameID, sourceGameID); err != nil {
		return Game{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Game{}, err
	}
	defer tx.Rollback()
	if checked {
		plan, planErr := buildGameMergePlan(ctx, tx, targetGameID, sourceGameID, "")
		if planErr != nil {
			if errors.Is(planErr, sql.ErrNoRows) || errors.Is(planErr, ErrPlatformMismatch) {
				return Game{}, ErrGameMergeStale
			}
			return Game{}, planErr
		}
		if plan.SnapshotFingerprint != expectedFingerprint {
			return Game{}, ErrGameMergeStale
		}
	}
	if err = mergeGamesTx(ctx, tx, targetGameID, sourceGameID); err != nil {
		return Game{}, err
	}
	if err = tx.Commit(); err != nil {
		return Game{}, err
	}
	return s.GetGame(ctx, targetGameID, "")
}

func mergeGamesTx(ctx context.Context, tx *sql.Tx, targetGameID, sourceGameID string) error {
	var targetPlatform, sourcePlatform, targetPrimary, sourcePrimary string
	if err := tx.QueryRowContext(ctx, `SELECT platform,primary_edition_id FROM games WHERE id=?`, targetGameID).Scan(&targetPlatform, &targetPrimary); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT platform,primary_edition_id FROM games WHERE id=?`, sourceGameID).Scan(&sourcePlatform, &sourcePrimary); err != nil {
		return err
	}
	if targetPlatform != sourcePlatform {
		return ErrPlatformMismatch
	}
	now := nowText()
	if _, err := tx.ExecContext(ctx, `UPDATE editions SET game_id=?,updated_at=? WHERE game_id=?`, targetGameID, now, sourceGameID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO localized_titles(owner_type,owner_id,locale,title,sort_title) SELECT 'game',?,locale,title,sort_title FROM localized_titles WHERE owner_type='game' AND owner_id=?`, targetGameID, sourceGameID); err != nil {
		return err
	}
	// Game-scoped artwork belongs to the logical game, so it follows the
	// merge instead of being removed by the source Game's ON DELETE CASCADE.
	if _, err := tx.ExecContext(ctx, `UPDATE media_assets SET game_id=? WHERE game_id=?`, targetGameID, sourceGameID); err != nil {
		return err
	}
	// Preserve series placement. Existing target membership wins if both
	// Games were already present in the same Series.
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO series_members(series_id,game_id,relation_type,sort_order) SELECT series_id,?,relation_type,sort_order FROM series_members WHERE game_id=?`, targetGameID, sourceGameID); err != nil {
		return err
	}
	if targetPrimary == "" && sourcePrimary != "" {
		targetPrimary = sourcePrimary
	}
	if _, err := tx.ExecContext(ctx, `UPDATE games SET primary_edition_id=?,updated_at=? WHERE id=?`, targetPrimary, now, targetGameID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM localized_titles WHERE owner_type='game' AND owner_id=?`, sourceGameID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM games WHERE id=?`, sourceGameID); err != nil {
		return err
	}
	return nil
}

func buildGameMergePlan(ctx context.Context, query mergeQueryer, targetGameID, sourceGameID, locale string) (GameMergePlan, error) {
	if err := validateMergeIDs(targetGameID, sourceGameID); err != nil {
		return GameMergePlan{}, err
	}
	target, err := readMergeGame(ctx, query, targetGameID)
	if err != nil {
		return GameMergePlan{}, err
	}
	source, err := readMergeGame(ctx, query, sourceGameID)
	if err != nil {
		return GameMergePlan{}, err
	}
	if target.platform != source.platform {
		return GameMergePlan{}, ErrPlatformMismatch
	}
	digest := newMergeDigest(targetGameID, sourceGameID)
	digest.add("game", target.id, target.defaultTitle, target.platform, target.primaryEditionID, target.createdAt, target.updatedAt)
	digest.add("game", source.id, source.defaultTitle, source.platform, source.primaryEditionID, source.createdAt, source.updatedAt)
	plan := GameMergePlan{
		TargetGameID: target.id, SourceGameID: source.id, TargetTitle: target.defaultTitle, SourceTitle: source.defaultTitle, Platform: target.platform,
	}
	targetTitles, sourceTitles := map[string]string{}, map[string]string{}
	titleRows, err := query.QueryContext(ctx, `
		SELECT owner_type,owner_id,locale,title,sort_title
		FROM localized_titles
		WHERE (owner_type='game' AND owner_id IN (?,?))
		   OR (owner_type='edition' AND owner_id IN (SELECT id FROM editions WHERE game_id IN (?,?)))
		ORDER BY owner_type,owner_id,locale`, targetGameID, sourceGameID, targetGameID, sourceGameID)
	if err != nil {
		return GameMergePlan{}, err
	}
	for titleRows.Next() {
		var ownerType, ownerID, titleLocale, title, sortTitle string
		if err = titleRows.Scan(&ownerType, &ownerID, &titleLocale, &title, &sortTitle); err != nil {
			titleRows.Close()
			return GameMergePlan{}, err
		}
		digest.add("localized_title", ownerType, ownerID, titleLocale, title, sortTitle)
		if ownerType == "game" && ownerID == targetGameID {
			targetTitles[titleLocale] = title
		}
		if ownerType == "game" && ownerID == sourceGameID {
			sourceTitles[titleLocale] = title
			plan.SourceLocalizedTitles++
		}
	}
	if err = titleRows.Err(); err != nil {
		titleRows.Close()
		return GameMergePlan{}, err
	}
	titleRows.Close()
	for titleLocale := range sourceTitles {
		if _, exists := targetTitles[titleLocale]; exists {
			plan.CollidingLocalizedTitles++
		} else {
			plan.AddedLocalizedTitles++
		}
	}
	plan.TargetTitle = resolveTitle(locale, target.defaultTitle, targetTitles)
	plan.SourceTitle = resolveTitle(locale, source.defaultTitle, sourceTitles)

	editionRows, err := query.QueryContext(ctx, `
		SELECT id,game_id,default_title,edition_type,version,languages_json,author,save_namespace,serial,product_code,title_id,sort_order,created_at,updated_at
		FROM editions WHERE game_id IN (?,?) ORDER BY game_id,id`, targetGameID, sourceGameID)
	if err != nil {
		return GameMergePlan{}, err
	}
	for editionRows.Next() {
		var id, gameID, defaultTitle, editionType, version, languagesJSON, author, saveNamespace, serial, productCode, titleID, createdAt, updatedAt string
		var sortOrder int
		if err = editionRows.Scan(&id, &gameID, &defaultTitle, &editionType, &version, &languagesJSON, &author, &saveNamespace, &serial, &productCode, &titleID, &sortOrder, &createdAt, &updatedAt); err != nil {
			editionRows.Close()
			return GameMergePlan{}, err
		}
		digest.add("edition", id, gameID, defaultTitle, editionType, version, languagesJSON, author, saveNamespace, serial, productCode, titleID, sortOrder, createdAt, updatedAt)
		if gameID == targetGameID {
			plan.TargetEditions++
		} else {
			plan.SourceEditions++
		}
	}
	if err = editionRows.Err(); err != nil {
		editionRows.Close()
		return GameMergePlan{}, err
	}
	editionRows.Close()
	plan.ResultEditions = plan.TargetEditions + plan.SourceEditions

	artifactRows, err := query.QueryContext(ctx, `
		SELECT e.game_id,a.id,a.edition_id,a.path,a.role,a.disc_index,a.size,a.sha256,a.missing,a.created_at,a.updated_at,a.storage_kind,a.source_path,a.original_name
		FROM artifacts a JOIN editions e ON e.id=a.edition_id
		WHERE e.game_id IN (?,?) ORDER BY e.game_id,a.id`, targetGameID, sourceGameID)
	if err != nil {
		return GameMergePlan{}, err
	}
	for artifactRows.Next() {
		var gameID, id, editionID, path, role, sha256Value, createdAt, updatedAt, storageKind, sourcePath, originalName string
		var discIndex int
		var size int64
		var missing int
		if err = artifactRows.Scan(&gameID, &id, &editionID, &path, &role, &discIndex, &size, &sha256Value, &missing, &createdAt, &updatedAt, &storageKind, &sourcePath, &originalName); err != nil {
			artifactRows.Close()
			return GameMergePlan{}, err
		}
		digest.add("artifact", gameID, id, editionID, path, role, discIndex, size, sha256Value, missing, createdAt, updatedAt, storageKind, sourcePath, originalName)
		if gameID == sourceGameID {
			plan.SourceArtifacts++
		}
	}
	if err = artifactRows.Err(); err != nil {
		artifactRows.Close()
		return GameMergePlan{}, err
	}
	artifactRows.Close()

	mediaRows, err := query.QueryContext(ctx, `
		SELECT COALESCE(e.game_id,m.game_id),m.id,m.game_id,m.edition_id,m.kind,m.storage_kind,m.path,m.source_path,m.original_name,m.mime_type,m.size,m.sha256,m.locale,m.source_type,m.sort_order,m.created_at,m.content_status,m.content_checked_at
		FROM media_assets m LEFT JOIN editions e ON e.id=m.edition_id
		WHERE m.game_id IN (?,?) OR e.game_id IN (?,?)
		ORDER BY COALESCE(e.game_id,m.game_id),m.id`, targetGameID, sourceGameID, targetGameID, sourceGameID)
	if err != nil {
		return GameMergePlan{}, err
	}
	for mediaRows.Next() {
		var ownerGameID, id, kind, storageKind, path, sourcePath, originalName, mimeType, sha256Value, mediaLocale, sourceType, createdAt, contentStatus, contentCheckedAt string
		var gameID, editionID sql.NullString
		var size int64
		var sortOrder int
		if err = mediaRows.Scan(&ownerGameID, &id, &gameID, &editionID, &kind, &storageKind, &path, &sourcePath, &originalName, &mimeType, &size, &sha256Value, &mediaLocale, &sourceType, &sortOrder, &createdAt, &contentStatus, &contentCheckedAt); err != nil {
			mediaRows.Close()
			return GameMergePlan{}, err
		}
		digest.add("media", ownerGameID, id, nullableMergeString(gameID), nullableMergeString(editionID), kind, storageKind, path, sourcePath, originalName, mimeType, size, sha256Value, mediaLocale, sourceType, sortOrder, createdAt, contentStatus, contentCheckedAt)
		if ownerGameID == sourceGameID {
			if gameID.Valid {
				plan.SourceGameMedia++
			} else {
				plan.SourceEditionMedia++
			}
		}
	}
	if err = mediaRows.Err(); err != nil {
		mediaRows.Close()
		return GameMergePlan{}, err
	}
	mediaRows.Close()

	targetSeries := map[string]bool{}
	type membership struct {
		seriesID, gameID, relationType string
		sortOrder                      int
	}
	memberships := []membership{}
	seriesRows, err := query.QueryContext(ctx, `SELECT series_id,game_id,relation_type,sort_order FROM series_members WHERE game_id IN (?,?) ORDER BY series_id,game_id`, targetGameID, sourceGameID)
	if err != nil {
		return GameMergePlan{}, err
	}
	for seriesRows.Next() {
		var item membership
		if err = seriesRows.Scan(&item.seriesID, &item.gameID, &item.relationType, &item.sortOrder); err != nil {
			seriesRows.Close()
			return GameMergePlan{}, err
		}
		digest.add("series_member", item.seriesID, item.gameID, item.relationType, item.sortOrder)
		memberships = append(memberships, item)
		if item.gameID == targetGameID {
			targetSeries[item.seriesID] = true
		}
	}
	if err = seriesRows.Err(); err != nil {
		seriesRows.Close()
		return GameMergePlan{}, err
	}
	seriesRows.Close()
	for _, item := range memberships {
		if item.gameID != sourceGameID {
			continue
		}
		plan.SourceSeriesMemberships++
		if targetSeries[item.seriesID] {
			plan.SeriesCollisions++
		}
	}
	plan.SnapshotFingerprint = digest.sum()
	return plan, nil
}

func nullableMergeString(value sql.NullString) string {
	if !value.Valid {
		return "<null>"
	}
	return value.String
}
