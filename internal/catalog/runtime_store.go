package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"varkiv/internal/platforms"
)

var supportLevels = map[string]bool{"catalogued": true, "package-tested": true, "hardware-tested": true, "sync-tested": true}

var launchVariablePattern = regexp.MustCompile(`\{\{([a-z0-9_.]+)\}\}`)
var allowedLaunchVariables = map[string]bool{
	"edition.id": true, "edition.title": true, "edition.save_namespace": true,
	"edition.serial": true, "edition.product_code": true, "edition.title_id": true,
	"edition.title_id_high": true, "edition.title_id_low": true,
	"platform.id": true, "rom.path": true, "rom.source_path": true, "rom.stem": true,
	"core.id": true, "core.library": true,
	"device.id": true, "device.target": true, "device.config_dir": true, "device.save_dir": true, "device.core_dir": true, "device.emulator_dir": true,
}
var allowedAndroidIntentVariables = map[string]bool{
	"rom.uri": true, "edition.id": true, "platform.id": true,
	"core.library": true, "android.package_data": true,
}

func validateAndroidIntentTemplate(value string) error {
	for _, match := range launchVariablePattern.FindAllStringSubmatch(value, -1) {
		if !allowedAndroidIntentVariables[match[1]] {
			return fmt.Errorf("android intent template variable %q is not allowed", match[1])
		}
	}
	remaining := launchVariablePattern.ReplaceAllString(value, "")
	if strings.ContainsAny(remaining, "{}") {
		return errors.New("android intent template contains malformed braces")
	}
	return nil
}

// ValidateLaunchArguments accepts an argv array, never a shell command. It is
// intentionally small: no functions, conditionals, environment lookup, or
// multiline arguments are part of the contract.
func ValidateLaunchArguments(arguments []string) error {
	if len(arguments) > 32 {
		return errors.New("launch arguments must not exceed 32 entries")
	}
	for index, argument := range arguments {
		if len(argument) > 2048 || strings.ContainsAny(argument, "\x00\r\n") {
			return fmt.Errorf("launch argument %d exceeds the safe size or contains a control character", index)
		}
		cleaned := launchVariablePattern.ReplaceAllStringFunc(argument, func(match string) string {
			name := strings.TrimSuffix(strings.TrimPrefix(match, "{{"), "}}")
			if allowedLaunchVariables[name] {
				return ""
			}
			return match
		})
		if strings.Contains(cleaned, "{{") || strings.Contains(cleaned, "}}") || strings.ContainsAny(cleaned, "{}") {
			return fmt.Errorf("launch argument %d contains an unknown or malformed template variable", index)
		}
	}
	return nil
}

func normalizeSupportLevel(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		value = "catalogued"
	}
	if !supportLevels[value] {
		return "", errors.New("support_level must be catalogued, package-tested, hardware-tested, or sync-tested")
	}
	return value, nil
}

func supportEvidenceString(evidence map[string]any, key string) string {
	value, _ := evidence[key].(string)
	return strings.TrimSpace(value)
}

func nonEmptySupportScenarios(value any) bool {
	switch scenarios := value.(type) {
	case []string:
		for _, scenario := range scenarios {
			if strings.TrimSpace(scenario) != "" {
				return true
			}
		}
	case []any:
		for _, value := range scenarios {
			if scenario, ok := value.(string); ok && strings.TrimSpace(scenario) != "" {
				return true
			}
		}
	}
	return false
}

func validateSupportEvidence(level string, evidence map[string]any) error {
	if level != "hardware-tested" && level != "sync-tested" {
		return nil
	}
	wantScope := "hardware"
	if level == "sync-tested" {
		wantScope = "sync"
	}
	if supportEvidenceString(evidence, "scope") != wantScope {
		return fmt.Errorf("%s evidence requires scope %q", level, wantScope)
	}
	for _, key := range []string{"device", "software_version", "verified_at", "result"} {
		if supportEvidenceString(evidence, key) == "" {
			return fmt.Errorf("%s evidence requires %s", level, key)
		}
	}
	verifiedAt := supportEvidenceString(evidence, "verified_at")
	if _, err := time.Parse("2006-01-02", verifiedAt); err != nil {
		if _, timestampErr := time.Parse(time.RFC3339, verifiedAt); timestampErr != nil {
			return fmt.Errorf("%s evidence verified_at must be YYYY-MM-DD or RFC3339", level)
		}
	}
	if supportEvidenceString(evidence, "result") != "passed" {
		return fmt.Errorf("%s evidence result must be passed", level)
	}
	if !nonEmptySupportScenarios(evidence["scenarios"]) {
		return fmt.Errorf("%s evidence requires at least one verified scenario", level)
	}
	return nil
}

func evidenceInteger(evidence map[string]any, key string) (int, bool) {
	switch value := evidence[key].(type) {
	case int:
		return value, true
	case int64:
		return int(value), int64(int(value)) == value
	case float64:
		integer := int(value)
		return integer, float64(integer) == value
	default:
		return 0, false
	}
}

func bindSupportEvidence(evidence map[string]any, kind, id string, contractVersion int) map[string]any {
	bound := make(map[string]any, len(evidence)+3)
	for key, value := range evidence {
		bound[key] = value
	}
	bound["runtime_object_kind"] = kind
	bound["runtime_object_id"] = id
	bound["runtime_contract_version"] = contractVersion
	return bound
}

func validateSupportEvidenceBinding(level string, evidence map[string]any, kind, id string, contractVersion int) error {
	if level != "hardware-tested" && level != "sync-tested" {
		return nil
	}
	version, ok := evidenceInteger(evidence, "runtime_contract_version")
	if !ok || version != contractVersion || supportEvidenceString(evidence, "runtime_object_kind") != kind || supportEvidenceString(evidence, "runtime_object_id") != id {
		return errors.New("hardware support evidence is stale or is bound to a different runtime contract")
	}
	return nil
}

func validateStoredSupportEvidence(level string, evidence map[string]any, kind, id string, contractVersion int) error {
	normalized, err := normalizeSupportLevel(level)
	if err != nil || normalized != level {
		return errors.New("stored support level is invalid")
	}
	if err = validateSupportEvidence(level, evidence); err != nil {
		return fmt.Errorf("stored support evidence is invalid: %w", err)
	}
	if err = validateSupportEvidenceBinding(level, evidence, kind, id, contractVersion); err != nil {
		return fmt.Errorf("stored support evidence is invalid: %w", err)
	}
	return nil
}

func enabledValue(value *bool) bool {
	return value == nil || *value
}

func jsonText(value any, fallback string) string {
	data, err := json.Marshal(value)
	if err != nil || string(data) == "null" {
		return fallback
	}
	return string(data)
}

func parseTime(value string) time.Time {
	result, _ := time.Parse(time.RFC3339Nano, value)
	return result
}

var sourceAdapterHandlers = map[string]bool{"rom_directory": true, "pegasus": true, "esde": true, "varkiv": true}
var frontendAdapterHandlers = map[string]bool{"pegasus": true, "es-de": true}

func normalizeSourceAdapter(in NewSourceAdapter) (NewSourceAdapter, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Format = strings.ToLower(strings.TrimSpace(in.Format))
	in.Handler = strings.ToLower(strings.TrimSpace(in.Handler))
	if in.Name == "" || in.Format == "" || in.Handler == "" {
		return in, errors.New("name, format, and handler are required")
	}
	for _, char := range in.Format {
		if !(char >= 'a' && char <= 'z') && !(char >= '0' && char <= '9') && char != '-' && char != '_' && char != '.' {
			return in, errors.New("format may contain only lowercase letters, numbers, dot, dash, and underscore")
		}
	}
	if !sourceAdapterHandlers[in.Handler] {
		return in, errors.New("handler must be rom_directory, pegasus, esde, or varkiv")
	}
	if in.ContractVersion == 0 {
		in.ContractVersion = 1
	}
	if in.ContractVersion < 1 {
		return in, errors.New("contract_version must be positive")
	}
	level, err := normalizeSupportLevel(in.SupportLevel)
	in.SupportLevel = level
	if err == nil {
		err = validateSupportEvidence(level, in.Evidence)
	}
	return in, err
}

func (s *Store) CreateSourceAdapter(ctx context.Context, in NewSourceAdapter) (SourceAdapter, error) {
	in, err := normalizeSourceAdapter(in)
	if err != nil {
		return SourceAdapter{}, err
	}
	if in.ID == "" {
		in.ID = NewID()
	}
	if err = validateBuiltinID(in.ID, in.Builtin); err != nil {
		return SourceAdapter{}, err
	}
	if in.SupportLevel == "hardware-tested" || in.SupportLevel == "sync-tested" {
		return SourceAdapter{}, errors.New("hardware support claims require reviewed evidence")
	}
	now := nowText()
	_, err = s.db.ExecContext(ctx, `INSERT INTO source_adapters(id,name,format,handler,contract_version,capabilities_json,support_level,evidence_json,builtin,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, in.ID, in.Name, in.Format, in.Handler, in.ContractVersion, jsonText(in.Capabilities, "{}"), in.SupportLevel, jsonText(in.Evidence, "{}"), boolInt(in.Builtin), boolInt(enabledValue(in.Enabled)), now, now)
	if err != nil {
		return SourceAdapter{}, err
	}
	return s.GetSourceAdapter(ctx, in.ID)
}

func (s *Store) ReconcileBuiltinSourceAdapter(ctx context.Context, in NewSourceAdapter) (SourceAdapter, error) {
	current, err := s.GetSourceAdapter(ctx, in.ID)
	if err != nil {
		return SourceAdapter{}, err
	}
	if !current.Builtin {
		return SourceAdapter{}, errors.New("refusing to reconcile a user-created source adapter")
	}
	in.Builtin = true
	in, err = normalizeSourceAdapter(in)
	if err != nil {
		return SourceAdapter{}, err
	}
	if in.ContractVersion <= current.ContractVersion {
		return current, nil
	}
	_, err = s.db.ExecContext(ctx, `UPDATE source_adapters SET name=?,format=?,handler=?,contract_version=?,capabilities_json=?,support_level=?,evidence_json=?,enabled=?,updated_at=? WHERE id=? AND builtin=1`, in.Name, in.Format, in.Handler, in.ContractVersion, jsonText(in.Capabilities, "{}"), in.SupportLevel, jsonText(in.Evidence, "{}"), boolInt(enabledValue(in.Enabled)), nowText(), in.ID)
	if err != nil {
		return SourceAdapter{}, err
	}
	return s.GetSourceAdapter(ctx, in.ID)
}

func scanSourceAdapter(scanner interface{ Scan(...any) error }) (SourceAdapter, error) {
	var item SourceAdapter
	var capabilities, evidence, created, updated string
	var builtin, enabled int
	err := scanner.Scan(&item.ID, &item.Name, &item.Format, &item.Handler, &item.ContractVersion, &capabilities, &item.SupportLevel, &evidence, &builtin, &enabled, &created, &updated)
	if err != nil {
		return SourceAdapter{}, err
	}
	if err = json.Unmarshal([]byte(capabilities), &item.Capabilities); err != nil {
		return SourceAdapter{}, errors.New("stored source adapter capabilities are invalid")
	}
	if err = json.Unmarshal([]byte(evidence), &item.Evidence); err != nil {
		return SourceAdapter{}, errors.New("stored source adapter evidence is invalid")
	}
	if item.Capabilities == nil {
		item.Capabilities = map[string]bool{}
	}
	if item.Evidence == nil {
		item.Evidence = map[string]any{}
	}
	if err = validateStoredSupportEvidence(item.SupportLevel, item.Evidence, "source_adapter", item.ID, item.ContractVersion); err != nil {
		return SourceAdapter{}, fmt.Errorf("source adapter %s: %w", item.ID, err)
	}
	item.Builtin, item.Enabled = builtin != 0, enabled != 0
	item.CreatedAt, item.UpdatedAt = parseTime(created), parseTime(updated)
	return item, nil
}

const sourceAdapterColumns = `id,name,format,handler,contract_version,capabilities_json,support_level,evidence_json,builtin,enabled,created_at,updated_at`

func (s *Store) GetSourceAdapter(ctx context.Context, id string) (SourceAdapter, error) {
	return scanSourceAdapter(s.db.QueryRowContext(ctx, `SELECT `+sourceAdapterColumns+` FROM source_adapters WHERE id=?`, id))
}

func (s *Store) ListSourceAdapters(ctx context.Context) ([]SourceAdapter, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+sourceAdapterColumns+` FROM source_adapters ORDER BY enabled DESC,builtin DESC,lower(name),id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []SourceAdapter{}
	for rows.Next() {
		item, scanErr := scanSourceAdapter(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) UpdateSourceAdapter(ctx context.Context, id string, in NewSourceAdapter) (SourceAdapter, error) {
	current, err := s.GetSourceAdapter(ctx, id)
	if err != nil {
		return SourceAdapter{}, err
	}
	if current.Builtin {
		return SourceAdapter{}, ErrBuiltinImmutable
	}
	in, err = normalizeSourceAdapter(in)
	if err != nil {
		return SourceAdapter{}, err
	}
	if supportLevelRank(in.SupportLevel) >= supportLevelRank("hardware-tested") {
		if supportLevelRank(current.SupportLevel) < supportLevelRank("hardware-tested") || in.ContractVersion != current.ContractVersion {
			return SourceAdapter{}, errors.New("hardware support claims require reviewed evidence for the current runtime contract")
		}
		if err = validateSupportEvidenceBinding(in.SupportLevel, in.Evidence, "source_adapter", id, in.ContractVersion); err != nil {
			return SourceAdapter{}, err
		}
	}
	if in.Handler != current.Handler {
		var references int
		if err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM library_sources WHERE source_adapter_id=?`, id).Scan(&references); err != nil {
			return SourceAdapter{}, err
		}
		if references > 0 {
			return SourceAdapter{}, ErrRuntimeObjectInUse
		}
	}
	result, err := s.db.ExecContext(ctx, `UPDATE source_adapters SET name=?,format=?,handler=?,contract_version=?,capabilities_json=?,support_level=?,evidence_json=?,enabled=?,updated_at=? WHERE id=?`, in.Name, in.Format, in.Handler, in.ContractVersion, jsonText(in.Capabilities, "{}"), in.SupportLevel, jsonText(in.Evidence, "{}"), boolInt(enabledValue(in.Enabled)), nowText(), id)
	if err != nil {
		return SourceAdapter{}, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return SourceAdapter{}, sql.ErrNoRows
	}
	return s.GetSourceAdapter(ctx, id)
}

func (s *Store) DeleteSourceAdapter(ctx context.Context, id string) error {
	item, err := s.GetSourceAdapter(ctx, id)
	if err != nil {
		return err
	}
	if item.Builtin {
		return ErrBuiltinImmutable
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM source_adapters WHERE id=?`, id)
	if err != nil {
		return ErrRuntimeObjectInUse
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func normalizeFrontendAdapter(in NewFrontendAdapter) (NewFrontendAdapter, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Format = strings.ToLower(strings.TrimSpace(in.Format))
	in.Handler = strings.ToLower(strings.TrimSpace(in.Handler))
	if in.Name == "" || in.Format == "" {
		return in, errors.New("name and format are required")
	}
	for _, char := range in.Format {
		if !(char >= 'a' && char <= 'z') && !(char >= '0' && char <= '9') && char != '-' && char != '_' && char != '.' {
			return in, errors.New("format may contain only lowercase letters, numbers, dot, dash, and underscore")
		}
	}
	// Exact built-in format names remain source-compatible. A custom format
	// may stay unbound as legacy/catalog metadata, but it cannot be selected by
	// a PackageProfile until an audited handler is chosen explicitly.
	if in.Handler == "" && frontendAdapterHandlers[in.Format] {
		in.Handler = in.Format
	}
	if in.Handler != "" && !frontendAdapterHandlers[in.Handler] {
		return in, errors.New("handler must be pegasus or es-de")
	}
	if in.ContractVersion == 0 {
		in.ContractVersion = 1
	}
	if in.ContractVersion < 1 {
		return in, errors.New("contract_version must be positive")
	}
	level, err := normalizeSupportLevel(in.SupportLevel)
	in.SupportLevel = level
	if err == nil {
		err = validateSupportEvidence(level, in.Evidence)
	}
	return in, err
}

func (s *Store) CreateFrontendAdapter(ctx context.Context, in NewFrontendAdapter) (FrontendAdapter, error) {
	in, err := normalizeFrontendAdapter(in)
	if err != nil {
		return FrontendAdapter{}, err
	}
	if in.ID == "" {
		in.ID = NewID()
	}
	if err = validateBuiltinID(in.ID, in.Builtin); err != nil {
		return FrontendAdapter{}, err
	}
	if in.SupportLevel == "hardware-tested" || in.SupportLevel == "sync-tested" {
		return FrontendAdapter{}, errors.New("hardware support claims require reviewed evidence")
	}
	now := nowText()
	_, err = s.db.ExecContext(ctx, `INSERT INTO frontend_adapters(id,name,format,handler,contract_version,capabilities_json,support_level,evidence_json,builtin,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, in.ID, in.Name, in.Format, in.Handler, in.ContractVersion, jsonText(in.Capabilities, "{}"), in.SupportLevel, jsonText(in.Evidence, "{}"), boolInt(in.Builtin), boolInt(enabledValue(in.Enabled)), now, now)
	if err != nil {
		return FrontendAdapter{}, err
	}
	return s.GetFrontendAdapter(ctx, in.ID)
}

func (s *Store) ReconcileBuiltinFrontendAdapter(ctx context.Context, in NewFrontendAdapter) (FrontendAdapter, error) {
	current, err := s.GetFrontendAdapter(ctx, in.ID)
	if err != nil {
		return FrontendAdapter{}, err
	}
	if !current.Builtin {
		return FrontendAdapter{}, errors.New("refusing to reconcile a user-created frontend adapter")
	}
	in.Builtin = true
	in, err = normalizeFrontendAdapter(in)
	if err != nil {
		return FrontendAdapter{}, err
	}
	if in.ContractVersion <= current.ContractVersion {
		return current, nil
	}
	_, err = s.db.ExecContext(ctx, `UPDATE frontend_adapters SET name=?,format=?,handler=?,contract_version=?,capabilities_json=?,support_level=?,evidence_json=?,enabled=?,updated_at=? WHERE id=? AND builtin=1`, in.Name, in.Format, in.Handler, in.ContractVersion, jsonText(in.Capabilities, "{}"), in.SupportLevel, jsonText(in.Evidence, "{}"), boolInt(enabledValue(in.Enabled)), nowText(), in.ID)
	if err != nil {
		return FrontendAdapter{}, err
	}
	return s.GetFrontendAdapter(ctx, in.ID)
}

func scanFrontendAdapter(scanner interface{ Scan(...any) error }) (FrontendAdapter, error) {
	var item FrontendAdapter
	var capabilities, evidence, created, updated string
	var builtin, enabled int
	err := scanner.Scan(&item.ID, &item.Name, &item.Format, &item.Handler, &item.ContractVersion, &capabilities, &item.SupportLevel, &evidence, &builtin, &enabled, &created, &updated)
	if err != nil {
		return FrontendAdapter{}, err
	}
	if err = json.Unmarshal([]byte(capabilities), &item.Capabilities); err != nil {
		return FrontendAdapter{}, errors.New("stored frontend adapter capabilities are invalid")
	}
	if err = json.Unmarshal([]byte(evidence), &item.Evidence); err != nil {
		return FrontendAdapter{}, errors.New("stored frontend adapter evidence is invalid")
	}
	if item.Capabilities == nil {
		item.Capabilities = map[string]bool{}
	}
	if item.Evidence == nil {
		item.Evidence = map[string]any{}
	}
	if err = validateStoredSupportEvidence(item.SupportLevel, item.Evidence, "frontend_adapter", item.ID, item.ContractVersion); err != nil {
		return FrontendAdapter{}, fmt.Errorf("frontend adapter %s: %w", item.ID, err)
	}
	item.Builtin, item.Enabled = builtin != 0, enabled != 0
	item.CreatedAt, item.UpdatedAt = parseTime(created), parseTime(updated)
	return item, nil
}

const frontendAdapterColumns = `id,name,format,handler,contract_version,capabilities_json,support_level,evidence_json,builtin,enabled,created_at,updated_at`

func (s *Store) GetFrontendAdapter(ctx context.Context, id string) (FrontendAdapter, error) {
	return scanFrontendAdapter(s.db.QueryRowContext(ctx, `SELECT `+frontendAdapterColumns+` FROM frontend_adapters WHERE id=?`, id))
}

func (s *Store) ListFrontendAdapters(ctx context.Context) ([]FrontendAdapter, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+frontendAdapterColumns+` FROM frontend_adapters ORDER BY enabled DESC,builtin DESC,lower(name),id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []FrontendAdapter{}
	for rows.Next() {
		item, scanErr := scanFrontendAdapter(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) UpdateFrontendAdapter(ctx context.Context, id string, in NewFrontendAdapter) (FrontendAdapter, error) {
	current, err := s.GetFrontendAdapter(ctx, id)
	if err != nil {
		return FrontendAdapter{}, err
	}
	if supportLevelRank(in.SupportLevel) >= supportLevelRank("hardware-tested") {
		if supportLevelRank(current.SupportLevel) < supportLevelRank("hardware-tested") || in.ContractVersion != current.ContractVersion {
			return FrontendAdapter{}, errors.New("hardware support claims require reviewed evidence for the current runtime contract")
		}
		if err = validateSupportEvidenceBinding(in.SupportLevel, in.Evidence, "frontend_adapter", id, in.ContractVersion); err != nil {
			return FrontendAdapter{}, err
		}
	}
	if current.Builtin {
		return FrontendAdapter{}, ErrBuiltinImmutable
	}
	in, err = normalizeFrontendAdapter(in)
	if err != nil {
		return FrontendAdapter{}, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE frontend_adapters SET name=?,format=?,handler=?,contract_version=?,capabilities_json=?,support_level=?,evidence_json=?,enabled=?,updated_at=? WHERE id=?`, in.Name, in.Format, in.Handler, in.ContractVersion, jsonText(in.Capabilities, "{}"), in.SupportLevel, jsonText(in.Evidence, "{}"), boolInt(enabledValue(in.Enabled)), nowText(), id)
	if err != nil {
		return FrontendAdapter{}, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return FrontendAdapter{}, sql.ErrNoRows
	}
	return s.GetFrontendAdapter(ctx, id)
}

func (s *Store) DeleteFrontendAdapter(ctx context.Context, id string) error {
	item, err := s.GetFrontendAdapter(ctx, id)
	if err != nil {
		return err
	}
	if item.Builtin {
		return ErrBuiltinImmutable
	}
	return s.deleteRuntimeObjectWithEvidence(ctx, "frontend_adapters", "frontend_adapter", id)
}

// deleteRuntimeObjectWithEvidence keeps polymorphic evidence rows from becoming
// orphaned. Functional foreign-key references still reject the delete, and the
// evidence removal rolls back with it.
func (s *Store) deleteRuntimeObjectWithEvidence(ctx context.Context, table, kind, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM runtime_evidence_claims WHERE runtime_kind=? AND runtime_id=?`, kind, id); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE id=?`, id)
	if err != nil {
		return ErrRuntimeObjectInUse
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

func normalizeDeviceProfilePaths(input map[string]string) (map[string]string, error) {
	allowedPaths := map[string]bool{"config_dir": true, "save_dir": true, "rom_dir": true, "core_dir": true, "emulator_dir": true}
	normalizedPaths := map[string]string{}
	for key, value := range input {
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !allowedPaths[key] {
			return nil, fmt.Errorf("device profile path key %q is not supported", key)
		}
		if value == "" || len(value) > 512 || strings.ContainsAny(value, "\x00\r\n\\:") || strings.HasPrefix(value, "/") {
			return nil, fmt.Errorf("device profile path %s must be a portable relative path", key)
		}
		cleaned := path.Clean(value)
		if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
			return nil, fmt.Errorf("device profile path %s must be a portable relative path", key)
		}
		normalizedPaths[key] = cleaned
	}
	return normalizedPaths, nil
}

func normalizeDeviceProfile(in NewDeviceProfile) (NewDeviceProfile, error) {
	in.Name, in.Target = strings.TrimSpace(in.Name), strings.ToLower(strings.TrimSpace(in.Target))
	in.OSFamily, in.Distribution = strings.ToLower(strings.TrimSpace(in.OSFamily)), strings.ToLower(strings.TrimSpace(in.Distribution))
	in.Architecture, in.PathStyle = strings.ToLower(strings.TrimSpace(in.Architecture)), strings.ToLower(strings.TrimSpace(in.PathStyle))
	in.DefaultFrontendID = strings.TrimSpace(in.DefaultFrontendID)
	if in.Name == "" || in.Target == "" || in.OSFamily == "" {
		return in, errors.New("name, target, and os_family are required")
	}
	if in.ContractVersion == 0 {
		in.ContractVersion = 1
	}
	if in.ContractVersion < 1 {
		return in, errors.New("contract_version must be positive")
	}
	if in.PathStyle == "" {
		in.PathStyle = "posix"
	}
	if in.PathStyle != "posix" && in.PathStyle != "windows" && in.PathStyle != "android-uri" {
		return in, errors.New("path_style must be posix, windows, or android-uri")
	}
	if in.MaxPath == 0 {
		in.MaxPath = 255
	}
	if in.MaxPath < 64 || in.MaxPath > 32767 {
		return in, errors.New("max_path must be between 64 and 32767")
	}
	if len(in.IllegalCharacters) > 128 || strings.ContainsAny(in.IllegalCharacters, "\x00\r\n") {
		return in, errors.New("illegal_characters exceeds the safe limit or contains a control character")
	}
	normalizedPaths, err := normalizeDeviceProfilePaths(in.Paths)
	if err != nil {
		return in, err
	}
	in.Paths = normalizedPaths
	level, err := normalizeSupportLevel(in.SupportLevel)
	in.SupportLevel = level
	if err == nil {
		err = validateSupportEvidence(level, in.Evidence)
	}
	return in, err
}

func (s *Store) CreateDeviceProfile(ctx context.Context, in NewDeviceProfile) (DeviceProfile, error) {
	in, err := normalizeDeviceProfile(in)
	if err != nil {
		return DeviceProfile{}, err
	}
	if in.ID == "" {
		in.ID = NewID()
	}
	if err = validateBuiltinID(in.ID, in.Builtin); err != nil {
		return DeviceProfile{}, err
	}
	if in.SupportLevel == "hardware-tested" || in.SupportLevel == "sync-tested" {
		return DeviceProfile{}, errors.New("hardware support claims require reviewed evidence")
	}
	caseSensitive := in.PathStyle != "windows"
	if in.CaseSensitive != nil {
		caseSensitive = *in.CaseSensitive
	}
	now := nowText()
	_, err = s.db.ExecContext(ctx, `INSERT INTO device_profiles(id,name,contract_version,target,os_family,distribution,architecture,path_style,case_sensitive,max_path,illegal_characters,supports_hardlink,supports_hooks,default_frontend_id,paths_json,support_level,evidence_json,builtin,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,NULLIF(?,''),?,?,?,?,?,?,?)`, in.ID, in.Name, in.ContractVersion, in.Target, in.OSFamily, in.Distribution, in.Architecture, in.PathStyle, boolInt(caseSensitive), in.MaxPath, in.IllegalCharacters, boolInt(in.SupportsHardlink), boolInt(in.SupportsHooks), in.DefaultFrontendID, jsonText(in.Paths, "{}"), in.SupportLevel, jsonText(in.Evidence, "{}"), boolInt(in.Builtin), boolInt(enabledValue(in.Enabled)), now, now)
	if err != nil {
		return DeviceProfile{}, err
	}
	return s.GetDeviceProfile(ctx, in.ID)
}

func scanDeviceProfile(scanner interface{ Scan(...any) error }) (DeviceProfile, error) {
	var item DeviceProfile
	var frontend sql.NullString
	var paths, evidence, created, updated string
	var caseSensitive, hardlink, hooks, builtin, enabled int
	err := scanner.Scan(&item.ID, &item.Name, &item.ContractVersion, &item.Target, &item.OSFamily, &item.Distribution, &item.Architecture, &item.PathStyle, &caseSensitive, &item.MaxPath, &item.IllegalCharacters, &hardlink, &hooks, &frontend, &paths, &item.SupportLevel, &evidence, &builtin, &enabled, &created, &updated)
	if err != nil {
		return DeviceProfile{}, err
	}
	item.DefaultFrontendID = frontend.String
	if err = json.Unmarshal([]byte(paths), &item.Paths); err != nil {
		return DeviceProfile{}, errors.New("stored device profile paths are invalid")
	}
	if err = json.Unmarshal([]byte(evidence), &item.Evidence); err != nil {
		return DeviceProfile{}, errors.New("stored device profile evidence is invalid")
	}
	if item.Paths == nil {
		item.Paths = map[string]string{}
	}
	if item.Evidence == nil {
		item.Evidence = map[string]any{}
	}
	item.Paths, err = normalizeDeviceProfilePaths(item.Paths)
	if err != nil {
		return DeviceProfile{}, fmt.Errorf("device profile %s has invalid stored paths: %w", item.ID, err)
	}
	if err = validateStoredSupportEvidence(item.SupportLevel, item.Evidence, "device_profile", item.ID, item.ContractVersion); err != nil {
		return DeviceProfile{}, fmt.Errorf("device profile %s: %w", item.ID, err)
	}
	item.CaseSensitive, item.SupportsHardlink, item.SupportsHooks, item.Builtin, item.Enabled = caseSensitive != 0, hardlink != 0, hooks != 0, builtin != 0, enabled != 0
	item.CreatedAt, item.UpdatedAt = parseTime(created), parseTime(updated)
	return item, nil
}

const deviceProfileColumns = `id,name,contract_version,target,os_family,distribution,architecture,path_style,case_sensitive,max_path,illegal_characters,supports_hardlink,supports_hooks,default_frontend_id,paths_json,support_level,evidence_json,builtin,enabled,created_at,updated_at`

func (s *Store) GetDeviceProfile(ctx context.Context, id string) (DeviceProfile, error) {
	return scanDeviceProfile(s.db.QueryRowContext(ctx, `SELECT `+deviceProfileColumns+` FROM device_profiles WHERE id=?`, id))
}
func (s *Store) ListDeviceProfiles(ctx context.Context) ([]DeviceProfile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+deviceProfileColumns+` FROM device_profiles ORDER BY enabled DESC,builtin DESC,lower(name),id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []DeviceProfile{}
	for rows.Next() {
		item, e := scanDeviceProfile(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) deviceProfileReferenceCount(ctx context.Context, id string, includeEvidence bool) (int, error) {
	queries := []struct {
		query string
		args  []any
	}{
		{`SELECT COUNT(*) FROM devices WHERE device_profile_id=?`, []any{id}},
		{`SELECT COUNT(*) FROM package_profiles WHERE device_profile_id=?`, []any{id}},
		{`SELECT COUNT(*) FROM launch_bindings WHERE device_profile_id=?`, []any{id}},
		{`SELECT COUNT(*) FROM save_bindings WHERE device_profile_id=?`, []any{id}},
		{`SELECT COUNT(*) FROM core_mappings WHERE scope_type='device_profile' AND scope_key=?`, []any{id}},
		{`SELECT COUNT(*) FROM runtime_import_hints WHERE device_profile_id=?`, []any{id}},
		{`SELECT COUNT(*) FROM pairing_codes WHERE redeemed_at='' AND expires_at>? AND json_extract(requested_device_json,'$.device_profile_id')=?`, []any{nowText(), id}},
	}
	if includeEvidence {
		queries = append(queries, struct {
			query string
			args  []any
		}{`SELECT COUNT(*) FROM runtime_evidence_claims WHERE runtime_kind='device_profile' AND runtime_id=?`, []any{id}})
	}
	total := 0
	for _, item := range queries {
		var count int
		if err := s.db.QueryRowContext(ctx, item.query, item.args...).Scan(&count); err != nil {
			return 0, err
		}
		total += count
	}
	return total, nil
}

func deviceProfileIdentityChanged(current DeviceProfile, next NewDeviceProfile) bool {
	return current.Target != next.Target || current.OSFamily != next.OSFamily || current.Architecture != next.Architecture
}

func (s *Store) UpdateDeviceProfile(ctx context.Context, id string, in NewDeviceProfile) (DeviceProfile, error) {
	current, err := s.GetDeviceProfile(ctx, id)
	if err != nil {
		return DeviceProfile{}, err
	}
	if supportLevelRank(in.SupportLevel) >= supportLevelRank("hardware-tested") {
		if supportLevelRank(current.SupportLevel) < supportLevelRank("hardware-tested") || in.ContractVersion != current.ContractVersion {
			return DeviceProfile{}, errors.New("hardware support claims require reviewed evidence for the current runtime contract")
		}
		if err = validateSupportEvidenceBinding(in.SupportLevel, in.Evidence, "device_profile", id, in.ContractVersion); err != nil {
			return DeviceProfile{}, err
		}
	}
	if current.Builtin {
		return DeviceProfile{}, ErrBuiltinImmutable
	}
	in, err = normalizeDeviceProfile(in)
	if err != nil {
		return DeviceProfile{}, err
	}
	if deviceProfileIdentityChanged(current, in) {
		references, referenceErr := s.deviceProfileReferenceCount(ctx, id, true)
		if referenceErr != nil {
			return DeviceProfile{}, referenceErr
		}
		if references > 0 {
			return DeviceProfile{}, fmt.Errorf("%w: device profile target, os_family, and architecture are immutable while referenced", ErrRuntimeObjectInUse)
		}
	}
	caseSensitive := in.PathStyle != "windows"
	if in.CaseSensitive != nil {
		caseSensitive = *in.CaseSensitive
	}
	result, err := s.db.ExecContext(ctx, `UPDATE device_profiles SET name=?,contract_version=?,target=?,os_family=?,distribution=?,architecture=?,path_style=?,case_sensitive=?,max_path=?,illegal_characters=?,supports_hardlink=?,supports_hooks=?,default_frontend_id=NULLIF(?,''),paths_json=?,support_level=?,evidence_json=?,enabled=?,updated_at=? WHERE id=?`, in.Name, in.ContractVersion, in.Target, in.OSFamily, in.Distribution, in.Architecture, in.PathStyle, boolInt(caseSensitive), in.MaxPath, in.IllegalCharacters, boolInt(in.SupportsHardlink), boolInt(in.SupportsHooks), in.DefaultFrontendID, jsonText(in.Paths, "{}"), in.SupportLevel, jsonText(in.Evidence, "{}"), boolInt(enabledValue(in.Enabled)), nowText(), id)
	if err != nil {
		return DeviceProfile{}, err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return DeviceProfile{}, sql.ErrNoRows
	}
	return s.GetDeviceProfile(ctx, id)
}

func (s *Store) ReconcileBuiltinDeviceProfile(ctx context.Context, in NewDeviceProfile) (DeviceProfile, error) {
	current, err := s.GetDeviceProfile(ctx, in.ID)
	if err != nil {
		return DeviceProfile{}, err
	}
	if !current.Builtin {
		return DeviceProfile{}, errors.New("refusing to reconcile a user-created device profile")
	}
	in.Builtin = true
	in, err = normalizeDeviceProfile(in)
	if err != nil {
		return DeviceProfile{}, err
	}
	if in.ContractVersion <= current.ContractVersion {
		return current, nil
	}
	if deviceProfileIdentityChanged(current, in) {
		references, referenceErr := s.deviceProfileReferenceCount(ctx, in.ID, true)
		if referenceErr != nil {
			return DeviceProfile{}, referenceErr
		}
		if references > 0 {
			return DeviceProfile{}, fmt.Errorf("%w: builtin device profile target, os_family, and architecture are immutable while referenced", ErrRuntimeObjectInUse)
		}
	}
	caseSensitive := in.PathStyle != "windows"
	if in.CaseSensitive != nil {
		caseSensitive = *in.CaseSensitive
	}
	_, err = s.db.ExecContext(ctx, `UPDATE device_profiles SET name=?,contract_version=?,target=?,os_family=?,distribution=?,architecture=?,path_style=?,case_sensitive=?,max_path=?,illegal_characters=?,supports_hardlink=?,supports_hooks=?,default_frontend_id=NULLIF(?,''),paths_json=?,support_level=?,evidence_json=?,enabled=?,updated_at=? WHERE id=? AND builtin=1`, in.Name, in.ContractVersion, in.Target, in.OSFamily, in.Distribution, in.Architecture, in.PathStyle, boolInt(caseSensitive), in.MaxPath, in.IllegalCharacters, boolInt(in.SupportsHardlink), boolInt(in.SupportsHooks), in.DefaultFrontendID, jsonText(in.Paths, "{}"), in.SupportLevel, jsonText(in.Evidence, "{}"), boolInt(enabledValue(in.Enabled)), nowText(), in.ID)
	if err != nil {
		return DeviceProfile{}, err
	}
	return s.GetDeviceProfile(ctx, in.ID)
}
func (s *Store) DeleteDeviceProfile(ctx context.Context, id string) error {
	item, err := s.GetDeviceProfile(ctx, id)
	if err != nil {
		return err
	}
	if item.Builtin {
		return ErrBuiltinImmutable
	}
	references, err := s.deviceProfileReferenceCount(ctx, id, false)
	if err != nil {
		return err
	}
	if references > 0 {
		return ErrRuntimeObjectInUse
	}
	return s.deleteRuntimeObjectWithEvidence(ctx, "device_profiles", "device_profile", id)
}

func normalizeEmulatorDriver(in NewEmulatorDriver) (NewEmulatorDriver, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Family = strings.ToLower(strings.TrimSpace(in.Family))
	if in.Name == "" || in.Family == "" {
		return in, errors.New("name and family are required")
	}
	if in.ContractVersion == 0 {
		in.ContractVersion = 1
	}
	if in.ContractVersion < 1 {
		return in, errors.New("contract_version must be positive")
	}
	if len(in.Platforms) == 0 || len(in.Targets) == 0 {
		return in, errors.New("platforms and targets are required")
	}
	normalizedConfigPaths := map[string]string{}
	for key, value := range in.ConfigPaths {
		key, value = strings.TrimSpace(key), strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
		if key == "" || len(key) > 64 || value == "" || len(value) > 512 || strings.ContainsAny(key+value, "\x00\r\n") || strings.HasPrefix(value, "/") || strings.Contains(value, "../") || value == ".." {
			return in, errors.New("config_paths must contain named relative paths without traversal")
		}
		for _, char := range key {
			if !(char >= 'a' && char <= 'z') && !(char >= 'A' && char <= 'Z') && !(char >= '0' && char <= '9') && char != '-' && char != '_' && char != '.' {
				return in, errors.New("config_paths keys may contain only letters, numbers, dot, dash, and underscore")
			}
		}
		normalizedConfigPaths[key] = value
	}
	in.ConfigPaths = normalizedConfigPaths
	if err := ValidateLaunchArguments(in.Launch.Arguments); err != nil {
		return in, err
	}
	if intent := in.Launch.AndroidIntent; intent != nil {
		intent.Package = strings.TrimSpace(intent.Package)
		candidatePackages := []string{}
		seenPackages := map[string]bool{intent.Package: true}
		for _, candidate := range intent.PackageCandidates {
			candidate = strings.TrimSpace(candidate)
			if candidate != "" && !seenPackages[candidate] {
				candidatePackages = append(candidatePackages, candidate)
				seenPackages[candidate] = true
			}
		}
		intent.PackageCandidates = candidatePackages
		intent.Activity = strings.TrimSpace(intent.Activity)
		intent.Action = strings.TrimSpace(intent.Action)
		intent.Data = strings.TrimSpace(intent.Data)
		intent.MIMEType = strings.TrimSpace(intent.MIMEType)
		portableComponent := regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z][A-Za-z0-9_]*)+$`)
		if !portableComponent.MatchString(intent.Package) {
			return in, errors.New("android intent package must be an explicit Java package name")
		}
		if len(intent.PackageCandidates) > 8 {
			return in, errors.New("android intent package candidates exceed the limit")
		}
		for _, candidate := range intent.PackageCandidates {
			if !portableComponent.MatchString(candidate) {
				return in, errors.New("android intent package candidate must be an explicit Java package name")
			}
		}
		if intent.Activity != "" {
			activity := strings.TrimPrefix(intent.Activity, ".")
			if !portableComponent.MatchString(activity) && !portableComponent.MatchString(intent.Package+"."+activity) {
				return in, errors.New("android intent activity must be an explicit component name")
			}
		}
		if intent.Action == "" {
			intent.Action = "android.intent.action.VIEW"
		}
		if !portableComponent.MatchString(intent.Action) {
			return in, errors.New("android intent action is invalid")
		}
		allowedFlags := map[string]bool{"grant-read-uri": true, "grant-write-uri": true, "new-task": true, "clear-top": true}
		for _, flag := range intent.Flags {
			if !allowedFlags[flag] {
				return in, fmt.Errorf("unsupported android intent flag %q", flag)
			}
		}
		for key, value := range intent.StringExtras {
			if strings.TrimSpace(key) == "" || len(key) > 128 || strings.ContainsAny(key+value, "\x00\r\n") || len(value) > 2048 {
				return in, errors.New("android intent string extra is invalid")
			}
			if err := validateAndroidIntentTemplate(value); err != nil {
				return in, err
			}
		}
		for _, value := range append(append([]string{}, intent.Categories...), intent.Data, intent.MIMEType) {
			if len(value) > 2048 || strings.ContainsAny(value, "\x00\r\n") {
				return in, errors.New("android intent value is invalid")
			}
			if err := validateAndroidIntentTemplate(value); err != nil {
				return in, err
			}
		}
		in.Launch.AndroidPackage, in.Launch.AndroidActivity = intent.Package, intent.Activity
	}
	if in.Save.Scope == "" {
		in.Save.Scope = "game"
	}
	validScope := func(scope string) bool { return scope == "game" || scope == "platform" || scope == "container" }
	if !validScope(in.Save.Scope) {
		return in, errors.New("save scope must be game, platform, or container")
	}
	for platform, scope := range in.Save.ScopeByPlatform {
		if strings.TrimSpace(platform) == "" || !validScope(scope) {
			return in, errors.New("save scope_by_platform must map platform IDs to game, platform, or container")
		}
	}
	patterns := append([]string{}, in.Save.Patterns...)
	for platform, values := range in.Save.PatternsByPlatform {
		if strings.TrimSpace(platform) == "" {
			return in, errors.New("save patterns_by_platform requires non-empty platform IDs")
		}
		patterns = append(patterns, values...)
	}
	for _, pattern := range patterns {
		if err := ValidateSavePathTemplate(pattern); err != nil {
			return in, fmt.Errorf("driver save pattern: %w", err)
		}
	}
	level, err := normalizeSupportLevel(in.SupportLevel)
	in.SupportLevel = level
	if err == nil {
		err = validateSupportEvidence(level, in.Evidence)
	}
	return in, err
}
func (s *Store) CreateEmulatorDriver(ctx context.Context, in NewEmulatorDriver) (EmulatorDriver, error) {
	in, err := normalizeEmulatorDriver(in)
	if err != nil {
		return EmulatorDriver{}, err
	}
	if in.ID == "" {
		in.ID = NewID()
	}
	if err = validateBuiltinID(in.ID, in.Builtin); err != nil {
		return EmulatorDriver{}, err
	}
	if in.SupportLevel == "hardware-tested" || in.SupportLevel == "sync-tested" {
		return EmulatorDriver{}, errors.New("hardware support claims require reviewed evidence")
	}
	now := nowText()
	_, err = s.db.ExecContext(ctx, `INSERT INTO emulator_drivers(id,name,family,contract_version,platforms_json,targets_json,launch_json,save_json,config_paths_json,support_level,evidence_json,builtin,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, in.ID, in.Name, in.Family, in.ContractVersion, jsonText(in.Platforms, "[]"), jsonText(in.Targets, "[]"), jsonText(in.Launch, "{}"), jsonText(in.Save, "{}"), jsonText(in.ConfigPaths, "{}"), in.SupportLevel, jsonText(in.Evidence, "{}"), boolInt(in.Builtin), boolInt(enabledValue(in.Enabled)), now, now)
	if err != nil {
		return EmulatorDriver{}, err
	}
	return s.GetEmulatorDriver(ctx, in.ID)
}

// ReconcileBuiltinEmulatorDriver upgrades a curated built-in contract while
// keeping user-created drivers immutable from seed updates. A contract version
// must increase for any built-in change, so ordinary restarts do not rewrite
// catalog timestamps or silently alter a frozen adapter contract.
func (s *Store) ReconcileBuiltinEmulatorDriver(ctx context.Context, in NewEmulatorDriver) (EmulatorDriver, error) {
	current, err := s.GetEmulatorDriver(ctx, in.ID)
	if err != nil {
		return EmulatorDriver{}, err
	}
	if !current.Builtin {
		return EmulatorDriver{}, errors.New("refusing to reconcile a user-created emulator driver")
	}
	in.Builtin = true
	in, err = normalizeEmulatorDriver(in)
	if err != nil {
		return EmulatorDriver{}, err
	}
	if in.ContractVersion <= current.ContractVersion {
		return current, nil
	}
	_, err = s.db.ExecContext(ctx, `UPDATE emulator_drivers SET name=?,family=?,contract_version=?,platforms_json=?,targets_json=?,launch_json=?,save_json=?,config_paths_json=?,support_level=?,evidence_json=?,enabled=?,updated_at=? WHERE id=? AND builtin=1`, in.Name, in.Family, in.ContractVersion, jsonText(in.Platforms, "[]"), jsonText(in.Targets, "[]"), jsonText(in.Launch, "{}"), jsonText(in.Save, "{}"), jsonText(in.ConfigPaths, "{}"), in.SupportLevel, jsonText(in.Evidence, "{}"), boolInt(enabledValue(in.Enabled)), nowText(), in.ID)
	if err != nil {
		return EmulatorDriver{}, err
	}
	return s.GetEmulatorDriver(ctx, in.ID)
}
func scanEmulatorDriver(scanner interface{ Scan(...any) error }) (EmulatorDriver, error) {
	var item EmulatorDriver
	var platforms, targets, launch, save, config, evidence, created, updated string
	var builtin, enabled int
	err := scanner.Scan(&item.ID, &item.Name, &item.Family, &item.ContractVersion, &platforms, &targets, &launch, &save, &config, &item.SupportLevel, &evidence, &builtin, &enabled, &created, &updated)
	if err != nil {
		return EmulatorDriver{}, err
	}
	if err = json.Unmarshal([]byte(platforms), &item.Platforms); err != nil {
		return EmulatorDriver{}, errors.New("stored emulator driver platforms are invalid")
	}
	if err = json.Unmarshal([]byte(targets), &item.Targets); err != nil {
		return EmulatorDriver{}, errors.New("stored emulator driver targets are invalid")
	}
	if err = json.Unmarshal([]byte(launch), &item.Launch); err != nil {
		return EmulatorDriver{}, errors.New("stored emulator driver launch contract is invalid")
	}
	if err = json.Unmarshal([]byte(save), &item.Save); err != nil {
		return EmulatorDriver{}, errors.New("stored emulator driver save contract is invalid")
	}
	if err = json.Unmarshal([]byte(config), &item.ConfigPaths); err != nil {
		return EmulatorDriver{}, errors.New("stored emulator driver config paths are invalid")
	}
	if err = json.Unmarshal([]byte(evidence), &item.Evidence); err != nil {
		return EmulatorDriver{}, errors.New("stored emulator driver evidence is invalid")
	}
	if item.Platforms == nil {
		item.Platforms = []string{}
	}
	if item.Targets == nil {
		item.Targets = []string{}
	}
	if item.ConfigPaths == nil {
		item.ConfigPaths = map[string]string{}
	}
	if item.Evidence == nil {
		item.Evidence = map[string]any{}
	}
	if err = validateStoredSupportEvidence(item.SupportLevel, item.Evidence, "emulator_driver", item.ID, item.ContractVersion); err != nil {
		return EmulatorDriver{}, fmt.Errorf("emulator driver %s: %w", item.ID, err)
	}
	item.Builtin, item.Enabled = builtin != 0, enabled != 0
	item.CreatedAt, item.UpdatedAt = parseTime(created), parseTime(updated)
	return item, nil
}

const emulatorDriverColumns = `id,name,family,contract_version,platforms_json,targets_json,launch_json,save_json,config_paths_json,support_level,evidence_json,builtin,enabled,created_at,updated_at`

func (s *Store) GetEmulatorDriver(ctx context.Context, id string) (EmulatorDriver, error) {
	return scanEmulatorDriver(s.db.QueryRowContext(ctx, `SELECT `+emulatorDriverColumns+` FROM emulator_drivers WHERE id=?`, id))
}
func (s *Store) ListEmulatorDrivers(ctx context.Context) ([]EmulatorDriver, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+emulatorDriverColumns+` FROM emulator_drivers ORDER BY enabled DESC,builtin DESC,lower(name),id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []EmulatorDriver{}
	for rows.Next() {
		item, e := scanEmulatorDriver(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (s *Store) UpdateEmulatorDriver(ctx context.Context, id string, in NewEmulatorDriver) (EmulatorDriver, error) {
	current, err := s.GetEmulatorDriver(ctx, id)
	if err != nil {
		return EmulatorDriver{}, err
	}
	if supportLevelRank(in.SupportLevel) >= supportLevelRank("hardware-tested") {
		if supportLevelRank(current.SupportLevel) < supportLevelRank("hardware-tested") || in.ContractVersion != current.ContractVersion {
			return EmulatorDriver{}, errors.New("hardware support claims require reviewed evidence for the current runtime contract")
		}
		if err = validateSupportEvidenceBinding(in.SupportLevel, in.Evidence, "emulator_driver", id, in.ContractVersion); err != nil {
			return EmulatorDriver{}, err
		}
	}
	if current.Builtin {
		return EmulatorDriver{}, ErrBuiltinImmutable
	}
	in, err = normalizeEmulatorDriver(in)
	if err != nil {
		return EmulatorDriver{}, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE emulator_drivers SET name=?,family=?,contract_version=?,platforms_json=?,targets_json=?,launch_json=?,save_json=?,config_paths_json=?,support_level=?,evidence_json=?,enabled=?,updated_at=? WHERE id=?`, in.Name, in.Family, in.ContractVersion, jsonText(in.Platforms, "[]"), jsonText(in.Targets, "[]"), jsonText(in.Launch, "{}"), jsonText(in.Save, "{}"), jsonText(in.ConfigPaths, "{}"), in.SupportLevel, jsonText(in.Evidence, "{}"), boolInt(enabledValue(in.Enabled)), nowText(), id)
	if err != nil {
		return EmulatorDriver{}, err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return EmulatorDriver{}, sql.ErrNoRows
	}
	return s.GetEmulatorDriver(ctx, id)
}
func (s *Store) DeleteEmulatorDriver(ctx context.Context, id string) error {
	item, err := s.GetEmulatorDriver(ctx, id)
	if err != nil {
		return err
	}
	if item.Builtin {
		return ErrBuiltinImmutable
	}
	return s.deleteRuntimeObjectWithEvidence(ctx, "emulator_drivers", "emulator_driver", id)
}

func normalizeRetroArchCore(in NewRetroArchCore) (NewRetroArchCore, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.LibraryNames) == 0 || len(in.Platforms) == 0 {
		return in, errors.New("name, library_names, and platforms are required")
	}
	if in.ContractVersion == 0 {
		in.ContractVersion = 1
	}
	if in.ContractVersion < 1 {
		return in, errors.New("contract_version must be positive")
	}
	level, err := normalizeSupportLevel(in.SupportLevel)
	in.SupportLevel = level
	if err == nil {
		err = validateSupportEvidence(level, in.Evidence)
	}
	return in, err
}

// ValidateSupportEvidence audits persisted runtime-catalog claims. It catches
// unsupported direct database edits without treating a preset or fixture as
// proof of real hardware behavior.
func (s *Store) ValidateSupportEvidence(ctx context.Context) error {
	tables := []struct {
		name string
		kind string
	}{
		{"source_adapters", "source_adapter"},
		{"frontend_adapters", "frontend_adapter"},
		{"device_profiles", "device_profile"},
		{"emulator_drivers", "emulator_driver"},
		{"retroarch_cores", "retroarch_core"},
	}
	for _, table := range tables {
		rows, err := s.db.QueryContext(ctx, `SELECT id,contract_version,support_level,evidence_json FROM `+table.name+` WHERE support_level IN ('hardware-tested','sync-tested')`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id, level, encoded string
			var contractVersion int
			if err = rows.Scan(&id, &contractVersion, &level, &encoded); err != nil {
				rows.Close()
				return err
			}
			var evidence map[string]any
			if err = json.Unmarshal([]byte(encoded), &evidence); err != nil {
				rows.Close()
				return fmt.Errorf("support evidence for %s %s is not valid JSON", table.name, id)
			}
			if err = validateSupportEvidence(level, evidence); err == nil {
				err = validateSupportEvidenceBinding(level, evidence, table.kind, id, contractVersion)
			}
			if err != nil {
				rows.Close()
				return fmt.Errorf("support evidence for %s %s: %w", table.name, id, err)
			}
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	return nil
}

// ValidateRuntimeCatalog applies the same read-time safety boundary used by
// the API to every persisted runtime object. It additionally catches malformed
// JSON and non-portable DeviceProfile paths introduced by legacy data or direct
// database edits.
func (s *Store) ValidateRuntimeCatalog(ctx context.Context) error {
	checks := []struct {
		name string
		run  func() error
	}{
		{"source adapters", func() error { _, err := s.ListSourceAdapters(ctx); return err }},
		{"frontend adapters", func() error { _, err := s.ListFrontendAdapters(ctx); return err }},
		{"device profiles", func() error { _, err := s.ListDeviceProfiles(ctx); return err }},
		{"emulator drivers", func() error { _, err := s.ListEmulatorDrivers(ctx); return err }},
		{"RetroArch cores", func() error { _, err := s.ListRetroArchCores(ctx); return err }},
		{"custom platforms", func() error {
			items, err := s.ListCustomPlatforms(ctx, true)
			if err != nil {
				return err
			}
			for _, item := range items {
				if err = platforms.ValidateCustom(item.Platform()); err != nil {
					return fmt.Errorf("platform %s: %w", item.ID, err)
				}
			}
			_, err = s.PlatformRegistry(ctx)
			return err
		}},
	}
	for _, check := range checks {
		if err := check.run(); err != nil {
			return fmt.Errorf("runtime catalog %s: %w", check.name, err)
		}
	}
	return nil
}
func (s *Store) CreateRetroArchCore(ctx context.Context, in NewRetroArchCore) (RetroArchCore, error) {
	in, err := normalizeRetroArchCore(in)
	if err != nil {
		return RetroArchCore{}, err
	}
	if in.ID == "" {
		in.ID = NewID()
	}
	if err = validateBuiltinID(in.ID, in.Builtin); err != nil {
		return RetroArchCore{}, err
	}
	if in.SupportLevel == "hardware-tested" || in.SupportLevel == "sync-tested" {
		return RetroArchCore{}, errors.New("hardware support claims require reviewed evidence")
	}
	now := nowText()
	_, err = s.db.ExecContext(ctx, `INSERT INTO retroarch_cores(id,name,contract_version,library_names_json,platforms_json,support_level,evidence_json,builtin,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, in.ID, in.Name, in.ContractVersion, jsonText(in.LibraryNames, "[]"), jsonText(in.Platforms, "[]"), in.SupportLevel, jsonText(in.Evidence, "{}"), boolInt(in.Builtin), boolInt(enabledValue(in.Enabled)), now, now)
	if err != nil {
		return RetroArchCore{}, err
	}
	return s.GetRetroArchCore(ctx, in.ID)
}
func scanRetroArchCore(scanner interface{ Scan(...any) error }) (RetroArchCore, error) {
	var item RetroArchCore
	var libraries, platforms, evidence, created, updated string
	var builtin, enabled int
	err := scanner.Scan(&item.ID, &item.Name, &item.ContractVersion, &libraries, &platforms, &item.SupportLevel, &evidence, &builtin, &enabled, &created, &updated)
	if err != nil {
		return RetroArchCore{}, err
	}
	if err = json.Unmarshal([]byte(libraries), &item.LibraryNames); err != nil {
		return RetroArchCore{}, errors.New("stored RetroArch core libraries are invalid")
	}
	if err = json.Unmarshal([]byte(platforms), &item.Platforms); err != nil {
		return RetroArchCore{}, errors.New("stored RetroArch core platforms are invalid")
	}
	if err = json.Unmarshal([]byte(evidence), &item.Evidence); err != nil {
		return RetroArchCore{}, errors.New("stored RetroArch core evidence is invalid")
	}
	if item.LibraryNames == nil {
		item.LibraryNames = []string{}
	}
	if item.Platforms == nil {
		item.Platforms = []string{}
	}
	if item.Evidence == nil {
		item.Evidence = map[string]any{}
	}
	if err = validateStoredSupportEvidence(item.SupportLevel, item.Evidence, "retroarch_core", item.ID, item.ContractVersion); err != nil {
		return RetroArchCore{}, fmt.Errorf("RetroArch core %s: %w", item.ID, err)
	}
	item.Builtin, item.Enabled = builtin != 0, enabled != 0
	item.CreatedAt, item.UpdatedAt = parseTime(created), parseTime(updated)
	return item, nil
}

const retroArchCoreColumns = `id,name,contract_version,library_names_json,platforms_json,support_level,evidence_json,builtin,enabled,created_at,updated_at`

func (s *Store) GetRetroArchCore(ctx context.Context, id string) (RetroArchCore, error) {
	return scanRetroArchCore(s.db.QueryRowContext(ctx, `SELECT `+retroArchCoreColumns+` FROM retroarch_cores WHERE id=?`, id))
}
func (s *Store) ListRetroArchCores(ctx context.Context) ([]RetroArchCore, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+retroArchCoreColumns+` FROM retroarch_cores ORDER BY enabled DESC,builtin DESC,lower(name),id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []RetroArchCore{}
	for rows.Next() {
		item, e := scanRetroArchCore(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (s *Store) UpdateRetroArchCore(ctx context.Context, id string, in NewRetroArchCore) (RetroArchCore, error) {
	current, err := s.GetRetroArchCore(ctx, id)
	if err != nil {
		return RetroArchCore{}, err
	}
	if supportLevelRank(in.SupportLevel) >= supportLevelRank("hardware-tested") {
		if supportLevelRank(current.SupportLevel) < supportLevelRank("hardware-tested") || in.ContractVersion != current.ContractVersion {
			return RetroArchCore{}, errors.New("hardware support claims require reviewed evidence for the current runtime contract")
		}
		if err = validateSupportEvidenceBinding(in.SupportLevel, in.Evidence, "retroarch_core", id, in.ContractVersion); err != nil {
			return RetroArchCore{}, err
		}
	}
	if current.Builtin {
		return RetroArchCore{}, ErrBuiltinImmutable
	}
	in, err = normalizeRetroArchCore(in)
	if err != nil {
		return RetroArchCore{}, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE retroarch_cores SET name=?,contract_version=?,library_names_json=?,platforms_json=?,support_level=?,evidence_json=?,enabled=?,updated_at=? WHERE id=?`, in.Name, in.ContractVersion, jsonText(in.LibraryNames, "[]"), jsonText(in.Platforms, "[]"), in.SupportLevel, jsonText(in.Evidence, "{}"), boolInt(enabledValue(in.Enabled)), nowText(), id)
	if err != nil {
		return RetroArchCore{}, err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return RetroArchCore{}, sql.ErrNoRows
	}
	return s.GetRetroArchCore(ctx, id)
}

func (s *Store) ReconcileBuiltinRetroArchCore(ctx context.Context, in NewRetroArchCore) (RetroArchCore, error) {
	current, err := s.GetRetroArchCore(ctx, in.ID)
	if err != nil {
		return RetroArchCore{}, err
	}
	if !current.Builtin {
		return RetroArchCore{}, errors.New("refusing to reconcile a user-created RetroArch core")
	}
	in.Builtin = true
	in, err = normalizeRetroArchCore(in)
	if err != nil {
		return RetroArchCore{}, err
	}
	if in.ContractVersion <= current.ContractVersion {
		return current, nil
	}
	_, err = s.db.ExecContext(ctx, `UPDATE retroarch_cores SET name=?,contract_version=?,library_names_json=?,platforms_json=?,support_level=?,evidence_json=?,enabled=?,updated_at=? WHERE id=? AND builtin=1`, in.Name, in.ContractVersion, jsonText(in.LibraryNames, "[]"), jsonText(in.Platforms, "[]"), in.SupportLevel, jsonText(in.Evidence, "{}"), boolInt(enabledValue(in.Enabled)), nowText(), in.ID)
	if err != nil {
		return RetroArchCore{}, err
	}
	return s.GetRetroArchCore(ctx, in.ID)
}
func (s *Store) DeleteRetroArchCore(ctx context.Context, id string) error {
	item, err := s.GetRetroArchCore(ctx, id)
	if err != nil {
		return err
	}
	if item.Builtin {
		return ErrBuiltinImmutable
	}
	return s.deleteRuntimeObjectWithEvidence(ctx, "retroarch_cores", "retroarch_core", id)
}

func normalizeCoreMapping(in NewCoreMapping) (NewCoreMapping, error) {
	in.ScopeType = strings.ToLower(strings.TrimSpace(in.ScopeType))
	in.ScopeKey = strings.TrimSpace(in.ScopeKey)
	in.PlatformID = strings.ToLower(strings.TrimSpace(in.PlatformID))
	in.CoreID = strings.TrimSpace(in.CoreID)
	if in.PlatformID == "" || in.CoreID == "" {
		return in, errors.New("platform_id and core_id are required")
	}
	switch in.ScopeType {
	case "global":
		if in.ScopeKey != "" {
			return in, errors.New("global core mapping scope_key must be empty")
		}
	case "platform":
		if in.ScopeKey != "" {
			return in, errors.New("platform core mapping scope_key must be empty")
		}
	case "device_profile", "edition":
		if in.ScopeKey == "" {
			return in, fmt.Errorf("%s core mapping requires scope_key", in.ScopeType)
		}
	default:
		return in, errors.New("scope_type must be global, platform, device_profile, or edition")
	}
	return in, nil
}
func (s *Store) CreateCoreMapping(ctx context.Context, in NewCoreMapping) (CoreMapping, error) {
	in, err := normalizeCoreMapping(in)
	if err != nil {
		return CoreMapping{}, err
	}
	if in.ID == "" {
		in.ID = NewID()
	}
	if err = validateBuiltinID(in.ID, in.Builtin); err != nil {
		return CoreMapping{}, err
	}
	now := nowText()
	_, err = s.db.ExecContext(ctx, `INSERT INTO core_mappings(id,scope_type,scope_key,platform_id,core_id,priority,notes,builtin,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, in.ID, in.ScopeType, in.ScopeKey, in.PlatformID, in.CoreID, in.Priority, strings.TrimSpace(in.Notes), boolInt(in.Builtin), now, now)
	if err != nil {
		return CoreMapping{}, err
	}
	return s.GetCoreMapping(ctx, in.ID)
}
func scanCoreMapping(scanner interface{ Scan(...any) error }) (CoreMapping, error) {
	var item CoreMapping
	var builtin int
	var created, updated string
	err := scanner.Scan(&item.ID, &item.ScopeType, &item.ScopeKey, &item.PlatformID, &item.CoreID, &item.Priority, &item.Notes, &builtin, &created, &updated)
	item.Builtin = builtin != 0
	item.CreatedAt, item.UpdatedAt = parseTime(created), parseTime(updated)
	return item, err
}

const coreMappingColumns = `id,scope_type,scope_key,platform_id,core_id,priority,notes,builtin,created_at,updated_at`

func (s *Store) GetCoreMapping(ctx context.Context, id string) (CoreMapping, error) {
	return scanCoreMapping(s.db.QueryRowContext(ctx, `SELECT `+coreMappingColumns+` FROM core_mappings WHERE id=?`, id))
}
func (s *Store) ListCoreMappings(ctx context.Context, platformID string) ([]CoreMapping, error) {
	query := `SELECT ` + coreMappingColumns + ` FROM core_mappings`
	args := []any{}
	if strings.TrimSpace(platformID) != "" {
		query += ` WHERE platform_id=?`
		args = append(args, strings.ToLower(strings.TrimSpace(platformID)))
	}
	query += ` ORDER BY platform_id,priority DESC,scope_type,scope_key,id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []CoreMapping{}
	for rows.Next() {
		item, e := scanCoreMapping(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (s *Store) UpdateCoreMapping(ctx context.Context, id string, in NewCoreMapping) (CoreMapping, error) {
	current, err := s.GetCoreMapping(ctx, id)
	if err != nil {
		return CoreMapping{}, err
	}
	if current.Builtin {
		return CoreMapping{}, ErrBuiltinImmutable
	}
	in, err = normalizeCoreMapping(in)
	if err != nil {
		return CoreMapping{}, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE core_mappings SET scope_type=?,scope_key=?,platform_id=?,core_id=?,priority=?,notes=?,updated_at=? WHERE id=?`, in.ScopeType, in.ScopeKey, in.PlatformID, in.CoreID, in.Priority, strings.TrimSpace(in.Notes), nowText(), id)
	if err != nil {
		return CoreMapping{}, err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return CoreMapping{}, sql.ErrNoRows
	}
	return s.GetCoreMapping(ctx, id)
}
func (s *Store) DeleteCoreMapping(ctx context.Context, id string) error {
	item, err := s.GetCoreMapping(ctx, id)
	if err != nil {
		return err
	}
	if item.Builtin {
		return ErrBuiltinImmutable
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM core_mappings WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
func (s *Store) ResolveCore(ctx context.Context, platformID, editionID, deviceProfileID string) (CoreResolution, error) {
	platformID = strings.ToLower(strings.TrimSpace(platformID))
	editionID = strings.TrimSpace(editionID)
	deviceProfileID = strings.TrimSpace(deviceProfileID)
	resolution := CoreResolution{PlatformID: platformID, EditionID: editionID, DeviceProfileID: deviceProfileID, Resolution: "unmapped"}
	candidates := [][2]string{{"edition", editionID}, {"device_profile", deviceProfileID}, {"platform", ""}, {"global", ""}}
	for _, candidate := range candidates {
		if candidate[1] == "" && (candidate[0] == "edition" || candidate[0] == "device_profile") {
			continue
		}
		mapping, err := scanCoreMapping(s.db.QueryRowContext(ctx, `SELECT `+coreMappingColumns+` FROM core_mappings WHERE platform_id=? AND scope_type=? AND scope_key=? ORDER BY priority DESC,id LIMIT 1`, platformID, candidate[0], candidate[1]))
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return resolution, err
		}
		core, err := s.GetRetroArchCore(ctx, mapping.CoreID)
		if err != nil {
			return resolution, err
		}
		resolution.Mapping = &mapping
		resolution.Core = &core
		resolution.Resolution = candidate[0]
		return resolution, nil
	}
	return resolution, nil
}

func normalizeLaunchBinding(in NewLaunchBinding) (NewLaunchBinding, error) {
	in.EditionID = strings.TrimSpace(in.EditionID)
	in.DeviceProfileID = strings.TrimSpace(in.DeviceProfileID)
	in.DriverID = strings.TrimSpace(in.DriverID)
	in.FrontendAdapterID = strings.TrimSpace(in.FrontendAdapterID)
	in.CoreID = strings.TrimSpace(in.CoreID)
	if in.EditionID == "" || in.DriverID == "" {
		return in, errors.New("edition_id and driver_id are required")
	}
	if err := ValidateLaunchArguments(in.Arguments); err != nil {
		return in, err
	}
	return in, nil
}
func (s *Store) CreateLaunchBinding(ctx context.Context, in NewLaunchBinding) (LaunchBinding, error) {
	in, err := normalizeLaunchBinding(in)
	if err != nil {
		return LaunchBinding{}, err
	}
	if in.ID == "" {
		in.ID = NewID()
	}
	now := nowText()
	_, err = s.db.ExecContext(ctx, `INSERT INTO launch_bindings(id,edition_id,device_profile_id,driver_id,frontend_adapter_id,core_id,arguments_json,enabled,created_at,updated_at) VALUES(?,?,NULLIF(?,''),?,NULLIF(?,''),NULLIF(?,''),?,?,?,?)`, in.ID, in.EditionID, in.DeviceProfileID, in.DriverID, in.FrontendAdapterID, in.CoreID, jsonText(in.Arguments, "[]"), boolInt(enabledValue(in.Enabled)), now, now)
	if err != nil {
		return LaunchBinding{}, err
	}
	return s.GetLaunchBinding(ctx, in.ID)
}
func scanLaunchBinding(scanner interface{ Scan(...any) error }) (LaunchBinding, error) {
	var item LaunchBinding
	var device, frontend, core sql.NullString
	var arguments, created, updated string
	var enabled int
	err := scanner.Scan(&item.ID, &item.EditionID, &device, &item.DriverID, &frontend, &core, &arguments, &enabled, &created, &updated)
	item.DeviceProfileID, item.FrontendAdapterID, item.CoreID = device.String, frontend.String, core.String
	_ = json.Unmarshal([]byte(arguments), &item.Arguments)
	if item.Arguments == nil {
		item.Arguments = []string{}
	}
	item.Enabled = enabled != 0
	item.CreatedAt, item.UpdatedAt = parseTime(created), parseTime(updated)
	return item, err
}

const launchBindingColumns = `id,edition_id,device_profile_id,driver_id,frontend_adapter_id,core_id,arguments_json,enabled,created_at,updated_at`

func (s *Store) GetLaunchBinding(ctx context.Context, id string) (LaunchBinding, error) {
	return scanLaunchBinding(s.db.QueryRowContext(ctx, `SELECT `+launchBindingColumns+` FROM launch_bindings WHERE id=?`, id))
}
func (s *Store) ListLaunchBindings(ctx context.Context, editionID string) ([]LaunchBinding, error) {
	query := `SELECT ` + launchBindingColumns + ` FROM launch_bindings`
	args := []any{}
	if strings.TrimSpace(editionID) != "" {
		query += ` WHERE edition_id=?`
		args = append(args, strings.TrimSpace(editionID))
	}
	query += ` ORDER BY edition_id,device_profile_id IS NULL DESC,device_profile_id,id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []LaunchBinding{}
	for rows.Next() {
		item, e := scanLaunchBinding(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (s *Store) UpdateLaunchBinding(ctx context.Context, id string, in NewLaunchBinding) (LaunchBinding, error) {
	in, err := normalizeLaunchBinding(in)
	if err != nil {
		return LaunchBinding{}, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE launch_bindings SET edition_id=?,device_profile_id=NULLIF(?,''),driver_id=?,frontend_adapter_id=NULLIF(?,''),core_id=NULLIF(?,''),arguments_json=?,enabled=?,updated_at=? WHERE id=?`, in.EditionID, in.DeviceProfileID, in.DriverID, in.FrontendAdapterID, in.CoreID, jsonText(in.Arguments, "[]"), boolInt(enabledValue(in.Enabled)), nowText(), id)
	if err != nil {
		return LaunchBinding{}, err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return LaunchBinding{}, sql.ErrNoRows
	}
	return s.GetLaunchBinding(ctx, id)
}
func (s *Store) DeleteLaunchBinding(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM launch_bindings WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
func (s *Store) ResolveLaunchBinding(ctx context.Context, editionID, deviceProfileID string) (LaunchBinding, error) {
	editionID, deviceProfileID = strings.TrimSpace(editionID), strings.TrimSpace(deviceProfileID)
	if deviceProfileID != "" {
		item, err := scanLaunchBinding(s.db.QueryRowContext(ctx, `SELECT `+launchBindingColumns+` FROM launch_bindings WHERE edition_id=? AND device_profile_id=? AND enabled=1 LIMIT 1`, editionID, deviceProfileID))
		if err == nil {
			return item, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return LaunchBinding{}, err
		}
	}
	return scanLaunchBinding(s.db.QueryRowContext(ctx, `SELECT `+launchBindingColumns+` FROM launch_bindings WHERE edition_id=? AND device_profile_id IS NULL AND enabled=1 LIMIT 1`, editionID))
}
