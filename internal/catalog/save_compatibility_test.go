package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func createCompatibilityFixture(t *testing.T, store *Store) (Device, SaveStream, SaveBinding, NewSaveCompatibilityGroup) {
	t.Helper()
	ctx := context.Background()
	enabled := true
	webDriver, err := store.CreateEmulatorDriver(ctx, NewEmulatorDriver{ID: "fixture-web-snes", Name: "Web Snes", Family: "web", ContractVersion: 5, Platforms: []string{"snes"}, Targets: []string{"web"}, Launch: DriverLaunchSpec{Arguments: []string{"{{rom.path}}"}}, Save: DriverSaveSpec{Scope: "game"}, Builtin: true, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	nativeDriver, err := store.CreateEmulatorDriver(ctx, NewEmulatorDriver{ID: "fixture-retroarch", Name: "RetroArch", Family: "retroarch", ContractVersion: 6, Platforms: []string{"snes"}, Targets: []string{"fixture-linux"}, Launch: DriverLaunchSpec{RequiresCore: true, Executables: map[string][]string{"fixture-linux": {"retroarch"}}, Arguments: []string{"-L", "{{core.library}}", "{{rom.path}}"}}, Save: DriverSaveSpec{Scope: "game"}, Builtin: true, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	core, err := store.CreateRetroArchCore(ctx, NewRetroArchCore{ID: "fixture-snes-core", Name: "Snes9x", ContractVersion: 3, LibraryNames: []string{"snes9x_libretro"}, Platforms: []string{"snes"}, Builtin: true, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	groupInput := NewSaveCompatibilityGroup{ID: "fixture-snes-raw", Name: "Exact SNES SRAM", Format: "raw-libretro-srm", ContractVersion: 1, Builtin: true, Enabled: &enabled, Members: []SaveCompatibilityMember{
		{DriverID: webDriver.ID, RuntimeKind: "server", DriverContractVersion: webDriver.ContractVersion},
		{DriverID: nativeDriver.ID, CoreID: core.ID, RuntimeKind: "device", DriverContractVersion: nativeDriver.ContractVersion, CoreContractVersion: core.ContractVersion, OSFamily: "linux", Architecture: "arm64", DriverSHA256: strings.Repeat("a", 64), DriverSize: 10, CoreSHA256: strings.Repeat("b", 64), CoreSize: 20},
	}}
	if _, err = store.ReconcileBuiltinSaveCompatibilityGroup(ctx, groupInput); err != nil {
		t.Fatal(err)
	}
	profile, err := store.CreateDeviceProfile(ctx, NewDeviceProfile{ID: "fixture-linux-profile", Name: "Fixture Linux", Target: "fixture-linux", OSFamily: "handheld-linux", Architecture: "aarch64"})
	if err != nil {
		t.Fatal(err)
	}
	device, err := store.CreateDevice(ctx, NewDevice{ID: "fixture-device", Name: "Fixture Device", DeviceProfileID: profile.ID, OSFamily: "linux", Architecture: "arm64"})
	if err != nil {
		t.Fatal(err)
	}
	game, err := store.CreateGame(ctx, NewGame{DefaultTitle: "Fixture", Platform: "snes"})
	if err != nil {
		t.Fatal(err)
	}
	edition, err := store.AddEdition(ctx, NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original"})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := store.CreateSaveStream(ctx, NewSaveStream{OwnerType: "edition", OwnerKey: edition.ID, DriverID: webDriver.ID, Portability: "core-dependent", CompatibilityGroupID: groupInput.ID, EditionIDs: []string{edition.ID}})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := store.CreateSaveBinding(ctx, NewSaveBinding{StreamID: stream.ID, EditionID: edition.ID, DeviceProfileID: profile.ID, DriverID: nativeDriver.ID, CoreID: core.ID, LocalPaths: []string{"{{device.save_dir}}/{{rom.stem}}.srm"}})
	if err != nil {
		t.Fatal(err)
	}
	return device, stream, binding, groupInput
}

func TestExactRuntimeAttestationGatesCrossDriverSaveBinding(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	device, stream, binding, group := createCompatibilityFixture(t, store)
	if stream.CompatibilityGroupID != group.ID || binding.CoreID == "" {
		t.Fatalf("compatibility metadata missing: stream=%#v binding=%#v", stream, binding)
	}
	if authorized, err := store.SaveBindingRuntimeAuthorized(ctx, device, binding); err != nil || authorized {
		t.Fatalf("unattested runtime authorized=%v err=%v", authorized, err)
	}
	wrong := []RuntimeAttestationReport{
		{Kind: "driver", RuntimeID: binding.DriverID, ContractVersion: 6, SHA256: strings.Repeat("c", 64), Size: 10},
		{Kind: "core", RuntimeID: binding.CoreID, ContractVersion: 3, SHA256: strings.Repeat("b", 64), Size: 20},
	}
	updated, err := store.RecordDeviceHeartbeat(ctx, device.ID, map[string]bool{"runtime_probe": true}, wrong)
	if err != nil || updated.Capabilities["verified_save_bridge"] {
		t.Fatalf("wrong identity promoted bridge: %#v err=%v", updated.Capabilities, err)
	}
	if authorized, err := store.SaveBindingRuntimeAuthorized(ctx, updated, binding); err != nil || authorized {
		t.Fatalf("wrong identity authorized=%v err=%v", authorized, err)
	}
	exact := []RuntimeAttestationReport{
		{Kind: "driver", RuntimeID: binding.DriverID, ContractVersion: 6, SHA256: strings.Repeat("a", 64), Size: 10},
		{Kind: "core", RuntimeID: binding.CoreID, ContractVersion: 3, SHA256: strings.Repeat("b", 64), Size: 20},
	}
	updated, err = store.RecordDeviceHeartbeat(ctx, device.ID, map[string]bool{"runtime_probe": true}, exact)
	if err != nil || !updated.Capabilities["runtime_identity_attested"] || !updated.Capabilities["verified_save_bridge"] {
		t.Fatalf("exact identity did not promote bridge: %#v err=%v", updated.Capabilities, err)
	}
	if authorized, err := store.SaveBindingRuntimeAuthorized(ctx, updated, binding); err != nil || !authorized {
		t.Fatalf("exact identity authorized=%v err=%v", authorized, err)
	}
	items, err := store.ListRuntimeAttestations(ctx, device.ID)
	if err != nil || len(items) != 2 {
		t.Fatalf("attestations=%#v err=%v", items, err)
	}
	encoded, _ := json.Marshal(items)
	if strings.Contains(string(encoded), "/") || strings.Contains(string(encoded), "\\") || strings.Contains(string(encoded), "retroarch.exe") {
		t.Fatalf("attestation leaked a path or basename: %s", encoded)
	}
	updated, err = store.RecordDeviceHeartbeat(ctx, device.ID, map[string]bool{"runtime_probe": true}, nil)
	if err != nil || updated.Capabilities["runtime_identity_attested"] || updated.Capabilities["verified_save_bridge"] {
		t.Fatalf("omitted snapshot did not revoke bridge: %#v err=%v", updated.Capabilities, err)
	}
	if items, err = store.ListRuntimeAttestations(ctx, device.ID); err != nil || len(items) != 0 {
		t.Fatalf("stale attestations remained: %#v err=%v", items, err)
	}
}

func TestRuntimeAttestationRequirementsAreScopedToDeviceOSAndArchitecture(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	linuxDevice, _, binding, group := createCompatibilityFixture(t, store)
	linuxRequirements, err := store.ListRuntimeAttestationRequirementsForDevice(ctx, linuxDevice)
	if err != nil || len(linuxRequirements) != 2 {
		t.Fatalf("linux requirements=%#v err=%v", linuxRequirements, err)
	}
	profile, err := store.CreateDeviceProfile(ctx, NewDeviceProfile{ID: "fixture-android-profile", Name: "Fixture Android", Target: "android", OSFamily: "android", Architecture: "aarch64"})
	if err != nil {
		t.Fatal(err)
	}
	androidDevice, err := store.CreateDevice(ctx, NewDevice{ID: "fixture-android-device", Name: "Android", DeviceProfileID: profile.ID, OSFamily: "android", Architecture: "arm64"})
	if err != nil {
		t.Fatal(err)
	}
	androidRequirements, err := store.ListRuntimeAttestationRequirementsForDevice(ctx, androidDevice)
	if err != nil || len(androidRequirements) != 0 {
		t.Fatalf("android learned linux requirements=%#v err=%v", androidRequirements, err)
	}
	exactLinux := []RuntimeAttestationReport{
		{Kind: "driver", RuntimeID: binding.DriverID, ContractVersion: 6, SHA256: strings.Repeat("a", 64), Size: 10},
		{Kind: "core", RuntimeID: binding.CoreID, ContractVersion: 3, SHA256: strings.Repeat("b", 64), Size: 20},
	}
	if _, err = store.RecordDeviceHeartbeat(ctx, androidDevice.ID, nil, exactLinux); err == nil || !strings.Contains(err.Error(), "not requested") {
		t.Fatalf("android device accepted linux identities: %v", err)
	}
	if items, listErr := store.ListRuntimeAttestations(ctx, androidDevice.ID); listErr != nil || len(items) != 0 {
		t.Fatalf("rejected cross-platform report changed snapshot: %#v err=%v", items, listErr)
	}
	if group.Members[1].OSFamily != "linux" || group.Members[1].Architecture != "arm64" {
		t.Fatalf("fixture platform drifted: %#v", group.Members[1])
	}
}

func TestRuntimeAttestationReplacementIsAtomicAndCrossDriverNeedsGroup(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	device, _, binding, _ := createCompatibilityFixture(t, store)
	exact := []RuntimeAttestationReport{{Kind: "driver", RuntimeID: binding.DriverID, ContractVersion: 6, SHA256: strings.Repeat("a", 64), Size: 10}, {Kind: "core", RuntimeID: binding.CoreID, ContractVersion: 3, SHA256: strings.Repeat("b", 64), Size: 20}}
	if _, err := store.RecordDeviceHeartbeat(ctx, device.ID, nil, exact); err != nil {
		t.Fatal(err)
	}
	invalid := append([]RuntimeAttestationReport{}, exact...)
	invalid = append(invalid, RuntimeAttestationReport{Kind: "core", RuntimeID: "missing-core", ContractVersion: 1, SHA256: strings.Repeat("d", 64), Size: 1})
	if _, err := store.RecordDeviceHeartbeat(ctx, device.ID, nil, invalid); err == nil {
		t.Fatal("invalid batch unexpectedly replaced attestations")
	}
	items, err := store.ListRuntimeAttestations(ctx, device.ID)
	if err != nil || len(items) != 2 || items[0].SHA256 != strings.Repeat("b", 64) && items[1].SHA256 != strings.Repeat("b", 64) {
		t.Fatalf("failed batch changed prior snapshot: %#v err=%v", items, err)
	}

	game, _ := store.CreateGame(ctx, NewGame{DefaultTitle: "Isolated", Platform: "snes"})
	edition, _ := store.AddEdition(ctx, NewEdition{GameID: game.ID, DefaultTitle: "Isolated", EditionType: "original"})
	stream, err := store.CreateSaveStream(ctx, NewSaveStream{OwnerType: "edition", OwnerKey: edition.ID, DriverID: "fixture-web-snes", EditionIDs: []string{edition.ID}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateSaveBinding(ctx, NewSaveBinding{StreamID: stream.ID, EditionID: edition.ID, DeviceProfileID: device.DeviceProfileID, DriverID: binding.DriverID, CoreID: binding.CoreID, LocalPaths: []string{"save.srm"}})
	if err == nil || !strings.Contains(err.Error(), "compatibility group") {
		t.Fatalf("cross-driver binding without group accepted: %v", err)
	}
}

func TestCreateCrossDriverSaveSetupIsAtomic(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	device, _, binding, group := createCompatibilityFixture(t, store)
	game, err := store.CreateGame(ctx, NewGame{DefaultTitle: "Atomic bridge", Platform: "snes"})
	if err != nil {
		t.Fatal(err)
	}
	edition, err := store.AddEdition(ctx, NewEdition{GameID: game.ID, DefaultTitle: "Original", EditionType: "original"})
	if err != nil {
		t.Fatal(err)
	}
	input := NewSaveSetup{
		Stream: NewSaveStream{
			OwnerType:            "edition",
			OwnerKey:             edition.ID,
			DriverID:             "fixture-web-snes",
			Portability:          "core-dependent",
			CompatibilityGroupID: group.ID,
			EditionIDs:           []string{edition.ID},
			Compatibility:        "verified",
		},
		Binding: NewSaveBinding{
			EditionID:       edition.ID,
			DeviceProfileID: device.DeviceProfileID,
			DriverID:        binding.DriverID,
			CoreID:          binding.CoreID,
			LocalPaths:      []string{"{{device.save_dir}}/{{rom.stem}}.srm"},
		},
	}
	created, err := store.CreateSaveSetup(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if created.Stream.CompatibilityGroupID != group.ID || created.Stream.DriverID == created.Binding.DriverID || created.Binding.CoreID != binding.CoreID {
		t.Fatalf("cross-driver setup lost exact compatibility identity: %#v", created)
	}

	beforeStreams, _ := store.ListSaveStreams(ctx, edition.ID)
	beforeBindings, _ := store.ListSaveBindings(ctx, edition.ID, "")
	input.Stream.ID = "rejected-cross-driver-stream"
	input.Binding.ID = "rejected-cross-driver-binding"
	input.Binding.CoreID = "missing-core"
	if _, err = store.CreateSaveSetup(ctx, input); err == nil {
		t.Fatal("invalid exact compatibility member unexpectedly created a setup")
	}
	afterStreams, _ := store.ListSaveStreams(ctx, edition.ID)
	afterBindings, _ := store.ListSaveBindings(ctx, edition.ID, "")
	if len(afterStreams) != len(beforeStreams) || len(afterBindings) != len(beforeBindings) {
		t.Fatalf("failed cross-driver setup left partial rows: streams %d->%d bindings %d->%d", len(beforeStreams), len(afterStreams), len(beforeBindings), len(afterBindings))
	}
}

func TestDisabledCompatibilityGroupCannotRequestOrAuthorizeRuntime(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	device, _, binding, group := createCompatibilityFixture(t, store)
	if _, err := store.db.ExecContext(ctx, `UPDATE save_compatibility_groups SET enabled=0 WHERE id=?`, group.ID); err != nil {
		t.Fatal(err)
	}
	requirements, err := store.ListRuntimeAttestationRequirements(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(requirements) != 0 {
		t.Fatalf("disabled group still requested runtime files: %#v", requirements)
	}
	_, err = store.RecordDeviceHeartbeat(ctx, device.ID, map[string]bool{"runtime_probe": true}, []RuntimeAttestationReport{
		{Kind: "driver", RuntimeID: binding.DriverID, ContractVersion: 6, SHA256: strings.Repeat("a", 64), Size: 10},
		{Kind: "core", RuntimeID: binding.CoreID, ContractVersion: 3, SHA256: strings.Repeat("b", 64), Size: 20},
	})
	if err == nil || !strings.Contains(err.Error(), "not requested") {
		t.Fatalf("disabled group accepted runtime identities: %v", err)
	}
	items, listErr := store.ListRuntimeAttestations(ctx, device.ID)
	if listErr != nil || len(items) != 0 {
		t.Fatalf("rejected report persisted attestations: %#v err=%v", items, listErr)
	}
	updated, err := store.GetDevice(ctx, device.ID)
	if err != nil {
		t.Fatal(err)
	}
	if authorized, authErr := store.SaveBindingRuntimeAuthorized(ctx, updated, binding); authErr != nil || authorized {
		t.Fatalf("disabled group authorized binding=%v err=%v", authorized, authErr)
	}
}

func TestMigrationFromV21AddsRuntimeAttestationContractWithoutTouchingHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v21.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	game, _ := store.CreateGame(ctx, NewGame{DefaultTitle: "Preserved", Platform: "snes"})
	edition, _ := store.AddEdition(ctx, NewEdition{GameID: game.ID, DefaultTitle: "Preserved", EditionType: "original"})
	stream, _ := store.CreateSaveStream(ctx, NewSaveStream{OwnerType: "edition", OwnerKey: edition.ID, DriverID: "manual", EditionIDs: []string{edition.ID}})
	if _, err = store.db.Exec(`
		DROP TRIGGER IF EXISTS trg_save_streams_compatibility_insert;
		DROP TRIGGER IF EXISTS trg_save_streams_compatibility_update;
		DROP TRIGGER IF EXISTS trg_save_bindings_compatibility_insert;
		DROP TRIGGER IF EXISTS trg_save_bindings_compatibility_update;
		DROP INDEX IF EXISTS idx_save_streams_compatibility_group;
		DROP INDEX IF EXISTS idx_save_compatibility_members_runtime;
		DROP INDEX IF EXISTS idx_runtime_attestations_device;
		ALTER TABLE save_streams DROP COLUMN compatibility_group_id;
		ALTER TABLE save_bindings DROP COLUMN core_id;
		DROP TABLE runtime_attestations;
		DROP TABLE save_compatibility_members;
		DROP TABLE save_compatibility_groups;
		PRAGMA user_version=21;
	`); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	migrated, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	version, _ := migrated.SchemaVersion(ctx)
	if version != CurrentSchemaVersion {
		t.Fatalf("schema=%d", version)
	}
	preserved, err := migrated.GetSaveStream(ctx, stream.ID)
	if err != nil || preserved.DriverID != "manual" || preserved.CompatibilityGroupID != "" {
		t.Fatalf("save history identity not preserved: %#v err=%v", preserved, err)
	}
	for _, table := range []string{"save_compatibility_groups", "save_compatibility_members", "runtime_attestations"} {
		var name string
		if err = migrated.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name); err != nil || name != table {
			t.Fatalf("migration table %s missing: %v", table, err)
		}
	}
	if _, err = migrated.db.Exec(`INSERT INTO runtime_attestations(device_id,kind,runtime_id,contract_version,sha256,size,observed_at) VALUES('missing','core','x',1,?,1,'now')`, strings.Repeat("a", 64)); err == nil || !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Fatalf("runtime attestation foreign key not enforced: %v", err)
	}
}

func TestRuntimeNotAttestedErrorIdentity(t *testing.T) {
	if !errors.Is(ErrSaveRuntimeNotAttested, ErrSaveRuntimeNotAttested) {
		t.Fatal("sentinel error identity changed")
	}
}
