package deviceagent

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeARM64ELF(t *testing.T, path string) {
	t.Helper()
	header := make([]byte, 64)
	copy(header[:4], []byte("\x7fELF"))
	header[4] = 2
	header[5] = 1
	binary.LittleEndian.PutUint16(header[16:18], 2)
	binary.LittleEndian.PutUint16(header[18:20], 183)
	if err := os.WriteFile(path, header, 0o700); err != nil {
		t.Fatal(err)
	}
}

func writeARM32ELF(t *testing.T, path string) {
	t.Helper()
	header := make([]byte, 52)
	copy(header[:4], []byte("\x7fELF"))
	header[4] = 1
	header[5] = 1
	binary.LittleEndian.PutUint16(header[16:18], 2)
	binary.LittleEndian.PutUint16(header[18:20], 40)
	if err := os.WriteFile(path, header, 0o700); err != nil {
		t.Fatal(err)
	}
}

func writeAMD64ELF(t *testing.T, path string) {
	t.Helper()
	header := make([]byte, 64)
	copy(header[:4], []byte("\x7fELF"))
	header[4] = 2
	header[5] = 1
	binary.LittleEndian.PutUint16(header[16:18], 2)
	binary.LittleEndian.PutUint16(header[18:20], 62)
	if err := os.WriteFile(path, header, 0o700); err != nil {
		t.Fatal(err)
	}
}

func writePE(t *testing.T, path string, machine uint16) {
	t.Helper()
	header := make([]byte, 0x88)
	copy(header[:2], []byte("MZ"))
	binary.LittleEndian.PutUint32(header[0x3c:0x40], 0x80)
	copy(header[0x80:0x84], []byte("PE\x00\x00"))
	binary.LittleEndian.PutUint16(header[0x84:0x86], machine)
	if err := os.WriteFile(path, header, 0o700); err != nil {
		t.Fatal(err)
	}
}

func writeTargetConfig(t *testing.T, path, target string) {
	t.Helper()
	config := Config{
		ServerURL:       "https://library.example.test",
		DeviceID:        "device-test",
		AccessToken:     "private-target-token",
		DeviceProfileID: "rocknix-handheld",
		DeviceTarget:    target,
		RootDir:         t.TempDir(),
	}
	if err := SaveConfig(path, config); err != nil {
		t.Fatal(err)
	}
}

func TestBuildROCKNIXTargetPackage(t *testing.T) {
	root := t.TempDir()
	binaryPath := filepath.Join(root, "varkiv-linux-arm64")
	configPath := filepath.Join(root, "agent.json")
	output := filepath.Join(root, "rocknix-private")
	writeARM64ELF(t, binaryPath)
	writeTargetConfig(t, configPath, "rocknix")

	result, err := BuildTargetPackage(TargetPackageInput{Kind: "rocknix", BinaryPath: binaryPath, ConfigPath: configPath, OutputPath: output})
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != "rocknix" || !result.Sensitive || len(result.Files) != 8 {
		t.Fatalf("unexpected result: %#v", result)
	}
	expectedModes := map[string]os.FileMode{
		"storage/.config/varkiv/varkiv":                          0o700,
		"storage/.config/varkiv/agent.json":                      0o600,
		"storage/.config/modules/Varkiv Sync Now.sh":             0o700,
		"storage/.config/modules/Varkiv Start Automatic Sync.sh": 0o700,
		"storage/.config/modules/Varkiv Stop Automatic Sync.sh":  0o700,
		"README.txt":                  0o600,
		"HARDWARE-ACCEPTANCE.txt":     0o600,
		"varkiv-target-manifest.json": 0o600,
	}
	for relative, mode := range expectedModes {
		info, statErr := os.Stat(filepath.Join(output, filepath.FromSlash(relative)))
		if statErr != nil {
			t.Fatalf("%s: %v", relative, statErr)
		}
		if info.Mode().Perm() != mode {
			t.Fatalf("%s mode = %04o, want %04o", relative, info.Mode().Perm(), mode)
		}
	}
	configBytes, err := os.ReadFile(filepath.Join(output, "storage/.config/varkiv/agent.json"))
	if err != nil || !strings.Contains(string(configBytes), "private-target-token") {
		t.Fatalf("private config was not copied intact: %v", err)
	}
	syncNow, _ := os.ReadFile(filepath.Join(output, "storage/.config/modules/Varkiv Sync Now.sh"))
	if strings.Contains(string(syncNow), "eval") || !strings.Contains(string(syncNow), "agent sync --config") {
		t.Fatalf("unsafe or incomplete sync command: %s", syncNow)
	}
	start, _ := os.ReadFile(filepath.Join(output, "storage/.config/modules/Varkiv Start Automatic Sync.sh"))
	if strings.Contains(string(start), "eval") || !strings.Contains(string(start), "agent run") || !strings.Contains(string(start), "nohup") {
		t.Fatalf("unsafe or incomplete background command: %s", start)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(output, "varkiv-target-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		FormatVersion int                  `json:"format_version"`
		Sensitive     bool                 `json:"sensitive"`
		Files         []targetManifestFile `json:"files"`
	}
	if err = json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.FormatVersion != 1 || !manifest.Sensitive || len(manifest.Files) != 7 {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
	if strings.Contains(string(manifestBytes), "private-target-token") || strings.Contains(string(manifestBytes), root) {
		t.Fatal("manifest leaked a token or host path")
	}
	guide, err := os.ReadFile(filepath.Join(output, "HARDWARE-ACCEPTANCE.txt"))
	if err != nil || !strings.Contains(string(guide), "agent acceptance") || !strings.Contains(string(guide), "requires maintainer review") || strings.Contains(string(guide), "private-target-token") || strings.Contains(string(guide), root) {
		t.Fatalf("hardware acceptance guide privacy: %v %s", err, guide)
	}
}

func TestBuildKNULLITargetPackage(t *testing.T) {
	root := t.TempDir()
	binaryPath := filepath.Join(root, "varkiv-linux-arm64")
	configPath := filepath.Join(root, "agent.json")
	output := filepath.Join(root, "knulli-private")
	writeARM64ELF(t, binaryPath)
	writeTargetConfig(t, configPath, "knulli")
	result, err := BuildTargetPackage(TargetPackageInput{Kind: "knulli", BinaryPath: binaryPath, ConfigPath: configPath, OutputPath: output})
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != "knulli" || !result.Sensitive || len(result.Files) != 7 {
		t.Fatalf("unexpected result: %#v", result)
	}
	service, err := os.ReadFile(filepath.Join(output, "userdata/system/services/varkiv"))
	if err != nil {
		t.Fatal(err)
	}
	hook, err := os.ReadFile(filepath.Join(output, "userdata/system/scripts/varkiv-sync.sh"))
	if err != nil {
		t.Fatal(err)
	}
	combined := string(service) + string(hook)
	if strings.Contains(combined, "eval") || !strings.Contains(string(service), "start|stop|status") || !strings.Contains(string(hook), "gameStop") {
		t.Fatalf("unsafe or incomplete KNULLI scripts: %s", combined)
	}
	if strings.Contains(combined, "$2") || strings.Contains(combined, "$3") || strings.Contains(combined, "$4") || strings.Contains(combined, "$5") {
		t.Fatal("KNULLI hook should not read frontend ROM or emulator arguments")
	}
	manifest, err := os.ReadFile(filepath.Join(output, "varkiv-target-manifest.json"))
	if err != nil || !strings.Contains(string(manifest), `"kind": "knulli"`) {
		t.Fatalf("KNULLI manifest: %v %s", err, manifest)
	}
}

func TestBuildArkOSTargetPackage(t *testing.T) {
	root := t.TempDir()
	binaryPath := filepath.Join(root, "varkiv-linux-arm64")
	configPath := filepath.Join(root, "agent.json")
	output := filepath.Join(root, "arkos-private")
	writeARM64ELF(t, binaryPath)
	writeTargetConfig(t, configPath, "arkos")
	result, err := BuildTargetPackage(TargetPackageInput{Kind: "arkos", BinaryPath: binaryPath, ConfigPath: configPath, OutputPath: output})
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != "arkos" || !result.Sensitive || len(result.Files) != 8 {
		t.Fatalf("unexpected result: %#v", result)
	}
	for _, relative := range []string{
		"roms/tools/.varkiv/varkiv",
		"roms/tools/.varkiv/agent.json",
		"roms/tools/Varkiv Sync Now.sh",
		"roms/tools/Varkiv Start Automatic Sync.sh",
		"roms/tools/Varkiv Stop Automatic Sync.sh",
		"README.txt",
		"HARDWARE-ACCEPTANCE.txt",
		"varkiv-target-manifest.json",
	} {
		if _, err = os.Stat(filepath.Join(output, filepath.FromSlash(relative))); err != nil {
			t.Fatalf("%s: %v", relative, err)
		}
	}
	syncNow, _ := os.ReadFile(filepath.Join(output, "roms/tools/Varkiv Sync Now.sh"))
	start, _ := os.ReadFile(filepath.Join(output, "roms/tools/Varkiv Start Automatic Sync.sh"))
	stop, _ := os.ReadFile(filepath.Join(output, "roms/tools/Varkiv Stop Automatic Sync.sh"))
	combined := string(syncNow) + string(start) + string(stop)
	if strings.Contains(combined, "eval") || !strings.Contains(string(syncNow), "/roms/tools/.varkiv/varkiv agent sync --config") || !strings.Contains(string(start), "agent run") {
		t.Fatalf("unsafe or incomplete ArkOS scripts: %s", combined)
	}
	readme, _ := os.ReadFile(filepath.Join(output, "README.txt"))
	if !strings.Contains(string(readme), "only adds files under /roms/tools") || !strings.Contains(string(readme), "does not modify the operating-system partition") || !strings.Contains(string(readme), "retired upstream on 2025-12-30") {
		t.Fatalf("ArkOS safety boundary missing: %s", readme)
	}
	manifest, err := os.ReadFile(filepath.Join(output, "varkiv-target-manifest.json"))
	if err != nil || !strings.Contains(string(manifest), `"kind": "arkos"`) || strings.Contains(string(manifest), "private-target-token") || strings.Contains(string(manifest), root) {
		t.Fatalf("ArkOS manifest privacy: %v %s", err, manifest)
	}
}

func TestBuildDArkOSTargetPackage(t *testing.T) {
	root := t.TempDir()
	binaryPath := filepath.Join(root, "varkiv-linux-arm64")
	configPath := filepath.Join(root, "agent.json")
	output := filepath.Join(root, "darkos-private")
	writeARM64ELF(t, binaryPath)
	config := Config{ServerURL: "https://library.example.test", DeviceID: "device-test", AccessToken: "private-target-token", DeviceProfileID: "builtin-device-darkos", DeviceTarget: "darkos", RootDir: "/roms"}
	if err := SaveConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	result, err := BuildTargetPackage(TargetPackageInput{Kind: "darkos", BinaryPath: binaryPath, ConfigPath: configPath, OutputPath: output})
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != "darkos" || !result.Sensitive || len(result.Files) != 8 {
		t.Fatalf("unexpected result: %#v", result)
	}
	for _, relative := range []string{
		"roms/tools/.varkiv/varkiv",
		"roms/tools/.varkiv/agent.json",
		"roms/tools/Varkiv Sync Now.sh",
		"roms/tools/Varkiv Start Automatic Sync.sh",
		"roms/tools/Varkiv Stop Automatic Sync.sh",
		"README.txt",
		"HARDWARE-ACCEPTANCE.txt",
		"varkiv-target-manifest.json",
	} {
		if _, err = os.Stat(filepath.Join(output, filepath.FromSlash(relative))); err != nil {
			t.Fatalf("%s: %v", relative, err)
		}
	}
	readme, _ := os.ReadFile(filepath.Join(output, "README.txt"))
	if !strings.Contains(string(readme), "Varkiv dArkOS private device package") || strings.Contains(string(readme), "retired upstream") {
		t.Fatalf("dArkOS branding or lifecycle guidance is wrong: %s", readme)
	}
	manifest, _ := os.ReadFile(filepath.Join(output, "varkiv-target-manifest.json"))
	if !strings.Contains(string(manifest), `"kind": "darkos"`) || strings.Contains(string(manifest), "private-target-token") || strings.Contains(string(manifest), root) {
		t.Fatalf("dArkOS manifest privacy: %s", manifest)
	}
}

func buildVerificationTargetPackage(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	binaryPath := filepath.Join(root, "varkiv-linux-arm64")
	configPath := filepath.Join(root, "agent.json")
	output := filepath.Join(root, "darkos-private")
	writeARM64ELF(t, binaryPath)
	config := Config{ServerURL: "https://library.example.test", DeviceID: "device-test", AccessToken: "private-target-token", DeviceProfileID: "builtin-device-darkos", DeviceTarget: "darkos", RootDir: "/roms"}
	if err := SaveConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildTargetPackage(TargetPackageInput{Kind: "darkos", BinaryPath: binaryPath, ConfigPath: configPath, OutputPath: output}); err != nil {
		t.Fatal(err)
	}
	return output
}

func TestVerifyTargetPackageStrictReadOnlyContract(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		output := buildVerificationTargetPackage(t)
		result, err := VerifyTargetPackage(output)
		if err != nil || !result.Verified || result.Kind != "darkos" || result.Files != 8 || result.Missing != 0 || result.Changed != 0 || result.Unsafe != 0 || result.Extra != 0 {
			t.Fatalf("valid verification = %#v, %v", result, err)
		}
	})
	t.Run("changed", func(t *testing.T) {
		output := buildVerificationTargetPackage(t)
		path := filepath.Join(output, "roms/tools/Varkiv Sync Now.sh")
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 9\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		result, err := VerifyTargetPackage(output)
		if err != nil || result.Verified || result.Changed != 1 {
			t.Fatalf("changed verification = %#v, %v", result, err)
		}
	})
	t.Run("missing", func(t *testing.T) {
		output := buildVerificationTargetPackage(t)
		if err := os.Remove(filepath.Join(output, "README.txt")); err != nil {
			t.Fatal(err)
		}
		result, err := VerifyTargetPackage(output)
		if err != nil || result.Verified || result.Missing != 1 {
			t.Fatalf("missing verification = %#v, %v", result, err)
		}
	})
	t.Run("extra", func(t *testing.T) {
		output := buildVerificationTargetPackage(t)
		if err := os.WriteFile(filepath.Join(output, "unexpected.private"), []byte("not part of the package"), 0o600); err != nil {
			t.Fatal(err)
		}
		result, err := VerifyTargetPackage(output)
		if err != nil || result.Verified || result.Extra != 1 {
			t.Fatalf("extra verification = %#v, %v", result, err)
		}
	})
	t.Run("unsafe-link", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symbolic-link creation is not reliably available")
		}
		output := buildVerificationTargetPackage(t)
		path := filepath.Join(output, "README.txt")
		target := filepath.Join(t.TempDir(), "outside.txt")
		if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		result, err := VerifyTargetPackage(output)
		if err != nil || result.Verified || result.Unsafe == 0 {
			t.Fatalf("unsafe verification = %#v, %v", result, err)
		}
	})
	t.Run("mode", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Windows does not preserve POSIX package modes")
		}
		output := buildVerificationTargetPackage(t)
		if err := os.Chmod(filepath.Join(output, "README.txt"), 0o644); err != nil {
			t.Fatal(err)
		}
		result, err := VerifyTargetPackage(output)
		if err != nil || result.Verified || result.ModeMismatch != 1 || !result.ModeChecksRun {
			t.Fatalf("mode verification = %#v, %v", result, err)
		}
	})
	t.Run("manifest-path", func(t *testing.T) {
		output := buildVerificationTargetPackage(t)
		manifestPath := filepath.Join(output, "varkiv-target-manifest.json")
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			t.Fatal(err)
		}
		var manifest targetManifest
		if err = json.Unmarshal(data, &manifest); err != nil {
			t.Fatal(err)
		}
		manifest.Files[0].Path = "../outside"
		data, err = json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(manifestPath, append(data, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		result, err := VerifyTargetPackage(output)
		if err != nil || result.Verified || result.Unsafe == 0 {
			t.Fatalf("unsafe manifest path = %#v, %v", result, err)
		}
	})
}

func TestBuildMuOSTargetPackage(t *testing.T) {
	root := t.TempDir()
	binaryPath := filepath.Join(root, "varkiv-linux-arm64")
	configPath := filepath.Join(root, "agent.json")
	output := filepath.Join(root, "muos-private")
	writeARM64ELF(t, binaryPath)
	writeTargetConfig(t, configPath, "muos")
	result, err := BuildTargetPackage(TargetPackageInput{Kind: "muos", BinaryPath: binaryPath, ConfigPath: configPath, OutputPath: output})
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != "muos" || !result.Sensitive || len(result.Files) != 8 {
		t.Fatalf("unexpected result: %#v", result)
	}
	for _, relative := range []string{
		"application/Varkiv/varkiv",
		"application/Varkiv/agent.json",
		"application/Varkiv/mux_launch.sh",
		"application/Varkiv Start Automatic Sync/mux_launch.sh",
		"application/Varkiv Stop Automatic Sync/mux_launch.sh",
		"README.txt",
		"HARDWARE-ACCEPTANCE.txt",
		"varkiv-target-manifest.json",
	} {
		if _, err = os.Stat(filepath.Join(output, filepath.FromSlash(relative))); err != nil {
			t.Fatalf("%s: %v", relative, err)
		}
	}
	combined := []byte{}
	for _, relative := range []string{"application/Varkiv/mux_launch.sh", "application/Varkiv Start Automatic Sync/mux_launch.sh", "application/Varkiv Stop Automatic Sync/mux_launch.sh"} {
		content, readErr := os.ReadFile(filepath.Join(output, filepath.FromSlash(relative)))
		if readErr != nil {
			t.Fatal(readErr)
		}
		combined = append(combined, content...)
	}
	if strings.Contains(string(combined), "eval") || !strings.Contains(string(combined), "/run/muos/storage/application/Varkiv") || !strings.Contains(string(combined), "agent run") {
		t.Fatalf("unsafe or incomplete muOS scripts: %s", combined)
	}
	readme, _ := os.ReadFile(filepath.Join(output, "README.txt"))
	if !strings.Contains(string(readme), "No boot script is installed") || !strings.Contains(string(readme), "never writes /opt/muos") {
		t.Fatalf("muOS safety boundary missing: %s", readme)
	}
	manifest, _ := os.ReadFile(filepath.Join(output, "varkiv-target-manifest.json"))
	if strings.Contains(string(manifest), "private-target-token") || strings.Contains(string(manifest), root) {
		t.Fatal("muOS manifest leaked a token or host path")
	}
}

func TestBuildOnionOSTargetPackageRequiresARM32(t *testing.T) {
	root := t.TempDir()
	arm32 := filepath.Join(root, "varkiv-linux-armv7")
	arm64 := filepath.Join(root, "varkiv-linux-arm64")
	configPath := filepath.Join(root, "agent.json")
	output := filepath.Join(root, "onionos-private")
	writeARM32ELF(t, arm32)
	writeARM64ELF(t, arm64)
	writeTargetConfig(t, configPath, "onionos")
	if _, err := BuildTargetPackage(TargetPackageInput{Kind: "onionos", BinaryPath: arm64, ConfigPath: configPath, OutputPath: filepath.Join(root, "wrong")}); err == nil || !strings.Contains(err.Error(), "32-bit") {
		t.Fatalf("OnionOS accepted the wrong agent architecture: %v", err)
	}
	result, err := BuildTargetPackage(TargetPackageInput{Kind: "onionos", BinaryPath: arm32, ConfigPath: configPath, OutputPath: output})
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != "onionos" || !result.Sensitive || len(result.Files) != 11 {
		t.Fatalf("unexpected result: %#v", result)
	}
	for _, relative := range []string{
		"App/Varkiv/varkiv",
		"App/Varkiv/agent.json",
		"App/Varkiv/launch.sh",
		"App/Varkiv/config.json",
		"App/Varkiv Start Automatic Sync/launch.sh",
		"App/Varkiv Start Automatic Sync/config.json",
		"App/Varkiv Stop Automatic Sync/launch.sh",
		"App/Varkiv Stop Automatic Sync/config.json",
		"README.txt",
		"HARDWARE-ACCEPTANCE.txt",
		"varkiv-target-manifest.json",
	} {
		if _, err = os.Stat(filepath.Join(output, filepath.FromSlash(relative))); err != nil {
			t.Fatalf("%s: %v", relative, err)
		}
	}
	config, _ := os.ReadFile(filepath.Join(output, "App/Varkiv/config.json"))
	launch, _ := os.ReadFile(filepath.Join(output, "App/Varkiv/launch.sh"))
	if !strings.Contains(string(config), `"launch": "launch.sh"`) || !strings.Contains(string(launch), "/mnt/SDCARD/App/Varkiv/varkiv agent sync --config") || strings.Contains(string(launch), "eval") {
		t.Fatalf("invalid OnionOS app contract: %s %s", config, launch)
	}
	readme, _ := os.ReadFile(filepath.Join(output, "README.txt"))
	if !strings.Contains(string(readme), "No boot script or network service is installed") || !strings.Contains(string(readme), "does not modify Onion's .tmp_update") {
		t.Fatalf("OnionOS safety boundary missing: %s", readme)
	}
	manifest, _ := os.ReadFile(filepath.Join(output, "varkiv-target-manifest.json"))
	if strings.Contains(string(manifest), "private-target-token") || strings.Contains(string(manifest), root) {
		t.Fatal("OnionOS manifest leaked a token or host path")
	}
}

func TestBuildSteamOSTargetPackage(t *testing.T) {
	root := t.TempDir()
	binaryPath := filepath.Join(root, "varkiv-linux-amd64")
	wrongBinary := filepath.Join(root, "varkiv-linux-arm64")
	configPath := filepath.Join(root, "agent.json")
	output := filepath.Join(root, "steamos-private")
	writeAMD64ELF(t, binaryPath)
	writeARM64ELF(t, wrongBinary)
	writeTargetConfig(t, configPath, "steamos-bazzite")

	if _, err := BuildTargetPackage(TargetPackageInput{Kind: "steamos-bazzite", BinaryPath: wrongBinary, ConfigPath: configPath, OutputPath: filepath.Join(root, "wrong")}); err == nil || !strings.Contains(err.Error(), "x86-64") {
		t.Fatalf("expected x86-64 rejection, got %v", err)
	}
	result, err := BuildTargetPackage(TargetPackageInput{Kind: "steamos-bazzite", BinaryPath: binaryPath, ConfigPath: configPath, OutputPath: output})
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != "steamos-bazzite" || !result.Sensitive || len(result.Files) != 6 {
		t.Fatalf("unexpected result: %#v", result)
	}
	service, err := os.ReadFile(filepath.Join(output, "home/.config/systemd/user/varkiv-agent.service"))
	if err != nil {
		t.Fatal(err)
	}
	serviceText := string(service)
	if strings.Contains(serviceText, "/bin/sh") || strings.Contains(serviceText, "sudo") || strings.Contains(serviceText, "eval") || !strings.Contains(serviceText, "ExecStart=%h/.local/share/varkiv/varkiv agent run") || !strings.Contains(serviceText, "NoNewPrivileges=true") {
		t.Fatalf("unsafe or incomplete user unit: %s", serviceText)
	}
	readme, err := os.ReadFile(filepath.Join(output, "README.txt"))
	if err != nil || !strings.Contains(string(readme), "does not run those commands") || !strings.Contains(string(readme), "Package-tested only") {
		t.Fatalf("missing SteamOS safety guidance: %v %s", err, readme)
	}
	manifest, err := os.ReadFile(filepath.Join(output, "varkiv-target-manifest.json"))
	if err != nil || strings.Contains(string(manifest), "private-target-token") || strings.Contains(string(manifest), root) {
		t.Fatalf("manifest leaked private content or host path: %v %s", err, manifest)
	}
	for relative, mode := range map[string]os.FileMode{
		"home/.local/share/varkiv/varkiv":                0o700,
		"home/.config/varkiv/agent.json":                 0o600,
		"home/.config/systemd/user/varkiv-agent.service": 0o600,
		"README.txt":                  0o600,
		"HARDWARE-ACCEPTANCE.txt":     0o600,
		"varkiv-target-manifest.json": 0o600,
	} {
		info, statErr := os.Stat(filepath.Join(output, filepath.FromSlash(relative)))
		if statErr != nil || info.Mode().Perm() != mode {
			t.Fatalf("%s mode: %v %#o", relative, statErr, info.Mode().Perm())
		}
	}
}

func TestBuildWindowsHandheldTargetPackage(t *testing.T) {
	root := t.TempDir()
	binaryPath := filepath.Join(root, "varkiv-windows-amd64.exe")
	wrongBinary := filepath.Join(root, "varkiv-windows-arm64.exe")
	configPath := filepath.Join(root, "agent.json")
	output := filepath.Join(root, "windows-private")
	writePE(t, binaryPath, 0x8664)
	writePE(t, wrongBinary, 0xaa64)
	config := Config{ServerURL: "https://library.example.test", DeviceID: "device-test", AccessToken: "private-target-token", DeviceProfileID: "builtin-device-windows-handheld", DeviceTarget: "windows", RootDir: `C:\Users\Player\VarkivData`}
	if err := SaveConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	input := TargetPackageInput{Kind: "windows-handheld", BinaryPath: binaryPath, ConfigPath: configPath, OutputPath: output, WindowsUser: `HANDHELD\Player`, WindowsInstallDir: `C:\Users\Player\AppData\Local\Varkiv`}
	if _, err := BuildTargetPackage(TargetPackageInput{Kind: "windows-handheld", BinaryPath: wrongBinary, ConfigPath: configPath, OutputPath: filepath.Join(root, "wrong"), WindowsUser: input.WindowsUser, WindowsInstallDir: input.WindowsInstallDir}); err == nil || !strings.Contains(err.Error(), "x86-64") {
		t.Fatalf("expected non-x64 PE rejection, got %v", err)
	}
	result, err := BuildTargetPackage(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != "windows-handheld" || !result.Sensitive || len(result.Files) != 7 {
		t.Fatalf("unexpected result: %#v", result)
	}
	for _, relative := range []string{"Varkiv/varkiv.exe", "Varkiv/agent.json", "Varkiv/varkiv-agent-task.xml", "Varkiv/varkiv-agent-tray-task.xml", "README.txt", "HARDWARE-ACCEPTANCE.txt", "varkiv-target-manifest.json"} {
		if _, err = os.Stat(filepath.Join(output, filepath.FromSlash(relative))); err != nil {
			t.Fatalf("%s: %v", relative, err)
		}
	}
	task, _ := os.ReadFile(filepath.Join(output, "Varkiv/varkiv-agent-task.xml"))
	if !strings.Contains(string(task), `<UserId>HANDHELD\Player</UserId>`) || !strings.Contains(string(task), `<RunLevel>LeastPrivilege</RunLevel>`) || !strings.Contains(string(task), `<WorkingDirectory>C:\Users\Player\AppData\Local\Varkiv</WorkingDirectory>`) || strings.Contains(string(task), "powershell") || strings.Contains(string(task), "cmd.exe") {
		t.Fatalf("unsafe or incomplete Windows task: %s", task)
	}
	trayTask, _ := os.ReadFile(filepath.Join(output, "Varkiv/varkiv-agent-tray-task.xml"))
	if !strings.Contains(string(trayTask), `agent tray --config`) || strings.Contains(string(trayTask), `agent run --config`) || strings.Contains(string(trayTask), "powershell") || strings.Contains(string(trayTask), "cmd.exe") {
		t.Fatalf("unsafe or incomplete Windows tray task: %s", trayTask)
	}
	readme, _ := os.ReadFile(filepath.Join(output, "README.txt"))
	if !strings.Contains(string(readme), "never installs, merges, overwrites, or removes") || !strings.Contains(string(readme), "does not connect to the handheld") || !strings.Contains(string(readme), "Do not import both") {
		t.Fatalf("Windows safety boundary missing: %s", readme)
	}
	manifest, _ := os.ReadFile(filepath.Join(output, "varkiv-target-manifest.json"))
	if strings.Contains(string(manifest), "private-target-token") || strings.Contains(string(manifest), root) || strings.Contains(string(manifest), `C:\Users\Player`) {
		t.Fatalf("Windows manifest leaked private content: %s", manifest)
	}
}

func TestBuildTargetPackageRejectsSymlinkedPrivateConfig(t *testing.T) {
	root := t.TempDir()
	binaryPath := filepath.Join(root, "varkiv")
	configPath := filepath.Join(root, "agent.json")
	linkPath := filepath.Join(root, "agent-link.json")
	writeARM64ELF(t, binaryPath)
	writeTargetConfig(t, configPath, "rocknix")
	if err := os.Symlink(configPath, linkPath); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildTargetPackage(TargetPackageInput{Kind: "rocknix", BinaryPath: binaryPath, ConfigPath: linkPath, OutputPath: filepath.Join(root, "out")}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink refusal, got %v", err)
	}
}

func TestBuildTargetPackageRefusesExistingOutput(t *testing.T) {
	root := t.TempDir()
	binaryPath := filepath.Join(root, "varkiv")
	configPath := filepath.Join(root, "agent.json")
	output := filepath.Join(root, "existing")
	writeARM64ELF(t, binaryPath)
	writeTargetConfig(t, configPath, "rocknix")
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(output, "keep.txt")
	if err := os.WriteFile(marker, []byte("do-not-change"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := BuildTargetPackage(TargetPackageInput{Kind: "rocknix", BinaryPath: binaryPath, ConfigPath: configPath, OutputPath: output})
	if err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("expected overwrite refusal, got %v", err)
	}
	data, readErr := os.ReadFile(marker)
	if readErr != nil || string(data) != "do-not-change" {
		t.Fatalf("existing output changed: %q %v", data, readErr)
	}
}

func TestBuildTargetPackageRejectsWrongBinaryAndOpenConfig(t *testing.T) {
	root := t.TempDir()
	wrong := filepath.Join(root, "wrong")
	if err := os.WriteFile(wrong, []byte("not an elf"), 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "agent.json")
	writeTargetConfig(t, configPath, "rocknix")
	if _, err := BuildTargetPackage(TargetPackageInput{Kind: "rocknix", BinaryPath: wrong, ConfigPath: configPath, OutputPath: filepath.Join(root, "one")}); err == nil {
		t.Fatal("expected non-ELF binary rejection")
	}
	arm := filepath.Join(root, "arm")
	writeARM64ELF(t, arm)
	if err := os.Chmod(configPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildTargetPackage(TargetPackageInput{Kind: "rocknix", BinaryPath: arm, ConfigPath: configPath, OutputPath: filepath.Join(root, "two")}); err == nil || !strings.Contains(err.Error(), "permissions") {
		t.Fatalf("expected private config permission rejection, got %v", err)
	}
}

func TestBuildTargetPackageRejectsMismatchedBuiltInProfile(t *testing.T) {
	root := t.TempDir()
	binaryPath := filepath.Join(root, "varkiv-linux-arm64")
	configPath := filepath.Join(root, "agent.json")
	writeARM64ELF(t, binaryPath)
	config := Config{
		ServerURL:       "https://library.example.test",
		DeviceID:        "device-test",
		AccessToken:     "private-target-token",
		DeviceProfileID: "builtin-device-rocknix",
		DeviceTarget:    "rocknix",
		RootDir:         t.TempDir(),
	}
	if err := SaveConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	_, err := BuildTargetPackage(TargetPackageInput{
		Kind:       "muos",
		BinaryPath: binaryPath,
		ConfigPath: configPath,
		OutputPath: filepath.Join(root, "wrong-profile"),
	})
	if err == nil || !strings.Contains(err.Error(), "does not match muos") {
		t.Fatalf("expected built-in profile mismatch error, got %v", err)
	}
}

func TestBuildTargetPackageRequiresExactPairedTargetForCustomProfile(t *testing.T) {
	root := t.TempDir()
	binaryPath := filepath.Join(root, "varkiv-linux-arm64")
	writeARM64ELF(t, binaryPath)

	missingPath := filepath.Join(root, "missing-target.json")
	missing := Config{ServerURL: "https://library.example.test", DeviceID: "device-test", AccessToken: "private-target-token", DeviceProfileID: "custom-handheld", RootDir: t.TempDir()}
	if err := SaveConfig(missingPath, missing); err != nil {
		t.Fatal(err)
	}
	missingOutput := filepath.Join(root, "missing-output")
	if _, err := BuildTargetPackage(TargetPackageInput{Kind: "rocknix", BinaryPath: binaryPath, ConfigPath: missingPath, OutputPath: missingOutput}); err == nil || !strings.Contains(err.Error(), "no paired device target") {
		t.Fatalf("missing target was accepted: %v", err)
	}
	if _, err := os.Lstat(missingOutput); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing-target failure created output: %v", err)
	}

	driftPath := filepath.Join(root, "drift-target.json")
	drift := missing
	drift.DeviceTarget = "rocknix"
	if err := SaveConfig(driftPath, drift); err != nil {
		t.Fatal(err)
	}
	driftOutput := filepath.Join(root, "drift-output")
	if _, err := BuildTargetPackage(TargetPackageInput{Kind: "muos", BinaryPath: binaryPath, ConfigPath: driftPath, OutputPath: driftOutput}); err == nil || !strings.Contains(err.Error(), `target "rocknix" does not match muos`) {
		t.Fatalf("custom-profile target drift was accepted: %v", err)
	}
	if _, err := os.Lstat(driftOutput); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target-drift failure created output: %v", err)
	}
}
