package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

var runtimeHintIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var ErrRuntimeHintBatchStale = errors.New("runtime hint batch review is stale")
var ErrRuntimeHintBatchConflict = errors.New("runtime hint batch conflicts with an existing launch binding")

func normalizeRuntimeImportHint(in NewRuntimeImportHint) (NewRuntimeImportHint, error) {
	in.EditionID = strings.TrimSpace(in.EditionID)
	in.SourceKind = strings.ToLower(strings.TrimSpace(in.SourceKind))
	in.SourceFormat = strings.ToLower(strings.TrimSpace(in.SourceFormat))
	in.DeviceProfileID = strings.TrimSpace(in.DeviceProfileID)
	in.FrontendAdapterID = strings.TrimSpace(in.FrontendAdapterID)
	in.DriverID = strings.TrimSpace(in.DriverID)
	in.CoreID = strings.TrimSpace(in.CoreID)
	in.RawCommand = strings.TrimSpace(in.RawCommand)
	in.SourceRef = filepath.ToSlash(strings.TrimSpace(in.SourceRef))
	if in.EditionID == "" || in.SourceFormat == "" {
		return in, errors.New("edition_id and source_format are required")
	}
	for name, value := range map[string]string{
		"device_profile_id":   in.DeviceProfileID,
		"frontend_adapter_id": in.FrontendAdapterID,
		"driver_id":           in.DriverID,
		"core_id":             in.CoreID,
	} {
		if value != "" && !runtimeHintIDPattern.MatchString(value) {
			return in, fmt.Errorf("%s is not a portable catalog identifier", name)
		}
	}
	if len(in.SourceFormat) > 128 || strings.ContainsAny(in.SourceFormat, "\x00\r\n") {
		return in, errors.New("source_format is invalid")
	}
	if in.SourceRef != "" {
		clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(in.SourceRef)))
		if filepath.IsAbs(filepath.FromSlash(in.SourceRef)) || clean == ".." || strings.HasPrefix(clean, "../") || clean == "." || strings.ContainsAny(clean, "\x00\r\n") || len(clean) > 1024 {
			return in, errors.New("source_ref must be a portable path inside the library root")
		}
		in.SourceRef = clean
	}
	if err := ValidateLaunchArguments(in.Arguments); err != nil {
		return in, err
	}
	switch in.SourceKind {
	case "structured-sidecar":
		in.Trust = "structured"
		if in.RawCommand != "" {
			return in, errors.New("structured runtime hints cannot contain raw commands")
		}
	case "pegasus-command", "esde-system":
		in.Trust = "untrusted"
		if in.RawCommand == "" {
			return in, errors.New("untrusted runtime hint requires a raw command")
		}
	default:
		return in, errors.New("source_kind must be structured-sidecar, pegasus-command, or esde-system")
	}
	if len(in.RawCommand) > 8192 || strings.ContainsRune(in.RawCommand, '\x00') {
		return in, errors.New("raw command exceeds the inert import limit or contains NUL")
	}
	return in, nil
}

func insertRuntimeImportHintTx(ctx context.Context, tx *sql.Tx, in NewRuntimeImportHint) error {
	var err error
	in, err = suggestUntrustedRuntimeHintTx(ctx, tx, in)
	if err != nil {
		return err
	}
	in, err = normalizeRuntimeImportHint(in)
	if err != nil {
		return err
	}
	if in.ID == "" {
		in.ID = NewID()
	}
	now := nowText()
	_, err = tx.ExecContext(ctx, `INSERT INTO runtime_import_hints(id,edition_id,source_kind,source_format,device_profile_id,frontend_adapter_id,driver_id,core_id,arguments_json,raw_command,trust,status,source_ref,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,'pending',?,?,?)`,
		in.ID, in.EditionID, in.SourceKind, in.SourceFormat, in.DeviceProfileID, in.FrontendAdapterID, in.DriverID, in.CoreID, jsonText(in.Arguments, "[]"), in.RawCommand, in.Trust, in.SourceRef, now, now)
	return err
}

// suggestUntrustedRuntimeHintTx recognizes only exact executable/core basenames
// already present in the enabled runtime catalog. The original command remains
// inert and untrusted, no arguments are copied, and ambiguous matches stay
// blank so the Web review still requires an explicit human decision.
func suggestUntrustedRuntimeHintTx(ctx context.Context, tx *sql.Tx, in NewRuntimeImportHint) (NewRuntimeImportHint, error) {
	if (in.SourceKind != "pegasus-command" && in.SourceKind != "esde-system") || strings.TrimSpace(in.RawCommand) == "" {
		return in, nil
	}
	var platform string
	if err := tx.QueryRowContext(ctx, `SELECT g.platform FROM editions e JOIN games g ON g.id=e.game_id WHERE e.id=?`, strings.TrimSpace(in.EditionID)).Scan(&platform); err != nil {
		return in, err
	}
	tokens := runtimeCommandBasenames(in.RawCommand)
	if in.CoreID == "" {
		rows, err := tx.QueryContext(ctx, `SELECT id,library_names_json,platforms_json FROM retroarch_cores WHERE enabled=1 ORDER BY id`)
		if err != nil {
			return in, err
		}
		matches := []string{}
		for rows.Next() {
			var id, librariesJSON, platformsJSON string
			if err = rows.Scan(&id, &librariesJSON, &platformsJSON); err != nil {
				rows.Close()
				return in, err
			}
			var libraries, platforms []string
			if json.Unmarshal([]byte(librariesJSON), &libraries) != nil || json.Unmarshal([]byte(platformsJSON), &platforms) != nil || !runtimeStringSliceContains(platforms, platform) {
				continue
			}
			for _, library := range libraries {
				if tokens[strings.ToLower(strings.TrimSpace(library))] {
					matches = append(matches, id)
					break
				}
			}
		}
		if err := rows.Close(); err != nil {
			return in, err
		}
		if err := rows.Err(); err != nil {
			return in, err
		}
		if len(matches) == 1 {
			in.CoreID = matches[0]
		}
	}
	if in.DriverID == "" && in.CoreID != "" && tokens["retroarch"] {
		rows, err := tx.QueryContext(ctx, `SELECT id,platforms_json FROM emulator_drivers WHERE enabled=1 AND family='retroarch' ORDER BY id`)
		if err != nil {
			return in, err
		}
		matches := []string{}
		for rows.Next() {
			var id, platformsJSON string
			if err = rows.Scan(&id, &platformsJSON); err != nil {
				rows.Close()
				return in, err
			}
			var platforms []string
			if json.Unmarshal([]byte(platformsJSON), &platforms) == nil && runtimeStringSliceContains(platforms, platform) {
				matches = append(matches, id)
			}
		}
		if err := rows.Close(); err != nil {
			return in, err
		}
		if err := rows.Err(); err != nil {
			return in, err
		}
		if len(matches) == 1 {
			in.DriverID = matches[0]
		}
	}
	return in, nil
}

func runtimeCommandBasenames(raw string) map[string]bool {
	result := map[string]bool{}
	for _, token := range strings.FieldsFunc(raw, func(char rune) bool {
		return char == ' ' || char == '\t' || char == '\r' || char == '\n' || strings.ContainsRune(`"'{}()[];,=`, char)
	}) {
		token = strings.ReplaceAll(strings.TrimSpace(token), `\`, "/")
		base := strings.ToLower(filepath.Base(token))
		for _, extension := range []string{".exe", ".dll", ".dylib", ".so"} {
			base = strings.TrimSuffix(base, extension)
		}
		if base != "" && base != "." {
			result[base] = true
		}
	}
	return result
}

func runtimeStringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}

// SuggestPendingRuntimeHints refreshes catalog suggestions for historical raw
// hints after built-in/custom runtime definitions have been reconciled. It is
// idempotent and deliberately leaves trust, status, raw text, and arguments
// unchanged.
func (s *Store) SuggestPendingRuntimeHints(ctx context.Context) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	type pendingHint struct {
		ID           string
		EditionID    string
		SourceKind   string
		SourceFormat string
		DriverID     string
		CoreID       string
		RawCommand   string
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,edition_id,source_kind,source_format,driver_id,core_id,raw_command FROM runtime_import_hints WHERE status='pending' AND trust='untrusted' ORDER BY id`)
	if err != nil {
		return 0, err
	}
	items := []pendingHint{}
	for rows.Next() {
		var item pendingHint
		if err = rows.Scan(&item.ID, &item.EditionID, &item.SourceKind, &item.SourceFormat, &item.DriverID, &item.CoreID, &item.RawCommand); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, item)
	}
	if err = rows.Close(); err != nil {
		return 0, err
	}
	if err = rows.Err(); err != nil {
		return 0, err
	}
	changed := 0
	for _, item := range items {
		candidate, suggestErr := suggestUntrustedRuntimeHintTx(ctx, tx, NewRuntimeImportHint{
			EditionID: item.EditionID, SourceKind: item.SourceKind, SourceFormat: item.SourceFormat,
			DriverID: item.DriverID, CoreID: item.CoreID, RawCommand: item.RawCommand,
		})
		if suggestErr != nil {
			return 0, suggestErr
		}
		if candidate.DriverID == item.DriverID && candidate.CoreID == item.CoreID {
			continue
		}
		result, updateErr := tx.ExecContext(ctx, `UPDATE runtime_import_hints SET driver_id=?,core_id=?,updated_at=? WHERE id=? AND status='pending' AND trust='untrusted'`, candidate.DriverID, candidate.CoreID, nowText(), item.ID)
		if updateErr != nil {
			return 0, updateErr
		}
		if affected, _ := result.RowsAffected(); affected == 1 {
			changed++
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return changed, nil
}

const runtimeImportHintColumns = `id,edition_id,source_kind,source_format,device_profile_id,frontend_adapter_id,driver_id,core_id,arguments_json,raw_command,trust,status,source_ref,created_at,updated_at`

func scanRuntimeImportHint(scanner interface{ Scan(...any) error }) (RuntimeImportHint, error) {
	var item RuntimeImportHint
	var arguments, created, updated string
	err := scanner.Scan(&item.ID, &item.EditionID, &item.SourceKind, &item.SourceFormat, &item.DeviceProfileID, &item.FrontendAdapterID, &item.DriverID, &item.CoreID, &arguments, &item.RawCommand, &item.Trust, &item.Status, &item.SourceRef, &created, &updated)
	if err != nil {
		return RuntimeImportHint{}, err
	}
	_ = json.Unmarshal([]byte(arguments), &item.Arguments)
	if item.Arguments == nil {
		item.Arguments = []string{}
	}
	item.CreatedAt, item.UpdatedAt = parseTime(created), parseTime(updated)
	return item, nil
}

func (s *Store) GetRuntimeImportHint(ctx context.Context, id string) (RuntimeImportHint, error) {
	return scanRuntimeImportHint(s.db.QueryRowContext(ctx, `SELECT `+runtimeImportHintColumns+` FROM runtime_import_hints WHERE id=?`, strings.TrimSpace(id)))
}

func (s *Store) ListRuntimeImportHints(ctx context.Context, editionID, status string) ([]RuntimeImportHint, error) {
	query := `SELECT ` + runtimeImportHintColumns + ` FROM runtime_import_hints WHERE 1=1`
	args := []any{}
	if editionID = strings.TrimSpace(editionID); editionID != "" {
		query += ` AND edition_id=?`
		args = append(args, editionID)
	}
	if status = strings.ToLower(strings.TrimSpace(status)); status != "" {
		if status != "pending" && status != "applied" && status != "dismissed" {
			return nil, errors.New("status must be pending, applied, or dismissed")
		}
		query += ` AND status=?`
		args = append(args, status)
	}
	query += ` ORDER BY created_at,id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []RuntimeImportHint{}
	for rows.Next() {
		item, scanErr := scanRuntimeImportHint(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// ApplyRuntimeImportHint creates a normal declarative launch binding and marks
// the hint applied in one transaction. RawCommand is deliberately ignored.
func (s *Store) ApplyRuntimeImportHint(ctx context.Context, id string, in NewLaunchBinding) (LaunchBinding, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LaunchBinding{}, err
	}
	defer tx.Rollback()
	hint, err := scanRuntimeImportHint(tx.QueryRowContext(ctx, `SELECT `+runtimeImportHintColumns+` FROM runtime_import_hints WHERE id=? AND status='pending'`, strings.TrimSpace(id)))
	if err != nil {
		return LaunchBinding{}, err
	}
	if strings.TrimSpace(in.EditionID) != "" && strings.TrimSpace(in.EditionID) != hint.EditionID {
		return LaunchBinding{}, errors.New("launch binding edition does not match runtime hint")
	}
	in.EditionID = hint.EditionID
	if strings.TrimSpace(in.DeviceProfileID) == "" {
		in.DeviceProfileID = hint.DeviceProfileID
	}
	if strings.TrimSpace(in.FrontendAdapterID) == "" {
		in.FrontendAdapterID = hint.FrontendAdapterID
	}
	if strings.TrimSpace(in.DriverID) == "" {
		in.DriverID = hint.DriverID
	}
	if strings.TrimSpace(in.CoreID) == "" {
		in.CoreID = hint.CoreID
	}
	if in.Arguments == nil {
		in.Arguments = append([]string{}, hint.Arguments...)
	}
	in, err = normalizeLaunchBinding(in)
	if err != nil {
		return LaunchBinding{}, err
	}
	if in.ID == "" {
		in.ID = NewID()
	}
	now := nowText()
	_, err = tx.ExecContext(ctx, `INSERT INTO launch_bindings(id,edition_id,device_profile_id,driver_id,frontend_adapter_id,core_id,arguments_json,enabled,created_at,updated_at) VALUES(?,?,NULLIF(?,''),?,NULLIF(?,''),NULLIF(?,''),?,?,?,?)`, in.ID, in.EditionID, in.DeviceProfileID, in.DriverID, in.FrontendAdapterID, in.CoreID, jsonText(in.Arguments, "[]"), boolInt(enabledValue(in.Enabled)), now, now)
	if err != nil {
		return LaunchBinding{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE runtime_import_hints SET status='applied',updated_at=? WHERE id=? AND status='pending'`, now, hint.ID)
	if err != nil {
		return LaunchBinding{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return LaunchBinding{}, errors.New("runtime hint changed while applying")
	}
	if err = tx.Commit(); err != nil {
		return LaunchBinding{}, err
	}
	return s.GetLaunchBinding(ctx, in.ID)
}

func (s *Store) DismissRuntimeImportHint(ctx context.Context, id string) (RuntimeImportHint, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE runtime_import_hints SET status='dismissed',updated_at=? WHERE id=? AND status='pending'`, nowText(), strings.TrimSpace(id))
	if err != nil {
		return RuntimeImportHint{}, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return RuntimeImportHint{}, sql.ErrNoRows
	}
	return s.GetRuntimeImportHint(ctx, id)
}

func runtimeHintReviewFingerprint(hint RuntimeImportHint, editionUpdated, gameUpdated string) (string, error) {
	payload, err := json.Marshal(struct {
		Hint           RuntimeImportHint `json:"hint"`
		EditionUpdated string            `json:"edition_updated"`
		GameUpdated    string            `json:"game_updated"`
	}{Hint: hint, EditionUpdated: editionUpdated, GameUpdated: gameUpdated})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("%x", sum[:]), nil
}

func runtimeBatchDefinitionFingerprint(profile DeviceProfile, driver EmulatorDriver, frontend *FrontendAdapter, core *RetroArchCore) (string, error) {
	payload, err := json.Marshal(struct {
		Profile  DeviceProfile    `json:"profile"`
		Driver   EmulatorDriver   `json:"driver"`
		Frontend *FrontendAdapter `json:"frontend,omitempty"`
		Core     *RetroArchCore   `json:"core,omitempty"`
	}{Profile: profile, Driver: driver, Frontend: frontend, Core: core})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("%x", sum[:]), nil
}

func normalizeRuntimeHintBatchReview(in RuntimeHintBatchReview) (RuntimeHintBatchReview, error) {
	in.DeviceProfileID = strings.TrimSpace(in.DeviceProfileID)
	in.DriverID = strings.TrimSpace(in.DriverID)
	in.FrontendAdapterID = strings.TrimSpace(in.FrontendAdapterID)
	in.CoreID = strings.TrimSpace(in.CoreID)
	if in.DeviceProfileID == "" || in.DriverID == "" {
		return in, errors.New("device_profile_id and driver_id are required")
	}
	if len(in.HintIDs) == 0 || len(in.HintIDs) > 500 {
		return in, errors.New("hint_ids must contain between 1 and 500 items")
	}
	seen := map[string]bool{}
	ids := make([]string, 0, len(in.HintIDs))
	for _, id := range in.HintIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return in, errors.New("hint_ids must contain unique non-empty ids")
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Strings(ids)
	in.HintIDs = ids
	if in.Arguments == nil {
		in.Arguments = []string{}
	}
	if err := ValidateLaunchArguments(in.Arguments); err != nil {
		return in, err
	}
	return in, nil
}

func reviewRuntimeImportHintBatchTx(ctx context.Context, tx *sql.Tx, in RuntimeHintBatchReview) (RuntimeHintBatchSnapshot, error) {
	in, err := normalizeRuntimeHintBatchReview(in)
	if err != nil {
		return RuntimeHintBatchSnapshot{}, err
	}
	profile, err := scanDeviceProfile(tx.QueryRowContext(ctx, `SELECT `+deviceProfileColumns+` FROM device_profiles WHERE id=?`, in.DeviceProfileID))
	if err != nil || !profile.Enabled {
		return RuntimeHintBatchSnapshot{}, errors.New("device profile is unavailable or disabled")
	}
	driver, err := scanEmulatorDriver(tx.QueryRowContext(ctx, `SELECT `+emulatorDriverColumns+` FROM emulator_drivers WHERE id=?`, in.DriverID))
	if err != nil || !driver.Enabled {
		return RuntimeHintBatchSnapshot{}, errors.New("emulator driver is unavailable or disabled")
	}
	if !runtimeStringSliceContains(driver.Targets, profile.Target) {
		return RuntimeHintBatchSnapshot{}, errors.New("emulator driver does not support the selected device target")
	}
	if in.FrontendAdapterID == "" {
		in.FrontendAdapterID = profile.DefaultFrontendID
	}
	var frontend *FrontendAdapter
	if in.FrontendAdapterID != "" {
		item, frontendErr := scanFrontendAdapter(tx.QueryRowContext(ctx, `SELECT `+frontendAdapterColumns+` FROM frontend_adapters WHERE id=?`, in.FrontendAdapterID))
		if frontendErr != nil || !item.Enabled {
			return RuntimeHintBatchSnapshot{}, errors.New("frontend adapter is unavailable or disabled")
		}
		frontend = &item
	}
	var core *RetroArchCore
	if in.CoreID != "" {
		item, coreErr := scanRetroArchCore(tx.QueryRowContext(ctx, `SELECT `+retroArchCoreColumns+` FROM retroarch_cores WHERE id=?`, in.CoreID))
		if coreErr != nil || !item.Enabled {
			return RuntimeHintBatchSnapshot{}, errors.New("RetroArch core is unavailable or disabled")
		}
		core = &item
	}
	if driver.Launch.RequiresCore && core == nil {
		return RuntimeHintBatchSnapshot{}, errors.New("selected emulator driver requires a RetroArch core")
	}
	snapshot := RuntimeHintBatchSnapshot{Review: in, Hints: []RuntimeHintBatchHintSnapshot{}}
	for _, id := range in.HintIDs {
		hint, hintErr := scanRuntimeImportHint(tx.QueryRowContext(ctx, `SELECT `+runtimeImportHintColumns+` FROM runtime_import_hints WHERE id=? AND status='pending'`, id))
		if hintErr != nil {
			return RuntimeHintBatchSnapshot{}, ErrRuntimeHintBatchStale
		}
		var platform, editionUpdated, gameUpdated string
		if hintErr = tx.QueryRowContext(ctx, `SELECT g.platform,e.updated_at,g.updated_at FROM editions e JOIN games g ON g.id=e.game_id WHERE e.id=?`, hint.EditionID).Scan(&platform, &editionUpdated, &gameUpdated); hintErr != nil {
			return RuntimeHintBatchSnapshot{}, ErrRuntimeHintBatchStale
		}
		platform = strings.ToLower(strings.TrimSpace(platform))
		if snapshot.PlatformID == "" {
			snapshot.PlatformID = platform
		} else if snapshot.PlatformID != platform {
			return RuntimeHintBatchSnapshot{}, errors.New("all runtime hints in a batch must use the same platform")
		}
		if !runtimeStringSliceContains(driver.Platforms, platform) {
			return RuntimeHintBatchSnapshot{}, errors.New("emulator driver does not support the selected platform")
		}
		if core != nil && !runtimeStringSliceContains(core.Platforms, platform) {
			return RuntimeHintBatchSnapshot{}, errors.New("RetroArch core does not support the selected platform")
		}
		var existing int
		if hintErr = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM launch_bindings WHERE edition_id=? AND COALESCE(device_profile_id,'')=?`, hint.EditionID, in.DeviceProfileID).Scan(&existing); hintErr != nil {
			return RuntimeHintBatchSnapshot{}, hintErr
		}
		if existing != 0 {
			return RuntimeHintBatchSnapshot{}, ErrRuntimeHintBatchConflict
		}
		fingerprint, fingerprintErr := runtimeHintReviewFingerprint(hint, editionUpdated, gameUpdated)
		if fingerprintErr != nil {
			return RuntimeHintBatchSnapshot{}, fingerprintErr
		}
		snapshot.Hints = append(snapshot.Hints, RuntimeHintBatchHintSnapshot{
			HintID: hint.ID, EditionID: hint.EditionID, PlatformID: platform,
			Fingerprint: fingerprint,
		})
	}
	snapshot.Review = in
	snapshot.DefinitionFingerprint, err = runtimeBatchDefinitionFingerprint(profile, driver, frontend, core)
	if err != nil {
		return RuntimeHintBatchSnapshot{}, err
	}
	return snapshot, nil
}

// ReviewRuntimeImportHintBatch returns a canonical, privacy-minimized snapshot
// for signing by the API layer. The read transaction prevents a mixed snapshot.
func (s *Store) ReviewRuntimeImportHintBatch(ctx context.Context, in RuntimeHintBatchReview) (RuntimeHintBatchSnapshot, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return RuntimeHintBatchSnapshot{}, err
	}
	defer tx.Rollback()
	snapshot, err := reviewRuntimeImportHintBatchTx(ctx, tx, in)
	if err != nil {
		return RuntimeHintBatchSnapshot{}, err
	}
	if err = tx.Commit(); err != nil {
		return RuntimeHintBatchSnapshot{}, err
	}
	return snapshot, nil
}

// ApplyRuntimeImportHintBatchIfSnapshot rechecks the complete signed review in
// the same transaction that creates every binding. A single drift or conflict
// produces zero bindings and leaves every hint pending.
func (s *Store) ApplyRuntimeImportHintBatchIfSnapshot(ctx context.Context, expected RuntimeHintBatchSnapshot) (RuntimeHintBatchResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RuntimeHintBatchResult{}, err
	}
	defer tx.Rollback()
	current, err := reviewRuntimeImportHintBatchTx(ctx, tx, expected.Review)
	if err != nil {
		if errors.Is(err, ErrRuntimeHintBatchConflict) {
			return RuntimeHintBatchResult{}, err
		}
		return RuntimeHintBatchResult{}, ErrRuntimeHintBatchStale
	}
	if !reflect.DeepEqual(current, expected) {
		return RuntimeHintBatchResult{}, ErrRuntimeHintBatchStale
	}
	now := nowText()
	bindingIDs := make([]string, 0, len(current.Hints))
	for _, item := range current.Hints {
		bindingID := NewID()
		_, err = tx.ExecContext(ctx, `INSERT INTO launch_bindings(id,edition_id,device_profile_id,driver_id,frontend_adapter_id,core_id,arguments_json,enabled,created_at,updated_at) VALUES(?,?,?,?,NULLIF(?,''),NULLIF(?,''),?,1,?,?)`, bindingID, item.EditionID, current.Review.DeviceProfileID, current.Review.DriverID, current.Review.FrontendAdapterID, current.Review.CoreID, jsonText(current.Review.Arguments, "[]"), now, now)
		if err != nil {
			return RuntimeHintBatchResult{}, ErrRuntimeHintBatchConflict
		}
		result, updateErr := tx.ExecContext(ctx, `UPDATE runtime_import_hints SET status='applied',updated_at=? WHERE id=? AND status='pending'`, now, item.HintID)
		if updateErr != nil {
			return RuntimeHintBatchResult{}, updateErr
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return RuntimeHintBatchResult{}, ErrRuntimeHintBatchStale
		}
		bindingIDs = append(bindingIDs, bindingID)
	}
	if err = tx.Commit(); err != nil {
		return RuntimeHintBatchResult{}, err
	}
	applications := make([]RuntimeHintApplication, 0, len(bindingIDs))
	for index, id := range bindingIDs {
		hint, hintErr := s.GetRuntimeImportHint(ctx, current.Hints[index].HintID)
		if hintErr != nil {
			return RuntimeHintBatchResult{}, hintErr
		}
		binding, bindingErr := s.GetLaunchBinding(ctx, id)
		if bindingErr != nil {
			return RuntimeHintBatchResult{}, bindingErr
		}
		applications = append(applications, RuntimeHintApplication{Hint: hint, Binding: binding})
	}
	return RuntimeHintBatchResult{Applied: len(applications), Applications: applications}, nil
}
