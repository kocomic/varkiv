package bundler

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"varkiv/internal/catalog"
)

func TestValidateDeviceTargetPathWindowsRules(t *testing.T) {
	device := catalog.DeviceProfile{
		PathStyle:         "windows",
		MaxPath:           64,
		IllegalCharacters: `<>:"/\|?*`,
	}
	tests := []struct {
		path string
		want string
	}{
		{path: "roms/GBA/game.gba"},
		{path: "roms/CON.gba", want: "reserved device name"},
		{path: "roms/bad?.gba", want: "device-illegal"},
		{path: "roms/trailing./game.gba", want: "space or period"},
		{path: "roms/" + strings.Repeat("a", 64) + ".gba", want: "exceeds device maximum"},
	}
	for _, test := range tests {
		err := validateDeviceTargetPath(test.path, device)
		if test.want == "" && err != nil {
			t.Fatalf("valid path %q rejected: %v", test.path, err)
		}
		if test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
			t.Fatalf("path %q error=%v, want %q", test.path, err, test.want)
		}
	}
}

func TestPlanPreflightsDeviceCapabilitiesCaseCollisionsAndSpace(t *testing.T) {
	store, err := catalog.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	caseSensitive := false
	device, err := store.CreateDeviceProfile(ctx, catalog.NewDeviceProfile{
		ID:                "windows-no-links",
		Name:              "Windows FAT target",
		Target:            "windows",
		OSFamily:          "windows",
		PathStyle:         "windows",
		CaseSensitive:     &caseSensitive,
		MaxPath:           260,
		IllegalCharacters: `<>:"/\|?*`,
		SupportsHardlink:  false,
	})
	if err != nil {
		t.Fatal(err)
	}
	profile := Profile{
		Name:            "preflight",
		Frontend:        "pegasus",
		Target:          "windows",
		DeviceProfileID: device.ID,
		FileMode:        "hardlink",
		Templates: []ConfigTemplate{
			{Name: "Upper", Scope: "package", OutputPath: "Config/options.ini", Body: "value=1\n"},
			{Name: "Lower", Scope: "package", OutputPath: "config/options.ini", Body: "value=2\n"},
		},
	}
	root := t.TempDir()
	out := filepath.Join(t.TempDir(), "package")
	plan, err := PlanWithStorage(ctx, store, root, root, root, out, profile)
	if err != nil {
		t.Fatal(err)
	}
	wanted := map[string]bool{
		"file-mode:hardlink": false,
		"Config/options.ini": false,
		"config/options.ini": false,
	}
	for _, conflict := range plan.Conflicts {
		if _, ok := wanted[conflict]; ok {
			wanted[conflict] = true
		}
	}
	for conflict, found := range wanted {
		if !found {
			t.Errorf("missing conflict %q in %#v", conflict, plan.Conflicts)
		}
	}
	if !plan.SpaceChecked || plan.EstimatedWriteBytes < packageSpaceReserveBytes || plan.AvailableBytes <= 0 {
		t.Fatalf("space preflight missing: %#v", plan)
	}
}
