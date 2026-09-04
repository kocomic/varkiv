package catalog

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func reviewedEvidence(level string) map[string]any {
	scope := "hardware"
	if level == "sync-tested" {
		scope = "sync"
	}
	return map[string]any{
		"scope": scope, "device": "windows/windows/amd64", "software_version": "Device Agent test",
		"verified_at": "2026-08-27", "result": "passed", "scenarios": []string{"frontend-launch", "rom-launch"},
	}
}

func TestReviewedHardwareEvidencePromotesRuntimeObjectsAtomically(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	frontend, err := store.CreateFrontendAdapter(ctx, NewFrontendAdapter{ID: "fixture-frontend", Name: "Frontend", Format: "fixture", SupportLevel: "package-tested"})
	if err != nil {
		t.Fatal(err)
	}
	driver, err := store.CreateEmulatorDriver(ctx, NewEmulatorDriver{ID: "fixture-driver", Name: "Driver", Family: "fixture", Platforms: []string{"gba"}, Targets: []string{"windows", "android"}, Launch: DriverLaunchSpec{RequiresCore: true, Arguments: []string{"{{rom.path}}"}}, Save: DriverSaveSpec{Scope: "game", Layout: "single-file"}})
	if err != nil {
		t.Fatal(err)
	}
	core, err := store.CreateRetroArchCore(ctx, NewRetroArchCore{ID: "fixture-core", Name: "Core", LibraryNames: []string{"fixture_libretro"}, Platforms: []string{"gba"}})
	if err != nil {
		t.Fatal(err)
	}
	device, err := store.CreateDeviceProfile(ctx, NewDeviceProfile{ID: "fixture-device", Name: "Device", Target: "windows", OSFamily: "windows", Architecture: "x86_64", DefaultFrontendID: frontend.ID})
	if err != nil {
		t.Fatal(err)
	}

	result, err := store.ApplyReviewedHardwareEvidence(ctx, ReviewedHardwareEvidence{
		DeviceProfileID: device.ID, DeviceContractVersion: device.ContractVersion,
		ExpectedFrontendID: frontend.ID, FrontendContractVersion: frontend.ContractVersion,
		DriverID: driver.ID, DriverContractVersion: driver.ContractVersion,
		CoreID: core.ID, CoreContractVersion: core.ContractVersion,
		SupportLevel: "sync-tested", Evidence: reviewedEvidence("sync-tested"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.DeviceProfile.SupportLevel != "sync-tested" || result.EmulatorDriver.SupportLevel != "sync-tested" || result.Frontend == nil || result.Frontend.SupportLevel != "sync-tested" || result.RetroArchCore == nil || result.RetroArchCore.SupportLevel != "sync-tested" {
		t.Fatalf("reviewed evidence result=%#v", result)
	}
	if result.DeviceProfile.Target != "windows" || result.EmulatorDriver.Launch.Arguments[0] != "{{rom.path}}" || result.RetroArchCore.LibraryNames[0] != "fixture_libretro" {
		t.Fatal("hardware review changed a runtime definition")
	}
	bindings := []struct {
		kind     string
		id       string
		version  int
		evidence map[string]any
	}{
		{"device_profile", result.DeviceProfile.ID, result.DeviceProfile.ContractVersion, result.DeviceProfile.Evidence},
		{"frontend_adapter", result.Frontend.ID, result.Frontend.ContractVersion, result.Frontend.Evidence},
		{"emulator_driver", result.EmulatorDriver.ID, result.EmulatorDriver.ContractVersion, result.EmulatorDriver.Evidence},
		{"retroarch_core", result.RetroArchCore.ID, result.RetroArchCore.ContractVersion, result.RetroArchCore.Evidence},
	}
	for _, binding := range bindings {
		if err = validateSupportEvidenceBinding("sync-tested", binding.evidence, binding.kind, binding.id, binding.version); err != nil {
			t.Fatalf("reviewed %s evidence was not bound to its runtime contract: %v", binding.kind, err)
		}
	}
	android, err := store.CreateDeviceProfile(ctx, NewDeviceProfile{ID: "fixture-android", Name: "Android device", Target: "android", OSFamily: "android", Architecture: "arm64", DefaultFrontendID: frontend.ID})
	if err != nil {
		t.Fatal(err)
	}
	androidEvidence := reviewedEvidence("hardware-tested")
	androidEvidence["device"] = "android/android/arm64"
	androidResult, err := store.ApplyReviewedHardwareEvidence(ctx, ReviewedHardwareEvidence{
		DeviceProfileID: android.ID, DeviceContractVersion: android.ContractVersion,
		ExpectedFrontendID: frontend.ID, FrontendContractVersion: frontend.ContractVersion,
		DriverID: driver.ID, DriverContractVersion: driver.ContractVersion,
		CoreID: core.ID, CoreContractVersion: core.ContractVersion,
		SupportLevel: "hardware-tested", Evidence: androidEvidence,
	})
	if err != nil {
		t.Fatal(err)
	}
	if androidResult.EmulatorDriver.SupportLevel != "sync-tested" || androidResult.Frontend.SupportLevel != "sync-tested" || androidResult.RetroArchCore.SupportLevel != "sync-tested" {
		t.Fatalf("a second target downgraded stronger evidence: %#v", androidResult)
	}
	for kind, evidence := range map[string]map[string]any{
		"frontend": androidResult.Frontend.Evidence,
		"driver":   androidResult.EmulatorDriver.Evidence,
		"core":     androidResult.RetroArchCore.Evidence,
	} {
		claims, ok := evidence["target_claims"].([]any)
		if !ok || len(claims) != 2 {
			t.Fatalf("%s did not retain independent Windows and Android evidence: %#v", kind, evidence)
		}
	}
	if _, err = store.db.ExecContext(ctx, `UPDATE emulator_drivers SET contract_version=contract_version+1 WHERE id=?`, driver.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.GetEmulatorDriver(ctx, driver.ID); err == nil || !strings.Contains(err.Error(), "stale or is bound") {
		t.Fatalf("runtime contract drift did not invalidate reviewed evidence: %v", err)
	}
}

func TestReviewedHardwareEvidenceRejectsIncompatibleSelectionWithoutPartialPromotion(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	frontend, _ := store.CreateFrontendAdapter(ctx, NewFrontendAdapter{ID: "fixture-frontend", Name: "Frontend", Format: "fixture"})
	driver, _ := store.CreateEmulatorDriver(ctx, NewEmulatorDriver{ID: "fixture-driver", Name: "Driver", Family: "fixture", Platforms: []string{"gba"}, Targets: []string{"windows"}, Launch: DriverLaunchSpec{RequiresCore: true}, Save: DriverSaveSpec{Scope: "game", Layout: "single-file"}})
	core, _ := store.CreateRetroArchCore(ctx, NewRetroArchCore{ID: "fixture-core", Name: "Core", LibraryNames: []string{"fixture_libretro"}, Platforms: []string{"3ds"}})
	device, _ := store.CreateDeviceProfile(ctx, NewDeviceProfile{ID: "fixture-device", Name: "Device", Target: "windows", OSFamily: "windows", DefaultFrontendID: frontend.ID})

	if _, err = store.ApplyReviewedHardwareEvidence(ctx, ReviewedHardwareEvidence{
		DeviceProfileID: device.ID, DeviceContractVersion: device.ContractVersion + 1,
		ExpectedFrontendID: frontend.ID, FrontendContractVersion: frontend.ContractVersion,
		DriverID: driver.ID, DriverContractVersion: driver.ContractVersion,
		CoreID: core.ID, CoreContractVersion: core.ContractVersion,
		SupportLevel: "hardware-tested", Evidence: reviewedEvidence("hardware-tested"),
	}); !errors.Is(err, ErrReviewedHardwareEvidenceStale) {
		t.Fatalf("stale runtime contract error=%v", err)
	}

	if _, err = store.ApplyReviewedHardwareEvidence(ctx, ReviewedHardwareEvidence{
		DeviceProfileID: device.ID, DeviceContractVersion: device.ContractVersion,
		ExpectedFrontendID: frontend.ID, FrontendContractVersion: frontend.ContractVersion,
		DriverID: driver.ID, DriverContractVersion: driver.ContractVersion,
		CoreID: core.ID, CoreContractVersion: core.ContractVersion,
		SupportLevel: "hardware-tested", Evidence: reviewedEvidence("hardware-tested"),
	}); err == nil {
		t.Fatal("incompatible reviewed core was accepted")
	}
	for kind, check := range map[string]func() string{
		"device":   func() string { item, _ := store.GetDeviceProfile(ctx, device.ID); return item.SupportLevel },
		"frontend": func() string { item, _ := store.GetFrontendAdapter(ctx, frontend.ID); return item.SupportLevel },
		"driver":   func() string { item, _ := store.GetEmulatorDriver(ctx, driver.ID); return item.SupportLevel },
		"core":     func() string { item, _ := store.GetRetroArchCore(ctx, core.ID); return item.SupportLevel },
	} {
		if level := check(); level != "catalogued" {
			t.Fatalf("%s was partially promoted to %s", kind, level)
		}
	}
}

func TestDeletingUnusedCustomRuntimeObjectsRemovesTheirEvidenceClaims(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	frontend, err := store.CreateFrontendAdapter(ctx, NewFrontendAdapter{ID: "delete-evidence-frontend", Name: "Frontend", Format: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	driver, err := store.CreateEmulatorDriver(ctx, NewEmulatorDriver{ID: "delete-evidence-driver", Name: "Driver", Family: "fixture", Platforms: []string{"gba"}, Targets: []string{"windows"}, Launch: DriverLaunchSpec{RequiresCore: true}, Save: DriverSaveSpec{Scope: "game", Layout: "single-file"}})
	if err != nil {
		t.Fatal(err)
	}
	core, err := store.CreateRetroArchCore(ctx, NewRetroArchCore{ID: "delete-evidence-core", Name: "Core", LibraryNames: []string{"fixture_libretro"}, Platforms: []string{"gba"}})
	if err != nil {
		t.Fatal(err)
	}
	device, err := store.CreateDeviceProfile(ctx, NewDeviceProfile{ID: "delete-evidence-device", Name: "Device", Target: "windows", OSFamily: "windows", Architecture: "x86_64", DefaultFrontendID: frontend.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ApplyReviewedHardwareEvidence(ctx, ReviewedHardwareEvidence{
		DeviceProfileID: device.ID, DeviceContractVersion: device.ContractVersion,
		ExpectedFrontendID: frontend.ID, FrontendContractVersion: frontend.ContractVersion,
		DriverID: driver.ID, DriverContractVersion: driver.ContractVersion,
		CoreID: core.ID, CoreContractVersion: core.ContractVersion,
		SupportLevel: "sync-tested", Evidence: reviewedEvidence("sync-tested"),
	}); err != nil {
		t.Fatal(err)
	}
	enabled := true
	if _, err = store.UpdateDeviceProfile(ctx, device.ID, NewDeviceProfile{Name: device.Name, Target: "android", OSFamily: "android", Architecture: "arm64", PathStyle: device.PathStyle, MaxPath: device.MaxPath, DefaultFrontendID: frontend.ID, SupportLevel: device.SupportLevel, Evidence: device.Evidence, Enabled: &enabled}); !errors.Is(err, ErrRuntimeObjectInUse) {
		t.Fatalf("target-specific evidence allowed device identity drift: %v", err)
	}
	for _, item := range []struct {
		id     string
		remove func(context.Context, string) error
	}{
		{device.ID, store.DeleteDeviceProfile},
		{driver.ID, store.DeleteEmulatorDriver},
		{core.ID, store.DeleteRetroArchCore},
		{frontend.ID, store.DeleteFrontendAdapter},
	} {
		if err = item.remove(ctx, item.id); err != nil {
			t.Fatal(err)
		}
	}
	var claims int
	if err = store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runtime_evidence_claims WHERE runtime_id IN (?,?,?,?)`, device.ID, frontend.ID, driver.ID, core.ID).Scan(&claims); err != nil || claims != 0 {
		t.Fatalf("deleted runtime evidence claims=%d err=%v", claims, err)
	}
}
