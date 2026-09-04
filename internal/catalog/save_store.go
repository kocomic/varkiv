package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"varkiv/internal/portablepath"
)

func normalizeSaveStream(in NewSaveStream) (NewSaveStream, error) {
	in.OwnerType = strings.TrimSpace(in.OwnerType)
	in.OwnerKey = strings.TrimSpace(in.OwnerKey)
	in.DriverID = strings.TrimSpace(in.DriverID)
	in.CompatibilityGroupID = strings.TrimSpace(in.CompatibilityGroupID)
	if in.DriverID == "" {
		in.DriverID = "manual"
	}
	if in.OwnerType != "edition" && in.OwnerType != "platform" && in.OwnerType != "container" {
		return in, errors.New("owner_type must be edition, platform, or container")
	}
	if in.OwnerKey == "" {
		return in, errors.New("owner_key is required")
	}
	if in.Portability == "" {
		in.Portability = "driver-dependent"
	}
	if in.Portability != "portable" && in.Portability != "core-dependent" && in.Portability != "driver-dependent" && in.Portability != "device-dependent" {
		return in, errors.New("invalid save stream portability")
	}
	if in.Compatibility == "" {
		in.Compatibility = "native"
	}
	if in.Compatibility != "native" && in.Compatibility != "verified" && in.Compatibility != "manual" {
		return in, errors.New("invalid save stream compatibility")
	}
	seen := map[string]bool{}
	editions := make([]string, 0, len(in.EditionIDs)+1)
	if in.OwnerType == "edition" {
		editions = append(editions, in.OwnerKey)
		seen[in.OwnerKey] = true
	}
	for _, id := range in.EditionIDs {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			editions = append(editions, id)
		}
	}
	in.EditionIDs = editions
	return in, nil
}

func (s *Store) CreateSaveStream(ctx context.Context, in NewSaveStream) (SaveStream, error) {
	in, err := normalizeSaveStream(in)
	if err != nil {
		return SaveStream{}, err
	}
	if in.ID == "" {
		in.ID = NewID()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SaveStream{}, err
	}
	defer tx.Rollback()
	if in.Namespace == "" {
		if in.OwnerType == "edition" {
			if err = tx.QueryRowContext(ctx, `SELECT save_namespace FROM editions WHERE id=?`, in.OwnerKey).Scan(&in.Namespace); err != nil {
				return SaveStream{}, err
			}
			in.Namespace += ":" + in.DriverID
		} else {
			in.Namespace = in.OwnerType + ":" + in.OwnerKey + ":" + in.DriverID
		}
	}
	now := nowText()
	if _, err = tx.ExecContext(ctx, `INSERT INTO save_streams(id,namespace,owner_type,owner_key,driver_id,portability,compatibility_group_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, in.ID, in.Namespace, in.OwnerType, in.OwnerKey, in.DriverID, in.Portability, nullIfEmpty(in.CompatibilityGroupID), now, now); err != nil {
		return SaveStream{}, err
	}
	for _, editionID := range in.EditionIDs {
		if _, err = tx.ExecContext(ctx, `INSERT INTO save_stream_editions(stream_id,edition_id,compatibility,created_at) VALUES(?,?,?,?)`, in.ID, editionID, in.Compatibility, now); err != nil {
			return SaveStream{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return SaveStream{}, err
	}
	return s.GetSaveStream(ctx, in.ID)
}

func scanSaveStream(scanner interface{ Scan(...any) error }) (SaveStream, error) {
	var item SaveStream
	var created, updated string
	err := scanner.Scan(&item.ID, &item.Namespace, &item.OwnerType, &item.OwnerKey, &item.DriverID, &item.Portability, &item.CompatibilityGroupID, &created, &updated)
	item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return item, err
}

const saveStreamColumns = `id,namespace,owner_type,owner_key,driver_id,portability,COALESCE(compatibility_group_id,''),created_at,updated_at`

func (s *Store) loadSaveStreamEditions(ctx context.Context, item *SaveStream) error {
	rows, err := s.db.QueryContext(ctx, `SELECT edition_id,compatibility,created_at FROM save_stream_editions WHERE stream_id=? ORDER BY edition_id`, item.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	item.Editions = []SaveStreamEdition{}
	for rows.Next() {
		var relation SaveStreamEdition
		var created string
		if err = rows.Scan(&relation.EditionID, &relation.Compatibility, &created); err != nil {
			return err
		}
		relation.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		item.Editions = append(item.Editions, relation)
	}
	return rows.Err()
}

func (s *Store) GetSaveStream(ctx context.Context, id string) (SaveStream, error) {
	item, err := scanSaveStream(s.db.QueryRowContext(ctx, `SELECT `+saveStreamColumns+` FROM save_streams WHERE id=?`, id))
	if err != nil {
		return SaveStream{}, err
	}
	if err = s.loadSaveStreamEditions(ctx, &item); err != nil {
		return SaveStream{}, err
	}
	return item, nil
}

func (s *Store) ListSaveStreams(ctx context.Context, editionID string) ([]SaveStream, error) {
	query := `SELECT ` + saveStreamColumns + ` FROM save_streams`
	args := []any{}
	if editionID != "" {
		query += ` WHERE id IN (SELECT stream_id FROM save_stream_editions WHERE edition_id=?)`
		args = append(args, editionID)
	}
	query += ` ORDER BY created_at,id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var item SaveStream
		var created, updated string
		if err = rows.Scan(&item.ID, &item.Namespace, &item.OwnerType, &item.OwnerKey, &item.DriverID, &item.Portability, &item.CompatibilityGroupID, &created, &updated); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, item.ID)
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	out := make([]SaveStream, 0, len(ids))
	for _, id := range ids {
		item, getErr := s.GetSaveStream(ctx, id)
		if getErr != nil {
			return nil, getErr
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *Store) ResolveSaveStream(ctx context.Context, editionID, driverID, scopeType, scopeKey string) (SaveStream, error) {
	if driverID == "" {
		driverID = "manual"
	}
	ownerType, ownerKey := scopeType, strings.TrimSpace(scopeKey)
	if scopeType == "" || scopeType == "game" {
		ownerType, ownerKey = "edition", editionID
	}
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT s.id FROM save_streams s JOIN save_stream_editions se ON se.stream_id=s.id WHERE s.owner_type=? AND s.owner_key=? AND s.driver_id=? AND se.edition_id=?`, ownerType, ownerKey, driverID, editionID).Scan(&id)
	if err != nil {
		return SaveStream{}, err
	}
	return s.GetSaveStream(ctx, id)
}

func cleanSaveLogicalPath(value string) (string, error) {
	return portablepath.CleanSaveLogical(value)
}

func addSaveRevisionSize(total, size int64) (int64, error) {
	if total < 0 || size < 0 || total > portablepath.MaxSaveRevisionBytes || size > portablepath.MaxSaveRevisionBytes-total {
		return 0, errors.New("save revision exceeds the aggregate size limit")
	}
	return total + size, nil
}

func revisionContentHash(files []NewSaveFile) string {
	ordered := append([]NewSaveFile(nil), files...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].LogicalPath < ordered[j].LogicalPath })
	h := sha256.New()
	for _, file := range ordered {
		h.Write([]byte(file.LogicalPath))
		h.Write([]byte{0})
		h.Write([]byte(file.Checksum))
		h.Write([]byte{0})
		h.Write([]byte(strconv.FormatInt(file.Size, 10)))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (s *Store) ensureLegacyStreamTx(ctx context.Context, tx *sql.Tx, in NewSaveRevision) (string, error) {
	if in.StreamID != "" {
		var id string
		err := tx.QueryRowContext(ctx, `SELECT id FROM save_streams WHERE id=?`, in.StreamID).Scan(&id)
		return id, err
	}
	if in.EditionID == "" {
		return "", errors.New("edition_id or stream_id is required")
	}
	driverID := strings.TrimSpace(in.DriverID)
	if driverID == "" {
		driverID = "manual"
	}
	scopeType := in.ScopeType
	if scopeType == "" {
		scopeType = "game"
	}
	ownerType, ownerKey := scopeType, strings.TrimSpace(in.ScopeKey)
	if scopeType == "game" {
		ownerType, ownerKey = "edition", in.EditionID
	}
	if ownerType != "edition" && ownerType != "platform" && ownerType != "container" || ownerKey == "" {
		return "", errors.New("scope_type must be game, platform, or container and scope_key is required for shared scopes")
	}
	var streamID string
	err := tx.QueryRowContext(ctx, `SELECT id FROM save_streams WHERE owner_type=? AND owner_key=? AND driver_id=?`, ownerType, ownerKey, driverID).Scan(&streamID)
	if err == nil {
		_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO save_stream_editions(stream_id,edition_id,compatibility,created_at) VALUES(?,?,?,?)`, streamID, in.EditionID, "native", nowText())
		return streamID, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	streamID = NewID()
	namespace := ownerType + ":" + ownerKey + ":" + driverID
	if ownerType == "edition" {
		if err = tx.QueryRowContext(ctx, `SELECT save_namespace FROM editions WHERE id=?`, in.EditionID).Scan(&namespace); err != nil {
			return "", err
		}
		namespace += ":" + driverID
	}
	now := nowText()
	if _, err = tx.ExecContext(ctx, `INSERT INTO save_streams(id,namespace,owner_type,owner_key,driver_id,portability,compatibility_group_id,created_at,updated_at) VALUES(?,?,?,?,?,'driver-dependent',NULL,?,?)`, streamID, namespace, ownerType, ownerKey, driverID, now, now); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO save_stream_editions(stream_id,edition_id,compatibility,created_at) VALUES(?,?,?,?)`, streamID, in.EditionID, "native", now); err != nil {
		return "", err
	}
	return streamID, nil
}

func (s *Store) AddSaveRevision(ctx context.Context, in NewSaveRevision) (SaveRevision, error) {
	if in.DeviceID == "" {
		return SaveRevision{}, errors.New("device_id is required")
	}
	if len(in.Files) == 0 {
		in.Files = []NewSaveFile{{LogicalPath: in.RelativePath, Checksum: in.Checksum, Size: in.Size, BlobPath: in.BlobPath}}
	}
	if len(in.Files) == 0 || len(in.Files) > portablepath.MaxSaveRevisionFiles {
		return SaveRevision{}, errors.New("a revision must contain 1..4096 files")
	}
	seen := map[string]bool{}
	var total int64
	for index := range in.Files {
		file := &in.Files[index]
		var err error
		file.LogicalPath, err = cleanSaveLogicalPath(file.LogicalPath)
		if err != nil {
			return SaveRevision{}, err
		}
		if seen[file.LogicalPath] {
			return SaveRevision{}, errors.New("save revision contains a duplicate logical path")
		}
		seen[file.LogicalPath] = true
		if strings.TrimSpace(file.Checksum) == "" || strings.TrimSpace(file.BlobPath) == "" || file.Size < 0 {
			return SaveRevision{}, errors.New("each save file requires checksum, non-negative size, and blob_path")
		}
		if file.ID == "" {
			file.ID = NewID()
		}
		total, err = addSaveRevisionSize(total, file.Size)
		if err != nil {
			return SaveRevision{}, err
		}
	}
	if in.ContentHash == "" {
		in.ContentHash = revisionContentHash(in.Files)
	}
	if in.ID == "" {
		in.ID = NewID()
	}
	if in.ParentRevisionID == "" {
		in.ParentRevisionID = in.BaseRevisionID
	}
	if in.Status == "" {
		if in.Conflict {
			in.Status = "conflict"
		} else {
			in.Status = "current"
		}
	}
	if in.Status != "current" && in.Status != "superseded" && in.Status != "conflict" && in.Status != "quarantined" {
		return SaveRevision{}, errors.New("invalid save revision status")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SaveRevision{}, err
	}
	defer tx.Rollback()
	streamID, err := s.ensureLegacyStreamTx(ctx, tx, in)
	if err != nil {
		return SaveRevision{}, err
	}
	if in.Status == "current" {
		if _, err = tx.ExecContext(ctx, `UPDATE save_revisions SET status='superseded' WHERE stream_id=? AND status='current'`, streamID); err != nil {
			return SaveRevision{}, err
		}
	}
	now := nowText()
	if _, err = tx.ExecContext(ctx, `INSERT INTO save_revisions(id,stream_id,device_id,parent_revision_id,content_hash,total_size,file_count,status,created_at) VALUES(?,?,?,NULLIF(?,''),?,?,?,?,?)`, in.ID, streamID, in.DeviceID, in.ParentRevisionID, in.ContentHash, total, len(in.Files), in.Status, now); err != nil {
		return SaveRevision{}, err
	}
	for _, file := range in.Files {
		if _, err = tx.ExecContext(ctx, `INSERT INTO save_files(id,revision_id,logical_path,checksum,size,blob_path,mtime_ns,mode) VALUES(?,?,?,?,?,?,?,?)`, file.ID, in.ID, file.LogicalPath, file.Checksum, file.Size, file.BlobPath, file.MTimeNS, file.Mode); err != nil {
			return SaveRevision{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE devices SET last_seen_at=?,updated_at=? WHERE id=?`, now, now, in.DeviceID); err != nil {
		return SaveRevision{}, err
	}
	if err = tx.Commit(); err != nil {
		return SaveRevision{}, err
	}
	return s.GetSaveRevision(ctx, in.ID)
}

func (s *Store) loadSaveFiles(ctx context.Context, revision *SaveRevision) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id,revision_id,logical_path,checksum,size,blob_path,mtime_ns,mode FROM save_files WHERE revision_id=? ORDER BY logical_path,id`, revision.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	revision.Files = []SaveFile{}
	for rows.Next() {
		var file SaveFile
		if err = rows.Scan(&file.ID, &file.RevisionID, &file.LogicalPath, &file.Checksum, &file.Size, &file.BlobPath, &file.MTimeNS, &file.Mode); err != nil {
			return err
		}
		revision.Files = append(revision.Files, file)
	}
	return rows.Err()
}

func (s *Store) GetSaveFile(ctx context.Context, revisionID, fileID string) (SaveFile, error) {
	var file SaveFile
	err := s.db.QueryRowContext(ctx, `SELECT id,revision_id,logical_path,checksum,size,blob_path,mtime_ns,mode FROM save_files WHERE revision_id=? AND id=?`, revisionID, fileID).Scan(&file.ID, &file.RevisionID, &file.LogicalPath, &file.Checksum, &file.Size, &file.BlobPath, &file.MTimeNS, &file.Mode)
	return file, err
}

func (s *Store) hydrateRevisionCompatibility(ctx context.Context, revision *SaveRevision) error {
	stream, err := s.GetSaveStream(ctx, revision.StreamID)
	if err != nil {
		return err
	}
	revision.DriverID = stream.DriverID
	if stream.OwnerType == "edition" {
		revision.EditionID, revision.ScopeType = stream.OwnerKey, "game"
	} else {
		revision.ScopeType, revision.ScopeKey = stream.OwnerType, stream.OwnerKey
		if len(stream.Editions) > 0 {
			revision.EditionID = stream.Editions[0].EditionID
		}
	}
	revision.BaseRevisionID = revision.ParentRevisionID
	revision.Conflict = revision.Status == "conflict"
	if len(revision.Files) == 1 {
		file := revision.Files[0]
		revision.RelativePath, revision.Checksum, revision.Size, revision.BlobPath = file.LogicalPath, file.Checksum, file.Size, file.BlobPath
	}
	return nil
}

func (s *Store) GetSaveRevision(ctx context.Context, id string) (SaveRevision, error) {
	var item SaveRevision
	var parent sql.NullString
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT id,stream_id,device_id,parent_revision_id,content_hash,total_size,file_count,status,created_at FROM save_revisions WHERE id=?`, id).Scan(&item.ID, &item.StreamID, &item.DeviceID, &parent, &item.ContentHash, &item.TotalSize, &item.FileCount, &item.Status, &created)
	if err != nil {
		return SaveRevision{}, err
	}
	item.ParentRevisionID = parent.String
	item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	if err = s.loadSaveFiles(ctx, &item); err != nil {
		return SaveRevision{}, err
	}
	if err = s.hydrateRevisionCompatibility(ctx, &item); err != nil {
		return SaveRevision{}, err
	}
	return item, nil
}

func (s *Store) LatestStreamRevision(ctx context.Context, streamID string) (SaveRevision, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM save_revisions WHERE stream_id=? AND status<>'quarantined' ORDER BY created_at DESC,id DESC LIMIT 1`, streamID).Scan(&id)
	if err != nil {
		return SaveRevision{}, err
	}
	return s.GetSaveRevision(ctx, id)
}

func (s *Store) CurrentStreamRevision(ctx context.Context, streamID string) (SaveRevision, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM save_revisions WHERE stream_id=? AND status='current' ORDER BY created_at DESC,id DESC LIMIT 1`, streamID).Scan(&id)
	if err != nil {
		return SaveRevision{}, err
	}
	return s.GetSaveRevision(ctx, id)
}

func (s *Store) FindStreamRevisionByContentHash(ctx context.Context, streamID, contentHash string) (SaveRevision, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM save_revisions WHERE stream_id=? AND content_hash=? AND status<>'quarantined' ORDER BY created_at DESC,id DESC LIMIT 1`, streamID, contentHash).Scan(&id)
	if err != nil {
		return SaveRevision{}, err
	}
	return s.GetSaveRevision(ctx, id)
}

func (s *Store) ListStreamRevisions(ctx context.Context, streamID string) ([]SaveRevision, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM save_revisions WHERE stream_id=? ORDER BY created_at DESC,id DESC`, streamID)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	items := make([]SaveRevision, 0, len(ids))
	for _, id := range ids {
		item, getErr := s.GetSaveRevision(ctx, id)
		if getErr != nil {
			return nil, getErr
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Store) LatestSaveRevision(ctx context.Context, editionID, driverID, scopeType, scopeKey, relativePath string) (SaveRevision, error) {
	if driverID == "" {
		driverID = "manual"
	}
	ownerType, ownerKey := scopeType, strings.TrimSpace(scopeKey)
	if scopeType == "" || scopeType == "game" {
		ownerType, ownerKey = "edition", editionID
	}
	var streamID string
	err := s.db.QueryRowContext(ctx, `SELECT s.id FROM save_streams s JOIN save_stream_editions se ON se.stream_id=s.id WHERE s.owner_type=? AND s.owner_key=? AND s.driver_id=? AND se.edition_id=?`, ownerType, ownerKey, driverID, editionID).Scan(&streamID)
	if err != nil {
		return SaveRevision{}, err
	}
	query := `SELECT r.id FROM save_revisions r WHERE r.stream_id=? AND r.status<>'quarantined'`
	args := []any{streamID}
	if strings.TrimSpace(relativePath) != "" {
		cleaned, cleanErr := cleanSaveLogicalPath(relativePath)
		if cleanErr != nil {
			return SaveRevision{}, cleanErr
		}
		query += ` AND EXISTS(SELECT 1 FROM save_files f WHERE f.revision_id=r.id AND f.logical_path=?)`
		args = append(args, cleaned)
	}
	query += ` ORDER BY r.created_at DESC,r.id DESC LIMIT 1`
	var id string
	if err = s.db.QueryRowContext(ctx, query, args...).Scan(&id); err != nil {
		return SaveRevision{}, err
	}
	return s.GetSaveRevision(ctx, id)
}

func (s *Store) ListSaveRevisions(ctx context.Context, editionID string) ([]SaveRevision, error) {
	query := `SELECT DISTINCT r.id FROM save_revisions r`
	args := []any{}
	if editionID != "" {
		query += ` JOIN save_stream_editions se ON se.stream_id=r.stream_id WHERE se.edition_id=?`
		args = append(args, editionID)
	}
	query += ` ORDER BY r.created_at DESC,r.id DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	out := make([]SaveRevision, 0, len(ids))
	for _, id := range ids {
		item, getErr := s.GetSaveRevision(ctx, id)
		if getErr != nil {
			return nil, getErr
		}
		out = append(out, item)
	}
	return out, nil
}
