package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func fixtureHardwareAcceptanceReport() hardwareAcceptanceReport {
	now := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	return hardwareAcceptanceReport{
		Format: "varkiv-hardware-acceptance-v1", GeneratedAt: now, AgentVersion: "preview-23",
		HostOS: "windows", HostArchitecture: "amd64", Target: "windows", ConfigProtected: true,
		Roots: acceptanceRootSummary{AgentRootReal: true, ROMRootsConfigured: 2, ROMRootsReal: true, DriverRootsConfigured: 1, DriverRootsReal: true, PathOverrides: 2},
		Runtime: acceptanceRuntimeProbe{
			Target: "windows", EmulatorDirConfigured: true, CoreDirConfigured: true,
			Drivers:          []acceptanceProbeItem{{ID: "builtin-driver-retroarch", Name: "RetroArch", Status: "installed", Match: "retroarch.exe"}},
			Cores:            []acceptanceProbeItem{{ID: "builtin-core-mgba", Name: "mGBA", Status: "installed", Match: "mgba_libretro.dll"}},
			InstalledDrivers: 1, InstalledCores: 1,
		},
		LastSync:           &acceptanceSyncStatus{State: "complete", AttemptedAt: now, FinishedAt: now, LastSuccessAt: now, SessionRecorded: true, Uploaded: 1, Downloaded: 1},
		ObservedOnHardware: []string{"frontend-launch", "rom-launch", "emulator-exit", "save-created", "sync-upload", "sync-download", "conflict-recovery", "offline-play", "sleep-resume", "token-revocation", "upgrade"},
		SoftwarePreflight:  true, EvidenceLevel: "candidate", RequiresReview: true, ContainsPrivateData: false,
	}
}

func TestHardwareAcceptancePreviewAndCommitAreSignedSanitizedAndAtomic(t *testing.T) {
	store, handler, _ := testServer(t)
	request := hardwareAcceptanceRequest{
		Report: fixtureHardwareAcceptanceReport(), DeviceProfileID: "builtin-device-windows-handheld",
		DriverID: "builtin-driver-retroarch", CoreID: "builtin-core-mgba",
	}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	recorder := jsonErrorRequest(t, handler, http.MethodPost, "/api/v1/hardware-acceptance/preview", request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "retroarch.exe") || strings.Contains(recorder.Body.String(), "mgba_libretro.dll") || strings.Contains(recorder.Body.String(), "uploaded") {
		t.Fatalf("preview leaked probe or sync details: %s", recorder.Body.String())
	}
	var preview hardwareAcceptancePreview
	if err = json.Unmarshal(recorder.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if !preview.Eligible || preview.SupportLevel != "sync-tested" || preview.PreviewToken == "" || preview.Frontend == nil || preview.RetroArchCore == nil {
		t.Fatalf("preview=%#v request=%s", preview, data)
	}
	request.PreviewToken = preview.PreviewToken
	var committed struct {
		SupportLevel string `json:"support_level"`
		Updated      struct {
			DeviceProfile struct {
				SupportLevel string `json:"support_level"`
			} `json:"device_profile"`
			Frontend struct {
				SupportLevel string `json:"support_level"`
			} `json:"frontend"`
			Driver struct {
				SupportLevel string `json:"support_level"`
			} `json:"emulator_driver"`
			Core struct {
				SupportLevel string `json:"support_level"`
			} `json:"retroarch_core"`
		} `json:"updated"`
	}
	jsonRequest(t, handler, http.MethodPost, "/api/v1/hardware-acceptance/commit", request, &committed)
	if committed.SupportLevel != "sync-tested" || committed.Updated.DeviceProfile.SupportLevel != "sync-tested" || committed.Updated.Frontend.SupportLevel != "sync-tested" || committed.Updated.Driver.SupportLevel != "sync-tested" || committed.Updated.Core.SupportLevel != "sync-tested" {
		t.Fatalf("commit=%#v", committed)
	}
	device, _ := store.GetDeviceProfile(t.Context(), "builtin-device-windows-handheld")
	if device.Evidence["report_sha256"] == "" || device.Evidence["scope"] != "sync" {
		t.Fatalf("persisted evidence=%#v", device.Evidence)
	}
	encoded, _ := json.Marshal(device.Evidence)
	if strings.Contains(string(encoded), "retroarch.exe") || strings.Contains(string(encoded), "mgba_libretro.dll") {
		t.Fatalf("persisted evidence leaked local matches: %s", encoded)
	}
}

func TestHardwareAcceptanceRejectsTamperingAndPrivateReports(t *testing.T) {
	_, handler, _ := testServer(t)
	request := hardwareAcceptanceRequest{Report: fixtureHardwareAcceptanceReport(), DeviceProfileID: "builtin-device-windows-handheld", DriverID: "builtin-driver-retroarch", CoreID: "builtin-core-mgba"}
	var preview hardwareAcceptancePreview
	jsonRequest(t, handler, http.MethodPost, "/api/v1/hardware-acceptance/preview", request, &preview)
	request.PreviewToken = preview.PreviewToken
	request.Report.ObservedOnHardware = request.Report.ObservedOnHardware[:len(request.Report.ObservedOnHardware)-1]
	tampered := jsonErrorRequest(t, handler, http.MethodPost, "/api/v1/hardware-acceptance/commit", request)
	if tampered.Code != http.StatusConflict || !strings.Contains(tampered.Body.String(), "hardware_acceptance_stale") {
		t.Fatalf("tampered commit: %d %s", tampered.Code, tampered.Body.String())
	}

	privateMarker := `C:\\Users\\private-owner\\Saves`
	request = hardwareAcceptanceRequest{Report: fixtureHardwareAcceptanceReport(), DeviceProfileID: "builtin-device-windows-handheld", DriverID: "builtin-driver-retroarch", CoreID: "builtin-core-mgba"}
	request.Report.ContainsPrivateData = true
	request.Report.Runtime.Drivers[0].Match = privateMarker
	rejected := jsonErrorRequest(t, handler, http.MethodPost, "/api/v1/hardware-acceptance/preview", request)
	if rejected.Code != http.StatusBadRequest || strings.Contains(rejected.Body.String(), privateMarker) {
		t.Fatalf("private report rejection: %d %s", rejected.Code, rejected.Body.String())
	}

	request = hardwareAcceptanceRequest{Report: fixtureHardwareAcceptanceReport(), DeviceProfileID: "builtin-device-windows-handheld", DriverID: "builtin-driver-retroarch", CoreID: "builtin-core-mgba"}
	request.Report.LastSync.Uploaded = -1
	rejected = jsonErrorRequest(t, handler, http.MethodPost, "/api/v1/hardware-acceptance/preview", request)
	if rejected.Code != http.StatusBadRequest || !strings.Contains(rejected.Body.String(), "counts are out of range") {
		t.Fatalf("invalid sync summary: %d %s", rejected.Code, rejected.Body.String())
	}
}

func TestHardwareAcceptanceRequirementsAreTargetSpecific(t *testing.T) {
	android := fixtureHardwareAcceptanceReport()
	android.Target, android.Runtime.Target, android.HostOS, android.HostArchitecture = "android", "android", "android", "arm64"
	android.LastSync = nil
	android.ObservedOnHardware = []string{"frontend-launch", "rom-launch", "emulator-exit"}
	_, level, missingHardware, _, err := validateAcceptanceReport(android)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"saf-rom-root", "saf-save-tree", "keystore-token", "retroarch-intent", "ppsspp-intent", "background-recovery", "upgrade"} {
		if !slices.Contains(missingHardware, required) {
			t.Fatalf("Android requirement %q was not enforced: %#v", required, missingHardware)
		}
		android.ObservedOnHardware = append(android.ObservedOnHardware, required)
	}
	if level != "" {
		t.Fatalf("incomplete Android report reached %q", level)
	}
	_, level, missingHardware, _, err = validateAcceptanceReport(android)
	if err != nil || level != "hardware-tested" || len(missingHardware) != 0 {
		t.Fatalf("complete Android boundary report level=%q missing=%#v err=%v", level, missingHardware, err)
	}

	linux := fixtureHardwareAcceptanceReport()
	linux.Target, linux.Runtime.Target, linux.HostOS, linux.HostArchitecture = "rocknix", "rocknix", "linux", "arm64"
	linux.LastSync = nil
	linux.ObservedOnHardware = []string{"frontend-launch", "rom-launch", "emulator-exit"}
	_, level, missingHardware, _, err = validateAcceptanceReport(linux)
	if err != nil || level != "" || !slices.Contains(missingHardware, "network-recovery") || !slices.Contains(missingHardware, "upgrade") {
		t.Fatalf("handheld Linux target requirements level=%q missing=%#v err=%v", level, missingHardware, err)
	}
}

func TestAndroidAgentAcceptanceFixtureMatchesServerContract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "android", "acceptance-candidate.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report hardwareAcceptanceReport
	if err = json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	report.GeneratedAt = time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	observed, level, missingHardware, _, err := validateAcceptanceReport(report)
	if err != nil || level != "hardware-tested" || len(missingHardware) != 0 || len(observed) != 10 {
		t.Fatalf("Android Agent fixture level=%q missing=%#v observed=%#v err=%v", level, missingHardware, observed, err)
	}
	encoded := string(data)
	for _, private := range []string{"server_url", "access_token", "device_id", "content://", "/storage/", "rom_name", "save_name"} {
		if strings.Contains(encoded, private) {
			t.Fatalf("Android Agent fixture leaked private field %q", private)
		}
	}
}
