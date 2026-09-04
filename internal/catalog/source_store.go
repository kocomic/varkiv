package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

func normalizeSourceInput(in NewLibrarySource) (NewLibrarySource, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Kind = strings.ToLower(strings.TrimSpace(in.Kind))
	in.SourceAdapterID = strings.TrimSpace(in.SourceAdapterID)
	in.Platform = strings.ToLower(strings.TrimSpace(in.Platform))
	in.MetadataLocale = strings.TrimSpace(in.MetadataLocale)
	in.ROMStoragePolicy = strings.ToLower(strings.TrimSpace(in.ROMStoragePolicy))
	in.MediaStoragePolicy = strings.ToLower(strings.TrimSpace(in.MediaStoragePolicy))
	if in.ROMStoragePolicy == "" {
		in.ROMStoragePolicy = "reference"
	}
	if in.MediaStoragePolicy == "" {
		in.MediaStoragePolicy = "copy"
	}
	cleanRelative := func(value string, allowRoot bool) (string, error) {
		value = filepath.ToSlash(strings.TrimSpace(value))
		if value == "" {
			return "", nil
		}
		if filepath.IsAbs(filepath.FromSlash(value)) || strings.HasPrefix(value, "/") {
			return "", errors.New("source paths must be relative to the library root")
		}
		value = filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
		if value == ".." || strings.HasPrefix(value, "../") {
			return "", errors.New("source paths must stay inside the library root")
		}
		if value == "." && !allowRoot {
			return "", errors.New("metadata_path must identify a file")
		}
		return value, nil
	}
	var err error
	if in.RootPath, err = cleanRelative(in.RootPath, true); err != nil {
		return in, err
	}
	if in.MetadataPath, err = cleanRelative(in.MetadataPath, false); err != nil {
		return in, err
	}
	if in.RuntimeMetadataPath, err = cleanRelative(in.RuntimeMetadataPath, false); err != nil {
		return in, fmt.Errorf("runtime_metadata_path: %w", err)
	}
	if in.Name == "" {
		return in, errors.New("name is required")
	}
	if in.Kind != "rom_directory" && in.Kind != "pegasus" && in.Kind != "esde" && in.Kind != "varkiv" {
		return in, errors.New("kind must be rom_directory, pegasus, esde, or varkiv")
	}
	if in.SourceAdapterID == "" {
		in.SourceAdapterID = BuiltinSourceAdapterID(in.Kind)
	}
	if in.Platform == "" && in.Kind != "varkiv" {
		return in, errors.New("platform is required unless kind is varkiv")
	}
	if in.Kind == "rom_directory" {
		if in.RootPath == "" {
			return in, errors.New("root_path is required for a ROM directory source")
		}
		in.MetadataPath = ""
		in.RuntimeMetadataPath = ""
	} else {
		if in.MetadataPath == "" {
			return in, errors.New("metadata_path is required for a metadata source")
		}
		if in.RootPath == "" || in.Kind == "varkiv" {
			in.RootPath = filepath.ToSlash(filepath.Dir(filepath.FromSlash(in.MetadataPath)))
		}
		if in.Kind != "esde" {
			in.RuntimeMetadataPath = ""
		}
	}
	if in.ROMStoragePolicy != "reference" && in.ROMStoragePolicy != "copy" {
		return in, errors.New("rom_storage_policy must be reference or copy")
	}
	if in.MediaStoragePolicy != "reference" && in.MediaStoragePolicy != "copy" && in.MediaStoragePolicy != "ignore" {
		return in, errors.New("media_storage_policy must be reference, copy, or ignore")
	}
	return in, nil
}

func BuiltinSourceAdapterID(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "rom_directory":
		return "builtin-source-direct-rom"
	case "pegasus":
		return "builtin-source-pegasus"
	case "esde":
		return "builtin-source-esde"
	case "varkiv":
		return "builtin-source-varkiv"
	default:
		return ""
	}
}

func (s *Store) validateSourceAdapter(ctx context.Context, in NewLibrarySource) error {
	adapter, err := s.GetSourceAdapter(ctx, in.SourceAdapterID)
	if err != nil {
		return fmt.Errorf("source_adapter_id: %w", err)
	}
	if !adapter.Enabled {
		return errors.New("source_adapter_id must reference an enabled adapter")
	}
	if adapter.Handler != in.Kind {
		return errors.New("source_adapter_id handler does not match source kind")
	}
	return nil
}

func (s *Store) CreateLibrarySource(ctx context.Context, in NewLibrarySource) (LibrarySource, error) {
	in, err := normalizeSourceInput(in)
	if err != nil {
		return LibrarySource{}, err
	}
	if err = s.validateSourceAdapter(ctx, in); err != nil {
		return LibrarySource{}, err
	}
	if in.ID == "" {
		in.ID = NewID()
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	now := nowText()
	_, err = s.db.ExecContext(ctx, `INSERT INTO library_sources(id,name,kind,source_adapter_id,root_path,metadata_path,runtime_metadata_path,platform,metadata_locale,rom_storage_policy,media_storage_policy,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		in.ID, in.Name, in.Kind, in.SourceAdapterID, in.RootPath, in.MetadataPath, in.RuntimeMetadataPath, in.Platform, in.MetadataLocale, in.ROMStoragePolicy, in.MediaStoragePolicy, boolInt(enabled), now, now)
	if err != nil {
		return LibrarySource{}, err
	}
	return s.GetLibrarySource(ctx, in.ID)
}

func scanLibrarySource(scanner interface{ Scan(...any) error }) (LibrarySource, error) {
	var source LibrarySource
	var enabled int
	var lastScan, created, updated string
	err := scanner.Scan(&source.ID, &source.Name, &source.Kind, &source.SourceAdapterID, &source.RootPath, &source.MetadataPath, &source.RuntimeMetadataPath, &source.Platform, &source.MetadataLocale, &source.ROMStoragePolicy, &source.MediaStoragePolicy, &enabled, &lastScan, &source.LastScanStatus, &source.LastError, &created, &updated)
	source.Enabled = enabled != 0
	source.LastScanAt, _ = time.Parse(time.RFC3339Nano, lastScan)
	source.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	source.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return source, err
}

const librarySourceColumns = `id,name,kind,source_adapter_id,root_path,metadata_path,runtime_metadata_path,platform,metadata_locale,rom_storage_policy,media_storage_policy,enabled,last_scan_at,last_scan_status,last_error,created_at,updated_at`

func (s *Store) GetLibrarySource(ctx context.Context, id string) (LibrarySource, error) {
	return scanLibrarySource(s.db.QueryRowContext(ctx, `SELECT `+librarySourceColumns+` FROM library_sources WHERE id=?`, id))
}

func (s *Store) ListLibrarySources(ctx context.Context) ([]LibrarySource, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+librarySourceColumns+` FROM library_sources ORDER BY enabled DESC,lower(name),id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LibrarySource{}
	for rows.Next() {
		item, scanErr := scanLibrarySource(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) UpdateLibrarySource(ctx context.Context, id string, in NewLibrarySource) (LibrarySource, error) {
	in, err := normalizeSourceInput(in)
	if err != nil {
		return LibrarySource{}, err
	}
	if err = s.validateSourceAdapter(ctx, in); err != nil {
		return LibrarySource{}, err
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	result, err := s.db.ExecContext(ctx, `UPDATE library_sources SET name=?,kind=?,source_adapter_id=?,root_path=?,metadata_path=?,runtime_metadata_path=?,platform=?,metadata_locale=?,rom_storage_policy=?,media_storage_policy=?,enabled=?,updated_at=? WHERE id=?`,
		in.Name, in.Kind, in.SourceAdapterID, in.RootPath, in.MetadataPath, in.RuntimeMetadataPath, in.Platform, in.MetadataLocale, in.ROMStoragePolicy, in.MediaStoragePolicy, boolInt(enabled), nowText(), id)
	if err != nil {
		return LibrarySource{}, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return LibrarySource{}, sql.ErrNoRows
	}
	return s.GetLibrarySource(ctx, id)
}

func (s *Store) DeleteLibrarySource(ctx context.Context, id string) error {
	var scans int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM source_scans WHERE source_id=?`, id).Scan(&scans); err != nil {
		return err
	}
	if scans > 0 {
		return ErrSourceHasScans
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM library_sources WHERE id=?`, id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) CreateSourceScan(ctx context.Context, in NewSourceScan) (SourceScan, error) {
	if in.SourceID == "" {
		return SourceScan{}, errors.New("source_id is required")
	}
	if in.ID == "" {
		in.ID = NewID()
	}
	if in.Status == "" {
		in.Status = "scanning"
	}
	requested := nowText()
	_, err := s.db.ExecContext(ctx, `INSERT INTO source_scans(id,source_id,status,requested_at,started_at,finished_at,expires_at,candidate_count,importable_count,missing_count,duplicate_count,conflict_count,preview_token_hash,failure_code,failure_detail) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		in.ID, in.SourceID, in.Status, requested, timeText(in.StartedAt), timeText(in.FinishedAt), timeText(in.ExpiresAt), in.CandidateCount, in.ImportableCount, in.MissingCount, in.DuplicateCount, in.ConflictCount, in.PreviewTokenHash, in.FailureCode, in.FailureDetail)
	if err != nil {
		return SourceScan{}, err
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE library_sources SET last_scan_at=?,last_scan_status=?,last_error=?,updated_at=? WHERE id=?`, requested, in.Status, in.FailureDetail, requested, in.SourceID)
	return s.GetSourceScan(ctx, in.ID)
}

func timeText(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func scanSourceScan(scanner interface{ Scan(...any) error }) (SourceScan, string, error) {
	var item SourceScan
	var requested, started, finished, expires, tokenHash string
	err := scanner.Scan(&item.ID, &item.SourceID, &item.Status, &requested, &started, &finished, &expires, &item.CandidateCount, &item.ImportableCount, &item.MissingCount, &item.DuplicateCount, &item.ConflictCount, &tokenHash, &item.FailureCode, &item.FailureDetail)
	item.RequestedAt, _ = time.Parse(time.RFC3339Nano, requested)
	item.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
	item.FinishedAt, _ = time.Parse(time.RFC3339Nano, finished)
	item.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expires)
	return item, tokenHash, err
}

const sourceScanColumns = `id,source_id,status,requested_at,started_at,finished_at,expires_at,candidate_count,importable_count,missing_count,duplicate_count,conflict_count,preview_token_hash,failure_code,failure_detail`

func (s *Store) GetSourceScan(ctx context.Context, id string) (SourceScan, error) {
	item, _, err := scanSourceScan(s.db.QueryRowContext(ctx, `SELECT `+sourceScanColumns+` FROM source_scans WHERE id=?`, id))
	return item, err
}

func (s *Store) SourceScanTokenHash(ctx context.Context, id string) (string, error) {
	_, tokenHash, err := scanSourceScan(s.db.QueryRowContext(ctx, `SELECT `+sourceScanColumns+` FROM source_scans WHERE id=?`, id))
	return tokenHash, err
}

func (s *Store) ListSourceScans(ctx context.Context, sourceID string) ([]SourceScan, error) {
	query, args := `SELECT `+sourceScanColumns+` FROM source_scans`, []any{}
	if sourceID != "" {
		query += ` WHERE source_id=?`
		args = append(args, sourceID)
	}
	query += ` ORDER BY requested_at DESC,id DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SourceScan{}
	for rows.Next() {
		item, _, scanErr := scanSourceScan(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) UpdateSourceScanStatus(ctx context.Context, id, status, failureCode, failureDetail string) (SourceScan, error) {
	finished := ""
	if status == "committed" || status == "failed" || status == "stale" {
		finished = nowText()
	}
	result, err := s.db.ExecContext(ctx, `UPDATE source_scans SET status=?,finished_at=CASE WHEN ?<>'' THEN ? ELSE finished_at END,failure_code=?,failure_detail=? WHERE id=?`, status, finished, finished, failureCode, failureDetail, id)
	if err != nil {
		return SourceScan{}, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return SourceScan{}, sql.ErrNoRows
	}
	item, err := s.GetSourceScan(ctx, id)
	if err == nil {
		_, _ = s.db.ExecContext(ctx, `UPDATE library_sources SET last_scan_at=?,last_scan_status=?,last_error=?,updated_at=? WHERE id=?`, nowText(), status, failureDetail, nowText(), item.SourceID)
	}
	return item, err
}
