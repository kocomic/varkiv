package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
)

type HardwareReadinessGate struct {
	ID               string   `json:"id"`
	Status           string   `json:"status"`
	RequiredLevel    string   `json:"required_level"`
	SatisfiedTargets []string `json:"satisfied_targets,omitempty"`
	Missing          []string `json:"missing,omitempty"`
}

type HardwareReadinessReport struct {
	Format string                  `json:"format"`
	Ready  bool                    `json:"ready"`
	Gates  []HardwareReadinessGate `json:"gates"`
}

type runtimeEvidenceClaim struct {
	Kind            string
	RuntimeID       string
	Target          string
	ContractVersion int
	SupportLevel    string
	Evidence        map[string]any
}

func runtimeTable(kind string) (string, bool) {
	switch kind {
	case "source_adapter":
		return "source_adapters", true
	case "frontend_adapter":
		return "frontend_adapters", true
	case "device_profile":
		return "device_profiles", true
	case "emulator_driver":
		return "emulator_drivers", true
	case "retroarch_core":
		return "retroarch_cores", true
	default:
		return "", false
	}
}

func (s *Store) runtimeClaim(ctx context.Context, kind, id, target string) (runtimeEvidenceClaim, error) {
	table, ok := runtimeTable(kind)
	if !ok {
		return runtimeEvidenceClaim{}, errors.New("unsupported runtime evidence kind")
	}
	var currentVersion int
	var enabled int
	if err := s.db.QueryRowContext(ctx, `SELECT contract_version,enabled FROM `+table+` WHERE id=?`, id).Scan(&currentVersion, &enabled); err != nil {
		return runtimeEvidenceClaim{}, err
	}
	if enabled == 0 {
		return runtimeEvidenceClaim{}, errors.New("runtime object is disabled")
	}
	claim := runtimeEvidenceClaim{Kind: kind, RuntimeID: id, Target: target}
	var encoded string
	if err := s.db.QueryRowContext(ctx, `SELECT contract_version,support_level,evidence_json FROM runtime_evidence_claims WHERE runtime_kind=? AND runtime_id=? AND target=?`, kind, id, target).Scan(&claim.ContractVersion, &claim.SupportLevel, &encoded); err != nil {
		return runtimeEvidenceClaim{}, err
	}
	if claim.ContractVersion != currentVersion {
		return runtimeEvidenceClaim{}, errors.New("runtime evidence contract is stale")
	}
	if err := json.Unmarshal([]byte(encoded), &claim.Evidence); err != nil {
		return runtimeEvidenceClaim{}, errors.New("runtime evidence is invalid")
	}
	if err := validateSupportEvidence(claim.SupportLevel, claim.Evidence); err != nil {
		return runtimeEvidenceClaim{}, err
	}
	if err := validateSupportEvidenceBinding(claim.SupportLevel, claim.Evidence, kind, id, currentVersion); err != nil {
		return runtimeEvidenceClaim{}, err
	}
	device := supportEvidenceString(claim.Evidence, "device")
	if device != target && !strings.HasPrefix(device, target+"/") {
		return runtimeEvidenceClaim{}, errors.New("runtime evidence target does not match")
	}
	return claim, nil
}

func claimScenarios(evidence map[string]any) map[string]bool {
	result := map[string]bool{}
	switch values := evidence["scenarios"].(type) {
	case []string:
		for _, value := range values {
			result[value] = true
		}
	case []any:
		for _, raw := range values {
			if value, ok := raw.(string); ok {
				result[value] = true
			}
		}
	}
	return result
}

func claimMeets(claim runtimeEvidenceClaim, level string, scenarios []string) bool {
	if supportLevelRank(claim.SupportLevel) < supportLevelRank(level) {
		return false
	}
	observed := claimScenarios(claim.Evidence)
	return !slices.ContainsFunc(scenarios, func(value string) bool { return !observed[value] })
}

func (s *Store) anyRuntimeClaimMeets(ctx context.Context, kind, target, level string, scenarios []string) (bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT runtime_id FROM runtime_evidence_claims WHERE runtime_kind=? AND target=? ORDER BY runtime_id`, kind, target)
	if err != nil {
		return false, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return false, err
		}
		ids = append(ids, id)
	}
	if err = rows.Close(); err != nil {
		return false, err
	}
	for _, id := range ids {
		claim, claimErr := s.runtimeClaim(ctx, kind, id, target)
		if claimErr == nil && claimMeets(claim, level, scenarios) {
			return true, nil
		}
	}
	return false, nil
}

func (s *Store) componentMeets(ctx context.Context, kind, id, target, level string, scenarios []string) bool {
	claim, err := s.runtimeClaim(ctx, kind, id, target)
	return err == nil && claimMeets(claim, level, scenarios)
}

func readinessGate(id, level string, targets, missing []string) HardwareReadinessGate {
	status := "passed"
	if len(missing) != 0 {
		status = "pending"
	}
	sort.Strings(targets)
	sort.Strings(missing)
	return HardwareReadinessGate{ID: id, Status: status, RequiredLevel: level, SatisfiedTargets: targets, Missing: missing}
}

// HardwareReadiness evaluates only reviewed, target-specific evidence stored in
// the catalog. It never probes devices, opens ROM/save paths, or turns fixture
// support into a hardware claim.
func (s *Store) HardwareReadiness(ctx context.Context) (HardwareReadinessReport, error) {
	if err := s.ValidateSupportEvidence(ctx); err != nil {
		return HardwareReadinessReport{}, err
	}
	base := []string{"frontend-launch", "rom-launch", "emulator-exit"}
	syncScenarios := []string{"frontend-launch", "rom-launch", "emulator-exit", "save-created", "sync-upload", "sync-download", "conflict-recovery", "offline-play", "sleep-resume", "token-revocation", "upgrade"}
	linuxScenarios := append(append([]string{}, base...), "network-recovery", "upgrade")
	androidScenarios := append(append([]string{}, base...), "saf-rom-root", "saf-save-tree", "keystore-token", "retroarch-intent", "ppsspp-intent", "background-recovery", "upgrade")

	windowsMissing := []string{}
	for label, ok := range map[string]bool{
		"device":   s.componentMeets(ctx, "device_profile", "builtin-device-windows-handheld", "windows", "sync-tested", syncScenarios),
		"frontend": s.componentMeets(ctx, "frontend_adapter", "builtin-frontend-pegasus", "windows", "sync-tested", syncScenarios),
		"driver":   s.componentMeets(ctx, "emulator_driver", "builtin-driver-retroarch", "windows", "sync-tested", syncScenarios),
	} {
		if !ok {
			windowsMissing = append(windowsMissing, label)
		}
	}
	coreOK, err := s.anyRuntimeClaimMeets(ctx, "retroarch_core", "windows", "sync-tested", syncScenarios)
	if err != nil {
		return HardwareReadinessReport{}, err
	}
	if !coreOK {
		windowsMissing = append(windowsMissing, "retroarch_core")
	}

	linuxGate := func(target, deviceID, frontendID string) (bool, []string, error) {
		missing := []string{}
		if !s.componentMeets(ctx, "device_profile", deviceID, target, "hardware-tested", linuxScenarios) {
			missing = append(missing, "device")
		}
		if !s.componentMeets(ctx, "frontend_adapter", frontendID, target, "hardware-tested", linuxScenarios) {
			missing = append(missing, "frontend")
		}
		driverOK, driverErr := s.anyRuntimeClaimMeets(ctx, "emulator_driver", target, "hardware-tested", linuxScenarios)
		if driverErr != nil {
			return false, nil, driverErr
		}
		if !driverOK {
			missing = append(missing, "driver")
		}
		return len(missing) == 0, missing, nil
	}
	steamOK, steamMissing, err := linuxGate("steamos-bazzite", "builtin-device-steamos-bazzite", "builtin-frontend-esde")
	if err != nil {
		return HardwareReadinessReport{}, err
	}
	handheldTargets := []struct{ target, device, frontend string }{
		{"rocknix", "builtin-device-rocknix", "builtin-frontend-esde"},
		{"darkos", "builtin-device-darkos", "builtin-frontend-esde"},
		{"arkos", "builtin-device-arkos", "builtin-frontend-esde"},
		{"knulli", "builtin-device-knulli", "builtin-frontend-esde"},
		{"muos", "builtin-device-muos", "builtin-frontend-pegasus"},
		{"onionos", "builtin-device-onionos", "builtin-frontend-pegasus"},
	}
	handheldSatisfied := []string{}
	for _, candidate := range handheldTargets {
		ok, _, gateErr := linuxGate(candidate.target, candidate.device, candidate.frontend)
		if gateErr != nil {
			return HardwareReadinessReport{}, gateErr
		}
		if ok {
			handheldSatisfied = append(handheldSatisfied, candidate.target)
		}
	}
	handheldMissing := []string{}
	if len(handheldSatisfied) == 0 {
		handheldMissing = append(handheldMissing, "one_handheld_linux_target")
	}

	androidMissing := []string{}
	for label, ok := range map[string]bool{
		"device":           s.componentMeets(ctx, "device_profile", "builtin-device-android-handheld", "android", "hardware-tested", androidScenarios),
		"frontend":         s.componentMeets(ctx, "frontend_adapter", "builtin-frontend-pegasus", "android", "hardware-tested", androidScenarios),
		"retroarch_driver": s.componentMeets(ctx, "emulator_driver", "builtin-driver-retroarch", "android", "hardware-tested", androidScenarios),
		"ppsspp_driver":    s.componentMeets(ctx, "emulator_driver", "builtin-driver-ppsspp", "android", "hardware-tested", androidScenarios),
	} {
		if !ok {
			androidMissing = append(androidMissing, label)
		}
	}
	androidCoreOK, err := s.anyRuntimeClaimMeets(ctx, "retroarch_core", "android", "hardware-tested", androidScenarios)
	if err != nil {
		return HardwareReadinessReport{}, err
	}
	if !androidCoreOK {
		androidMissing = append(androidMissing, "retroarch_core")
	}

	gates := []HardwareReadinessGate{
		readinessGate("windows-retroarch-sync", "sync-tested", nil, windowsMissing),
		readinessGate("steamos-bazzite-hardware", "hardware-tested", map[bool][]string{true: {"steamos-bazzite"}}[steamOK], steamMissing),
		readinessGate("handheld-linux-hardware", "hardware-tested", handheldSatisfied, handheldMissing),
		readinessGate("android-boundaries-hardware", "hardware-tested", map[bool][]string{true: {"android"}}[len(androidMissing) == 0], androidMissing),
	}
	ready := !slices.ContainsFunc(gates, func(gate HardwareReadinessGate) bool { return gate.Status != "passed" })
	return HardwareReadinessReport{Format: "varkiv-hardware-readiness-v1", Ready: ready, Gates: gates}, nil
}
