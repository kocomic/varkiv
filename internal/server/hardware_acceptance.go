package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"varkiv/internal/catalog"
)

var errHardwareAcceptanceStale = errors.New("hardware acceptance preview is stale")

var acceptanceIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+\-]{0,79}$`)
var windowsAbsolutePathPattern = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

var acceptedHardwareObservations = map[string]bool{
	"frontend-launch": true, "rom-launch": true, "emulator-exit": true, "save-created": true,
	"sync-upload": true, "sync-download": true, "conflict-recovery": true, "offline-play": true,
	"sleep-resume": true, "token-revocation": true, "upgrade": true,
	"network-recovery": true, "saf-rom-root": true, "saf-save-tree": true, "keystore-token": true,
	"retroarch-intent": true, "ppsspp-intent": true, "background-recovery": true,
}

var hardwareRequiredObservations = []string{"frontend-launch", "rom-launch", "emulator-exit"}
var syncRequiredObservations = []string{
	"frontend-launch", "rom-launch", "emulator-exit", "save-created", "sync-upload", "sync-download",
	"conflict-recovery", "offline-play", "sleep-resume", "token-revocation", "upgrade",
}

func hardwareRequiredForTarget(target string) []string {
	required := append([]string{}, hardwareRequiredObservations...)
	switch target {
	case "steamos-bazzite", "rocknix", "darkos", "arkos", "knulli", "muos", "onionos":
		required = append(required, "network-recovery", "upgrade")
	case "android":
		required = append(required, "saf-rom-root", "saf-save-tree", "keystore-token", "retroarch-intent", "ppsspp-intent", "background-recovery", "upgrade")
	}
	return required
}

type acceptanceRootSummary struct {
	AgentRootReal         bool `json:"agent_root_real"`
	ROMRootsConfigured    int  `json:"rom_roots_configured"`
	ROMRootsReal          bool `json:"rom_roots_real"`
	DriverRootsConfigured int  `json:"driver_roots_configured"`
	DriverRootsReal       bool `json:"driver_roots_real"`
	PathOverrides         int  `json:"path_overrides"`
}

type acceptanceProbeItem struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Match  string `json:"match,omitempty"`
}

type acceptanceRuntimeProbe struct {
	Target                string                `json:"target"`
	EmulatorDirConfigured bool                  `json:"emulator_dir_configured"`
	CoreDirConfigured     bool                  `json:"core_dir_configured"`
	Drivers               []acceptanceProbeItem `json:"drivers"`
	Cores                 []acceptanceProbeItem `json:"retroarch_cores"`
	InstalledDrivers      int                   `json:"installed_drivers"`
	InstalledCores        int                   `json:"installed_cores"`
}

type acceptanceSyncStatus struct {
	State           string `json:"state"`
	AttemptedAt     string `json:"attempted_at"`
	FinishedAt      string `json:"finished_at,omitempty"`
	LastSuccessAt   string `json:"last_success_at,omitempty"`
	SessionRecorded bool   `json:"session_recorded"`
	Uploaded        int    `json:"uploaded"`
	Downloaded      int    `json:"downloaded"`
	Conflicts       int    `json:"conflicts"`
	ErrorCode       string `json:"error_code,omitempty"`
}

type hardwareAcceptanceReport struct {
	Format              string                 `json:"format"`
	GeneratedAt         string                 `json:"generated_at"`
	AgentVersion        string                 `json:"agent_version"`
	HostOS              string                 `json:"host_os"`
	HostArchitecture    string                 `json:"host_architecture"`
	Target              string                 `json:"target"`
	ConfigProtected     bool                   `json:"config_protected"`
	Roots               acceptanceRootSummary  `json:"roots"`
	Runtime             acceptanceRuntimeProbe `json:"runtime"`
	LastSync            *acceptanceSyncStatus  `json:"last_sync,omitempty"`
	ObservedOnHardware  []string               `json:"observed_on_hardware"`
	SoftwarePreflight   bool                   `json:"software_preflight_passed"`
	EvidenceLevel       string                 `json:"evidence_level"`
	RequiresReview      bool                   `json:"requires_maintainer_review"`
	ContainsPrivateData bool                   `json:"contains_private_data"`
}

type hardwareAcceptanceRequest struct {
	Report          hardwareAcceptanceReport `json:"report"`
	DeviceProfileID string                   `json:"device_profile_id"`
	DriverID        string                   `json:"driver_id"`
	CoreID          string                   `json:"core_id,omitempty"`
	PreviewToken    string                   `json:"preview_token,omitempty"`
}

type acceptanceObjectPreview struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	CurrentLevel string `json:"current_level"`
}

type hardwareAcceptancePreview struct {
	Eligible       bool                     `json:"eligible"`
	PreviewToken   string                   `json:"preview_token,omitempty"`
	SupportLevel   string                   `json:"support_level,omitempty"`
	GeneratedAt    string                   `json:"generated_at"`
	AgentVersion   string                   `json:"agent_version"`
	Target         string                   `json:"target"`
	Host           string                   `json:"host"`
	Scenarios      []string                 `json:"scenarios"`
	Missing        []string                 `json:"missing_for_hardware,omitempty"`
	MissingForSync []string                 `json:"missing_for_sync,omitempty"`
	DeviceProfile  acceptanceObjectPreview  `json:"device_profile"`
	Frontend       *acceptanceObjectPreview `json:"frontend,omitempty"`
	EmulatorDriver acceptanceObjectPreview  `json:"emulator_driver"`
	RetroArchCore  *acceptanceObjectPreview `json:"retroarch_core,omitempty"`
}

type hardwareAcceptanceSnapshot struct {
	ReportDigest            string   `json:"report_digest"`
	GeneratedAt             string   `json:"generated_at"`
	AgentVersion            string   `json:"agent_version"`
	Target                  string   `json:"target"`
	HostOS                  string   `json:"host_os"`
	HostArchitecture        string   `json:"host_architecture"`
	Scenarios               []string `json:"scenarios"`
	SupportLevel            string   `json:"support_level"`
	DeviceProfileID         string   `json:"device_profile_id"`
	DeviceContractVersion   int      `json:"device_contract_version"`
	FrontendID              string   `json:"frontend_id,omitempty"`
	FrontendContractVersion int      `json:"frontend_contract_version,omitempty"`
	DriverID                string   `json:"driver_id"`
	DriverContractVersion   int      `json:"driver_contract_version"`
	CoreID                  string   `json:"core_id,omitempty"`
	CoreContractVersion     int      `json:"core_contract_version,omitempty"`
}

func missingObservations(observed map[string]bool, required []string) []string {
	missing := []string{}
	for _, value := range required {
		if !observed[value] {
			missing = append(missing, value)
		}
	}
	return missing
}

func validateAcceptanceText(name, value string) error {
	if !acceptanceIdentifierPattern.MatchString(value) {
		return fmt.Errorf("hardware acceptance %s is invalid", name)
	}
	return nil
}

func architectureMatches(profile, host string) bool {
	if profile == "" || profile == host {
		return true
	}
	return (profile == "x86_64" && host == "amd64") || (profile == "amd64" && host == "x86_64") ||
		(profile == "aarch64" && host == "arm64") || (profile == "arm64" && host == "aarch64")
}

func hostMatchesDeviceProfile(device catalog.DeviceProfile, report hardwareAcceptanceReport) bool {
	if device.Target != report.Target || !architectureMatches(device.Architecture, report.HostArchitecture) {
		return false
	}
	switch device.OSFamily {
	case "handheld-linux":
		return report.HostOS == "linux"
	case "portable":
		return true
	default:
		return device.OSFamily == report.HostOS
	}
}

func validateAcceptanceSyncStatus(status acceptanceSyncStatus) error {
	switch status.State {
	case "running", "complete", "conflict", "failed":
	default:
		return errors.New("hardware acceptance sync status has an invalid state")
	}
	attempted, err := time.Parse(time.RFC3339Nano, status.AttemptedAt)
	if err != nil {
		return errors.New("hardware acceptance sync status has an invalid attempted_at")
	}
	if status.Uploaded < 0 || status.Downloaded < 0 || status.Conflicts < 0 || status.Uploaded > 1_000_000 || status.Downloaded > 1_000_000 || status.Conflicts > 1_000_000 {
		return errors.New("hardware acceptance sync status counts are out of range")
	}
	if status.State == "running" {
		if status.FinishedAt != "" || status.ErrorCode != "" || status.SessionRecorded || status.Uploaded != 0 || status.Downloaded != 0 || status.Conflicts != 0 {
			return errors.New("running hardware acceptance sync status must not be finished")
		}
	} else {
		finished, parseErr := time.Parse(time.RFC3339Nano, status.FinishedAt)
		if parseErr != nil || finished.Before(attempted) {
			return errors.New("hardware acceptance sync status has an invalid finished_at")
		}
	}
	if status.LastSuccessAt != "" {
		if _, err = time.Parse(time.RFC3339Nano, status.LastSuccessAt); err != nil {
			return errors.New("hardware acceptance sync status has an invalid last_success_at")
		}
	}
	switch status.State {
	case "complete":
		if status.ErrorCode != "" || status.Conflicts != 0 || !status.SessionRecorded {
			return errors.New("complete hardware acceptance sync status must record a session without an error")
		}
	case "conflict":
		if status.ErrorCode != "sync_conflict" || status.Conflicts == 0 || !status.SessionRecorded {
			return errors.New("conflict hardware acceptance sync status requires a recorded conflict session")
		}
	case "failed":
		if status.ErrorCode != "sync_failed" || status.SessionRecorded {
			return errors.New("failed hardware acceptance sync status requires sync_failed without a recorded session")
		}
	}
	return nil
}

func validateAcceptanceReport(report hardwareAcceptanceReport) ([]string, string, []string, []string, error) {
	if report.Format != "varkiv-hardware-acceptance-v1" || report.EvidenceLevel != "candidate" || !report.RequiresReview {
		return nil, "", nil, nil, errors.New("hardware acceptance report has an invalid review contract")
	}
	if report.ContainsPrivateData {
		return nil, "", nil, nil, errors.New("hardware acceptance report declares private data and cannot be imported")
	}
	if !report.ConfigProtected || !report.SoftwarePreflight || !report.Roots.AgentRootReal || report.Roots.ROMRootsConfigured < 1 || !report.Roots.ROMRootsReal || !report.Roots.DriverRootsReal {
		return nil, "", nil, nil, errors.New("hardware acceptance software preflight did not pass")
	}
	if report.Roots.ROMRootsConfigured > 128 || report.Roots.DriverRootsConfigured > 128 || report.Roots.PathOverrides > 128 {
		return nil, "", nil, nil, errors.New("hardware acceptance root counts are out of range")
	}
	for name, value := range map[string]string{"agent_version": report.AgentVersion, "host_os": report.HostOS, "host_architecture": report.HostArchitecture, "target": report.Target} {
		if err := validateAcceptanceText(name, value); err != nil {
			return nil, "", nil, nil, err
		}
	}
	generatedAt, err := time.Parse(time.RFC3339Nano, report.GeneratedAt)
	if err != nil {
		return nil, "", nil, nil, errors.New("hardware acceptance generated_at is invalid")
	}
	now := time.Now().UTC()
	if generatedAt.After(now.Add(5*time.Minute)) || generatedAt.Before(now.AddDate(-1, 0, 0)) {
		return nil, "", nil, nil, errors.New("hardware acceptance report is outside the one-year review window")
	}
	if report.Runtime.Target != report.Target || len(report.Runtime.Drivers) > 128 || len(report.Runtime.Cores) > 128 {
		return nil, "", nil, nil, errors.New("hardware acceptance runtime probe is invalid")
	}
	installedDrivers, installedCores := 0, 0
	seenProbeIDs := map[string]bool{}
	for _, group := range [][]acceptanceProbeItem{report.Runtime.Drivers, report.Runtime.Cores} {
		for _, item := range group {
			if err = validateAcceptanceText("runtime object id", item.ID); err != nil || seenProbeIDs[item.ID] {
				return nil, "", nil, nil, errors.New("hardware acceptance runtime object IDs are invalid or duplicated")
			}
			seenProbeIDs[item.ID] = true
			if !slices.Contains([]string{"installed", "missing", "not-configured", "android-companion-required"}, item.Status) {
				return nil, "", nil, nil, errors.New("hardware acceptance runtime probe status is invalid")
			}
			if len(item.Name) > 160 || strings.ContainsAny(item.Name, "\x00\r\n") || len(item.Match) > 512 || strings.ContainsAny(item.Match, "\x00\r\n") || strings.HasPrefix(item.Match, "/") || windowsAbsolutePathPattern.MatchString(item.Match) || strings.Contains(strings.ReplaceAll(item.Match, "\\", "/"), "../") {
				return nil, "", nil, nil, errors.New("hardware acceptance runtime probe text is invalid")
			}
		}
	}
	for _, item := range report.Runtime.Drivers {
		if item.Status == "installed" {
			installedDrivers++
		}
	}
	for _, item := range report.Runtime.Cores {
		if item.Status == "installed" {
			installedCores++
		}
	}
	if installedDrivers != report.Runtime.InstalledDrivers || installedCores != report.Runtime.InstalledCores {
		return nil, "", nil, nil, errors.New("hardware acceptance installed runtime counts do not match the probe")
	}
	if report.LastSync != nil {
		if err = validateAcceptanceSyncStatus(*report.LastSync); err != nil {
			return nil, "", nil, nil, err
		}
	}
	if len(report.ObservedOnHardware) > len(acceptedHardwareObservations) {
		return nil, "", nil, nil, errors.New("hardware acceptance observations are invalid")
	}
	observedMap := map[string]bool{}
	for _, observation := range report.ObservedOnHardware {
		if !acceptedHardwareObservations[observation] || observedMap[observation] {
			return nil, "", nil, nil, errors.New("hardware acceptance observations are invalid or duplicated")
		}
		observedMap[observation] = true
	}
	observed := append([]string{}, report.ObservedOnHardware...)
	sort.Strings(observed)
	targetHardware := hardwareRequiredForTarget(report.Target)
	missingHardware := missingObservations(observedMap, targetHardware)
	targetSync := append(append([]string{}, syncRequiredObservations...), targetHardware...)
	slices.Sort(targetSync)
	targetSync = slices.Compact(targetSync)
	missingSync := missingObservations(observedMap, targetSync)
	level := ""
	if len(missingHardware) == 0 {
		level = "hardware-tested"
	}
	if len(missingSync) == 0 {
		if report.LastSync == nil || !report.LastSync.SessionRecorded || (report.LastSync.State != "complete" && report.LastSync.State != "conflict") {
			missingSync = append(missingSync, "recorded-sync-session")
		} else {
			level = "sync-tested"
		}
	}
	return observed, level, missingHardware, missingSync, nil
}

func installedProbeContains(items []acceptanceProbeItem, id string) bool {
	return slices.ContainsFunc(items, func(item acceptanceProbeItem) bool { return item.ID == id && item.Status == "installed" })
}

func reportDigest(report hardwareAcceptanceReport) (string, error) {
	data, err := json.Marshal(report)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func (s *Server) buildHardwareAcceptancePreview(ctx context.Context, in hardwareAcceptanceRequest) (hardwareAcceptancePreview, hardwareAcceptanceSnapshot, error) {
	observed, level, missingHardware, missingSync, err := validateAcceptanceReport(in.Report)
	if err != nil {
		return hardwareAcceptancePreview{}, hardwareAcceptanceSnapshot{}, fmt.Errorf("invalid hardware acceptance report: %w", err)
	}
	in.DeviceProfileID, in.DriverID, in.CoreID = strings.TrimSpace(in.DeviceProfileID), strings.TrimSpace(in.DriverID), strings.TrimSpace(in.CoreID)
	if !installedProbeContains(in.Report.Runtime.Drivers, in.DriverID) {
		return hardwareAcceptancePreview{}, hardwareAcceptanceSnapshot{}, errors.New("selected emulator driver was not installed in the reviewed probe")
	}
	if in.CoreID != "" && !installedProbeContains(in.Report.Runtime.Cores, in.CoreID) {
		return hardwareAcceptancePreview{}, hardwareAcceptanceSnapshot{}, errors.New("selected RetroArch core was not installed in the reviewed probe")
	}
	device, err := s.store.GetDeviceProfile(ctx, in.DeviceProfileID)
	if err != nil {
		return hardwareAcceptancePreview{}, hardwareAcceptanceSnapshot{}, err
	}
	if !device.Enabled || !hostMatchesDeviceProfile(device, in.Report) {
		return hardwareAcceptancePreview{}, hardwareAcceptanceSnapshot{}, errors.New("selected device profile does not match the reviewed host")
	}
	driver, err := s.store.GetEmulatorDriver(ctx, in.DriverID)
	if err != nil {
		return hardwareAcceptancePreview{}, hardwareAcceptanceSnapshot{}, err
	}
	if !driver.Enabled || !slices.Contains(driver.Targets, device.Target) || (driver.Launch.RequiresCore && in.CoreID == "") {
		return hardwareAcceptancePreview{}, hardwareAcceptanceSnapshot{}, errors.New("selected emulator driver is disabled, incompatible, or requires a core")
	}
	if !driver.Launch.RequiresCore && in.CoreID != "" {
		return hardwareAcceptancePreview{}, hardwareAcceptanceSnapshot{}, errors.New("selected emulator driver does not use a RetroArch core")
	}

	preview := hardwareAcceptancePreview{
		Eligible: len(missingHardware) == 0, SupportLevel: level, GeneratedAt: in.Report.GeneratedAt,
		AgentVersion: in.Report.AgentVersion, Target: in.Report.Target, Host: in.Report.HostOS + "/" + in.Report.HostArchitecture,
		Scenarios: observed, Missing: missingHardware, MissingForSync: missingSync,
		DeviceProfile:  acceptanceObjectPreview{ID: device.ID, Name: device.Name, CurrentLevel: device.SupportLevel},
		EmulatorDriver: acceptanceObjectPreview{ID: driver.ID, Name: driver.Name, CurrentLevel: driver.SupportLevel},
	}
	snapshot := hardwareAcceptanceSnapshot{
		GeneratedAt: in.Report.GeneratedAt, AgentVersion: in.Report.AgentVersion, Target: in.Report.Target,
		HostOS: in.Report.HostOS, HostArchitecture: in.Report.HostArchitecture, Scenarios: observed, SupportLevel: level,
		DeviceProfileID: device.ID, DeviceContractVersion: device.ContractVersion,
		DriverID: driver.ID, DriverContractVersion: driver.ContractVersion,
	}
	if snapshot.ReportDigest, err = reportDigest(in.Report); err != nil {
		return hardwareAcceptancePreview{}, hardwareAcceptanceSnapshot{}, err
	}
	if device.DefaultFrontendID != "" {
		frontend, frontendErr := s.store.GetFrontendAdapter(ctx, device.DefaultFrontendID)
		if frontendErr != nil {
			return hardwareAcceptancePreview{}, hardwareAcceptanceSnapshot{}, frontendErr
		}
		if !frontend.Enabled {
			return hardwareAcceptancePreview{}, hardwareAcceptanceSnapshot{}, errors.New("selected device profile default frontend is disabled")
		}
		preview.Frontend = &acceptanceObjectPreview{ID: frontend.ID, Name: frontend.Name, CurrentLevel: frontend.SupportLevel}
		snapshot.FrontendID, snapshot.FrontendContractVersion = frontend.ID, frontend.ContractVersion
	}
	if in.CoreID != "" {
		core, coreErr := s.store.GetRetroArchCore(ctx, in.CoreID)
		if coreErr != nil {
			return hardwareAcceptancePreview{}, hardwareAcceptanceSnapshot{}, coreErr
		}
		if !core.Enabled {
			return hardwareAcceptancePreview{}, hardwareAcceptanceSnapshot{}, errors.New("selected RetroArch core is disabled")
		}
		if !slices.ContainsFunc(driver.Platforms, func(platform string) bool { return slices.Contains(core.Platforms, platform) }) {
			return hardwareAcceptancePreview{}, hardwareAcceptanceSnapshot{}, errors.New("selected RetroArch core is incompatible with the emulator driver")
		}
		preview.RetroArchCore = &acceptanceObjectPreview{ID: core.ID, Name: core.Name, CurrentLevel: core.SupportLevel}
		snapshot.CoreID, snapshot.CoreContractVersion = core.ID, core.ContractVersion
	}
	if preview.Eligible {
		preview.PreviewToken, err = s.signPreviewValue(previewTokenDomainHardwareAcceptance, snapshot)
		if err != nil {
			return hardwareAcceptancePreview{}, hardwareAcceptanceSnapshot{}, err
		}
	}
	return preview, snapshot, nil
}

func (s *Server) previewHardwareAcceptance(w http.ResponseWriter, r *http.Request) {
	var in hardwareAcceptanceRequest
	if !decode(w, r, &in) {
		return
	}
	preview, _, err := s.buildHardwareAcceptancePreview(r.Context(), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) commitHardwareAcceptance(w http.ResponseWriter, r *http.Request) {
	var in hardwareAcceptanceRequest
	if !decode(w, r, &in) {
		return
	}
	preview, snapshot, err := s.buildHardwareAcceptancePreview(r.Context(), in)
	if err != nil {
		writeError(w, err)
		return
	}
	if !preview.Eligible || in.PreviewToken == "" || !hmac.Equal([]byte(preview.PreviewToken), []byte(in.PreviewToken)) {
		writeError(w, errHardwareAcceptanceStale)
		return
	}
	generatedAt, _ := time.Parse(time.RFC3339Nano, snapshot.GeneratedAt)
	evidence := map[string]any{
		"scope":            map[bool]string{true: "sync", false: "hardware"}[snapshot.SupportLevel == "sync-tested"],
		"device":           snapshot.Target + "/" + snapshot.HostOS + "/" + snapshot.HostArchitecture,
		"software_version": "Device Agent " + snapshot.AgentVersion,
		"verified_at":      generatedAt.UTC().Format("2006-01-02"), "result": "passed",
		"scenarios": snapshot.Scenarios, "report_format": in.Report.Format, "report_sha256": snapshot.ReportDigest,
		"reviewed_at": time.Now().UTC().Format("2006-01-02"),
	}
	result, err := s.store.ApplyReviewedHardwareEvidence(r.Context(), catalog.ReviewedHardwareEvidence{
		DeviceProfileID: snapshot.DeviceProfileID, DeviceContractVersion: snapshot.DeviceContractVersion,
		ExpectedFrontendID: snapshot.FrontendID, FrontendContractVersion: snapshot.FrontendContractVersion,
		DriverID: snapshot.DriverID, DriverContractVersion: snapshot.DriverContractVersion,
		CoreID: snapshot.CoreID, CoreContractVersion: snapshot.CoreContractVersion,
		SupportLevel: snapshot.SupportLevel, Evidence: evidence,
	})
	if err != nil {
		if errors.Is(err, catalog.ErrReviewedHardwareEvidenceStale) {
			writeError(w, errHardwareAcceptanceStale)
			return
		}
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"support_level": snapshot.SupportLevel, "evidence": evidence, "updated": result})
}
