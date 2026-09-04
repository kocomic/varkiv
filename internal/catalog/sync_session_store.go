package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

var ErrIdempotencyConflict = errors.New("idempotency key was already used with a different request")
var ErrOperationHashMismatch = errors.New("sync operation content hash mismatch")
var ErrInventoryMatchStale = errors.New("inventory match review is stale")
var ErrInventoryMatchNotAmbiguous = errors.New("inventory item is not ambiguous")

func normalizeInventoryItem(in NewInventoryItem) (NewInventoryItem, error) {
	in.ClientItemID = strings.TrimSpace(in.ClientItemID)
	in.PlatformID = strings.ToLower(strings.TrimSpace(in.PlatformID))
	in.SHA256 = strings.ToLower(strings.TrimSpace(in.SHA256))
	in.Serial = strings.TrimSpace(in.Serial)
	in.ProductCode = strings.TrimSpace(in.ProductCode)
	in.TitleID = strings.TrimSpace(in.TitleID)
	if in.ClientItemID == "" || len(in.ClientItemID) > 256 {
		return in, errors.New("client_item_id is required and must be at most 256 characters")
	}
	if in.PlatformID == "" {
		return in, errors.New("platform_id is required")
	}
	if in.Size < 0 {
		return in, errors.New("inventory size must be zero or greater")
	}
	if in.SHA256 != "" {
		decoded, err := hex.DecodeString(in.SHA256)
		if err != nil || len(decoded) != 32 {
			return in, errors.New("inventory sha256 must be 64 hexadecimal characters")
		}
	}
	return in, nil
}

type inventoryQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func inventoryCandidatesWith(ctx context.Context, queryer inventoryQueryer, platformID, method, value string) ([]string, error) {
	var query string
	switch method {
	case "sha256":
		query = `SELECT DISTINCT e.id FROM artifacts a JOIN editions e ON e.id=a.edition_id JOIN games g ON g.id=e.game_id WHERE lower(a.sha256)=lower(?) AND a.missing=0 AND g.platform=? ORDER BY e.id`
	case "serial":
		query = `SELECT e.id FROM editions e JOIN games g ON g.id=e.game_id WHERE upper(e.serial)=upper(?) AND g.platform=? ORDER BY e.id`
	case "product_code":
		query = `SELECT e.id FROM editions e JOIN games g ON g.id=e.game_id WHERE upper(e.product_code)=upper(?) AND g.platform=? ORDER BY e.id`
	case "title_id":
		query = `SELECT e.id FROM editions e JOIN games g ON g.id=e.game_id WHERE upper(e.title_id)=upper(?) AND g.platform=? ORDER BY e.id`
	default:
		return nil, errors.New("unsupported inventory match method")
	}
	rows, err := queryer.QueryContext(ctx, query, value, platformID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		items = append(items, id)
	}
	return items, rows.Err()
}

func (s *Store) inventoryCandidates(ctx context.Context, platformID, method, value string) ([]string, error) {
	return inventoryCandidatesWith(ctx, s.db, platformID, method, value)
}

func inventoryIdentifierValue(in NewInventoryItem, method string) string {
	switch method {
	case "sha256":
		return in.SHA256
	case "serial":
		return in.Serial
	case "product_code":
		return in.ProductCode
	case "title_id":
		return in.TitleID
	}
	return ""
}

func inventoryIdentityHashNormalized(in NewInventoryItem) string {
	payload, _ := json.Marshal(struct {
		PlatformID  string `json:"platform_id"`
		SHA256      string `json:"sha256"`
		Serial      string `json:"serial"`
		ProductCode string `json:"product_code"`
		TitleID     string `json:"title_id"`
		Size        int64  `json:"size"`
	}{
		PlatformID: in.PlatformID, SHA256: in.SHA256, Serial: strings.ToUpper(in.Serial),
		ProductCode: strings.ToUpper(in.ProductCode), TitleID: strings.ToUpper(in.TitleID), Size: in.Size,
	})
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func inventoryCandidateHash(candidateIDs []string) string {
	canonical := append([]string(nil), candidateIDs...)
	sort.Strings(canonical)
	payload, _ := json.Marshal(canonical)
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

// InventoryIdentityHash binds a confirmation to content and public emulator
// identifiers without retaining a local ROM name or path.
func InventoryIdentityHash(in NewInventoryItem) (string, error) {
	normalized, err := normalizeInventoryItem(in)
	if err != nil {
		return "", err
	}
	return inventoryIdentityHashNormalized(normalized), nil
}

func (s *Store) matchInventoryItemCanonical(ctx context.Context, in NewInventoryItem) (NewInventoryItem, []string, error) {
	in, err := normalizeInventoryItem(in)
	if err != nil {
		return in, nil, err
	}
	checks := []struct{ method, value string }{
		{"sha256", in.SHA256},
		{"serial", in.Serial},
		{"product_code", in.ProductCode},
		{"title_id", in.TitleID},
	}
	for _, check := range checks {
		if check.value == "" {
			continue
		}
		candidates, queryErr := s.inventoryCandidates(ctx, in.PlatformID, check.method, check.value)
		if queryErr != nil {
			return in, nil, queryErr
		}
		if len(candidates) == 1 {
			in.MatchStatus, in.MatchedEditionID, in.MatchMethod = "matched", candidates[0], check.method
			return in, candidates, nil
		}
		if len(candidates) > 1 {
			in.MatchStatus, in.MatchMethod = "ambiguous", check.method
			return in, candidates, nil
		}
	}
	in.MatchStatus = "unmatched"
	return in, nil, nil
}

// MatchInventoryItem deliberately returns ambiguity at the first identifier
// tier that has candidates. It never guesses using a weaker identifier.
func (s *Store) MatchInventoryItem(ctx context.Context, in NewInventoryItem) (NewInventoryItem, error) {
	matched, _, err := s.matchInventoryItemCanonical(ctx, in)
	return matched, err
}

// MatchInventoryItemForDevice applies a prior confirmation only when the same
// opaque client item, exact inventory identity, platform, match tier, and
// candidate set are still valid. A unique canonical match always wins.
func (s *Store) MatchInventoryItemForDevice(ctx context.Context, deviceID string, in NewInventoryItem) (NewInventoryItem, error) {
	matched, candidates, err := s.matchInventoryItemCanonical(ctx, in)
	if err != nil || matched.MatchStatus != "ambiguous" {
		return matched, err
	}
	override, err := s.GetInventoryMatchOverrideForClient(ctx, strings.TrimSpace(deviceID), matched.ClientItemID)
	if errors.Is(err, sql.ErrNoRows) {
		return matched, nil
	}
	if err != nil {
		return matched, err
	}
	identityHash := inventoryIdentityHashNormalized(matched)
	if override.PlatformID != matched.PlatformID || override.IdentityHash != identityHash || override.CandidateHash != inventoryCandidateHash(candidates) || override.MatchMethod != matched.MatchMethod {
		return matched, nil
	}
	for _, candidate := range candidates {
		if candidate == override.EditionID {
			matched.MatchStatus = "matched"
			matched.MatchedEditionID = override.EditionID
			matched.MatchMethod = "confirmed_" + override.MatchMethod
			return matched, nil
		}
	}
	return matched, nil
}

func validSyncStatus(value string) bool {
	switch value {
	case "proposed", "negotiating", "transferring", "verifying", "complete", "partial", "aborted", "failed":
		return true
	}
	return false
}

func validOperation(action, status string) bool {
	if action != "upload" && action != "download" && action != "conflict" && action != "noop" {
		return false
	}
	return status == "proposed" || status == "transferring" || status == "verified" || status == "complete" || status == "failed" || status == "skipped"
}

func (s *Store) CreateSyncSession(ctx context.Context, in NewSyncSession) (SyncSession, bool, error) {
	in.DeviceID = strings.TrimSpace(in.DeviceID)
	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	if in.DeviceID == "" || len(in.IdempotencyKey) < 8 || len(in.IdempotencyKey) > 128 || strings.ContainsAny(in.IdempotencyKey, "\x00\r\n") {
		return SyncSession{}, false, errors.New("device_id and an idempotency key of 8 to 128 characters are required")
	}
	if in.Status == "" {
		in.Status = "proposed"
	}
	if !validSyncStatus(in.Status) {
		return SyncSession{}, false, errors.New("invalid sync session status")
	}
	var existingID, existingFingerprint string
	err := s.db.QueryRowContext(ctx, `SELECT id,base_manifest_hash FROM sync_sessions WHERE device_id=? AND idempotency_key=?`, in.DeviceID, in.IdempotencyKey).Scan(&existingID, &existingFingerprint)
	if err == nil {
		if existingFingerprint != in.BaseManifestHash {
			return SyncSession{}, false, ErrIdempotencyConflict
		}
		item, getErr := s.GetSyncSession(ctx, existingID)
		return item, true, getErr
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return SyncSession{}, false, err
	}
	if in.ID == "" {
		in.ID = NewID()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SyncSession{}, false, err
	}
	defer tx.Rollback()
	now := nowText()
	if _, err = tx.ExecContext(ctx, `INSERT INTO sync_sessions(id,device_id,idempotency_key,status,base_manifest_hash,operation_plan_hash,uploaded_count,downloaded_count,conflict_count,failure_code,created_at,updated_at,finished_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, in.ID, in.DeviceID, in.IdempotencyKey, in.Status, in.BaseManifestHash, in.OperationPlanHash, 0, 0, 0, "", now, now, ""); err != nil {
		return SyncSession{}, false, err
	}
	for index := range in.Inventory {
		item := in.Inventory[index]
		if item.ID == "" {
			item.ID = NewID()
		}
		if item.MatchStatus != "matched" && item.MatchStatus != "ambiguous" && item.MatchStatus != "unmatched" {
			return SyncSession{}, false, errors.New("invalid inventory match status")
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO inventory_items(id,session_id,client_item_id,platform_id,sha256,serial,product_code,title_id,size,match_status,matched_edition_id,match_method,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, item.ID, in.ID, item.ClientItemID, item.PlatformID, item.SHA256, item.Serial, item.ProductCode, item.TitleID, item.Size, item.MatchStatus, nullIfEmpty(item.MatchedEditionID), item.MatchMethod, now); err != nil {
			return SyncSession{}, false, err
		}
	}
	for index := range in.Operations {
		op := in.Operations[index]
		if op.ID == "" {
			op.ID = NewID()
		}
		if op.Status == "" {
			op.Status = "proposed"
		}
		if !validOperation(op.Action, op.Status) {
			return SyncSession{}, false, errors.New("invalid sync operation")
		}
		detail, _ := json.Marshal(op.Detail)
		if _, err = tx.ExecContext(ctx, `INSERT INTO sync_operations(id,session_id,stream_id,action,status,base_revision_id,target_revision_id,expected_hash,actual_hash,bytes,failure_code,detail_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, op.ID, in.ID, op.StreamID, op.Action, op.Status, op.BaseRevisionID, op.TargetRevisionID, op.ExpectedHash, "", op.Bytes, "", string(detail), now, now); err != nil {
			return SyncSession{}, false, err
		}
	}
	if err = tx.Commit(); err != nil {
		return SyncSession{}, false, err
	}
	item, err := s.GetSyncSession(ctx, in.ID)
	return item, false, err
}

func nullIfEmpty(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func scanSyncSession(scanner interface{ Scan(...any) error }) (SyncSession, error) {
	var item SyncSession
	var created, updated, finished string
	err := scanner.Scan(&item.ID, &item.DeviceID, &item.IdempotencyKey, &item.Status, &item.BaseManifestHash, &item.OperationPlanHash, &item.UploadedCount, &item.DownloadedCount, &item.ConflictCount, &item.FailureCode, &created, &updated, &finished)
	item.CreatedAt, item.UpdatedAt, item.FinishedAt = parseTime(created), parseTime(updated), parseTime(finished)
	return item, err
}

const syncSessionColumns = `id,device_id,idempotency_key,status,base_manifest_hash,operation_plan_hash,uploaded_count,downloaded_count,conflict_count,failure_code,created_at,updated_at,finished_at`

func (s *Store) GetSyncSession(ctx context.Context, id string) (SyncSession, error) {
	item, err := scanSyncSession(s.db.QueryRowContext(ctx, `SELECT `+syncSessionColumns+` FROM sync_sessions WHERE id=?`, id))
	if err != nil {
		return SyncSession{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,session_id,stream_id,action,status,base_revision_id,target_revision_id,expected_hash,actual_hash,bytes,failure_code,detail_json,created_at,updated_at FROM sync_operations WHERE session_id=? ORDER BY id`, id)
	if err != nil {
		return SyncSession{}, err
	}
	defer rows.Close()
	item.Operations = []SyncOperation{}
	for rows.Next() {
		var op SyncOperation
		var detail, created, updated string
		if err = rows.Scan(&op.ID, &op.SessionID, &op.StreamID, &op.Action, &op.Status, &op.BaseRevisionID, &op.TargetRevisionID, &op.ExpectedHash, &op.ActualHash, &op.Bytes, &op.FailureCode, &detail, &created, &updated); err != nil {
			return SyncSession{}, err
		}
		_ = json.Unmarshal([]byte(detail), &op.Detail)
		if op.Detail == nil {
			op.Detail = map[string]any{}
		}
		op.CreatedAt, op.UpdatedAt = parseTime(created), parseTime(updated)
		item.Operations = append(item.Operations, op)
	}
	return item, rows.Err()
}

func (s *Store) ListSyncSessions(ctx context.Context, deviceID string) ([]SyncSession, error) {
	query := `SELECT id FROM sync_sessions`
	args := []any{}
	if strings.TrimSpace(deviceID) != "" {
		query += ` WHERE device_id=?`
		args = append(args, strings.TrimSpace(deviceID))
	}
	query += ` ORDER BY created_at DESC,id DESC LIMIT 200`
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
	items := make([]SyncSession, 0, len(ids))
	for _, id := range ids {
		item, getErr := s.GetSyncSession(ctx, id)
		if getErr != nil {
			return nil, getErr
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Store) ListInventoryItems(ctx context.Context, sessionID string) ([]InventoryItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,session_id,client_item_id,platform_id,sha256,serial,product_code,title_id,size,match_status,COALESCE(matched_edition_id,''),match_method,created_at FROM inventory_items WHERE session_id=? ORDER BY client_item_id,id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []InventoryItem{}
	for rows.Next() {
		var item InventoryItem
		var created string
		if err = rows.Scan(&item.ID, &item.SessionID, &item.ClientItemID, &item.PlatformID, &item.SHA256, &item.Serial, &item.ProductCode, &item.TitleID, &item.Size, &item.MatchStatus, &item.MatchedEditionID, &item.MatchMethod, &created); err != nil {
			return nil, err
		}
		item.CreatedAt = parseTime(created)
		items = append(items, item)
	}
	return items, rows.Err()
}

func inventoryItemAsNew(item InventoryItem) NewInventoryItem {
	return NewInventoryItem{
		ID: item.ID, ClientItemID: item.ClientItemID, PlatformID: item.PlatformID, SHA256: item.SHA256,
		Serial: item.Serial, ProductCode: item.ProductCode, TitleID: item.TitleID, Size: item.Size,
		MatchStatus: item.MatchStatus, MatchedEditionID: item.MatchedEditionID, MatchMethod: item.MatchMethod,
	}
}

func (s *Store) GetInventoryReviewItem(ctx context.Context, sessionID, inventoryItemID string) (InventoryReviewItem, error) {
	var review InventoryReviewItem
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT i.id,i.session_id,i.client_item_id,i.platform_id,i.sha256,i.serial,i.product_code,i.title_id,i.size,i.match_status,COALESCE(i.matched_edition_id,''),i.match_method,i.created_at,s.device_id,d.name
		FROM inventory_items i JOIN sync_sessions s ON s.id=i.session_id JOIN devices d ON d.id=s.device_id
		WHERE i.session_id=? AND i.id=?`, strings.TrimSpace(sessionID), strings.TrimSpace(inventoryItemID)).Scan(
		&review.ID, &review.SessionID, &review.ClientItemID, &review.PlatformID, &review.SHA256, &review.Serial,
		&review.ProductCode, &review.TitleID, &review.Size, &review.MatchStatus, &review.MatchedEditionID,
		&review.MatchMethod, &created, &review.DeviceID, &review.DeviceName,
	)
	review.CreatedAt = parseTime(created)
	return review, err
}

func (s *Store) ListAmbiguousInventoryReviewItems(ctx context.Context) ([]InventoryReviewItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT i.id,i.session_id,i.client_item_id,i.platform_id,i.sha256,i.serial,i.product_code,i.title_id,i.size,i.match_status,COALESCE(i.matched_edition_id,''),i.match_method,i.created_at,s.device_id,d.name
		FROM inventory_items i JOIN sync_sessions s ON s.id=i.session_id JOIN devices d ON d.id=s.device_id
		WHERE i.match_status='ambiguous' ORDER BY i.created_at DESC,i.id DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []InventoryReviewItem{}
	for rows.Next() {
		var item InventoryReviewItem
		var created string
		if err = rows.Scan(&item.ID, &item.SessionID, &item.ClientItemID, &item.PlatformID, &item.SHA256, &item.Serial, &item.ProductCode, &item.TitleID, &item.Size, &item.MatchStatus, &item.MatchedEditionID, &item.MatchMethod, &created, &item.DeviceID, &item.DeviceName); err != nil {
			return nil, err
		}
		item.CreatedAt = parseTime(created)
		items = append(items, item)
	}
	return items, rows.Err()
}

// ReviewInventoryMatch re-evaluates the strongest identifier tier against the
// current catalog. Candidate IDs are returned only when ambiguity still exists.
func (s *Store) ReviewInventoryMatch(ctx context.Context, sessionID, inventoryItemID string) (InventoryReviewItem, []string, error) {
	review, err := s.GetInventoryReviewItem(ctx, sessionID, inventoryItemID)
	if err != nil {
		return InventoryReviewItem{}, nil, err
	}
	if review.MatchStatus != "ambiguous" {
		return review, nil, ErrInventoryMatchNotAmbiguous
	}
	matched, candidates, err := s.matchInventoryItemCanonical(ctx, inventoryItemAsNew(review.InventoryItem))
	if err != nil {
		return review, nil, err
	}
	if matched.MatchStatus != "ambiguous" || matched.MatchMethod != review.MatchMethod || len(candidates) < 2 {
		return review, nil, ErrInventoryMatchStale
	}
	return review, candidates, nil
}

func scanInventoryMatchOverride(scanner interface{ Scan(...any) error }) (InventoryMatchOverride, error) {
	var item InventoryMatchOverride
	var created, updated string
	err := scanner.Scan(&item.ID, &item.DeviceID, &item.ClientItemID, &item.PlatformID, &item.IdentityHash, &item.CandidateHash, &item.EditionID, &item.MatchMethod, &item.SourceSessionID, &item.SourceInventoryItemID, &created, &updated)
	item.CreatedAt, item.UpdatedAt = parseTime(created), parseTime(updated)
	return item, err
}

const inventoryMatchOverrideColumns = `id,device_id,client_item_id,platform_id,identity_hash,candidate_hash,edition_id,match_method,source_session_id,source_inventory_item_id,created_at,updated_at`

func (s *Store) GetInventoryMatchOverrideForClient(ctx context.Context, deviceID, clientItemID string) (InventoryMatchOverride, error) {
	return scanInventoryMatchOverride(s.db.QueryRowContext(ctx, `SELECT `+inventoryMatchOverrideColumns+` FROM inventory_match_overrides WHERE device_id=? AND client_item_id=?`, strings.TrimSpace(deviceID), strings.TrimSpace(clientItemID)))
}

func validInventoryMatchMethod(method string) bool {
	return method == "sha256" || method == "serial" || method == "product_code" || method == "title_id"
}

// ConfirmInventoryMatchOverride rechecks the inventory row and candidate set in
// the same transaction that writes the override. It never reads ROM bytes.
func (s *Store) ConfirmInventoryMatchOverride(ctx context.Context, in NewInventoryMatchOverride) (InventoryMatchOverride, error) {
	in.DeviceID, in.ClientItemID = strings.TrimSpace(in.DeviceID), strings.TrimSpace(in.ClientItemID)
	in.PlatformID, in.IdentityHash = strings.ToLower(strings.TrimSpace(in.PlatformID)), strings.ToLower(strings.TrimSpace(in.IdentityHash))
	in.CandidateIDs = append([]string(nil), in.CandidateIDs...)
	for index := range in.CandidateIDs {
		in.CandidateIDs[index] = strings.TrimSpace(in.CandidateIDs[index])
	}
	sort.Strings(in.CandidateIDs)
	in.EditionID, in.MatchMethod = strings.TrimSpace(in.EditionID), strings.TrimSpace(in.MatchMethod)
	in.SourceSessionID, in.SourceInventoryItemID = strings.TrimSpace(in.SourceSessionID), strings.TrimSpace(in.SourceInventoryItemID)
	decoded, decodeErr := hex.DecodeString(in.IdentityHash)
	if in.DeviceID == "" || in.ClientItemID == "" || in.PlatformID == "" || in.EditionID == "" || in.SourceSessionID == "" || in.SourceInventoryItemID == "" || len(in.CandidateIDs) < 2 || decodeErr != nil || len(decoded) != sha256.Size || !validInventoryMatchMethod(in.MatchMethod) {
		return InventoryMatchOverride{}, errors.New("invalid inventory match confirmation")
	}
	if in.ID == "" {
		in.ID = NewID()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return InventoryMatchOverride{}, err
	}
	defer tx.Rollback()
	var item NewInventoryItem
	var sessionDeviceID string
	err = tx.QueryRowContext(ctx, `SELECT i.id,i.client_item_id,i.platform_id,i.sha256,i.serial,i.product_code,i.title_id,i.size,i.match_status,i.match_method,s.device_id
		FROM inventory_items i JOIN sync_sessions s ON s.id=i.session_id WHERE i.session_id=? AND i.id=?`, in.SourceSessionID, in.SourceInventoryItemID).Scan(
		&item.ID, &item.ClientItemID, &item.PlatformID, &item.SHA256, &item.Serial, &item.ProductCode, &item.TitleID, &item.Size, &item.MatchStatus, &item.MatchMethod, &sessionDeviceID,
	)
	if err != nil {
		return InventoryMatchOverride{}, err
	}
	item, err = normalizeInventoryItem(item)
	if err != nil {
		return InventoryMatchOverride{}, err
	}
	if item.MatchStatus != "ambiguous" || sessionDeviceID != in.DeviceID || item.ClientItemID != in.ClientItemID || item.PlatformID != in.PlatformID || item.MatchMethod != in.MatchMethod || inventoryIdentityHashNormalized(item) != in.IdentityHash {
		return InventoryMatchOverride{}, ErrInventoryMatchStale
	}
	value := inventoryIdentifierValue(item, item.MatchMethod)
	candidates, err := inventoryCandidatesWith(ctx, tx, item.PlatformID, item.MatchMethod, value)
	if err != nil {
		return InventoryMatchOverride{}, err
	}
	selected := false
	for _, candidate := range candidates {
		selected = selected || candidate == in.EditionID
	}
	if len(candidates) < 2 || !selected {
		return InventoryMatchOverride{}, ErrInventoryMatchStale
	}
	sort.Strings(candidates)
	if inventoryCandidateHash(candidates) != inventoryCandidateHash(in.CandidateIDs) {
		return InventoryMatchOverride{}, ErrInventoryMatchStale
	}
	var candidatePlatform string
	if err = tx.QueryRowContext(ctx, `SELECT g.platform FROM editions e JOIN games g ON g.id=e.game_id WHERE e.id=?`, in.EditionID).Scan(&candidatePlatform); err != nil {
		return InventoryMatchOverride{}, err
	}
	if candidatePlatform != in.PlatformID {
		return InventoryMatchOverride{}, ErrPlatformMismatch
	}
	now := nowText()
	candidateHash := inventoryCandidateHash(candidates)
	_, err = tx.ExecContext(ctx, `INSERT INTO inventory_match_overrides(id,device_id,client_item_id,platform_id,identity_hash,candidate_hash,edition_id,match_method,source_session_id,source_inventory_item_id,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(device_id,client_item_id) DO UPDATE SET platform_id=excluded.platform_id,identity_hash=excluded.identity_hash,candidate_hash=excluded.candidate_hash,edition_id=excluded.edition_id,match_method=excluded.match_method,source_session_id=excluded.source_session_id,source_inventory_item_id=excluded.source_inventory_item_id,updated_at=excluded.updated_at`,
		in.ID, in.DeviceID, in.ClientItemID, in.PlatformID, in.IdentityHash, candidateHash, in.EditionID, in.MatchMethod, in.SourceSessionID, in.SourceInventoryItemID, now, now)
	if err != nil {
		return InventoryMatchOverride{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE inventory_items SET match_status='matched',matched_edition_id=?,match_method=? WHERE session_id=? AND id=? AND match_status='ambiguous'`, in.EditionID, "confirmed_"+in.MatchMethod, in.SourceSessionID, in.SourceInventoryItemID); err != nil {
		return InventoryMatchOverride{}, err
	}
	if err = tx.Commit(); err != nil {
		return InventoryMatchOverride{}, err
	}
	itemOverride, err := s.GetInventoryMatchOverrideForClient(ctx, in.DeviceID, in.ClientItemID)
	if err != nil {
		return InventoryMatchOverride{}, fmt.Errorf("read confirmed inventory match: %w", err)
	}
	return itemOverride, nil
}

func (s *Store) GetSyncOperation(ctx context.Context, sessionID, operationID string) (SyncOperation, error) {
	var op SyncOperation
	var detail, created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT id,session_id,stream_id,action,status,base_revision_id,target_revision_id,expected_hash,actual_hash,bytes,failure_code,detail_json,created_at,updated_at FROM sync_operations WHERE session_id=? AND id=?`, sessionID, operationID).Scan(&op.ID, &op.SessionID, &op.StreamID, &op.Action, &op.Status, &op.BaseRevisionID, &op.TargetRevisionID, &op.ExpectedHash, &op.ActualHash, &op.Bytes, &op.FailureCode, &detail, &created, &updated)
	_ = json.Unmarshal([]byte(detail), &op.Detail)
	op.CreatedAt, op.UpdatedAt = parseTime(created), parseTime(updated)
	return op, err
}

func (s *Store) CompleteSyncOperation(ctx context.Context, sessionID, operationID, actualHash, targetRevisionID string, bytes int64) (SyncOperation, error) {
	op, err := s.GetSyncOperation(ctx, sessionID, operationID)
	if err != nil {
		return SyncOperation{}, err
	}
	if op.Status == "complete" {
		if op.ActualHash != actualHash || op.TargetRevisionID != targetRevisionID {
			return SyncOperation{}, ErrIdempotencyConflict
		}
		return op, nil
	}
	if op.Status == "failed" || op.Status == "skipped" {
		return SyncOperation{}, ErrIdempotencyConflict
	}
	if op.ExpectedHash != "" && op.ExpectedHash != actualHash {
		_, _ = s.db.ExecContext(ctx, `UPDATE sync_operations SET status='failed',actual_hash=?,failure_code='hash_mismatch',updated_at=? WHERE session_id=? AND id=?`, actualHash, nowText(), sessionID, operationID)
		return SyncOperation{}, ErrOperationHashMismatch
	}
	_, err = s.db.ExecContext(ctx, `UPDATE sync_operations SET status='complete',actual_hash=?,target_revision_id=?,bytes=?,updated_at=? WHERE session_id=? AND id=?`, actualHash, targetRevisionID, bytes, nowText(), sessionID, operationID)
	if err != nil {
		return SyncOperation{}, err
	}
	return s.GetSyncOperation(ctx, sessionID, operationID)
}

func (s *Store) FailSyncOperation(ctx context.Context, sessionID, operationID, failureCode, actualHash string) (SyncOperation, error) {
	if strings.TrimSpace(failureCode) == "" {
		return SyncOperation{}, errors.New("failure_code is required")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE sync_operations SET status='failed',failure_code=?,actual_hash=?,updated_at=? WHERE session_id=? AND id=? AND status<>'complete'`, failureCode, actualHash, nowText(), sessionID, operationID)
	if err != nil {
		return SyncOperation{}, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return SyncOperation{}, ErrIdempotencyConflict
	}
	return s.GetSyncOperation(ctx, sessionID, operationID)
}

func (s *Store) RecalculateSyncSession(ctx context.Context, id string) (SyncSession, error) {
	var pending, uploads, downloads, conflicts, failures int
	err := s.db.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN status NOT IN ('complete','failed','skipped') THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN action='upload' AND status='complete' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN action='download' AND status='complete' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN action='conflict' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN status='failed' THEN 1 ELSE 0 END),0)
		FROM sync_operations WHERE session_id=?`, id).Scan(&pending, &uploads, &downloads, &conflicts, &failures)
	if err != nil {
		return SyncSession{}, err
	}
	status, finished, failureCode := "transferring", "", ""
	if pending == 0 {
		finished = nowText()
		if failures > 0 {
			status, failureCode = "partial", "operation_failed"
		} else {
			status = "complete"
		}
	}
	_, err = s.db.ExecContext(ctx, `UPDATE sync_sessions SET status=?,uploaded_count=?,downloaded_count=?,conflict_count=?,failure_code=?,updated_at=?,finished_at=? WHERE id=?`, status, uploads, downloads, conflicts, failureCode, nowText(), finished, id)
	if err != nil {
		return SyncSession{}, err
	}
	return s.GetSyncSession(ctx, id)
}
