package catalog

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func validHardwareEvidence() map[string]any {
	return map[string]any{
		"scope":            "hardware",
		"device":           "fixture-handheld",
		"software_version": "fixture-emulator-1.0",
		"verified_at":      "2026-08-27",
		"result":           "passed",
		"scenarios":        []string{"cold-start", "launch", "resume"},
	}
}

func TestRuntimeSupportClaimsRequireStructuredEvidence(t *testing.T) {
	checks := []struct {
		name string
		run  func() error
	}{
		{"source adapter", func() error {
			_, err := normalizeSourceAdapter(NewSourceAdapter{Name: "Source", Format: "source", Handler: "pegasus", SupportLevel: "hardware-tested"})
			return err
		}},
		{"frontend adapter", func() error {
			_, err := normalizeFrontendAdapter(NewFrontendAdapter{Name: "Frontend", Format: "frontend", SupportLevel: "hardware-tested"})
			return err
		}},
		{"device profile", func() error {
			_, err := normalizeDeviceProfile(NewDeviceProfile{Name: "Device", Target: "windows", OSFamily: "windows", SupportLevel: "hardware-tested"})
			return err
		}},
		{"emulator driver", func() error {
			_, err := normalizeEmulatorDriver(NewEmulatorDriver{Name: "Driver", Family: "driver", Platforms: []string{"gba"}, Targets: []string{"windows"}, Save: DriverSaveSpec{Scope: "game", Layout: "single-file"}, SupportLevel: "hardware-tested"})
			return err
		}},
		{"RetroArch core", func() error {
			_, err := normalizeRetroArchCore(NewRetroArchCore{Name: "Core", LibraryNames: []string{"core_libretro"}, Platforms: []string{"gba"}, SupportLevel: "hardware-tested"})
			return err
		}},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.run(); err == nil || !strings.Contains(err.Error(), "hardware-tested evidence") {
				t.Fatalf("unsupported claim error=%v", err)
			}
		})
	}
	if err := validateSupportEvidence("hardware-tested", validHardwareEvidence()); err != nil {
		t.Fatalf("valid hardware evidence rejected: %v", err)
	}
	syncEvidence := validHardwareEvidence()
	syncEvidence["scope"] = "sync"
	syncEvidence["scenarios"] = []any{"upload", "download", "conflict", "offline-recovery"}
	if err := validateSupportEvidence("sync-tested", syncEvidence); err != nil {
		t.Fatalf("valid sync evidence rejected: %v", err)
	}
}

func TestValidateSupportEvidenceAuditsDirectDatabaseDrift(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	device, err := store.CreateDeviceProfile(ctx, NewDeviceProfile{Name: "Verified device", Target: "windows", OSFamily: "windows"})
	if err != nil {
		t.Fatal(err)
	}
	boundEvidence, err := json.Marshal(bindSupportEvidence(validHardwareEvidence(), "device_profile", device.ID, device.ContractVersion))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.ExecContext(ctx, `UPDATE device_profiles SET support_level='hardware-tested',evidence_json=? WHERE id=?`, string(boundEvidence), device.ID); err != nil {
		t.Fatal(err)
	}
	if err = store.ValidateSupportEvidence(ctx); err != nil {
		t.Fatalf("valid persisted evidence rejected: %v", err)
	}
	if _, err = store.db.ExecContext(ctx, `UPDATE device_profiles SET support_level='sync-tested',evidence_json='{}' WHERE id=?`, device.ID); err != nil {
		t.Fatal(err)
	}
	if err = store.ValidateSupportEvidence(ctx); err == nil || !strings.Contains(err.Error(), "sync-tested evidence") {
		t.Fatalf("direct evidence drift was not detected: %v", err)
	}
	if _, err = store.GetDeviceProfile(ctx, device.ID); err == nil || !strings.Contains(err.Error(), "stored support evidence") {
		t.Fatalf("read boundary accepted direct evidence drift: %v", err)
	}
	if err = store.ValidateRuntimeCatalog(ctx); err == nil || !strings.Contains(err.Error(), "device profiles") {
		t.Fatalf("full runtime catalog audit accepted direct evidence drift: %v", err)
	}
}

func TestDeviceProfilePathsStayPortableAndNonPrivate(t *testing.T) {
	invalid := []map[string]string{
		{"save_dir": "/" + "Users/example/Saves"},
		{"save_dir": `C:\Users\example\Saves`},
		{"save_dir": "../saves"},
		{"private_home": "saves"},
	}
	for _, paths := range invalid {
		if _, err := normalizeDeviceProfile(NewDeviceProfile{Name: "Device", Target: "windows", OSFamily: "windows", Paths: paths}); err == nil || !strings.Contains(err.Error(), "device profile path") {
			t.Fatalf("private or unsafe device path accepted: %#v, %v", paths, err)
		}
	}
	profile, err := normalizeDeviceProfile(NewDeviceProfile{Name: "Device", Target: "windows", OSFamily: "windows", Paths: map[string]string{"config_dir": "config/./retroarch", "save_dir": "saves"}})
	if err != nil || profile.Paths["config_dir"] != "config/retroarch" {
		t.Fatalf("portable paths were not normalized: %#v, %v", profile.Paths, err)
	}
}

func TestDeviceProfileReadBoundaryRejectsLegacyPrivatePaths(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	profile, err := store.CreateDeviceProfile(ctx, NewDeviceProfile{
		Name: "Legacy device", Target: "windows", OSFamily: "windows",
		Paths: map[string]string{"save_dir": "saves"},
	})
	if err != nil {
		t.Fatal(err)
	}
	privatePath := "/" + "Users/private-owner/Emulator/Saves"
	if _, err = store.db.ExecContext(ctx, `UPDATE device_profiles SET paths_json=? WHERE id=?`, `{"save_dir":"`+privatePath+`"}`, profile.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.GetDeviceProfile(ctx, profile.ID); err == nil || !strings.Contains(err.Error(), "portable relative path") || strings.Contains(err.Error(), privatePath) {
		t.Fatalf("read boundary did not safely reject a private path: %v", err)
	}
	if err = store.ValidateRuntimeCatalog(ctx); err == nil || !strings.Contains(err.Error(), "device profiles") || strings.Contains(err.Error(), privatePath) {
		t.Fatalf("catalog audit did not safely reject a private path: %v", err)
	}
}
