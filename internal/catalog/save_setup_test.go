package catalog

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateSaveSetupIsAtomic(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	enabled := true
	adapter, err := store.CreateFrontendAdapter(ctx, NewFrontendAdapter{ID: "adapter", Name: "Fixture frontend", Format: "fixture", Capabilities: map[string]bool{}, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	device, err := store.CreateDeviceProfile(ctx, NewDeviceProfile{ID: "device", Name: "Fixture device", Target: "windows", OSFamily: "windows", PathStyle: "windows", DefaultFrontendID: adapter.ID, Paths: map[string]string{"save_dir": "saves"}})
	if err != nil {
		t.Fatal(err)
	}
	driver, err := store.CreateEmulatorDriver(ctx, NewEmulatorDriver{ID: "driver", Name: "Fixture RetroArch", Family: "retroarch", Platforms: []string{"gba"}, Targets: []string{"windows"}, Launch: DriverLaunchSpec{RequiresCore: true, Arguments: []string{"{{rom.path}}"}}, Save: DriverSaveSpec{Scope: "game", Layout: "single-file"}})
	if err != nil {
		t.Fatal(err)
	}
	game, err := store.CreateGame(ctx, NewGame{DefaultTitle: "Fixture", Platform: "gba"})
	if err != nil {
		t.Fatal(err)
	}
	edition, err := store.AddEdition(ctx, NewEdition{GameID: game.ID, DefaultTitle: "Fixture", EditionType: "original"})
	if err != nil {
		t.Fatal(err)
	}
	input := NewSaveSetup{
		Stream:  NewSaveStream{OwnerType: "edition", OwnerKey: edition.ID, DriverID: driver.ID, Portability: "core-dependent"},
		Binding: NewSaveBinding{EditionID: edition.ID, DeviceProfileID: device.ID, DriverID: driver.ID, LocalPaths: []string{"{{device.save_dir}}/{{edition.save_namespace}}.srm"}},
	}
	created, err := store.CreateSaveSetup(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if created.Stream.ID == "" || created.Binding.StreamID != created.Stream.ID || len(created.Stream.Editions) != 1 {
		t.Fatalf("incomplete setup: %#v", created)
	}

	beforeStreams, _ := store.ListSaveStreams(ctx, edition.ID)
	beforeBindings, _ := store.ListSaveBindings(ctx, edition.ID, "")
	for _, test := range []struct {
		name string
		path string
		want string
	}{
		{name: "serial", path: "{{device.save_dir}}/{{edition.serial}}/save.bin", want: "edition.serial"},
		{name: "product code", path: "{{driver.user_dir}}/SAVEDATA/{{edition.product_code}}", want: "edition.product_code"},
		{name: "title id", path: "{{driver.user_dir}}/{{edition.title_id}}", want: "edition.title_id"},
		{name: "split title id", path: "{{driver.user_dir}}/{{edition.title_id_high}}/{{edition.title_id_low}}", want: "16-hex edition.title_id"},
	} {
		t.Run("missing identity "+test.name, func(t *testing.T) {
			input.Binding.LocalPaths = []string{test.path}
			if _, setupErr := store.CreateSaveSetup(ctx, input); setupErr == nil || !strings.Contains(setupErr.Error(), test.want) {
				t.Fatalf("missing %s was accepted or returned an unstable error: %v", test.name, setupErr)
			}
			streams, _ := store.ListSaveStreams(ctx, edition.ID)
			bindings, _ := store.ListSaveBindings(ctx, edition.ID, "")
			if len(streams) != len(beforeStreams) || len(bindings) != len(beforeBindings) {
				t.Fatalf("failed identity setup left partial rows: streams %d->%d bindings %d->%d", len(beforeStreams), len(streams), len(beforeBindings), len(bindings))
			}
		})
	}

	unsafeUpdate := NewSaveBinding{StreamID: created.Stream.ID, EditionID: edition.ID, DeviceProfileID: device.ID, DriverID: driver.ID, LocalPaths: []string{"{{driver.user_dir}}/SAVEDATA/{{edition.product_code}}"}}
	if _, err = store.UpdateSaveBinding(ctx, created.Binding.ID, unsafeUpdate); err == nil || !strings.Contains(err.Error(), "edition.product_code") {
		t.Fatalf("binding update accepted a missing identity: %v", err)
	}
	unchanged, err := store.GetSaveBinding(ctx, created.Binding.ID)
	if err != nil || len(unchanged.LocalPaths) != 1 || unchanged.LocalPaths[0] != "{{device.save_dir}}/{{edition.save_namespace}}.srm" {
		t.Fatalf("rejected update changed the binding: %#v %v", unchanged, err)
	}

	if _, err = store.UpdateEdition(ctx, edition.ID, NewEdition{GameID: game.ID, DefaultTitle: edition.DefaultTitle, EditionType: edition.EditionType, TitleID: "GGGGGGGGGGGGGGGG"}); err != nil {
		t.Fatal(err)
	}
	invalidHexUpdate := unsafeUpdate
	invalidHexUpdate.LocalPaths = []string{"{{driver.user_dir}}/{{edition.title_id_high}}{{edition.title_id_low}}"}
	if _, err = store.UpdateSaveBinding(ctx, created.Binding.ID, invalidHexUpdate); err == nil || !strings.Contains(err.Error(), "16-hex edition.title_id") {
		t.Fatalf("binding update accepted a non-hex split title identity: %v", err)
	}
	if _, err = store.UpdateEdition(ctx, edition.ID, NewEdition{GameID: game.ID, DefaultTitle: edition.DefaultTitle, EditionType: edition.EditionType, TitleID: "0100A5B00CBD5000"}); err != nil {
		t.Fatal(err)
	}
	validHexUpdate := invalidHexUpdate
	if updated, updateErr := store.UpdateSaveBinding(ctx, created.Binding.ID, validHexUpdate); updateErr != nil || updated.LocalPaths[0] != validHexUpdate.LocalPaths[0] {
		t.Fatalf("valid split title identity was rejected: %#v %v", updated, updateErr)
	}

	input.Binding.LocalPaths = []string{"{{unknown.path}}/save.srm"}
	if _, err = store.CreateSaveSetup(ctx, input); err == nil {
		t.Fatal("invalid path template unexpectedly created a setup")
	}
	afterStreams, _ := store.ListSaveStreams(ctx, edition.ID)
	afterBindings, _ := store.ListSaveBindings(ctx, edition.ID, "")
	if len(afterStreams) != len(beforeStreams) || len(afterBindings) != len(beforeBindings) {
		t.Fatalf("failed setup left partial rows: streams %d->%d bindings %d->%d", len(beforeStreams), len(afterStreams), len(beforeBindings), len(afterBindings))
	}
}
