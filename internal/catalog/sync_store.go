package catalog

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var allowedSavePathVariables = map[string]bool{
	"edition.id": true, "edition.save_namespace": true, "edition.serial": true,
	"edition.product_code": true, "edition.title_id": true,
	"edition.title_id_high": true, "edition.title_id_low": true,
	"platform.id": true, "rom.stem": true,
	"driver.id": true, "driver.user_dir": true,
	"device.id": true, "device.target": true,
	"device.config_dir": true, "device.save_dir": true, "device.core_dir": true, "device.emulator_dir": true,
}

// ValidateSavePathTemplate accepts only inert path templates. Host roots must
// come from an explicitly paired device configuration; environment variables,
// shell expressions and conditionals are not part of the format.
func ValidateSavePathTemplate(value string) error {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" || len(value) > 1024 || strings.ContainsAny(value, "\x00\r\n") {
		return errors.New("invalid local save path template")
	}
	if strings.HasPrefix(value, "/") || (len(value) >= 3 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':' && value[2] == '/') {
		return errors.New("local save path templates must begin with an authorized root variable or a relative path")
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." {
			return errors.New("local save path templates must not contain parent traversal")
		}
	}
	cleaned := launchVariablePattern.ReplaceAllStringFunc(value, func(match string) string {
		name := strings.TrimSuffix(strings.TrimPrefix(match, "{{"), "}}")
		if allowedSavePathVariables[name] {
			return ""
		}
		return match
	})
	if strings.Contains(cleaned, "{{") || strings.Contains(cleaned, "}}") || strings.ContainsAny(cleaned, "{}") {
		return errors.New("local save path contains an unknown or malformed template variable")
	}
	return nil
}

func normalizeSaveBinding(in NewSaveBinding) (NewSaveBinding, error) {
	in.StreamID = strings.TrimSpace(in.StreamID)
	in.EditionID = strings.TrimSpace(in.EditionID)
	in.DeviceProfileID = strings.TrimSpace(in.DeviceProfileID)
	in.DriverID = strings.TrimSpace(in.DriverID)
	in.CoreID = strings.TrimSpace(in.CoreID)
	if in.StreamID == "" || in.EditionID == "" || in.DriverID == "" {
		return in, errors.New("stream_id, edition_id, and driver_id are required")
	}
	if len(in.LocalPaths) == 0 || len(in.LocalPaths) > 32 {
		return in, errors.New("local_paths must contain between 1 and 32 paths")
	}
	seen := map[string]bool{}
	paths := make([]string, 0, len(in.LocalPaths))
	for _, value := range in.LocalPaths {
		value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
		if err := ValidateSavePathTemplate(value); err != nil {
			return in, err
		}
		if !seen[value] {
			seen[value] = true
			paths = append(paths, value)
		}
	}
	in.LocalPaths = paths
	if in.Discovery == nil {
		in.Discovery = map[string]any{}
	}
	return in, nil
}

type saveBindingIdentityQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// validateSaveBindingIdentityContext prevents an identity placeholder from
// collapsing into a broader parent directory. Device-local roots and ROM
// basenames are still resolved by the Agent, but edition metadata is known at
// control-plane write time and must fail atomically before a binding is saved.
func validateSaveBindingIdentityContext(ctx context.Context, query saveBindingIdentityQuerier, in NewSaveBinding) error {
	var serial, productCode, titleID string
	if err := query.QueryRowContext(ctx, `SELECT COALESCE(serial,''),COALESCE(product_code,''),COALESCE(title_id,'') FROM editions WHERE id=?`, in.EditionID).Scan(&serial, &productCode, &titleID); err != nil {
		return err
	}
	uses := func(variable string) bool {
		placeholder := "{{" + variable + "}}"
		for _, path := range in.LocalPaths {
			if strings.Contains(path, placeholder) {
				return true
			}
		}
		return false
	}
	if uses("edition.serial") && strings.TrimSpace(serial) == "" {
		return &SaveBindingIdentityError{Field: "edition.serial"}
	}
	if uses("edition.product_code") && strings.TrimSpace(productCode) == "" {
		return &SaveBindingIdentityError{Field: "edition.product_code"}
	}
	if uses("edition.title_id") && strings.TrimSpace(titleID) == "" {
		return &SaveBindingIdentityError{Field: "edition.title_id"}
	}
	if uses("edition.title_id_high") || uses("edition.title_id_low") {
		normalized := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(titleID), "-", ""), " ", "")
		decoded, err := hex.DecodeString(normalized)
		if err != nil || len(decoded) != 8 {
			return &SaveBindingIdentityError{Field: "edition.title_id", Requirement: "a 16-hex"}
		}
	}
	return nil
}

func (s *Store) validateSaveBindingRelations(ctx context.Context, in NewSaveBinding) error {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM save_streams s JOIN save_stream_editions se ON se.stream_id=s.id WHERE s.id=? AND se.edition_id=?`, in.StreamID, in.EditionID).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return errors.New("save binding must use a stream and edition that belong together")
	}
	var streamDriver, groupID string
	if err := s.db.QueryRowContext(ctx, `SELECT driver_id,COALESCE(compatibility_group_id,'') FROM save_streams WHERE id=?`, in.StreamID).Scan(&streamDriver, &groupID); err != nil {
		return err
	}
	if streamDriver != in.DriverID {
		if groupID == "" {
			return errors.New("cross-driver save binding requires a verified compatibility group")
		}
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM save_compatibility_groups g JOIN save_compatibility_members m ON m.group_id=g.id WHERE g.id=? AND g.enabled=1 AND m.driver_id=? AND m.core_id=? AND m.runtime_kind='device'`, groupID, in.DriverID, in.CoreID).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			return errors.New("cross-driver save binding is not an exact compatibility member")
		}
	}
	if in.CoreID != "" {
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM retroarch_cores WHERE id=? AND enabled=1`, in.CoreID).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			return errors.New("core_id must reference an enabled RetroArch core")
		}
	}
	if in.DeviceProfileID != "" {
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM device_profiles WHERE id=? AND enabled=1`, in.DeviceProfileID).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			return errors.New("device_profile_id must reference an enabled device profile")
		}
	}
	return nil
}

func (s *Store) CreateSaveBinding(ctx context.Context, in NewSaveBinding) (SaveBinding, error) {
	in, err := normalizeSaveBinding(in)
	if err != nil {
		return SaveBinding{}, err
	}
	if in.ID == "" {
		in.ID = NewID()
	}
	if err = s.validateSaveBindingRelations(ctx, in); err != nil {
		return SaveBinding{}, err
	}
	if err = validateSaveBindingIdentityContext(ctx, s.db, in); err != nil {
		return SaveBinding{}, err
	}
	paths, _ := json.Marshal(in.LocalPaths)
	discovery, _ := json.Marshal(in.Discovery)
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	now := nowText()
	_, err = s.db.ExecContext(ctx, `INSERT INTO save_bindings(id,stream_id,edition_id,device_profile_id,driver_id,core_id,local_paths_json,discovery_json,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, in.ID, in.StreamID, in.EditionID, in.DeviceProfileID, in.DriverID, nullIfEmpty(in.CoreID), string(paths), string(discovery), boolInt(enabled), now, now)
	if err != nil {
		return SaveBinding{}, err
	}
	return s.GetSaveBinding(ctx, in.ID)
}

// CreateSaveSetup creates the logical stream and its first device binding in
// one transaction. It is the control-plane operation used by the Web UI so a
// rejected path template can never leave an orphan stream behind.
func (s *Store) CreateSaveSetup(ctx context.Context, input NewSaveSetup) (SaveSetup, error) {
	streamInput, err := normalizeSaveStream(input.Stream)
	if err != nil {
		return SaveSetup{}, err
	}
	if streamInput.ID == "" {
		streamInput.ID = NewID()
	}
	input.Binding.StreamID = streamInput.ID
	bindingInput, err := normalizeSaveBinding(input.Binding)
	if err != nil {
		return SaveSetup{}, err
	}
	if bindingInput.ID == "" {
		bindingInput.ID = NewID()
	}
	if bindingInput.EditionID == "" {
		return SaveSetup{}, errors.New("binding edition is required")
	}
	linked := false
	for _, editionID := range streamInput.EditionIDs {
		if editionID == bindingInput.EditionID {
			linked = true
			break
		}
	}
	if !linked {
		return SaveSetup{}, errors.New("binding edition must belong to the new stream")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SaveSetup{}, err
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM emulator_drivers WHERE id=? AND enabled=1`, streamInput.DriverID).Scan(&count); err != nil || count == 0 {
		if err == nil {
			err = errors.New("driver_id must reference an enabled emulator driver")
		}
		return SaveSetup{}, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM emulator_drivers WHERE id=? AND enabled=1`, bindingInput.DriverID).Scan(&count); err != nil || count == 0 {
		if err == nil {
			err = errors.New("binding driver_id must reference an enabled emulator driver")
		}
		return SaveSetup{}, err
	}
	if bindingInput.CoreID != "" {
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM retroarch_cores WHERE id=? AND enabled=1`, bindingInput.CoreID).Scan(&count); err != nil || count == 0 {
			if err == nil {
				err = errors.New("binding core_id must reference an enabled RetroArch core")
			}
			return SaveSetup{}, err
		}
	}
	if bindingInput.DriverID != streamInput.DriverID {
		if streamInput.CompatibilityGroupID == "" {
			return SaveSetup{}, errors.New("cross-driver save setup requires a verified compatibility group")
		}
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM save_compatibility_groups g
			JOIN save_compatibility_members source ON source.group_id=g.id
			JOIN save_compatibility_members target ON target.group_id=g.id
			WHERE g.id=? AND g.enabled=1
			  AND source.driver_id=? AND source.core_id='' AND source.runtime_kind='server'
			  AND target.driver_id=? AND target.core_id=? AND target.runtime_kind='device'`, streamInput.CompatibilityGroupID, streamInput.DriverID, bindingInput.DriverID, bindingInput.CoreID).Scan(&count); err != nil || count == 0 {
			if err == nil {
				err = errors.New("cross-driver save setup members are not an exact verified pair")
			}
			return SaveSetup{}, err
		}
	}
	if bindingInput.DeviceProfileID != "" {
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM device_profiles WHERE id=? AND enabled=1`, bindingInput.DeviceProfileID).Scan(&count); err != nil || count == 0 {
			if err == nil {
				err = errors.New("device_profile_id must reference an enabled device profile")
			}
			return SaveSetup{}, err
		}
	}
	if err = validateSaveBindingIdentityContext(ctx, tx, bindingInput); err != nil {
		return SaveSetup{}, err
	}
	if streamInput.Namespace == "" {
		if streamInput.OwnerType == "edition" {
			if err = tx.QueryRowContext(ctx, `SELECT save_namespace FROM editions WHERE id=?`, streamInput.OwnerKey).Scan(&streamInput.Namespace); err != nil {
				return SaveSetup{}, err
			}
			streamInput.Namespace += ":" + streamInput.DriverID
		} else {
			streamInput.Namespace = streamInput.OwnerType + ":" + streamInput.OwnerKey + ":" + streamInput.DriverID
		}
	}
	now := nowText()
	if _, err = tx.ExecContext(ctx, `INSERT INTO save_streams(id,namespace,owner_type,owner_key,driver_id,portability,compatibility_group_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, streamInput.ID, streamInput.Namespace, streamInput.OwnerType, streamInput.OwnerKey, streamInput.DriverID, streamInput.Portability, nullIfEmpty(streamInput.CompatibilityGroupID), now, now); err != nil {
		return SaveSetup{}, err
	}
	for _, editionID := range streamInput.EditionIDs {
		if _, err = tx.ExecContext(ctx, `INSERT INTO save_stream_editions(stream_id,edition_id,compatibility,created_at) VALUES(?,?,?,?)`, streamInput.ID, editionID, streamInput.Compatibility, now); err != nil {
			return SaveSetup{}, err
		}
	}
	paths, _ := json.Marshal(bindingInput.LocalPaths)
	discovery, _ := json.Marshal(bindingInput.Discovery)
	enabled := true
	if bindingInput.Enabled != nil {
		enabled = *bindingInput.Enabled
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO save_bindings(id,stream_id,edition_id,device_profile_id,driver_id,core_id,local_paths_json,discovery_json,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, bindingInput.ID, streamInput.ID, bindingInput.EditionID, bindingInput.DeviceProfileID, bindingInput.DriverID, nullIfEmpty(bindingInput.CoreID), string(paths), string(discovery), boolInt(enabled), now, now); err != nil {
		return SaveSetup{}, err
	}
	if err = tx.Commit(); err != nil {
		return SaveSetup{}, err
	}
	stream, err := s.GetSaveStream(ctx, streamInput.ID)
	if err != nil {
		return SaveSetup{}, err
	}
	binding, err := s.GetSaveBinding(ctx, bindingInput.ID)
	return SaveSetup{Stream: stream, Binding: binding}, err
}

func scanSaveBinding(scanner interface{ Scan(...any) error }) (SaveBinding, error) {
	var item SaveBinding
	var paths, discovery, created, updated string
	var enabled int
	err := scanner.Scan(&item.ID, &item.StreamID, &item.EditionID, &item.DeviceProfileID, &item.DriverID, &item.CoreID, &paths, &discovery, &enabled, &created, &updated)
	_ = json.Unmarshal([]byte(paths), &item.LocalPaths)
	_ = json.Unmarshal([]byte(discovery), &item.Discovery)
	if item.LocalPaths == nil {
		item.LocalPaths = []string{}
	}
	if item.Discovery == nil {
		item.Discovery = map[string]any{}
	}
	item.Enabled = enabled != 0
	item.CreatedAt = parseTime(created)
	item.UpdatedAt = parseTime(updated)
	return item, err
}

const saveBindingColumns = `id,stream_id,edition_id,device_profile_id,driver_id,COALESCE(core_id,''),local_paths_json,discovery_json,enabled,created_at,updated_at`

func (s *Store) GetSaveBinding(ctx context.Context, id string) (SaveBinding, error) {
	return scanSaveBinding(s.db.QueryRowContext(ctx, `SELECT `+saveBindingColumns+` FROM save_bindings WHERE id=?`, id))
}

func (s *Store) ListSaveBindings(ctx context.Context, editionID, deviceProfileID string) ([]SaveBinding, error) {
	query := `SELECT ` + saveBindingColumns + ` FROM save_bindings WHERE 1=1`
	args := []any{}
	if strings.TrimSpace(editionID) != "" {
		query += ` AND edition_id=?`
		args = append(args, strings.TrimSpace(editionID))
	}
	if strings.TrimSpace(deviceProfileID) != "" {
		query += ` AND (device_profile_id=? OR device_profile_id='')`
		args = append(args, strings.TrimSpace(deviceProfileID))
	}
	query += ` ORDER BY CASE WHEN device_profile_id='' THEN 1 ELSE 0 END,edition_id,driver_id,id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []SaveBinding{}
	for rows.Next() {
		item, scanErr := scanSaveBinding(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) UpdateSaveBinding(ctx context.Context, id string, in NewSaveBinding) (SaveBinding, error) {
	in, err := normalizeSaveBinding(in)
	if err != nil {
		return SaveBinding{}, err
	}
	if err = s.validateSaveBindingRelations(ctx, in); err != nil {
		return SaveBinding{}, err
	}
	if err = validateSaveBindingIdentityContext(ctx, s.db, in); err != nil {
		return SaveBinding{}, err
	}
	paths, _ := json.Marshal(in.LocalPaths)
	discovery, _ := json.Marshal(in.Discovery)
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	result, err := s.db.ExecContext(ctx, `UPDATE save_bindings SET stream_id=?,edition_id=?,device_profile_id=?,driver_id=?,core_id=?,local_paths_json=?,discovery_json=?,enabled=?,updated_at=? WHERE id=?`, in.StreamID, in.EditionID, in.DeviceProfileID, in.DriverID, nullIfEmpty(in.CoreID), string(paths), string(discovery), boolInt(enabled), nowText(), id)
	if err != nil {
		return SaveBinding{}, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return SaveBinding{}, sql.ErrNoRows
	}
	return s.GetSaveBinding(ctx, id)
}

func (s *Store) DeleteSaveBinding(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM save_bindings WHERE id=?`, id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

var ErrPairingCodeInvalid = errors.New("pairing code is invalid")
var ErrPairingCodeExpired = errors.New("pairing code has expired")
var ErrPairingCodeRedeemed = errors.New("pairing code has already been redeemed")
var ErrPairingDeviceProfileMismatch = errors.New("pairing device profile does not match the administrator selection")
var ErrPairingDeviceProfileUnavailable = errors.New("pairing device profile is unavailable or disabled")
var ErrPairingDeviceProfileIncompatible = errors.New("pairing device operating system or architecture is incompatible with the selected profile")
var ErrClientTokenInvalid = errors.New("client token is invalid, expired, or revoked")
var ErrDeviceRevoked = errors.New("device is revoked")

func (s *Store) CreatePairingCode(ctx context.Context, in NewPairingCode) (PairingCode, error) {
	if strings.TrimSpace(in.CodeHash) == "" || in.ExpiresAt.IsZero() || !in.ExpiresAt.After(time.Now()) {
		return PairingCode{}, errors.New("code hash and a future expiry are required")
	}
	if in.ID == "" {
		in.ID = NewID()
	}
	requested, _ := json.Marshal(in.RequestedDevice)
	_, err := s.db.ExecContext(ctx, `INSERT INTO pairing_codes(id,code_hash,requested_device_json,expires_at,redeemed_at,created_at) VALUES(?,?,?,?,?,?)`, in.ID, in.CodeHash, string(requested), in.ExpiresAt.UTC().Format(time.RFC3339Nano), "", nowText())
	if err != nil {
		return PairingCode{}, err
	}
	return s.GetPairingCode(ctx, in.ID)
}

func scanPairingCode(scanner interface{ Scan(...any) error }) (PairingCode, error) {
	var item PairingCode
	var requested, expires, redeemed, created string
	err := scanner.Scan(&item.ID, &item.CodeHash, &requested, &expires, &redeemed, &created)
	_ = json.Unmarshal([]byte(requested), &item.RequestedDevice)
	if item.RequestedDevice == nil {
		item.RequestedDevice = map[string]any{}
	}
	item.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expires)
	item.RedeemedAt, _ = time.Parse(time.RFC3339Nano, redeemed)
	item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return item, err
}

func (s *Store) GetPairingCode(ctx context.Context, id string) (PairingCode, error) {
	return scanPairingCode(s.db.QueryRowContext(ctx, `SELECT id,code_hash,requested_device_json,expires_at,redeemed_at,created_at FROM pairing_codes WHERE id=?`, id))
}

func normalizePairingArchitecture(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64", "arm64-v8a":
		return "arm64"
	case "arm", "armv7", "armv7l", "armeabi-v7a":
		return "arm"
	case "x86", "386", "i386", "i686":
		return "386"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func pairingProfileCompatible(profileOS, profileArchitecture, deviceOS, deviceArchitecture string) bool {
	profileOS = strings.ToLower(strings.TrimSpace(profileOS))
	deviceOS = strings.ToLower(strings.TrimSpace(deviceOS))
	osCompatible := profileOS == deviceOS || profileOS == "handheld-linux" && deviceOS == "linux"
	if !osCompatible {
		return false
	}
	profileArchitecture = normalizePairingArchitecture(profileArchitecture)
	deviceArchitecture = normalizePairingArchitecture(deviceArchitecture)
	return profileArchitecture == "" || deviceArchitecture == "" || profileArchitecture == deviceArchitecture
}

func (s *Store) RedeemPairingCode(ctx context.Context, codeHash, tokenHash string, device NewDevice, scopes []string, tokenExpires time.Time) (Device, ClientToken, string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Device{}, ClientToken{}, "", err
	}
	defer tx.Rollback()
	var pairingID, expires, redeemed, requestedJSON string
	err = tx.QueryRowContext(ctx, `SELECT id,expires_at,redeemed_at,requested_device_json FROM pairing_codes WHERE code_hash=?`, codeHash).Scan(&pairingID, &expires, &redeemed, &requestedJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, ClientToken{}, "", ErrPairingCodeInvalid
	}
	if err != nil {
		return Device{}, ClientToken{}, "", err
	}
	if redeemed != "" {
		return Device{}, ClientToken{}, "", ErrPairingCodeRedeemed
	}
	expiresAt, _ := time.Parse(time.RFC3339Nano, expires)
	if !expiresAt.After(time.Now()) {
		return Device{}, ClientToken{}, "", ErrPairingCodeExpired
	}
	if strings.TrimSpace(device.Name) == "" || strings.TrimSpace(device.OSFamily) == "" {
		return Device{}, ClientToken{}, "", errors.New("device name and os_family are required")
	}
	var requested struct {
		DeviceProfileID string `json:"device_profile_id"`
	}
	if err = json.Unmarshal([]byte(requestedJSON), &requested); err != nil {
		return Device{}, ClientToken{}, "", errors.New("stored pairing request is invalid")
	}
	requested.DeviceProfileID = strings.TrimSpace(requested.DeviceProfileID)
	device.DeviceProfileID = strings.TrimSpace(device.DeviceProfileID)
	if requested.DeviceProfileID != "" {
		if device.DeviceProfileID != "" && device.DeviceProfileID != requested.DeviceProfileID {
			return Device{}, ClientToken{}, "", ErrPairingDeviceProfileMismatch
		}
		device.DeviceProfileID = requested.DeviceProfileID
	}
	deviceTarget := ""
	if device.DeviceProfileID != "" {
		var enabled int
		var profileOS, profileArchitecture string
		err = tx.QueryRowContext(ctx, `SELECT target,os_family,architecture,enabled FROM device_profiles WHERE id=?`, device.DeviceProfileID).Scan(&deviceTarget, &profileOS, &profileArchitecture, &enabled)
		if errors.Is(err, sql.ErrNoRows) || err == nil && enabled == 0 {
			return Device{}, ClientToken{}, "", ErrPairingDeviceProfileUnavailable
		}
		if err != nil {
			return Device{}, ClientToken{}, "", err
		}
		if !pairingProfileCompatible(profileOS, profileArchitecture, device.OSFamily, device.Architecture) {
			return Device{}, ClientToken{}, "", ErrPairingDeviceProfileIncompatible
		}
	}
	if device.ID == "" {
		device.ID = NewID()
	}
	if device.Status == "" {
		device.Status = "active"
	}
	capabilities, _ := json.Marshal(device.Capabilities)
	now := nowText()
	if _, err = tx.ExecContext(ctx, `INSERT INTO devices(id,name,device_profile_id,os_family,distribution,architecture,agent_version,status,capabilities_json,created_at,updated_at,last_seen_at,revoked_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, device.ID, strings.TrimSpace(device.Name), strings.TrimSpace(device.DeviceProfileID), strings.TrimSpace(device.OSFamily), strings.TrimSpace(device.Distribution), strings.TrimSpace(device.Architecture), strings.TrimSpace(device.AgentVersion), "active", string(capabilities), now, now, now, ""); err != nil {
		return Device{}, ClientToken{}, "", err
	}
	if len(scopes) == 0 {
		scopes = []string{"sync:read", "sync:write", "device:heartbeat"}
	}
	scopesJSON, _ := json.Marshal(scopes)
	tokenID := NewID()
	if _, err = tx.ExecContext(ctx, `INSERT INTO client_tokens(id,device_id,token_hash,scopes_json,issued_at,expires_at,revoked_at) VALUES(?,?,?,?,?,?,?)`, tokenID, device.ID, tokenHash, string(scopesJSON), now, tokenExpires.UTC().Format(time.RFC3339Nano), ""); err != nil {
		return Device{}, ClientToken{}, "", err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE pairing_codes SET redeemed_at=? WHERE id=? AND redeemed_at=''`, now, pairingID); err != nil {
		return Device{}, ClientToken{}, "", err
	}
	if err = tx.Commit(); err != nil {
		return Device{}, ClientToken{}, "", err
	}
	created, err := s.GetDevice(ctx, device.ID)
	if err != nil {
		return Device{}, ClientToken{}, "", err
	}
	return created, ClientToken{ID: tokenID, DeviceID: device.ID, Scopes: scopes, IssuedAt: parseTime(now), ExpiresAt: tokenExpires.UTC()}, strings.TrimSpace(deviceTarget), nil
}

func (s *Store) AuthenticateClientToken(ctx context.Context, tokenHash string) (ClientIdentity, error) {
	var identity ClientIdentity
	var scopes string
	err := s.db.QueryRowContext(ctx, `SELECT t.id,t.device_id,t.scopes_json FROM client_tokens t JOIN devices d ON d.id=t.device_id WHERE t.token_hash=? AND t.revoked_at='' AND t.expires_at>? AND d.status<>'revoked'`, tokenHash, nowText()).Scan(&identity.TokenID, &identity.DeviceID, &scopes)
	if errors.Is(err, sql.ErrNoRows) {
		return ClientIdentity{}, ErrClientTokenInvalid
	}
	if err != nil {
		return ClientIdentity{}, err
	}
	_ = json.Unmarshal([]byte(scopes), &identity.Scopes)
	return identity, nil
}

func (s *Store) RevokeDevice(ctx context.Context, id string) (Device, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Device{}, err
	}
	defer tx.Rollback()
	now := nowText()
	result, err := tx.ExecContext(ctx, `UPDATE devices SET status='revoked',revoked_at=?,updated_at=? WHERE id=? AND status<>'revoked'`, now, now, id)
	if err != nil {
		return Device{}, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		var exists int
		if queryErr := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM devices WHERE id=?`, id).Scan(&exists); queryErr != nil {
			return Device{}, queryErr
		}
		if exists == 0 {
			return Device{}, sql.ErrNoRows
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE client_tokens SET revoked_at=? WHERE device_id=? AND revoked_at=''`, now, id); err != nil {
		return Device{}, err
	}
	if err = tx.Commit(); err != nil {
		return Device{}, err
	}
	return s.GetDevice(ctx, id)
}
