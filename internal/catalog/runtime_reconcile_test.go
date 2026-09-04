package catalog

import (
	"context"
	"strings"
	"testing"
)

func TestBuiltinDriverContractUpgradeDoesNotTouchUserDrivers(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	enabled := true
	base := NewEmulatorDriver{ID: "builtin-driver", Name: "Built-in", Family: "fixture", ContractVersion: 1, Platforms: []string{"gba"}, Targets: []string{"windows"}, Launch: DriverLaunchSpec{Arguments: []string{"{{rom.path}}"}}, Save: DriverSaveSpec{Scope: "game"}, Builtin: true, Enabled: &enabled}
	if _, err := store.CreateEmulatorDriver(ctx, base); err != nil {
		t.Fatal(err)
	}
	base.ContractVersion = 2
	base.Launch.Executables = map[string][]string{"windows": {"fixture.exe"}}
	upgraded, err := store.ReconcileBuiltinEmulatorDriver(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if upgraded.ContractVersion != 2 || len(upgraded.Launch.Executables["windows"]) != 1 {
		t.Fatalf("built-in contract was not upgraded: %#v", upgraded)
	}
	custom := base
	custom.ID, custom.Builtin = "custom-driver", false
	if _, err = store.CreateEmulatorDriver(ctx, custom); err != nil {
		t.Fatal(err)
	}
	custom.ContractVersion = 3
	if _, err = store.ReconcileBuiltinEmulatorDriver(ctx, custom); err == nil || !strings.Contains(err.Error(), "user-created") {
		t.Fatalf("user driver reconciliation was not rejected: %v", err)
	}
}
