package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"varkiv/internal/hashpack"
)

var ErrHashReleaseConflict = errors.New("the source release already exists with different content")

type HashSource struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Publisher        string    `json:"publisher,omitempty"`
	License          string    `json:"license"`
	TrustLevel       string    `json:"trust_level"`
	ActiveReleaseID  string    `json:"active_release_id,omitempty"`
	ActiveVersion    string    `json:"active_version,omitempty"`
	ActivePackSHA256 string    `json:"active_pack_sha256,omitempty"`
	RecordCount      int       `json:"record_count"`
	ReleaseCount     int       `json:"release_count"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type HashRelease struct {
	ID            string    `json:"id"`
	SourceID      string    `json:"source_id"`
	Version       string    `json:"version"`
	FormatVersion int       `json:"format_version"`
	PackID        string    `json:"pack_id"`
	PackSHA256    string    `json:"pack_sha256"`
	RecordsSHA256 string    `json:"records_sha256"`
	RecordCount   int       `json:"record_count"`
	SourceName    string    `json:"source_name"`
	Publisher     string    `json:"publisher,omitempty"`
	License       string    `json:"license"`
	Active        bool      `json:"active"`
	ImportedAt    time.Time `json:"imported_at"`
}

type HashPackPreview struct {
	Source          hashpack.Source `json:"source"`
	Release         string          `json:"release"`
	PackID          string          `json:"pack_id"`
	RecordCount     int             `json:"record_count"`
	NewCount        int             `json:"new_count"`
	ExistingCount   int             `json:"existing_count"`
	ConflictCount   int             `json:"conflict_count"`
	ExistingRelease bool            `json:"existing_release"`
	ReleaseConflict bool            `json:"release_conflict"`
}

type HashPackImportResult struct {
	Source          HashSource  `json:"source"`
	Release         HashRelease `json:"release"`
	ImportedRecords int         `json:"imported_records"`
	ExistingRelease bool        `json:"existing_release"`
}

func hashRecordEqual(left, right hashpack.Record) bool {
	// GameKey is scoped to its publisher and is not a cross-source identity field.
	left.GameKey = ""
	right.GameKey = ""
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return string(leftJSON) == string(rightJSON)
}

func scanStoredHashRecord(scanner interface{ Scan(...any) error }) (hashpack.Record, error) {
	var record hashpack.Record
	var gameTitles, editionTitles, languages string
	err := scanner.Scan(&record.SHA256, &record.Size, &record.CRC32, &record.MD5, &record.Platform, &record.GameKey,
		&record.GameDefaultTitle, &gameTitles, &record.EditionDefaultTitle, &editionTitles, &record.EditionType,
		&record.Version, &languages, &record.Author, &record.Region, &record.Serial, &record.ProductCode,
		&record.TitleID, &record.Role, &record.DiscIndex, &record.ParentSHA256)
	if err != nil {
		return record, err
	}
	_ = json.Unmarshal([]byte(gameTitles), &record.GameTitles)
	_ = json.Unmarshal([]byte(editionTitles), &record.EditionTitles)
	_ = json.Unmarshal([]byte(languages), &record.Languages)
	return record, nil
}

const storedHashRecordColumns = `i.sha256,i.size,i.crc32,i.md5,i.platform,i.game_key,i.game_default_title,i.game_titles_json,i.edition_default_title,i.edition_titles_json,i.edition_type,i.version,i.languages_json,i.author,i.region,i.serial,i.product_code,i.title_id,i.role,i.disc_index,i.parent_sha256`

func (s *Store) PreviewHashPack(ctx context.Context, pack hashpack.Pack, packSHA256 string) (HashPackPreview, error) {
	preview := HashPackPreview{Source: pack.Manifest.Source, Release: pack.Manifest.Release, PackID: pack.Manifest.PackID, RecordCount: len(pack.Records)}
	var existingPackID string
	err := s.db.QueryRowContext(ctx, `SELECT pack_id FROM hash_releases WHERE source_id=? AND version=?`, pack.Manifest.Source.ID, pack.Manifest.Release).Scan(&existingPackID)
	switch {
	case err == nil:
		preview.ExistingRelease = existingPackID == pack.Manifest.PackID
		preview.ReleaseConflict = existingPackID != pack.Manifest.PackID
	case !errors.Is(err, sql.ErrNoRows):
		return preview, err
	}
	storedBySHA := make(map[string][]hashpack.Record)
	const queryChunk = 400
	for start := 0; start < len(pack.Records); start += queryChunk {
		end := min(start+queryChunk, len(pack.Records))
		arguments := make([]any, 0, end-start)
		placeholders := make([]string, 0, end-start)
		for _, record := range pack.Records[start:end] {
			arguments = append(arguments, record.SHA256)
			placeholders = append(placeholders, "?")
		}
		rows, queryErr := s.db.QueryContext(ctx, `SELECT `+storedHashRecordColumns+` FROM hash_identities i JOIN hash_releases r ON r.id=i.release_id WHERE i.sha256 IN (`+strings.Join(placeholders, ",")+`) AND r.active=1`, arguments...)
		if queryErr != nil {
			return preview, queryErr
		}
		for rows.Next() {
			stored, scanErr := scanStoredHashRecord(rows)
			if scanErr != nil {
				rows.Close()
				return preview, scanErr
			}
			storedBySHA[stored.SHA256] = append(storedBySHA[stored.SHA256], stored)
		}
		if queryErr = rows.Close(); queryErr != nil {
			return preview, queryErr
		}
	}
	for _, record := range pack.Records {
		stored := storedBySHA[record.SHA256]
		found, conflict := len(stored) > 0, false
		for _, candidate := range stored {
			if !hashRecordEqual(candidate, record) {
				conflict = true
			}
		}
		switch {
		case conflict:
			preview.ConflictCount++
		case found:
			preview.ExistingCount++
		default:
			preview.NewCount++
		}
	}
	return preview, nil
}

func (s *Store) ImportHashPack(ctx context.Context, pack hashpack.Pack, packSHA256 string) (HashPackImportResult, error) {
	if !hashpack.IsSHA256(packSHA256) {
		return HashPackImportResult{}, errors.New("pack SHA-256 must contain 64 lower-case hexadecimal characters")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return HashPackImportResult{}, err
	}
	defer tx.Rollback()
	var existingReleaseID, existingPackID string
	err = tx.QueryRowContext(ctx, `SELECT id,pack_id FROM hash_releases WHERE source_id=? AND version=?`, pack.Manifest.Source.ID, pack.Manifest.Release).Scan(&existingReleaseID, &existingPackID)
	if err == nil {
		if existingPackID != pack.Manifest.PackID {
			return HashPackImportResult{}, ErrHashReleaseConflict
		}
		if err = tx.Commit(); err != nil {
			return HashPackImportResult{}, err
		}
		source, getErr := s.GetHashSource(ctx, pack.Manifest.Source.ID)
		if getErr != nil {
			return HashPackImportResult{}, getErr
		}
		release, getErr := s.GetHashRelease(ctx, existingReleaseID)
		return HashPackImportResult{Source: source, Release: release, ExistingRelease: true}, getErr
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return HashPackImportResult{}, err
	}
	now := nowText()
	_, err = tx.ExecContext(ctx, `INSERT INTO hash_sources(id,name,publisher,license,trust_level,created_at,updated_at) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name,publisher=excluded.publisher,license=excluded.license,updated_at=excluded.updated_at`,
		pack.Manifest.Source.ID, pack.Manifest.Source.Name, pack.Manifest.Source.Publisher, pack.Manifest.Source.License, "imported", now, now)
	if err != nil {
		return HashPackImportResult{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE hash_releases SET active=0 WHERE source_id=? AND active=1`, pack.Manifest.Source.ID); err != nil {
		return HashPackImportResult{}, err
	}
	releaseID := NewID()
	_, err = tx.ExecContext(ctx, `INSERT INTO hash_releases(id,source_id,version,format_version,pack_id,pack_sha256,records_sha256,record_count,source_name,publisher,license,active,imported_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,1,?)`,
		releaseID, pack.Manifest.Source.ID, pack.Manifest.Release, pack.Manifest.FormatVersion, pack.Manifest.PackID, packSHA256, pack.Manifest.RecordsSHA256, len(pack.Records), pack.Manifest.Source.Name, pack.Manifest.Source.Publisher, pack.Manifest.Source.License, now)
	if err != nil {
		return HashPackImportResult{}, err
	}
	statement, err := tx.PrepareContext(ctx, `INSERT INTO hash_identities(id,release_id,source_id,sha256,size,crc32,md5,platform,game_key,game_default_title,game_titles_json,edition_default_title,edition_titles_json,edition_type,version,languages_json,author,region,serial,product_code,title_id,role,disc_index,parent_sha256,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return HashPackImportResult{}, err
	}
	defer statement.Close()
	for _, record := range pack.Records {
		if _, err = statement.ExecContext(ctx, NewID(), releaseID, pack.Manifest.Source.ID, record.SHA256, record.Size, record.CRC32, record.MD5, record.Platform, record.GameKey,
			record.GameDefaultTitle, jsonText(record.GameTitles, "{}"), record.EditionDefaultTitle, jsonText(record.EditionTitles, "{}"), record.EditionType,
			record.Version, jsonText(record.Languages, "[]"), record.Author, record.Region, record.Serial, record.ProductCode, record.TitleID, record.Role, record.DiscIndex, record.ParentSHA256, now); err != nil {
			return HashPackImportResult{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return HashPackImportResult{}, err
	}
	source, err := s.GetHashSource(ctx, pack.Manifest.Source.ID)
	if err != nil {
		return HashPackImportResult{}, err
	}
	release, err := s.GetHashRelease(ctx, releaseID)
	return HashPackImportResult{Source: source, Release: release, ImportedRecords: len(pack.Records)}, err
}

func scanHashSource(scanner interface{ Scan(...any) error }) (HashSource, error) {
	var item HashSource
	var created, updated string
	err := scanner.Scan(&item.ID, &item.Name, &item.Publisher, &item.License, &item.TrustLevel, &item.ActiveReleaseID, &item.ActiveVersion, &item.ActivePackSHA256, &item.RecordCount, &item.ReleaseCount, &created, &updated)
	item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return item, err
}

const hashSourceSelect = `SELECT s.id,s.name,s.publisher,s.license,s.trust_level,COALESCE(r.id,''),COALESCE(r.version,''),COALESCE(r.pack_sha256,''),COALESCE(r.record_count,0),(SELECT COUNT(*) FROM hash_releases x WHERE x.source_id=s.id),s.created_at,s.updated_at FROM hash_sources s LEFT JOIN hash_releases r ON r.source_id=s.id AND r.active=1`

func (s *Store) GetHashSource(ctx context.Context, id string) (HashSource, error) {
	return scanHashSource(s.db.QueryRowContext(ctx, hashSourceSelect+` WHERE s.id=?`, id))
}

func (s *Store) ListHashSources(ctx context.Context) ([]HashSource, error) {
	rows, err := s.db.QueryContext(ctx, hashSourceSelect+` ORDER BY lower(s.name),s.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []HashSource{}
	for rows.Next() {
		item, scanErr := scanHashSource(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanHashRelease(scanner interface{ Scan(...any) error }) (HashRelease, error) {
	var item HashRelease
	var active int
	var imported string
	err := scanner.Scan(&item.ID, &item.SourceID, &item.Version, &item.FormatVersion, &item.PackID, &item.PackSHA256, &item.RecordsSHA256, &item.RecordCount, &item.SourceName, &item.Publisher, &item.License, &active, &imported)
	item.Active = active != 0
	item.ImportedAt, _ = time.Parse(time.RFC3339Nano, imported)
	return item, err
}

const hashReleaseColumns = `id,source_id,version,format_version,pack_id,pack_sha256,records_sha256,record_count,source_name,publisher,license,active,imported_at`

func (s *Store) GetHashRelease(ctx context.Context, id string) (HashRelease, error) {
	return scanHashRelease(s.db.QueryRowContext(ctx, `SELECT `+hashReleaseColumns+` FROM hash_releases WHERE id=?`, id))
}

func (s *Store) LocalHashPackRecords(ctx context.Context) ([]hashpack.Record, error) {
	titleRows, err := s.db.QueryContext(ctx, `SELECT owner_type,owner_id,locale,title FROM localized_titles WHERE owner_type IN ('game','edition') ORDER BY owner_type,owner_id,locale`)
	if err != nil {
		return nil, err
	}
	titles := map[string]map[string]string{}
	for titleRows.Next() {
		var ownerType, ownerID, locale, title string
		if err = titleRows.Scan(&ownerType, &ownerID, &locale, &title); err != nil {
			titleRows.Close()
			return nil, err
		}
		key := ownerType + "\x00" + ownerID
		if titles[key] == nil {
			titles[key] = map[string]string{}
		}
		titles[key][locale] = title
	}
	if err = titleRows.Close(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT a.sha256,a.size,g.id,g.platform,g.default_title,e.id,e.default_title,e.edition_type,e.version,e.languages_json,e.author,e.serial,e.product_code,e.title_id,a.role,a.disc_index
		FROM artifacts a JOIN editions e ON e.id=a.edition_id JOIN games g ON g.id=e.game_id
		WHERE a.missing=0 AND a.size>0 AND length(a.sha256)=64 AND lower(a.sha256)=a.sha256 AND a.role IN ('rom','disc','executable')
		ORDER BY g.platform,lower(g.default_title),e.sort_order,a.disc_index,a.sha256`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := []hashpack.Record{}
	for rows.Next() {
		var record hashpack.Record
		var gameID, editionID, languages string
		if err = rows.Scan(&record.SHA256, &record.Size, &gameID, &record.Platform, &record.GameDefaultTitle, &editionID, &record.EditionDefaultTitle, &record.EditionType, &record.Version, &languages, &record.Author, &record.Serial, &record.ProductCode, &record.TitleID, &record.Role, &record.DiscIndex); err != nil {
			return nil, err
		}
		gameKeySum := sha256.Sum256([]byte("varkiv-game-key-v1\x00" + gameID))
		record.GameKey = hex.EncodeToString(gameKeySum[:])
		record.GameTitles = titles["game\x00"+gameID]
		record.EditionTitles = titles["edition\x00"+editionID]
		_ = json.Unmarshal([]byte(languages), &record.Languages)
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *Store) ResolveHashIdentities(ctx context.Context, sha256Value string) ([]hashpack.Record, error) {
	sha256Value = strings.ToLower(strings.TrimSpace(sha256Value))
	if !hashpack.IsSHA256(sha256Value) {
		return nil, errors.New("sha256 must contain 64 lower-case hexadecimal characters")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+storedHashRecordColumns+` FROM hash_identities i JOIN hash_releases r ON r.id=i.release_id WHERE i.sha256=? AND r.active=1 ORDER BY i.source_id`, sha256Value)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []hashpack.Record{}
	for rows.Next() {
		item, scanErr := scanStoredHashRecord(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func hashPackGeneratedAt() time.Time { return time.Now().UTC() }

func (s *Store) ExportHashPack(ctx context.Context, source hashpack.Source, release string) ([]byte, hashpack.Manifest, error) {
	records, err := s.LocalHashPackRecords(ctx)
	if err != nil {
		return nil, hashpack.Manifest{}, err
	}
	if len(records) == 0 {
		return nil, hashpack.Manifest{}, fmt.Errorf("no present ROM with a valid SHA-256 is available to export")
	}
	return hashpack.Encode(source, release, hashPackGeneratedAt(), records)
}
