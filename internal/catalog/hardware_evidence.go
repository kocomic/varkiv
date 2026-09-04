package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

var ErrReviewedHardwareEvidenceStale = errors.New("reviewed hardware evidence runtime contracts changed")

// ReviewedHardwareEvidence is the narrow write contract used after a
// maintainer has reviewed a privacy-minimized Device Agent report. It can
// change support claims, but cannot alter any runtime definition.
type ReviewedHardwareEvidence struct {
	DeviceProfileID         string         `json:"device_profile_id"`
	DeviceContractVersion   int            `json:"device_contract_version"`
	ExpectedFrontendID      string         `json:"expected_frontend_id,omitempty"`
	FrontendContractVersion int            `json:"frontend_contract_version,omitempty"`
	DriverID                string         `json:"driver_id"`
	DriverContractVersion   int            `json:"driver_contract_version"`
	CoreID                  string         `json:"core_id,omitempty"`
	CoreContractVersion     int            `json:"core_contract_version,omitempty"`
	SupportLevel            string         `json:"support_level"`
	Evidence                map[string]any `json:"evidence"`
}

type ReviewedHardwareEvidenceResult struct {
	DeviceProfile  DeviceProfile    `json:"device_profile"`
	Frontend       *FrontendAdapter `json:"frontend,omitempty"`
	EmulatorDriver EmulatorDriver   `json:"emulator_driver"`
	RetroArchCore  *RetroArchCore   `json:"retroarch_core,omitempty"`
}

func supportLevelRank(level string) int {
	switch level {
	case "catalogued":
		return 1
	case "package-tested":
		return 2
	case "hardware-tested":
		return 3
	case "sync-tested":
		return 4
	default:
		return 0
	}
}

func platformOverlap(left, right []string) bool {
	for _, value := range left {
		if slices.Contains(right, value) {
			return true
		}
	}
	return false
}

// ApplyReviewedHardwareEvidence atomically promotes only the selected
// device/frontend/driver/core support claims. Built-in definitions are
// intentionally eligible: this path updates evidence fields only and cannot
// rewrite the seeded executable, path, or launch contracts.
func (s *Store) ApplyReviewedHardwareEvidence(ctx context.Context, in ReviewedHardwareEvidence) (ReviewedHardwareEvidenceResult, error) {
	in.DeviceProfileID = strings.TrimSpace(in.DeviceProfileID)
	in.DriverID = strings.TrimSpace(in.DriverID)
	in.CoreID = strings.TrimSpace(in.CoreID)
	in.SupportLevel = strings.TrimSpace(in.SupportLevel)
	if in.DeviceProfileID == "" || in.DriverID == "" {
		return ReviewedHardwareEvidenceResult{}, errors.New("device_profile_id and driver_id are required")
	}
	if in.DeviceContractVersion < 1 || in.DriverContractVersion < 1 || (in.ExpectedFrontendID != "" && in.FrontendContractVersion < 1) || (in.CoreID != "" && in.CoreContractVersion < 1) {
		return ReviewedHardwareEvidenceResult{}, errors.New("reviewed hardware evidence requires expected runtime contract versions")
	}
	if in.SupportLevel != "hardware-tested" && in.SupportLevel != "sync-tested" {
		return ReviewedHardwareEvidenceResult{}, errors.New("reviewed hardware evidence must be hardware-tested or sync-tested")
	}
	if err := validateSupportEvidence(in.SupportLevel, in.Evidence); err != nil {
		return ReviewedHardwareEvidenceResult{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ReviewedHardwareEvidenceResult{}, err
	}
	defer tx.Rollback()

	device, err := scanDeviceProfile(tx.QueryRowContext(ctx, `SELECT `+deviceProfileColumns+` FROM device_profiles WHERE id=?`, in.DeviceProfileID))
	if err != nil {
		return ReviewedHardwareEvidenceResult{}, err
	}
	if !device.Enabled {
		return ReviewedHardwareEvidenceResult{}, errors.New("reviewed device profile is disabled")
	}
	if evidenceDevice := supportEvidenceString(in.Evidence, "device"); evidenceDevice != device.Target && !strings.HasPrefix(evidenceDevice, device.Target+"/") {
		return ReviewedHardwareEvidenceResult{}, errors.New("reviewed hardware evidence device does not match the selected target")
	}
	if device.ContractVersion != in.DeviceContractVersion || device.DefaultFrontendID != in.ExpectedFrontendID {
		return ReviewedHardwareEvidenceResult{}, ErrReviewedHardwareEvidenceStale
	}
	driver, err := scanEmulatorDriver(tx.QueryRowContext(ctx, `SELECT `+emulatorDriverColumns+` FROM emulator_drivers WHERE id=?`, in.DriverID))
	if err != nil {
		return ReviewedHardwareEvidenceResult{}, err
	}
	if !driver.Enabled || !slices.Contains(driver.Targets, device.Target) {
		return ReviewedHardwareEvidenceResult{}, errors.New("reviewed emulator driver is disabled or incompatible with the device target")
	}
	if driver.ContractVersion != in.DriverContractVersion {
		return ReviewedHardwareEvidenceResult{}, ErrReviewedHardwareEvidenceStale
	}

	var core *RetroArchCore
	if in.CoreID != "" {
		if !driver.Launch.RequiresCore {
			return ReviewedHardwareEvidenceResult{}, errors.New("reviewed emulator driver does not use a RetroArch core")
		}
		item, coreErr := scanRetroArchCore(tx.QueryRowContext(ctx, `SELECT `+retroArchCoreColumns+` FROM retroarch_cores WHERE id=?`, in.CoreID))
		if coreErr != nil {
			return ReviewedHardwareEvidenceResult{}, coreErr
		}
		if !item.Enabled || !platformOverlap(driver.Platforms, item.Platforms) {
			return ReviewedHardwareEvidenceResult{}, errors.New("reviewed RetroArch core is disabled or incompatible with the emulator driver")
		}
		if item.ContractVersion != in.CoreContractVersion {
			return ReviewedHardwareEvidenceResult{}, ErrReviewedHardwareEvidenceStale
		}
		core = &item
	}
	if driver.Launch.RequiresCore && core == nil {
		return ReviewedHardwareEvidenceResult{}, errors.New("reviewed emulator driver requires a RetroArch core")
	}

	var frontend *FrontendAdapter
	if device.DefaultFrontendID != "" {
		item, frontendErr := scanFrontendAdapter(tx.QueryRowContext(ctx, `SELECT `+frontendAdapterColumns+` FROM frontend_adapters WHERE id=?`, device.DefaultFrontendID))
		if frontendErr != nil {
			return ReviewedHardwareEvidenceResult{}, frontendErr
		}
		if !item.Enabled {
			return ReviewedHardwareEvidenceResult{}, errors.New("reviewed device profile default frontend is disabled")
		}
		if item.ContractVersion != in.FrontendContractVersion {
			return ReviewedHardwareEvidenceResult{}, ErrReviewedHardwareEvidenceStale
		}
		frontend = &item
	}

	now := nowText()
	targetRank := supportLevelRank(in.SupportLevel)
	updates := []struct {
		table           string
		kind            string
		id              string
		contractVersion int
		level           string
		evidence        map[string]any
	}{
		{"device_profiles", "device_profile", device.ID, device.ContractVersion, device.SupportLevel, device.Evidence},
		{"emulator_drivers", "emulator_driver", driver.ID, driver.ContractVersion, driver.SupportLevel, driver.Evidence},
	}
	if frontend != nil {
		updates = append(updates, struct {
			table           string
			kind            string
			id              string
			contractVersion int
			level           string
			evidence        map[string]any
		}{"frontend_adapters", "frontend_adapter", frontend.ID, frontend.ContractVersion, frontend.SupportLevel, frontend.Evidence})
	}
	if core != nil {
		updates = append(updates, struct {
			table           string
			kind            string
			id              string
			contractVersion int
			level           string
			evidence        map[string]any
		}{"retroarch_cores", "retroarch_core", core.ID, core.ContractVersion, core.SupportLevel, core.Evidence})
	}
	for _, update := range updates {
		bound := bindSupportEvidence(in.Evidence, update.kind, update.id, update.contractVersion)
		boundJSON := jsonText(bound, "{}")
		if _, updateErr := tx.ExecContext(ctx, `INSERT INTO runtime_evidence_claims(runtime_kind,runtime_id,target,contract_version,support_level,evidence_json,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?)
			ON CONFLICT(runtime_kind,runtime_id,target) DO UPDATE SET contract_version=excluded.contract_version,support_level=excluded.support_level,evidence_json=excluded.evidence_json,updated_at=excluded.updated_at`,
			update.kind, update.id, device.Target, update.contractVersion, in.SupportLevel, boundJSON, now, now); updateErr != nil {
			return ReviewedHardwareEvidenceResult{}, updateErr
		}

		level, summaryEvidence := in.SupportLevel, bound
		if supportLevelRank(update.level) > targetRank {
			level = update.level
			summaryEvidence = bindSupportEvidence(update.evidence, update.kind, update.id, update.contractVersion)
		}
		rows, queryErr := tx.QueryContext(ctx, `SELECT target,support_level,evidence_json FROM runtime_evidence_claims
			WHERE runtime_kind=? AND runtime_id=? AND contract_version=? ORDER BY target`, update.kind, update.id, update.contractVersion)
		if queryErr != nil {
			return ReviewedHardwareEvidenceResult{}, queryErr
		}
		claims := []map[string]any{}
		for rows.Next() {
			var target, claimLevel, encoded string
			if queryErr = rows.Scan(&target, &claimLevel, &encoded); queryErr != nil {
				rows.Close()
				return ReviewedHardwareEvidenceResult{}, queryErr
			}
			var claim map[string]any
			if queryErr = json.Unmarshal([]byte(encoded), &claim); queryErr != nil {
				rows.Close()
				return ReviewedHardwareEvidenceResult{}, errors.New("stored runtime evidence claim is invalid")
			}
			claims = append(claims, map[string]any{
				"target": target, "support_level": claimLevel,
				"device": supportEvidenceString(claim, "device"), "software_version": supportEvidenceString(claim, "software_version"),
				"verified_at": supportEvidenceString(claim, "verified_at"), "result": supportEvidenceString(claim, "result"),
				"scenarios": claim["scenarios"],
			})
		}
		if queryErr = rows.Close(); queryErr != nil {
			return ReviewedHardwareEvidenceResult{}, queryErr
		}
		summaryEvidence["target_claims"] = claims
		evidenceJSON := jsonText(summaryEvidence, "{}")
		result, updateErr := tx.ExecContext(ctx, `UPDATE `+update.table+` SET support_level=?,evidence_json=?,updated_at=? WHERE id=?`, level, evidenceJSON, now, update.id)
		if updateErr != nil {
			return ReviewedHardwareEvidenceResult{}, updateErr
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return ReviewedHardwareEvidenceResult{}, fmt.Errorf("reviewed runtime object %s changed during commit", update.id)
		}
	}
	if err = tx.Commit(); err != nil {
		return ReviewedHardwareEvidenceResult{}, err
	}

	result := ReviewedHardwareEvidenceResult{}
	if result.DeviceProfile, err = s.GetDeviceProfile(ctx, device.ID); err != nil {
		return ReviewedHardwareEvidenceResult{}, err
	}
	if result.EmulatorDriver, err = s.GetEmulatorDriver(ctx, driver.ID); err != nil {
		return ReviewedHardwareEvidenceResult{}, err
	}
	if frontend != nil {
		item, getErr := s.GetFrontendAdapter(ctx, frontend.ID)
		if getErr != nil {
			return ReviewedHardwareEvidenceResult{}, getErr
		}
		result.Frontend = &item
	}
	if core != nil {
		item, getErr := s.GetRetroArchCore(ctx, core.ID)
		if getErr != nil {
			return ReviewedHardwareEvidenceResult{}, getErr
		}
		result.RetroArchCore = &item
	}
	return result, nil
}
