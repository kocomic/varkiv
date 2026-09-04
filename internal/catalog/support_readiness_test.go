package catalog

import (
	"context"
	"path/filepath"
	"testing"
)

func TestHardwareReadinessRequiresIndependentTargetEvidence(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	pegasus, err := store.CreateFrontendAdapter(ctx, NewFrontendAdapter{ID: "builtin-frontend-pegasus", Name: "Pegasus", Format: "pegasus", Builtin: true})
	if err != nil {
		t.Fatal(err)
	}
	esde, err := store.CreateFrontendAdapter(ctx, NewFrontendAdapter{ID: "builtin-frontend-esde", Name: "ES-DE", Format: "es-de", Builtin: true})
	if err != nil {
		t.Fatal(err)
	}
	retroarch, err := store.CreateEmulatorDriver(ctx, NewEmulatorDriver{
		ID: "builtin-driver-retroarch", Name: "RetroArch", Family: "retroarch", Platforms: []string{"gba"}, Builtin: true,
		Targets: []string{"windows", "steamos-bazzite", "rocknix", "android"}, Launch: DriverLaunchSpec{RequiresCore: true},
		Save: DriverSaveSpec{Scope: "game", Layout: "single-file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ppsspp, err := store.CreateEmulatorDriver(ctx, NewEmulatorDriver{
		ID: "builtin-driver-ppsspp", Name: "PPSSPP", Family: "ppsspp", Platforms: []string{"psp"}, Targets: []string{"android"}, Builtin: true,
		Save: DriverSaveSpec{Scope: "game", Layout: "directory"},
	})
	if err != nil {
		t.Fatal(err)
	}
	core, err := store.CreateRetroArchCore(ctx, NewRetroArchCore{ID: "fixture-core", Name: "Core", LibraryNames: []string{"fixture_libretro"}, Platforms: []string{"gba"}})
	if err != nil {
		t.Fatal(err)
	}
	devices := map[string]DeviceProfile{}
	for _, spec := range []NewDeviceProfile{
		{ID: "builtin-device-windows-handheld", Name: "Windows", Target: "windows", OSFamily: "windows", DefaultFrontendID: pegasus.ID, Builtin: true},
		{ID: "builtin-device-steamos-bazzite", Name: "SteamOS", Target: "steamos-bazzite", OSFamily: "handheld-linux", DefaultFrontendID: esde.ID, Builtin: true},
		{ID: "builtin-device-rocknix", Name: "ROCKNIX", Target: "rocknix", OSFamily: "handheld-linux", DefaultFrontendID: esde.ID, Builtin: true},
		{ID: "builtin-device-android-handheld", Name: "Android", Target: "android", OSFamily: "android", DefaultFrontendID: pegasus.ID, Builtin: true},
	} {
		device, createErr := store.CreateDeviceProfile(ctx, spec)
		if createErr != nil {
			t.Fatal(createErr)
		}
		devices[device.Target] = device
	}

	apply := func(target, level string, frontend FrontendAdapter, driver EmulatorDriver, selectedCore *RetroArchCore, scenarios []string) {
		t.Helper()
		device := devices[target]
		evidence := reviewedEvidence(level)
		evidence["device"] = target + "/fixture/architecture"
		evidence["scenarios"] = scenarios
		input := ReviewedHardwareEvidence{
			DeviceProfileID: device.ID, DeviceContractVersion: device.ContractVersion,
			ExpectedFrontendID: frontend.ID, FrontendContractVersion: frontend.ContractVersion,
			DriverID: driver.ID, DriverContractVersion: driver.ContractVersion,
			SupportLevel: level, Evidence: evidence,
		}
		if selectedCore != nil {
			input.CoreID, input.CoreContractVersion = selectedCore.ID, selectedCore.ContractVersion
		}
		if _, applyErr := store.ApplyReviewedHardwareEvidence(ctx, input); applyErr != nil {
			t.Fatal(applyErr)
		}
	}
	syncScenarios := []string{"frontend-launch", "rom-launch", "emulator-exit", "save-created", "sync-upload", "sync-download", "conflict-recovery", "offline-play", "sleep-resume", "token-revocation", "upgrade"}
	linuxScenarios := []string{"frontend-launch", "rom-launch", "emulator-exit", "network-recovery", "upgrade"}
	androidScenarios := []string{"frontend-launch", "rom-launch", "emulator-exit", "saf-rom-root", "saf-save-tree", "keystore-token", "retroarch-intent", "ppsspp-intent", "background-recovery", "upgrade"}
	apply("windows", "sync-tested", pegasus, retroarch, &core, syncScenarios)
	apply("steamos-bazzite", "hardware-tested", esde, retroarch, &core, linuxScenarios)
	apply("rocknix", "hardware-tested", esde, retroarch, &core, linuxScenarios)
	apply("android", "hardware-tested", pegasus, retroarch, &core, androidScenarios)
	apply("android", "hardware-tested", pegasus, ppsspp, nil, androidScenarios)

	report, err := store.HardwareReadiness(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Ready || len(report.Gates) != 4 {
		t.Fatalf("complete target evidence did not pass readiness: %#v", report)
	}
	for _, gate := range report.Gates {
		if gate.Status != "passed" || len(gate.Missing) != 0 {
			t.Fatalf("gate did not pass: %#v", gate)
		}
	}

	if _, err = store.db.ExecContext(ctx, `UPDATE emulator_drivers SET contract_version=contract_version+1 WHERE id=?`, retroarch.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.HardwareReadiness(ctx); err == nil {
		t.Fatal("contract drift did not fail the readiness audit")
	}
}
