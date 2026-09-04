package catalog

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func customPlatformFixture() NewCustomPlatform {
	enabled := true
	return NewCustomPlatform{
		ID: "fixture-handheld", Name: "Fixture Handheld", NameZH: "测试掌机", Vendor: "Community", Category: "handheld",
		Aliases: []string{"fixture-hh"}, Extensions: []string{".opk", "directory"}, ESDESystems: []string{"fixture-handheld-es"},
		BIOS: "optional", Runtime: "native", SuggestedEmulators: map[string][]string{"handheld_linux": {"OpenEmu Runner"}}, Enabled: &enabled,
	}
}

func TestCustomPlatformCRUDRegistryAndReferenceSafety(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	created, err := store.CreateCustomPlatform(ctx, customPlatformFixture())
	if err != nil || created.ID != "fixture-handheld" || created.Builtin || !created.Enabled || created.SuggestedEmulators["handheld_linux"][0] != "OpenEmu Runner" {
		t.Fatalf("created=%#v err=%v", created, err)
	}
	registry, err := store.PlatformRegistry(ctx)
	if err != nil {
		t.Fatal(err)
	}
	resolved, ok := registry.Resolve("fixture-handheld-es")
	if !ok || resolved.ID != "fixture-handheld" || len(resolved.Extensions) != 2 {
		t.Fatalf("resolved=%#v ok=%v", resolved, ok)
	}

	conflict := customPlatformFixture()
	conflict.ID, conflict.Name, conflict.Aliases = "other-hand", "Other Hand", []string{"gba"}
	if _, err = store.CreateCustomPlatform(ctx, conflict); err == nil {
		t.Fatal("custom alias collision with a builtin platform was accepted")
	}
	if _, err = store.CreateGame(ctx, NewGame{DefaultTitle: "Custom Game", Platform: created.ID}); err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteCustomPlatform(ctx, created.ID); !errors.Is(err, ErrCustomPlatformInUse) {
		t.Fatalf("delete referenced platform err=%v", err)
	}

	disabled := customPlatformFixture()
	disabled.Enabled = boolPointerForTest(false)
	updated, err := store.UpdateCustomPlatform(ctx, created.ID, disabled)
	if err != nil || updated.Enabled {
		t.Fatalf("updated=%#v err=%v", updated, err)
	}
	registry, err = store.PlatformRegistry(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok = registry.Resolve("fixture-handheld"); ok {
		t.Fatal("disabled custom platform remains active in registry")
	}
	items, err := store.ListCustomPlatforms(ctx, true)
	if err != nil || len(items) != 1 || items[0].Enabled {
		t.Fatalf("items=%#v err=%v", items, err)
	}
}

func TestMigrationV16ToV17AddsCustomPlatformsWithoutChangingGames(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v16.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	game, err := store.CreateGame(ctx, NewGame{DefaultTitle: "Preserved", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DROP TABLE custom_platforms; PRAGMA user_version=16`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	if version, versionErr := migrated.SchemaVersion(ctx); versionErr != nil || version != CurrentSchemaVersion {
		t.Fatalf("version=%d err=%v", version, versionErr)
	}
	if preserved, getErr := migrated.GetGame(ctx, game.ID, ""); getErr != nil || preserved.DefaultTitle != "Preserved" {
		t.Fatalf("preserved=%#v err=%v", preserved, getErr)
	}
	if _, err = migrated.CreateCustomPlatform(ctx, customPlatformFixture()); err != nil {
		t.Fatalf("v17 custom platform table is not writable: %v", err)
	}
}

func TestPortableCustomPlatformValidationNeverOverwritesLocalDefinition(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	created, err := store.CreateCustomPlatform(ctx, customPlatformFixture())
	if err != nil {
		t.Fatal(err)
	}
	portable := created.PortableDefinition()
	if items, validateErr := store.ValidateCustomPlatformImports(ctx, []NewCustomPlatform{portable}); validateErr != nil || len(items) != 1 {
		t.Fatalf("compatible definition=%#v err=%v", items, validateErr)
	}
	conflict := portable
	conflict.Extensions = []string{".different"}
	if _, err = store.ValidateCustomPlatformImports(ctx, []NewCustomPlatform{conflict}); !errors.Is(err, ErrPlatformDefinitionConflict) {
		t.Fatalf("conflicting definition err=%v", err)
	}

	disabled := portable
	disabled.Enabled = boolPointerForTest(false)
	if _, err = store.UpdateCustomPlatform(ctx, created.ID, disabled); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ValidateCustomPlatformImports(ctx, []NewCustomPlatform{portable}); !errors.Is(err, ErrPlatformDefinitionDisabled) {
		t.Fatalf("disabled definition err=%v", err)
	}

	aliasConflict := customPlatformFixture()
	aliasConflict.ID, aliasConflict.Name, aliasConflict.Aliases, aliasConflict.Enabled = "portable-other", "Portable Other", []string{"gba"}, nil
	if _, err = store.ValidateCustomPlatformImports(ctx, []NewCustomPlatform{aliasConflict}); err == nil {
		t.Fatal("portable definition shadowed a built-in registry key")
	}
	unchanged, err := store.GetCustomPlatform(ctx, created.ID)
	if err != nil || unchanged.Extensions[0] != ".opk" || unchanged.Enabled {
		t.Fatalf("local definition was overwritten: %#v err=%v", unchanged, err)
	}
}

func boolPointerForTest(value bool) *bool { return &value }
