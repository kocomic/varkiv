package deviceagent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"varkiv/internal/catalog"
)

func TestRuntimeProbeUsesOnlyExplicitRootsAndReturnsNoPaths(t *testing.T) {
	root := t.TempDir()
	emulators := filepath.Join(root, "private-emulators")
	cores := filepath.Join(root, "private-cores")
	if err := os.MkdirAll(emulators, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cores, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(emulators, "RetroArch.EXE"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cores, "mgba_libretro.dll"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := Config{RootDir: root, PathOverrides: map[string]string{"emulator_dir": emulators, "core_dir": cores}}
	remote := deviceConfigResponse{
		DeviceProfile: catalog.DeviceProfile{Target: "windows", CaseSensitive: false},
		Drivers:       []catalog.EmulatorDriver{{ID: "retroarch", Name: "RetroArch", Enabled: true, Targets: []string{"windows"}, Launch: catalog.DriverLaunchSpec{Executables: map[string][]string{"windows": {"retroarch.exe"}}}}},
		Cores:         []catalog.RetroArchCore{{ID: "mgba", Name: "mGBA", Enabled: true, LibraryNames: []string{"mgba_libretro"}}},
	}
	result, err := probeRuntime(config, remote)
	if err != nil {
		t.Fatal(err)
	}
	if result.InstalledDrivers != 1 || result.InstalledCores != 1 || result.Drivers[0].Status != "installed" || result.Cores[0].Status != "installed" {
		t.Fatalf("unexpected probe: %#v", result)
	}
	data, _ := json.Marshal(result)
	if strings.Contains(string(data), root) {
		t.Fatalf("probe leaked a host path: %s", data)
	}
}

func TestRuntimeProbeAttestsOnlyRequiredExactFilesWithoutPaths(t *testing.T) {
	root := t.TempDir()
	emulators := filepath.Join(root, "emulators")
	cores := filepath.Join(root, "cores")
	if err := os.MkdirAll(emulators, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cores, 0o700); err != nil {
		t.Fatal(err)
	}
	driverBytes, coreBytes := []byte("exact-retroarch"), []byte("exact-snes9x-core")
	if err := os.WriteFile(filepath.Join(emulators, "retroarch"), driverBytes, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cores, "snes9x_libretro.so"), coreBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	remote := deviceConfigResponse{
		DeviceProfile:                  catalog.DeviceProfile{Target: "fixture-linux", CaseSensitive: true},
		Drivers:                        []catalog.EmulatorDriver{{ID: "retroarch", Name: "RetroArch", ContractVersion: 6, Enabled: true, Targets: []string{"fixture-linux"}, Launch: catalog.DriverLaunchSpec{Executables: map[string][]string{"fixture-linux": {"retroarch"}}}}},
		Cores:                          []catalog.RetroArchCore{{ID: "snes9x", Name: "Snes9x", ContractVersion: 3, Enabled: true, LibraryNames: []string{"snes9x_libretro"}}},
		RuntimeAttestationRequirements: []catalog.RuntimeAttestationRequirement{{Kind: "driver", RuntimeID: "retroarch", ContractVersion: 6}, {Kind: "core", RuntimeID: "snes9x", ContractVersion: 3}},
	}
	result, err := probeRuntime(Config{RootDir: root, PathOverrides: map[string]string{"emulator_dir": emulators, "core_dir": cores}}, remote)
	if err != nil || len(result.Attestations) != 2 {
		t.Fatalf("probe=%#v err=%v", result, err)
	}
	driverDigest, coreDigest := sha256.Sum256(driverBytes), sha256.Sum256(coreBytes)
	want := map[string]string{"driver\x00retroarch": hex.EncodeToString(driverDigest[:]), "core\x00snes9x": hex.EncodeToString(coreDigest[:])}
	for _, item := range result.Attestations {
		if item.SHA256 != want[item.Kind+"\x00"+item.RuntimeID] || item.Size <= 0 {
			t.Fatalf("unexpected attestation: %#v", item)
		}
	}
	encoded, _ := json.Marshal(result.Attestations)
	if strings.Contains(string(encoded), root) || strings.Contains(string(encoded), "retroarch/") || strings.Contains(string(encoded), "cores/") {
		t.Fatalf("attestation leaked a host path: %s", encoded)
	}
	remote.RuntimeAttestationRequirements = nil
	result, err = probeRuntime(Config{RootDir: root, PathOverrides: map[string]string{"emulator_dir": emulators, "core_dir": cores}}, remote)
	if err != nil || len(result.Attestations) != 0 {
		t.Fatalf("unrequired runtimes were hashed: %#v err=%v", result.Attestations, err)
	}
}

func TestRuntimeAttestationRejectsOversizedCandidateBeforeReading(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "oversized-core.so")
	handle, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err = handle.Truncate(maxRuntimeAttestationBytes + 1); err != nil {
		handle.Close()
		t.Fatal(err)
	}
	if err = handle.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = attestRuntimeCandidate(root, "oversized-core.so", "core", "fixture", 1); err == nil || !strings.Contains(err.Error(), "512 MiB") {
		t.Fatalf("oversized runtime accepted: %v", err)
	}
}

func TestRuntimeProbeRejectsSymlinkDirectory(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	link := filepath.Join(root, "link")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := probeRuntime(Config{RootDir: root, PathOverrides: map[string]string{"core_dir": link}}, deviceConfigResponse{DeviceProfile: catalog.DeviceProfile{Target: "windows"}})
	if err == nil {
		t.Fatal("symlink probe root was accepted")
	}
}
