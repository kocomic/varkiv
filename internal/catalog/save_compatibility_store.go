package catalog

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

var (
	ErrSaveRuntimeNotAttested         = errors.New("save binding runtime identity is not attested for this device")
	ErrRuntimeAttestationNotRequested = errors.New("runtime attestation identity was not requested for this device")
)

func normalizeRuntimeDigest(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return "", errors.New("runtime SHA-256 must be 64 hexadecimal characters")
	}
	return value, nil
}

func normalizeSaveCompatibilityGroup(in NewSaveCompatibilityGroup) (NewSaveCompatibilityGroup, error) {
	in.ID = strings.TrimSpace(in.ID)
	in.Name = strings.TrimSpace(in.Name)
	in.Format = strings.TrimSpace(in.Format)
	if in.ID == "" || in.Name == "" || in.Format == "" {
		return in, errors.New("compatibility group id, name, and format are required")
	}
	if in.ContractVersion < 1 {
		in.ContractVersion = 1
	}
	if len(in.Members) < 2 || len(in.Members) > 32 {
		return in, errors.New("compatibility group must contain between 2 and 32 exact members")
	}
	if in.Evidence == nil {
		in.Evidence = map[string]any{}
	}
	seen := map[string]bool{}
	hasServer, hasDevice := false, false
	for index := range in.Members {
		member := &in.Members[index]
		member.GroupID = in.ID
		member.DriverID = strings.TrimSpace(member.DriverID)
		member.CoreID = strings.TrimSpace(member.CoreID)
		member.RuntimeKind = strings.TrimSpace(member.RuntimeKind)
		member.OSFamily = strings.ToLower(strings.TrimSpace(member.OSFamily))
		member.Architecture = normalizePairingArchitecture(member.Architecture)
		if member.DriverID == "" || member.DriverContractVersion < 1 {
			return in, errors.New("compatibility members require a driver and exact driver contract")
		}
		key := strings.Join([]string{member.DriverID, member.CoreID, member.OSFamily, member.Architecture}, "\x00")
		if seen[key] {
			return in, errors.New("compatibility group members must be unique")
		}
		seen[key] = true
		switch member.RuntimeKind {
		case "server":
			hasServer = true
			if member.CoreID != "" || member.CoreContractVersion != 0 || member.OSFamily != "" || member.Architecture != "" || member.DriverSHA256 != "" || member.DriverSize != 0 || member.CoreSHA256 != "" || member.CoreSize != 0 {
				return in, errors.New("trusted server compatibility members must use only an exact driver contract")
			}
		case "device":
			hasDevice = true
			if member.OSFamily == "" || member.Architecture == "" || member.DriverSize <= 0 {
				return in, errors.New("device compatibility members require OS, architecture, and exact driver size")
			}
			var err error
			if member.DriverSHA256, err = normalizeRuntimeDigest(member.DriverSHA256); err != nil {
				return in, err
			}
			if member.CoreID == "" || member.CoreContractVersion < 1 || member.CoreSize <= 0 {
				return in, errors.New("device compatibility members require an exact core identity")
			}
			if member.CoreSHA256, err = normalizeRuntimeDigest(member.CoreSHA256); err != nil {
				return in, err
			}
		default:
			return in, errors.New("compatibility member runtime_kind must be server or device")
		}
	}
	if !hasServer || !hasDevice {
		return in, errors.New("compatibility group needs both a trusted server and an attested device member")
	}
	return in, nil
}

func validateCompatibilityMemberRuntimeTx(ctx context.Context, tx *sql.Tx, member SaveCompatibilityMember) error {
	var contract int
	var enabled int
	if err := tx.QueryRowContext(ctx, `SELECT contract_version,enabled FROM emulator_drivers WHERE id=?`, member.DriverID).Scan(&contract, &enabled); err != nil {
		return err
	}
	if enabled == 0 || contract != member.DriverContractVersion {
		return errors.New("compatibility member driver contract is unavailable or stale")
	}
	if member.CoreID == "" {
		return nil
	}
	if err := tx.QueryRowContext(ctx, `SELECT contract_version,enabled FROM retroarch_cores WHERE id=?`, member.CoreID).Scan(&contract, &enabled); err != nil {
		return err
	}
	if enabled == 0 || contract != member.CoreContractVersion {
		return errors.New("compatibility member core contract is unavailable or stale")
	}
	return nil
}

func (s *Store) ReconcileBuiltinSaveCompatibilityGroup(ctx context.Context, input NewSaveCompatibilityGroup) (SaveCompatibilityGroup, error) {
	input.Builtin = true
	input, err := normalizeSaveCompatibilityGroup(input)
	if err != nil {
		return SaveCompatibilityGroup{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SaveCompatibilityGroup{}, err
	}
	defer tx.Rollback()
	var currentVersion, builtin int
	readErr := tx.QueryRowContext(ctx, `SELECT contract_version,builtin FROM save_compatibility_groups WHERE id=?`, input.ID).Scan(&currentVersion, &builtin)
	if readErr != nil && !errors.Is(readErr, sql.ErrNoRows) {
		return SaveCompatibilityGroup{}, readErr
	}
	if readErr == nil && builtin == 0 {
		return SaveCompatibilityGroup{}, errors.New("built-in compatibility group id is owned by a custom record")
	}
	if readErr == nil && currentVersion > input.ContractVersion {
		return SaveCompatibilityGroup{}, errors.New("stored compatibility group contract is newer than this build")
	}
	for _, member := range input.Members {
		if err = validateCompatibilityMemberRuntimeTx(ctx, tx, member); err != nil {
			return SaveCompatibilityGroup{}, err
		}
	}
	now := nowText()
	evidence, _ := json.Marshal(input.Evidence)
	enabled := enabledValue(input.Enabled)
	if errors.Is(readErr, sql.ErrNoRows) {
		if _, err = tx.ExecContext(ctx, `INSERT INTO save_compatibility_groups(id,name,format,contract_version,evidence_json,builtin,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, input.ID, input.Name, input.Format, input.ContractVersion, string(evidence), 1, boolInt(enabled), now, now); err != nil {
			return SaveCompatibilityGroup{}, err
		}
	} else if currentVersion < input.ContractVersion {
		if _, err = tx.ExecContext(ctx, `UPDATE save_compatibility_groups SET name=?,format=?,contract_version=?,evidence_json=?,enabled=?,updated_at=? WHERE id=? AND builtin=1`, input.Name, input.Format, input.ContractVersion, string(evidence), boolInt(enabled), now, input.ID); err != nil {
			return SaveCompatibilityGroup{}, err
		}
	} else {
		if err = tx.Commit(); err != nil {
			return SaveCompatibilityGroup{}, err
		}
		return s.GetSaveCompatibilityGroup(ctx, input.ID)
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM save_compatibility_members WHERE group_id=?`, input.ID); err != nil {
		return SaveCompatibilityGroup{}, err
	}
	for _, member := range input.Members {
		if _, err = tx.ExecContext(ctx, `INSERT INTO save_compatibility_members(group_id,driver_id,core_id,runtime_kind,driver_contract_version,core_contract_version,os_family,architecture,driver_sha256,driver_size,core_sha256,core_size) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, input.ID, member.DriverID, member.CoreID, member.RuntimeKind, member.DriverContractVersion, member.CoreContractVersion, member.OSFamily, member.Architecture, member.DriverSHA256, member.DriverSize, member.CoreSHA256, member.CoreSize); err != nil {
			return SaveCompatibilityGroup{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return SaveCompatibilityGroup{}, err
	}
	return s.GetSaveCompatibilityGroup(ctx, input.ID)
}

func scanSaveCompatibilityGroup(scanner interface{ Scan(...any) error }) (SaveCompatibilityGroup, error) {
	var item SaveCompatibilityGroup
	var evidence, created, updated string
	var builtin, enabled int
	err := scanner.Scan(&item.ID, &item.Name, &item.Format, &item.ContractVersion, &evidence, &builtin, &enabled, &created, &updated)
	_ = json.Unmarshal([]byte(evidence), &item.Evidence)
	if item.Evidence == nil {
		item.Evidence = map[string]any{}
	}
	item.Builtin, item.Enabled = builtin != 0, enabled != 0
	item.CreatedAt, item.UpdatedAt = parseTime(created), parseTime(updated)
	return item, err
}

func (s *Store) loadSaveCompatibilityMembers(ctx context.Context, item *SaveCompatibilityGroup) error {
	rows, err := s.db.QueryContext(ctx, `SELECT group_id,driver_id,core_id,runtime_kind,driver_contract_version,core_contract_version,os_family,architecture,driver_sha256,driver_size,core_sha256,core_size FROM save_compatibility_members WHERE group_id=? ORDER BY runtime_kind,driver_id,core_id,os_family,architecture`, item.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	item.Members = []SaveCompatibilityMember{}
	for rows.Next() {
		var member SaveCompatibilityMember
		if err = rows.Scan(&member.GroupID, &member.DriverID, &member.CoreID, &member.RuntimeKind, &member.DriverContractVersion, &member.CoreContractVersion, &member.OSFamily, &member.Architecture, &member.DriverSHA256, &member.DriverSize, &member.CoreSHA256, &member.CoreSize); err != nil {
			return err
		}
		item.Members = append(item.Members, member)
	}
	return rows.Err()
}

func (s *Store) GetSaveCompatibilityGroup(ctx context.Context, id string) (SaveCompatibilityGroup, error) {
	item, err := scanSaveCompatibilityGroup(s.db.QueryRowContext(ctx, `SELECT id,name,format,contract_version,evidence_json,builtin,enabled,created_at,updated_at FROM save_compatibility_groups WHERE id=?`, strings.TrimSpace(id)))
	if err != nil {
		return SaveCompatibilityGroup{}, err
	}
	if err = s.loadSaveCompatibilityMembers(ctx, &item); err != nil {
		return SaveCompatibilityGroup{}, err
	}
	return item, nil
}

func (s *Store) ListSaveCompatibilityGroups(ctx context.Context) ([]SaveCompatibilityGroup, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM save_compatibility_groups ORDER BY lower(name),id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	items := make([]SaveCompatibilityGroup, 0, len(ids))
	for _, id := range ids {
		item, getErr := s.GetSaveCompatibilityGroup(ctx, id)
		if getErr != nil {
			return nil, getErr
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Store) AttachSaveCompatibilityGroup(ctx context.Context, groupID, driverID string) (int64, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE save_streams SET compatibility_group_id=?,updated_at=? WHERE driver_id=? AND compatibility_group_id IS NULL`, strings.TrimSpace(groupID), nowText(), strings.TrimSpace(driverID))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) ListRuntimeAttestationRequirements(ctx context.Context) ([]RuntimeAttestationRequirement, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT kind,runtime_id,contract_version FROM (
		SELECT 'driver' AS kind,m.driver_id AS runtime_id,m.driver_contract_version AS contract_version FROM save_compatibility_members m JOIN save_compatibility_groups g ON g.id=m.group_id AND g.enabled=1 WHERE m.runtime_kind='device'
		UNION SELECT 'core',m.core_id,m.core_contract_version FROM save_compatibility_members m JOIN save_compatibility_groups g ON g.id=m.group_id AND g.enabled=1 WHERE m.runtime_kind='device' AND m.core_id<>''
	) ORDER BY kind,runtime_id,contract_version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []RuntimeAttestationRequirement{}
	for rows.Next() {
		var item RuntimeAttestationRequirement
		if err = rows.Scan(&item.Kind, &item.RuntimeID, &item.ContractVersion); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// ListRuntimeAttestationRequirementsForDevice returns only identities that can
// authorize an enabled compatibility member for this device. A client never
// needs to learn about or probe binaries for another operating system or CPU.
func (s *Store) ListRuntimeAttestationRequirementsForDevice(ctx context.Context, device Device) ([]RuntimeAttestationRequirement, error) {
	groups, err := s.ListSaveCompatibilityGroups(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	items := []RuntimeAttestationRequirement{}
	appendRequirement := func(kind, runtimeID string, contractVersion int) {
		key := kind + "\x00" + runtimeID + "\x00" + fmt.Sprint(contractVersion)
		if runtimeID == "" || seen[key] {
			return
		}
		seen[key] = true
		items = append(items, RuntimeAttestationRequirement{Kind: kind, RuntimeID: runtimeID, ContractVersion: contractVersion})
	}
	for _, group := range groups {
		if !group.Enabled {
			continue
		}
		for _, member := range group.Members {
			if member.RuntimeKind != "device" || !runtimeOSMatches(member.OSFamily, device.OSFamily) || normalizePairingArchitecture(member.Architecture) != normalizePairingArchitecture(device.Architecture) {
				continue
			}
			appendRequirement("driver", member.DriverID, member.DriverContractVersion)
			appendRequirement("core", member.CoreID, member.CoreContractVersion)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		if items[i].RuntimeID != items[j].RuntimeID {
			return items[i].RuntimeID < items[j].RuntimeID
		}
		return items[i].ContractVersion < items[j].ContractVersion
	})
	return items, nil
}

func (s *Store) ListRuntimeAttestations(ctx context.Context, deviceID string) ([]RuntimeAttestation, error) {
	query, args := `SELECT device_id,kind,runtime_id,contract_version,sha256,size,observed_at FROM runtime_attestations`, []any{}
	if strings.TrimSpace(deviceID) != "" {
		query += ` WHERE device_id=?`
		args = append(args, strings.TrimSpace(deviceID))
	}
	query += ` ORDER BY device_id,kind,runtime_id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []RuntimeAttestation{}
	for rows.Next() {
		var item RuntimeAttestation
		var observed string
		if err = rows.Scan(&item.DeviceID, &item.Kind, &item.RuntimeID, &item.ContractVersion, &item.SHA256, &item.Size, &observed); err != nil {
			return nil, err
		}
		item.ObservedAt = parseTime(observed)
		items = append(items, item)
	}
	return items, rows.Err()
}

func runtimeOSMatches(required, actual string) bool {
	required, actual = strings.ToLower(strings.TrimSpace(required)), strings.ToLower(strings.TrimSpace(actual))
	return required == actual || required == "linux" && actual == "handheld-linux" || required == "handheld-linux" && actual == "linux"
}

func runtimeMemberMatchesDevice(member SaveCompatibilityMember, device Device, attestations map[string]RuntimeAttestation) bool {
	if member.RuntimeKind != "device" || !runtimeOSMatches(member.OSFamily, device.OSFamily) || normalizePairingArchitecture(member.Architecture) != normalizePairingArchitecture(device.Architecture) {
		return false
	}
	driver, ok := attestations["driver\x00"+member.DriverID]
	if !ok || driver.ContractVersion != member.DriverContractVersion || driver.SHA256 != member.DriverSHA256 || driver.Size != member.DriverSize {
		return false
	}
	if member.CoreID == "" {
		return true
	}
	core, ok := attestations["core\x00"+member.CoreID]
	return ok && core.ContractVersion == member.CoreContractVersion && core.SHA256 == member.CoreSHA256 && core.Size == member.CoreSize
}

func validateRuntimeAttestationTx(ctx context.Context, tx *sql.Tx, device Device, report *RuntimeAttestationReport) error {
	report.Kind = strings.TrimSpace(report.Kind)
	report.RuntimeID = strings.TrimSpace(report.RuntimeID)
	if report.RuntimeID == "" || report.ContractVersion < 1 || report.Size <= 0 {
		return errors.New("runtime attestation requires id, contract version, and positive size")
	}
	var err error
	if report.SHA256, err = normalizeRuntimeDigest(report.SHA256); err != nil {
		return err
	}
	var contract, enabled int
	switch report.Kind {
	case "driver":
		err = tx.QueryRowContext(ctx, `SELECT contract_version,enabled FROM emulator_drivers WHERE id=?`, report.RuntimeID).Scan(&contract, &enabled)
	case "core":
		err = tx.QueryRowContext(ctx, `SELECT contract_version,enabled FROM retroarch_cores WHERE id=?`, report.RuntimeID).Scan(&contract, &enabled)
	default:
		return errors.New("runtime attestation kind must be driver or core")
	}
	if err != nil {
		return err
	}
	if enabled == 0 || contract != report.ContractVersion {
		return errors.New("runtime attestation contract is unavailable or stale")
	}
	query := `SELECT m.os_family,m.architecture FROM save_compatibility_members m JOIN save_compatibility_groups g ON g.id=m.group_id AND g.enabled=1 WHERE m.runtime_kind='device'`
	args := []any{}
	switch report.Kind {
	case "driver":
		query += ` AND m.driver_id=? AND m.driver_contract_version=?`
		args = append(args, report.RuntimeID, report.ContractVersion)
	case "core":
		query += ` AND m.core_id=? AND m.core_contract_version=?`
		args = append(args, report.RuntimeID, report.ContractVersion)
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	requested := false
	for rows.Next() {
		var osFamily, architecture string
		if err = rows.Scan(&osFamily, &architecture); err != nil {
			return err
		}
		if runtimeOSMatches(osFamily, device.OSFamily) && normalizePairingArchitecture(architecture) == normalizePairingArchitecture(device.Architecture) {
			requested = true
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if !requested {
		return ErrRuntimeAttestationNotRequested
	}
	return nil
}

// RecordDeviceHeartbeat replaces the complete attestation snapshot and updates
// the device heartbeat in one transaction. Omitting a previously reported file
// immediately revokes its authorization; stale identities never linger.
func (s *Store) RecordDeviceHeartbeat(ctx context.Context, deviceID string, capabilities map[string]bool, reports []RuntimeAttestationReport) (Device, error) {
	deviceID = strings.TrimSpace(deviceID)
	if len(reports) > 128 {
		return Device{}, errors.New("runtime attestation count exceeds 128")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Device{}, err
	}
	defer tx.Rollback()
	var device Device
	var status string
	if err = tx.QueryRowContext(ctx, `SELECT id,os_family,architecture,status FROM devices WHERE id=?`, deviceID).Scan(&device.ID, &device.OSFamily, &device.Architecture, &status); err != nil {
		return Device{}, err
	}
	if status == "revoked" {
		return Device{}, ErrDeviceRevoked
	}
	seen := map[string]bool{}
	for index := range reports {
		if err = validateRuntimeAttestationTx(ctx, tx, device, &reports[index]); err != nil {
			return Device{}, err
		}
		key := reports[index].Kind + "\x00" + reports[index].RuntimeID
		if seen[key] {
			return Device{}, errors.New("runtime attestation identities must be unique")
		}
		seen[key] = true
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM runtime_attestations WHERE device_id=?`, deviceID); err != nil {
		return Device{}, err
	}
	now := nowText()
	attestations := map[string]RuntimeAttestation{}
	for _, report := range reports {
		if _, err = tx.ExecContext(ctx, `INSERT INTO runtime_attestations(device_id,kind,runtime_id,contract_version,sha256,size,observed_at) VALUES(?,?,?,?,?,?,?)`, deviceID, report.Kind, report.RuntimeID, report.ContractVersion, report.SHA256, report.Size, now); err != nil {
			return Device{}, err
		}
		attestations[report.Kind+"\x00"+report.RuntimeID] = RuntimeAttestation{DeviceID: deviceID, Kind: report.Kind, RuntimeID: report.RuntimeID, ContractVersion: report.ContractVersion, SHA256: report.SHA256, Size: report.Size, ObservedAt: time.Now().UTC()}
	}
	rows, err := tx.QueryContext(ctx, `SELECT m.group_id,m.driver_id,m.core_id,m.runtime_kind,m.driver_contract_version,m.core_contract_version,m.os_family,m.architecture,m.driver_sha256,m.driver_size,m.core_sha256,m.core_size FROM save_compatibility_members m JOIN save_compatibility_groups g ON g.id=m.group_id AND g.enabled=1 WHERE m.runtime_kind='device'`)
	if err != nil {
		return Device{}, err
	}
	verifiedGroups := map[string]bool{}
	for rows.Next() {
		var member SaveCompatibilityMember
		if err = rows.Scan(&member.GroupID, &member.DriverID, &member.CoreID, &member.RuntimeKind, &member.DriverContractVersion, &member.CoreContractVersion, &member.OSFamily, &member.Architecture, &member.DriverSHA256, &member.DriverSize, &member.CoreSHA256, &member.CoreSize); err != nil {
			rows.Close()
			return Device{}, err
		}
		if runtimeMemberMatchesDevice(member, device, attestations) {
			verifiedGroups[member.GroupID] = true
		}
	}
	if err = rows.Close(); err != nil {
		return Device{}, err
	}
	if capabilities == nil {
		capabilities = map[string]bool{}
	}
	capabilities["runtime_identity_attested"] = len(reports) > 0
	capabilities["verified_save_bridge"] = len(verifiedGroups) > 0
	encoded, _ := json.Marshal(capabilities)
	result, err := tx.ExecContext(ctx, `UPDATE devices SET status='active',capabilities_json=?,updated_at=?,last_seen_at=? WHERE id=? AND status<>'revoked'`, string(encoded), now, now, deviceID)
	if err != nil {
		return Device{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return Device{}, ErrDeviceRevoked
	}
	if err = tx.Commit(); err != nil {
		return Device{}, err
	}
	return s.GetDevice(ctx, deviceID)
}

func (s *Store) saveBindingRuntimeAuthorized(ctx context.Context, device Device, binding SaveBinding) (bool, error) {
	stream, err := s.GetSaveStream(ctx, binding.StreamID)
	if err != nil {
		return false, err
	}
	if binding.DriverID == stream.DriverID {
		return true, nil
	}
	if stream.CompatibilityGroupID == "" {
		return false, nil
	}
	group, err := s.GetSaveCompatibilityGroup(ctx, stream.CompatibilityGroupID)
	if err != nil {
		return false, err
	}
	if !group.Enabled {
		return false, nil
	}
	items, err := s.ListRuntimeAttestations(ctx, device.ID)
	if err != nil {
		return false, err
	}
	attestations := map[string]RuntimeAttestation{}
	for _, item := range items {
		attestations[item.Kind+"\x00"+item.RuntimeID] = item
	}
	for _, member := range group.Members {
		if member.DriverID == binding.DriverID && member.CoreID == binding.CoreID && runtimeMemberMatchesDevice(member, device, attestations) {
			return true, nil
		}
	}
	return false, nil
}

func (s *Store) SaveBindingRuntimeAuthorized(ctx context.Context, device Device, binding SaveBinding) (bool, error) {
	if !binding.Enabled || binding.DeviceProfileID != "" && binding.DeviceProfileID != device.DeviceProfileID {
		return false, nil
	}
	return s.saveBindingRuntimeAuthorized(ctx, device, binding)
}

func (s *Store) DeviceAuthorizedSaveStreams(ctx context.Context, device Device) (map[string]bool, error) {
	bindings, err := s.ListSaveBindings(ctx, "", device.DeviceProfileID)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, binding := range bindings {
		if device.DeviceProfileID == "" && binding.DeviceProfileID != "" {
			continue
		}
		ok, authErr := s.SaveBindingRuntimeAuthorized(ctx, device, binding)
		if authErr != nil {
			return nil, authErr
		}
		if ok {
			allowed[binding.StreamID] = true
		}
	}
	return allowed, nil
}
