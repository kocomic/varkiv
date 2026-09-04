package deviceagent

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"varkiv/internal/catalog"
	"varkiv/internal/server"
)

func TestHardwareAcceptanceReportIsUsefulAndPrivacyMinimized(t *testing.T) {
	library, state, root := t.TempDir(), t.TempDir(), t.TempDir()
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	app, err := server.New(store, library, server.WithStateRoot(state), server.WithToken("admin-secret"))
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(app.Handler())
	defer httpServer.Close()
	configPath := filepath.Join(t.TempDir(), "agent.json")
	config := pairTestAgent(t, httpServer.URL, app.Handler(), root, configPath, "Private handheld name")
	romRoot := filepath.Join(root, "roms")
	driverRoot := filepath.Join(root, "driver-user")
	emulatorRoot := filepath.Join(root, "emulators")
	coreRoot := filepath.Join(root, "cores")
	for _, directory := range []string{romRoot, driverRoot, emulatorRoot, coreRoot} {
		if err = os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.WriteFile(filepath.Join(emulatorRoot, "retroarch.exe"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(coreRoot, "mgba_libretro.dll"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	config.ROMRoots = map[string]string{"gba": romRoot}
	config.DriverRoots = map[string]string{"builtin-driver-retroarch": driverRoot}
	config.PathOverrides["emulator_dir"] = emulatorRoot
	config.PathOverrides["core_dir"] = coreRoot
	if err = UpdateConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	report, err := BuildHardwareAcceptanceReport(context.Background(), configPath, "test-version", []string{"sync-download", "frontend-launch", "sync-download"})
	if err != nil {
		t.Fatal(err)
	}
	if !report.SoftwarePreflight || !report.ConfigProtected || report.Target != "windows" || report.EvidenceLevel != "candidate" || !report.RequiresReview || report.ContainsPrivateData {
		t.Fatalf("acceptance report boundary=%#v", report)
	}
	if report.Roots.ROMRootsConfigured != 1 || !report.Roots.ROMRootsReal || report.Roots.DriverRootsConfigured != 1 || !report.Roots.DriverRootsReal || report.Runtime.InstalledDrivers == 0 || report.Runtime.InstalledCores == 0 {
		t.Fatalf("acceptance preflight=%#v", report)
	}
	if strings.Join(report.ObservedOnHardware, ",") != "frontend-launch,sync-download" {
		t.Fatalf("observations=%#v", report.ObservedOnHardware)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{httpServer.URL, config.DeviceID, config.AccessToken, config.DeviceProfileID, root, romRoot, driverRoot, emulatorRoot, coreRoot, configPath, "Private handheld name"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("acceptance report retained private value %q: %s", private, encoded)
		}
	}
	if _, err = BuildHardwareAcceptanceReport(context.Background(), configPath, "test-version", []string{"pretend-success"}); err == nil {
		t.Fatal("unsupported hardware observation was accepted")
	}
}
