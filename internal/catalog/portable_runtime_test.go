package catalog

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortableRuntimeConflictRejectsWholeImportBatch(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "portable-runtime.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	enabled := true
	if _, err = store.CreateEmulatorDriver(ctx, NewEmulatorDriver{ID: "portable-driver", Name: "Local definition", Family: "retroarch", Platforms: []string{"gba"}, Targets: []string{"rocknix"}, Launch: DriverLaunchSpec{Arguments: []string{"{{rom.path}}"}}, Save: DriverSaveSpec{Scope: "game"}, Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	profile := NewPackageProfile{ID: "portable-profile", Name: "Portable", Frontend: "pegasus", Target: "rocknix", Locale: "en", FileMode: "copy", OutputSlug: "portable-profile", Enabled: &enabled}
	game := ImportedGame{GameID: "portable-game", EditionID: "portable-edition", Platform: "gba", DefaultTitle: "Portable", EditionTitle: "Original", EditionType: "original", Artifacts: []NewArtifact{{Path: "gba/portable.gba", SHA256: "portable-runtime-conflict-rom"}}, RuntimeCatalog: &PortableRuntimeCatalog{EmulatorDrivers: []NewEmulatorDriver{{ID: "portable-driver", Name: "Package definition", Family: "retroarch", Platforms: []string{"gba"}, Targets: []string{"rocknix"}, Launch: DriverLaunchSpec{Arguments: []string{"-L", "{{rom.path}}"}}, Save: DriverSaveSpec{Scope: "game"}, Enabled: &enabled}}, PackageProfile: &profile}}
	err = store.ImportGamesAtomic(ctx, []ImportedGame{game})
	if !errors.Is(err, ErrRuntimeDefinitionConflict) {
		t.Fatalf("conflicting portable runtime error=%v", err)
	}
	if _, err = store.GetGame(ctx, game.GameID, "en"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("game survived rejected atomic import: %v", err)
	}
	if _, err = store.GetPackageProfile(ctx, profile.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("package profile survived rejected atomic import: %v", err)
	}
}

func TestPortableRuntimeDisabledDefinitionIsNeverReenabled(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "portable-runtime-disabled.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	disabled := false
	definition := NewRetroArchCore{ID: "portable-core", Name: "Portable core", LibraryNames: []string{"portable_libretro"}, Platforms: []string{"gba"}, Enabled: &disabled}
	if _, err = store.CreateRetroArchCore(ctx, definition); err != nil {
		t.Fatal(err)
	}
	enabled := true
	definition.Enabled = &enabled
	_, err = store.ValidatePortableRuntimeCatalogImports(ctx, PortableRuntimeCatalog{RetroArchCores: []NewRetroArchCore{definition}})
	if !errors.Is(err, ErrRuntimeDefinitionDisabled) {
		t.Fatalf("disabled local definition error=%v", err)
	}
	current, getErr := store.GetRetroArchCore(ctx, definition.ID)
	if getErr != nil || current.Enabled {
		t.Fatalf("validation changed disabled local definition: %#v err=%v", current, getErr)
	}
}

func TestPortableBuiltinRuntimeSnapshotRequiresMatchingBuiltinOwnership(t *testing.T) {
	ctx := context.Background()
	enabled := true
	definition := NewEmulatorDriver{
		ID: "builtin-driver-portable-fixture", Name: "Portable fixture", Family: "fixture", ContractVersion: 3,
		Platforms: []string{"gba"}, Targets: []string{"windows"}, Launch: DriverLaunchSpec{Arguments: []string{"{{rom.path}}"}},
		Save: DriverSaveSpec{Scope: "game"}, Enabled: &enabled,
	}

	t.Run("missing", func(t *testing.T) {
		store, err := Open(filepath.Join(t.TempDir(), "missing.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		_, err = store.ValidatePortableRuntimeCatalogImports(ctx, PortableRuntimeCatalog{EmulatorDrivers: []NewEmulatorDriver{definition}})
		if !errors.Is(err, ErrRuntimeDefinitionConflict) || !strings.Contains(err.Error(), "unavailable") {
			t.Fatalf("missing built-in snapshot error=%v", err)
		}
	})

	t.Run("custom namespace occupant", func(t *testing.T) {
		store, err := Open(filepath.Join(t.TempDir(), "custom.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		legacy := definition
		legacy.Builtin = true
		custom, err := store.CreateEmulatorDriver(ctx, legacy)
		if err != nil {
			t.Fatal(err)
		}
		// Model a tampered pre-v25 database so the portable validator remains a
		// defense-in-depth boundary even when the storage trigger was absent.
		if _, err = store.db.ExecContext(ctx, `DROP TRIGGER trg_emulator_drivers_builtin_ownership_update`); err != nil {
			t.Fatal(err)
		}
		if _, err = store.db.ExecContext(ctx, `UPDATE emulator_drivers SET builtin=0 WHERE id=?`, custom.ID); err != nil {
			t.Fatal(err)
		}
		_, err = store.ValidatePortableRuntimeCatalogImports(ctx, PortableRuntimeCatalog{EmulatorDrivers: []NewEmulatorDriver{PortableEmulatorDriver(custom)}})
		if !errors.Is(err, ErrRuntimeDefinitionConflict) || !strings.Contains(err.Error(), "does not resolve to a built-in definition") {
			t.Fatalf("custom built-in namespace occupant error=%v", err)
		}
	})

	t.Run("matching built-in", func(t *testing.T) {
		store, err := Open(filepath.Join(t.TempDir(), "builtin.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		definition.Builtin = true
		builtin, err := store.CreateEmulatorDriver(ctx, definition)
		if err != nil {
			t.Fatal(err)
		}
		normalized, err := store.ValidatePortableRuntimeCatalogImports(ctx, PortableRuntimeCatalog{EmulatorDrivers: []NewEmulatorDriver{PortableEmulatorDriver(builtin)}})
		if err != nil || !normalized.ExistingDriver[builtin.ID] {
			t.Fatalf("matching built-in snapshot=%#v err=%v", normalized, err)
		}
	})
}

func TestPortableBuiltinPackageProfileRequiresMatchingBuiltinOwnership(t *testing.T) {
	ctx := context.Background()
	enabled := true
	definition := NewPackageProfile{ID: "builtin-package-portable-fixture", Name: "Portable built-in package", Frontend: "pegasus", Target: "portable", Locale: "en", FileMode: "reference", OutputSlug: "portable-builtin-package", Enabled: &enabled}

	t.Run("missing", func(t *testing.T) {
		store := testStore(t)
		_, err := store.ValidatePortableRuntimeCatalogImports(ctx, PortableRuntimeCatalog{PackageProfile: &definition})
		if !errors.Is(err, ErrRuntimeDefinitionConflict) || !strings.Contains(err.Error(), "unavailable") {
			t.Fatalf("missing built-in package snapshot error=%v", err)
		}
	})

	t.Run("custom namespace occupant", func(t *testing.T) {
		store := testStore(t)
		owned := definition
		owned.Builtin = true
		profile, err := store.CreatePackageProfile(ctx, owned)
		if err != nil {
			t.Fatal(err)
		}
		// Model a tampered pre-v25 database; current schema rejects this write.
		if _, err = store.db.ExecContext(ctx, `DROP TRIGGER trg_package_profiles_builtin_ownership_update`); err != nil {
			t.Fatal(err)
		}
		if _, err = store.db.ExecContext(ctx, `UPDATE package_profiles SET builtin=0 WHERE id=?`, profile.ID); err != nil {
			t.Fatal(err)
		}
		portable := PortablePackageProfile(profile)
		_, err = store.ValidatePortableRuntimeCatalogImports(ctx, PortableRuntimeCatalog{PackageProfile: &portable})
		if !errors.Is(err, ErrRuntimeDefinitionConflict) || !strings.Contains(err.Error(), "does not resolve to a built-in definition") {
			t.Fatalf("custom built-in package namespace occupant error=%v", err)
		}
	})

	t.Run("matching built-in", func(t *testing.T) {
		store := testStore(t)
		owned := definition
		owned.Builtin = true
		profile, err := store.CreatePackageProfile(ctx, owned)
		if err != nil {
			t.Fatal(err)
		}
		portable := PortablePackageProfile(profile)
		normalized, err := store.ValidatePortableRuntimeCatalogImports(ctx, PortableRuntimeCatalog{PackageProfile: &portable})
		if err != nil || !normalized.ExistingProfile {
			t.Fatalf("matching built-in package snapshot=%#v err=%v", normalized, err)
		}
	})
}

func TestPortableRuntimeCustomFrontendHandlerAndProfileSurviveAtomicImport(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "portable-custom-frontend.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	enabled := true
	caseSensitive := true
	frontend := NewFrontendAdapter{ID: "portable-manga-frontend", Name: "Portable manga metadata", Format: "manga-pegasus", Handler: "pegasus", Capabilities: map[string]bool{"export": true}, Enabled: &enabled}
	device := NewDeviceProfile{ID: "portable-manga-device", Name: "Portable manga device", Target: "manga-device", OSFamily: "handheld-linux", PathStyle: "posix", CaseSensitive: &caseSensitive, DefaultFrontendID: frontend.ID, Paths: map[string]string{"rom_dir": "roms"}, Enabled: &enabled}
	profile := NewPackageProfile{ID: "portable-manga-profile", Name: "Portable manga package", Frontend: "pegasus", Target: device.Target, DeviceProfileID: device.ID, FrontendAdapterID: frontend.ID, Locale: "zh-CN", FileMode: "copy", OutputSlug: "portable-manga", Enabled: &enabled}
	game := ImportedGame{GameID: "portable-manga-game", EditionID: "portable-manga-edition", Platform: "gba", DefaultTitle: "Portable manga", EditionTitle: "Original", EditionType: "original", Artifacts: []NewArtifact{{Path: "gba/portable-manga.gba", SHA256: "portable-manga-rom"}}, RuntimeCatalog: &PortableRuntimeCatalog{FrontendAdapters: []NewFrontendAdapter{frontend}, DeviceProfiles: []NewDeviceProfile{device}, PackageProfile: &profile}}
	if err = store.ImportGamesAtomic(ctx, []ImportedGame{game}); err != nil {
		t.Fatal(err)
	}
	gotFrontend, err := store.GetFrontendAdapter(ctx, frontend.ID)
	if err != nil || gotFrontend.Format != frontend.Format || gotFrontend.Handler != "pegasus" {
		t.Fatalf("frontend=%#v err=%v", gotFrontend, err)
	}
	gotDevice, err := store.GetDeviceProfile(ctx, device.ID)
	if err != nil || gotDevice.DefaultFrontendID != frontend.ID {
		t.Fatalf("device=%#v err=%v", gotDevice, err)
	}
	gotProfile, err := store.GetPackageProfile(ctx, profile.ID)
	if err != nil || gotProfile.Frontend != "pegasus" || gotProfile.FrontendAdapterID != frontend.ID || gotProfile.DeviceProfileID != device.ID {
		t.Fatalf("profile=%#v err=%v", gotProfile, err)
	}
}

func TestPortableRuntimeUnboundFrontendRejectsWholeImportBatch(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "portable-unbound-frontend.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	enabled := true
	frontend := NewFrontendAdapter{ID: "portable-unbound-frontend", Name: "Unbound metadata", Format: "unbound-metadata", Enabled: &enabled}
	profile := NewPackageProfile{ID: "portable-unbound-profile", Name: "Unsafe portable package", Frontend: "pegasus", Target: "portable", FrontendAdapterID: frontend.ID, FileMode: "copy", OutputSlug: "portable-unbound", Enabled: &enabled}
	game := ImportedGame{GameID: "portable-unbound-game", EditionID: "portable-unbound-edition", Platform: "gba", DefaultTitle: "Unbound portable", EditionTitle: "Original", EditionType: "original", Artifacts: []NewArtifact{{Path: "gba/unbound.gba", SHA256: "portable-unbound-rom"}}, RuntimeCatalog: &PortableRuntimeCatalog{FrontendAdapters: []NewFrontendAdapter{frontend}, PackageProfile: &profile}}
	if err = store.ImportGamesAtomic(ctx, []ImportedGame{game}); err == nil {
		t.Fatal("expected an unbound portable frontend to reject the whole import")
	}
	if _, err = store.GetGame(ctx, game.GameID, "en"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("game survived rejected atomic import: %v", err)
	}
	if _, err = store.GetFrontendAdapter(ctx, frontend.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("frontend survived rejected atomic import: %v", err)
	}
	if _, err = store.GetPackageProfile(ctx, profile.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("profile survived rejected atomic import: %v", err)
	}
}
