package deviceagent

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type TargetPackageInput struct {
	Kind              string
	BinaryPath        string
	ConfigPath        string
	OutputPath        string
	WindowsUser       string
	WindowsInstallDir string
}

func validateWindowsAgent(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("agent binary must be an exact regular file")
	}
	handle, err := os.Open(path)
	if err != nil {
		return err
	}
	defer handle.Close()
	dosHeader := make([]byte, 64)
	if _, err = io.ReadFull(handle, dosHeader); err != nil || string(dosHeader[:2]) != "MZ" {
		return errors.New("Windows target package requires a complete PE executable")
	}
	peOffset := int64(binary.LittleEndian.Uint32(dosHeader[0x3c:0x40]))
	if peOffset < 64 || peOffset > 16<<20 || peOffset+6 > info.Size() {
		return errors.New("Windows target package has an invalid PE header offset")
	}
	peHeader := make([]byte, 6)
	if _, err = handle.ReadAt(peHeader, peOffset); err != nil || string(peHeader[:4]) != "PE\x00\x00" {
		return errors.New("Windows target package requires a valid PE executable")
	}
	if machine := binary.LittleEndian.Uint16(peHeader[4:6]); machine != 0x8664 {
		return errors.New("Windows handheld target package requires an x86-64 PE executable")
	}
	return nil
}

type TargetPackageResult struct {
	Kind      string   `json:"kind"`
	Files     []string `json:"files"`
	Sensitive bool     `json:"sensitive"`
}

type targetManifestFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Mode   uint32 `json:"mode"`
}

type targetManifest struct {
	FormatVersion int                  `json:"format_version"`
	Kind          string               `json:"kind"`
	Sensitive     bool                 `json:"sensitive"`
	Files         []targetManifestFile `json:"files"`
}

func targetPackageFileMode(relative string) os.FileMode {
	base := pathpkg.Base(filepath.ToSlash(relative))
	if base == "varkiv" || strings.EqualFold(base, "varkiv.exe") || strings.HasSuffix(strings.ToLower(base), ".sh") {
		return 0o700
	}
	return 0o600
}

type TargetPackageVerificationResult struct {
	Kind          string `json:"kind"`
	Files         int    `json:"files"`
	Missing       int    `json:"missing"`
	Changed       int    `json:"changed"`
	ModeMismatch  int    `json:"mode_mismatch"`
	Unsafe        int    `json:"unsafe"`
	Extra         int    `json:"extra"`
	ModeChecksRun bool   `json:"mode_checks_run"`
	Verified      bool   `json:"verified"`
}

func validTargetPackageKind(kind string) bool {
	switch kind {
	case "windows-handheld", "rocknix", "knulli", "arkos", "darkos", "muos", "onionos", "steamos-bazzite":
		return true
	default:
		return false
	}
}

func targetPackageDeviceTarget(kind string) string {
	if kind == "windows-handheld" {
		return "windows"
	}
	return kind
}

// VerifyTargetPackage performs a strict, read-only comparison against the
// generated package manifest. It never follows links, repairs permissions,
// removes extras, or exposes file contents in its result.
func VerifyTargetPackage(root string) (TargetPackageVerificationResult, error) {
	result := TargetPackageVerificationResult{ModeChecksRun: runtime.GOOS != "windows"}
	root = strings.TrimSpace(root)
	if root == "" {
		return result, errors.New("target package path is required")
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return result, errors.New("target package directory is unavailable")
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return result, errors.New("target package path must be an exact directory, not a symlink")
	}
	manifestPath := filepath.Join(root, "varkiv-target-manifest.json")
	manifestInfo, err := os.Lstat(manifestPath)
	if err != nil || !manifestInfo.Mode().IsRegular() || manifestInfo.Mode()&os.ModeSymlink != 0 {
		return result, errors.New("target package manifest must be an exact regular file")
	}
	handle, err := os.Open(manifestPath)
	if err != nil {
		return result, errors.New("target package manifest is unreadable")
	}
	data, readErr := io.ReadAll(io.LimitReader(handle, (1<<20)+1))
	closeErr := handle.Close()
	if readErr != nil || closeErr != nil || len(data) > 1<<20 {
		return result, errors.New("target package manifest is unreadable or too large")
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var manifest targetManifest
	if err = decoder.Decode(&manifest); err != nil {
		return result, errors.New("target package manifest is invalid")
	}
	var trailing any
	if err = decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return result, errors.New("target package manifest has trailing data")
	}
	if manifest.FormatVersion != 1 || !manifest.Sensitive || !validTargetPackageKind(manifest.Kind) || len(manifest.Files) == 0 {
		return result, errors.New("target package manifest contract is unsupported")
	}
	result.Kind = manifest.Kind
	expectedFiles := map[string]struct{}{"varkiv-target-manifest.json": {}}
	expectedDirs := map[string]struct{}{}
	if result.ModeChecksRun && manifestInfo.Mode().Perm() != 0o600 {
		result.ModeMismatch++
	}
	for _, item := range manifest.Files {
		relative := item.Path
		clean := pathpkg.Clean(relative)
		if relative == "" || relative != clean || clean == "." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") || strings.Contains(relative, `\`) || relative == "varkiv-target-manifest.json" || (item.Mode != 0o600 && item.Mode != 0o700) {
			result.Unsafe++
			continue
		}
		decodedDigest, digestErr := hex.DecodeString(item.SHA256)
		if digestErr != nil || len(decodedDigest) != sha256.Size {
			result.Unsafe++
			continue
		}
		if _, duplicate := expectedFiles[relative]; duplicate {
			result.Unsafe++
			continue
		}
		expectedFiles[relative] = struct{}{}
		for parent := pathpkg.Dir(relative); parent != "."; parent = pathpkg.Dir(parent) {
			expectedDirs[parent] = struct{}{}
		}
		actualPath := filepath.Join(root, filepath.FromSlash(relative))
		info, statErr := os.Lstat(actualPath)
		if errors.Is(statErr, os.ErrNotExist) {
			result.Missing++
			continue
		}
		if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			result.Unsafe++
			continue
		}
		digest, digestErr := fileDigest(actualPath)
		if digestErr != nil {
			result.Unsafe++
			continue
		}
		if digest != strings.ToLower(item.SHA256) {
			result.Changed++
		}
		if result.ModeChecksRun && uint32(info.Mode().Perm()) != item.Mode {
			result.ModeMismatch++
		}
	}
	result.Files = len(expectedFiles)
	walkErr := filepath.WalkDir(root, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errors.New("walk")
		}
		if current == root {
			return nil
		}
		relative, relativeErr := filepath.Rel(root, current)
		if relativeErr != nil {
			return errors.New("walk")
		}
		relative = filepath.ToSlash(relative)
		if entry.Type()&os.ModeSymlink != 0 {
			result.Unsafe++
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if _, ok := expectedDirs[relative]; !ok {
				result.Extra++
			}
			return nil
		}
		if _, ok := expectedFiles[relative]; !ok {
			result.Extra++
		}
		return nil
	})
	if walkErr != nil {
		return result, errors.New("target package directory could not be inspected safely")
	}
	result.Verified = result.Missing == 0 && result.Changed == 0 && result.ModeMismatch == 0 && result.Unsafe == 0 && result.Extra == 0
	return result, nil
}

func validateLinuxAgent(path, kind string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("agent binary must be an exact regular file")
	}
	handle, err := os.Open(path)
	if err != nil {
		return err
	}
	defer handle.Close()
	header := make([]byte, 20)
	if _, err = io.ReadFull(handle, header); err != nil {
		return errors.New("agent binary is not a complete ELF header")
	}
	if string(header[:4]) != "\x7fELF" || header[5] != 1 {
		return errors.New("target package requires a little-endian Linux ELF agent")
	}
	machine := binary.LittleEndian.Uint16(header[18:20])
	if kind == "steamos-bazzite" {
		if header[4] != 2 || machine != 62 {
			return errors.New("SteamOS/Bazzite target package requires a 64-bit little-endian Linux x86-64 ELF agent")
		}
		return nil
	}
	if kind == "onionos" {
		if header[4] != 1 || machine != 40 {
			return errors.New("OnionOS target package requires a 32-bit little-endian Linux ARM ELF agent")
		}
		return nil
	}
	if header[4] != 2 || machine != 183 {
		return errors.New("target package requires a 64-bit little-endian Linux ARM64 ELF agent")
	}
	return nil
}

func writeExclusive(path string, mode os.FileMode, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	handle, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, writeErr := handle.Write(content)
	if writeErr == nil {
		writeErr = handle.Sync()
	}
	if closeErr := handle.Close(); writeErr == nil {
		writeErr = closeErr
	}
	return writeErr
}

func copyPackageExclusive(source, target string, mode os.FileMode) error {
	handle, err := os.Open(source)
	if err != nil {
		return err
	}
	defer handle.Close()
	if err = os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, handle)
	if copyErr == nil {
		copyErr = output.Sync()
	}
	if closeErr := output.Close(); copyErr == nil {
		copyErr = closeErr
	}
	return copyErr
}

func fileDigest(path string) (string, error) {
	handle, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer handle.Close()
	digest := sha256.New()
	if _, err = io.Copy(digest, handle); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func buildROCKNIXStage(stage, binaryPath, configPath string) ([]string, error) {
	base := filepath.Join(stage, "storage", ".config", "varkiv")
	modules := filepath.Join(stage, "storage", ".config", "modules")
	files := []string{
		"storage/.config/varkiv/varkiv",
		"storage/.config/varkiv/agent.json",
		"storage/.config/modules/Varkiv Sync Now.sh",
		"storage/.config/modules/Varkiv Start Automatic Sync.sh",
		"storage/.config/modules/Varkiv Stop Automatic Sync.sh",
		"README.txt",
	}
	if err := copyPackageExclusive(binaryPath, filepath.Join(base, "varkiv"), 0o700); err != nil {
		return nil, err
	}
	if err := copyPackageExclusive(configPath, filepath.Join(base, "agent.json"), 0o600); err != nil {
		return nil, err
	}
	syncNow := "#!/bin/sh\numask 077\nexec /storage/.config/varkiv/varkiv agent sync --config /storage/.config/varkiv/agent.json\n"
	start := "#!/bin/sh\numask 077\nbase=/storage/.config/varkiv\npidfile=$base/agent.pid\nif [ -f \"$pidfile\" ]; then\n  read -r pid < \"$pidfile\"\n  case \"$pid\" in *[!0-9]*|'') pid=0;; esac\n  if [ \"$pid\" -gt 1 ] && kill -0 \"$pid\" 2>/dev/null; then exit 0; fi\nfi\nnohup \"$base/varkiv\" agent run --config \"$base/agent.json\" --interval 60s >>\"$base/agent.log\" 2>&1 &\npid=$!\ntmp=$base/.agent.pid.$$\nprintf '%s\\n' \"$pid\" >\"$tmp\" && mv \"$tmp\" \"$pidfile\"\n"
	stop := "#!/bin/sh\numask 077\nbase=/storage/.config/varkiv\npidfile=$base/agent.pid\n[ -f \"$pidfile\" ] || exit 0\nread -r pid < \"$pidfile\"\ncase \"$pid\" in *[!0-9]*|'') exit 1;; esac\n[ \"$pid\" -gt 1 ] || exit 1\nkill \"$pid\" 2>/dev/null || true\nrm -f \"$pidfile\"\n"
	for name, content := range map[string]string{
		"Varkiv Sync Now.sh":             syncNow,
		"Varkiv Start Automatic Sync.sh": start,
		"Varkiv Stop Automatic Sync.sh":  stop,
	} {
		if err := writeExclusive(filepath.Join(modules, name), 0o700, []byte(content)); err != nil {
			return nil, err
		}
	}
	readme := "Varkiv ROCKNIX private device package\n\nSENSITIVE: agent.json contains a device access token. Keep this folder private.\n\n1. Review every file.\n2. Copy the contents of this package's storage directory into /storage on the device using the officially supported SFTP/SSH or SD-card workflow.\n3. Existing files are not handled by this package. If a destination already exists, stop and compare it manually.\n4. In the ROCKNIX Tools collection, run 'Varkiv Sync Now' once, or explicitly run 'Varkiv Start Automatic Sync'. Use the matching Stop tool before replacing this private package.\n5. The background process polls every 60 seconds. It never scans outside roots recorded in agent.json and keeps recoverable backups before replacing a downloaded save.\n\nThis package does not modify the read-only OS partition, install a system service, or claim hardware verification.\n"
	if err := writeExclusive(filepath.Join(stage, "README.txt"), 0o600, []byte(readme)); err != nil {
		return nil, err
	}
	return files, nil
}

func buildKNULLIStage(stage, binaryPath, configPath string) ([]string, error) {
	base := filepath.Join(stage, "userdata", "system", "varkiv")
	servicePath := filepath.Join(stage, "userdata", "system", "services", "varkiv")
	hookPath := filepath.Join(stage, "userdata", "system", "scripts", "varkiv-sync.sh")
	files := []string{
		"userdata/system/varkiv/varkiv",
		"userdata/system/varkiv/agent.json",
		"userdata/system/services/varkiv",
		"userdata/system/scripts/varkiv-sync.sh",
		"README.txt",
	}
	if err := copyPackageExclusive(binaryPath, filepath.Join(base, "varkiv"), 0o700); err != nil {
		return nil, err
	}
	if err := copyPackageExclusive(configPath, filepath.Join(base, "agent.json"), 0o600); err != nil {
		return nil, err
	}
	service := "#!/bin/bash\nset -u\numask 077\nbase=/userdata/system/varkiv\npidfile=$base/agent.pid\nreadpid() { pid=0; if [ -f \"$pidfile\" ]; then read -r pid < \"$pidfile\"; case \"$pid\" in *[!0-9]*|'') pid=0;; esac; fi; }\ncase \"${1:-}\" in\n  start)\n    readpid\n    if [ \"$pid\" -gt 1 ] && kill -0 \"$pid\" 2>/dev/null; then exit 0; fi\n    nohup \"$base/varkiv\" agent run --config \"$base/agent.json\" --interval 60s >>\"$base/agent.log\" 2>&1 &\n    pid=$!\n    tmp=$base/.agent.pid.$$\n    printf '%s\\n' \"$pid\" >\"$tmp\" && mv \"$tmp\" \"$pidfile\"\n    ;;\n  stop)\n    readpid\n    if [ \"$pid\" -gt 1 ]; then kill \"$pid\" 2>/dev/null || true; fi\n    rm -f \"$pidfile\"\n    ;;\n  status)\n    readpid\n    if [ \"$pid\" -gt 1 ] && kill -0 \"$pid\" 2>/dev/null; then echo running; exit 0; fi\n    echo stopped; exit 1\n    ;;\n  *) echo 'usage: varkiv {start|stop|status}' >&2; exit 2;;\nesac\n"
	hook := "#!/bin/bash\nset -u\numask 077\n[ \"${1:-}\" = gameStop ] || exit 0\nbase=/userdata/system/varkiv\npidfile=$base/agent.pid\nif [ -f \"$pidfile\" ]; then\n  read -r pid < \"$pidfile\"\n  case \"$pid\" in *[!0-9]*|'') pid=0;; esac\n  if [ \"$pid\" -gt 1 ] && kill -0 \"$pid\" 2>/dev/null; then exit 0; fi\nfi\noneshot=$base/oneshot.pid\nif [ -f \"$oneshot\" ]; then\n  read -r pid < \"$oneshot\"\n  case \"$pid\" in *[!0-9]*|'') pid=0;; esac\n  if [ \"$pid\" -gt 1 ] && kill -0 \"$pid\" 2>/dev/null; then exit 0; fi\nfi\n(\n  trap 'rm -f \"$oneshot\"' EXIT\n  \"$base/varkiv\" agent sync --config \"$base/agent.json\" >>\"$base/agent.log\" 2>&1\n) &\npid=$!\ntmp=$base/.oneshot.pid.$$\nprintf '%s\\n' \"$pid\" >\"$tmp\" && mv \"$tmp\" \"$oneshot\"\n"
	if err := writeExclusive(servicePath, 0o700, []byte(service)); err != nil {
		return nil, err
	}
	if err := writeExclusive(hookPath, 0o700, []byte(hook)); err != nil {
		return nil, err
	}
	readme := "Varkiv KNULLI private device package\n\nSENSITIVE: agent.json contains a device access token. Keep this folder private.\n\n1. Review every file and stop if an equivalent destination already exists; this package never merges or overwrites it.\n2. Copy this package's userdata directory into /userdata using KNULLI's documented network-transfer or SD-card workflow.\n3. In System settings > Services, explicitly enable the 'varkiv' user service. The service polls every 60 seconds.\n4. The gameStop hook requests a one-shot sync only while the service is not running. It receives the frontend event but does not record or transmit the ROM path arguments.\n5. Disable/stop the service before replacing the package. Local saves are never removed by these scripts.\n\nThis package uses Batocera-compatible persistent user service and game event directories. It does not modify the read-only OS partition or claim hardware verification.\n"
	if err := writeExclusive(filepath.Join(stage, "README.txt"), 0o600, []byte(readme)); err != nil {
		return nil, err
	}
	return files, nil
}

func buildArkFamilyStage(stage, binaryPath, configPath, displayName string, legacy bool) ([]string, error) {
	base := filepath.Join(stage, "roms", "tools", ".varkiv")
	tools := filepath.Join(stage, "roms", "tools")
	files := []string{
		"roms/tools/.varkiv/varkiv",
		"roms/tools/.varkiv/agent.json",
		"roms/tools/Varkiv Sync Now.sh",
		"roms/tools/Varkiv Start Automatic Sync.sh",
		"roms/tools/Varkiv Stop Automatic Sync.sh",
		"README.txt",
	}
	if err := copyPackageExclusive(binaryPath, filepath.Join(base, "varkiv"), 0o700); err != nil {
		return nil, err
	}
	if err := copyPackageExclusive(configPath, filepath.Join(base, "agent.json"), 0o600); err != nil {
		return nil, err
	}
	syncNow := "#!/bin/sh\numask 077\nexec /roms/tools/.varkiv/varkiv agent sync --config /roms/tools/.varkiv/agent.json\n"
	start := "#!/bin/sh\numask 077\nbase=/roms/tools/.varkiv\npidfile=$base/agent.pid\nif [ -f \"$pidfile\" ]; then\n  read -r pid < \"$pidfile\"\n  case \"$pid\" in *[!0-9]*|'') pid=0;; esac\n  if [ \"$pid\" -gt 1 ] && kill -0 \"$pid\" 2>/dev/null; then exit 0; fi\nfi\nnohup \"$base/varkiv\" agent run --config \"$base/agent.json\" --interval 60s >>\"$base/agent.log\" 2>&1 &\npid=$!\ntmp=$base/.agent.pid.$$\nprintf '%s\\n' \"$pid\" >\"$tmp\" && mv \"$tmp\" \"$pidfile\"\n"
	stop := "#!/bin/sh\numask 077\nbase=/roms/tools/.varkiv\npidfile=$base/agent.pid\n[ -f \"$pidfile\" ] || exit 0\nread -r pid < \"$pidfile\"\ncase \"$pid\" in *[!0-9]*|'') exit 1;; esac\n[ \"$pid\" -gt 1 ] || exit 1\nkill \"$pid\" 2>/dev/null || true\nrm -f \"$pidfile\"\n"
	for name, content := range map[string]string{
		"Varkiv Sync Now.sh":             syncNow,
		"Varkiv Start Automatic Sync.sh": start,
		"Varkiv Stop Automatic Sync.sh":  stop,
	} {
		if err := writeExclusive(filepath.Join(tools, name), 0o700, []byte(content)); err != nil {
			return nil, err
		}
	}
	legacyNote := ""
	if legacy {
		legacyNote = "\nArkOS was retired upstream on 2025-12-30 and replaced by dArkOS. This target remains available only for existing ArkOS installations; choose the dArkOS target for current systems.\n"
	}
	readme := "Varkiv " + displayName + " private device package\n\nSENSITIVE: agent.json contains a device access token. Keep this folder private.\n" + legacyNote + "\n1. Review every file and stop if an equivalent destination already exists; this package never merges or overwrites it.\n2. Copy the contents of this package's roms directory into the " + displayName + " ROM partition so the scripts appear in the Tools collection.\n3. Run 'Varkiv Sync Now' once, or explicitly run 'Varkiv Start Automatic Sync'. Use the matching Stop tool before replacing this package.\n4. The background process polls every 60 seconds. It never scans outside roots recorded in agent.json and keeps recoverable backups before replacing a downloaded save.\n5. Removing the integration is a separate, manual operation; these scripts do not remove ROMs, saves, media, or other Tools entries.\n\nThis package only adds files under /roms/tools. It does not modify the operating-system partition, install a boot service, or claim hardware verification.\n"
	if err := writeExclusive(filepath.Join(stage, "README.txt"), 0o600, []byte(readme)); err != nil {
		return nil, err
	}
	return files, nil
}

func buildArkOSStage(stage, binaryPath, configPath string) ([]string, error) {
	return buildArkFamilyStage(stage, binaryPath, configPath, "ArkOS", true)
}

func buildDArkOSStage(stage, binaryPath, configPath string) ([]string, error) {
	return buildArkFamilyStage(stage, binaryPath, configPath, "dArkOS", false)
}

func buildMuOSStage(stage, binaryPath, configPath string) ([]string, error) {
	base := filepath.Join(stage, "application", "Varkiv")
	startDir := filepath.Join(stage, "application", "Varkiv Start Automatic Sync")
	stopDir := filepath.Join(stage, "application", "Varkiv Stop Automatic Sync")
	files := []string{
		"application/Varkiv/varkiv",
		"application/Varkiv/agent.json",
		"application/Varkiv/mux_launch.sh",
		"application/Varkiv Start Automatic Sync/mux_launch.sh",
		"application/Varkiv Stop Automatic Sync/mux_launch.sh",
		"README.txt",
	}
	if err := copyPackageExclusive(binaryPath, filepath.Join(base, "varkiv"), 0o700); err != nil {
		return nil, err
	}
	if err := copyPackageExclusive(configPath, filepath.Join(base, "agent.json"), 0o600); err != nil {
		return nil, err
	}
	runtimeBase := "/run/muos/storage/application/Varkiv"
	syncNow := "#!/bin/sh\numask 077\nexec " + runtimeBase + "/varkiv agent sync --config " + runtimeBase + "/agent.json\n"
	start := "#!/bin/sh\numask 077\nbase=" + runtimeBase + "\npidfile=$base/agent.pid\nif [ -f \"$pidfile\" ]; then\n  read -r pid < \"$pidfile\"\n  case \"$pid\" in *[!0-9]*|'') pid=0;; esac\n  if [ \"$pid\" -gt 1 ] && kill -0 \"$pid\" 2>/dev/null; then exit 0; fi\nfi\nnohup \"$base/varkiv\" agent run --config \"$base/agent.json\" --interval 60s >>\"$base/agent.log\" 2>&1 &\npid=$!\ntmp=$base/.agent.pid.$$\nprintf '%s\\n' \"$pid\" >\"$tmp\" && mv \"$tmp\" \"$pidfile\"\n"
	stop := "#!/bin/sh\numask 077\nbase=" + runtimeBase + "\npidfile=$base/agent.pid\n[ -f \"$pidfile\" ] || exit 0\nread -r pid < \"$pidfile\"\ncase \"$pid\" in *[!0-9]*|'') exit 1;; esac\n[ \"$pid\" -gt 1 ] || exit 1\nkill \"$pid\" 2>/dev/null || true\nrm -f \"$pidfile\"\n"
	for path, content := range map[string]string{
		filepath.Join(base, "mux_launch.sh"):     syncNow,
		filepath.Join(startDir, "mux_launch.sh"): start,
		filepath.Join(stopDir, "mux_launch.sh"):  stop,
	} {
		if err := writeExclusive(path, 0o700, []byte(content)); err != nil {
			return nil, err
		}
	}
	readme := "Varkiv muOS private device package\n\nSENSITIVE: application/Varkiv/agent.json contains a device access token. Keep this folder private.\n\n1. Review every file and stop if an equivalent destination already exists; this package never merges or overwrites it.\n2. Place this directory in an installable archive with the application top-level folder, then install it through muOS Archive Manager.\n3. The three application entries run one sync, explicitly start polling, or explicitly stop polling. No boot script is installed or enabled.\n4. The package uses muOS's persistent user application mapping and never writes /opt/muos or another system partition.\n5. Stop automatic sync before replacing the package. ROMs, saves, and unrelated applications are never removed by these scripts.\n\nThis layout follows the current muxapp/application archive contract but remains Package-tested until verified on real muOS hardware.\n"
	if err := writeExclusive(filepath.Join(stage, "README.txt"), 0o600, []byte(readme)); err != nil {
		return nil, err
	}
	return files, nil
}

func buildOnionOSStage(stage, binaryPath, configPath string) ([]string, error) {
	base := filepath.Join(stage, "App", "Varkiv")
	startDir := filepath.Join(stage, "App", "Varkiv Start Automatic Sync")
	stopDir := filepath.Join(stage, "App", "Varkiv Stop Automatic Sync")
	files := []string{
		"App/Varkiv/varkiv",
		"App/Varkiv/agent.json",
		"App/Varkiv/launch.sh",
		"App/Varkiv/config.json",
		"App/Varkiv Start Automatic Sync/launch.sh",
		"App/Varkiv Start Automatic Sync/config.json",
		"App/Varkiv Stop Automatic Sync/launch.sh",
		"App/Varkiv Stop Automatic Sync/config.json",
		"README.txt",
	}
	if err := copyPackageExclusive(binaryPath, filepath.Join(base, "varkiv"), 0o700); err != nil {
		return nil, err
	}
	if err := copyPackageExclusive(configPath, filepath.Join(base, "agent.json"), 0o600); err != nil {
		return nil, err
	}
	runtimeBase := "/mnt/SDCARD/App/Varkiv"
	scripts := map[string]string{
		filepath.Join(base, "launch.sh"):     "#!/bin/sh\numask 077\nexec " + runtimeBase + "/varkiv agent sync --config " + runtimeBase + "/agent.json\n",
		filepath.Join(startDir, "launch.sh"): "#!/bin/sh\numask 077\nbase=" + runtimeBase + "\npidfile=$base/agent.pid\nif [ -f \"$pidfile\" ]; then\n  read -r pid < \"$pidfile\"\n  case \"$pid\" in *[!0-9]*|'') pid=0;; esac\n  if [ \"$pid\" -gt 1 ] && kill -0 \"$pid\" 2>/dev/null; then exit 0; fi\nfi\nnohup \"$base/varkiv\" agent run --config \"$base/agent.json\" --interval 60s >>\"$base/agent.log\" 2>&1 &\npid=$!\ntmp=$base/.agent.pid.$$\nprintf '%s\\n' \"$pid\" >\"$tmp\" && mv \"$tmp\" \"$pidfile\"\n",
		filepath.Join(stopDir, "launch.sh"):  "#!/bin/sh\numask 077\nbase=" + runtimeBase + "\npidfile=$base/agent.pid\n[ -f \"$pidfile\" ] || exit 0\nread -r pid < \"$pidfile\"\ncase \"$pid\" in *[!0-9]*|'') exit 1;; esac\n[ \"$pid\" -gt 1 ] || exit 1\nkill \"$pid\" 2>/dev/null || true\nrm -f \"$pidfile\"\n",
	}
	for path, content := range scripts {
		if err := writeExclusive(path, 0o700, []byte(content)); err != nil {
			return nil, err
		}
	}
	configs := map[string]string{
		filepath.Join(base, "config.json"):     "Varkiv Sync Now",
		filepath.Join(startDir, "config.json"): "Varkiv Start Automatic Sync",
		filepath.Join(stopDir, "config.json"):  "Varkiv Stop Automatic Sync",
	}
	for path, label := range configs {
		content, err := json.MarshalIndent(map[string]any{"label": label, "launch": "launch.sh"}, "", "  ")
		if err != nil {
			return nil, err
		}
		if err = writeExclusive(path, 0o600, append(content, '\n')); err != nil {
			return nil, err
		}
	}
	readme := "Varkiv OnionOS private device package\n\nSENSITIVE: App/Varkiv/agent.json contains a device access token. Keep this folder private.\n\n1. Review every file and stop if an equivalent destination already exists; this package never merges or overwrites it.\n2. Copy this package's App directory to the root of the Onion SD card using an offline card workflow.\n3. The three App entries run one sync, explicitly start polling, or explicitly stop polling. No boot script or network service is installed.\n4. Stop automatic sync before replacing the package. ROMs, saves, themes, and unrelated apps are never removed by these scripts.\n5. This package requires the separately built 32-bit Linux ARM agent and does not modify Onion's .tmp_update system files.\n\nThis layout follows Onion's App launch.sh/config.json contract but remains Package-tested until verified on real hardware.\n"
	if err := writeExclusive(filepath.Join(stage, "README.txt"), 0o600, []byte(readme)); err != nil {
		return nil, err
	}
	return files, nil
}

func buildSteamOSStage(stage, binaryPath, configPath string) ([]string, error) {
	base := filepath.Join(stage, "home", ".local", "share", "varkiv")
	configDir := filepath.Join(stage, "home", ".config", "varkiv")
	serviceDir := filepath.Join(stage, "home", ".config", "systemd", "user")
	files := []string{
		"home/.local/share/varkiv/varkiv",
		"home/.config/varkiv/agent.json",
		"home/.config/systemd/user/varkiv-agent.service",
		"README.txt",
	}
	if err := copyPackageExclusive(binaryPath, filepath.Join(base, "varkiv"), 0o700); err != nil {
		return nil, err
	}
	if err := copyPackageExclusive(configPath, filepath.Join(configDir, "agent.json"), 0o600); err != nil {
		return nil, err
	}
	unit := "[Unit]\nDescription=Varkiv private save synchronization agent\nAfter=network-online.target\nWants=network-online.target\n\n[Service]\nType=simple\nUMask=0077\nExecStart=%h/.local/share/varkiv/varkiv agent run --config %h/.config/varkiv/agent.json --interval 60s\nRestart=on-failure\nRestartSec=15s\nNoNewPrivileges=true\nPrivateTmp=true\n\n[Install]\nWantedBy=default.target\n"
	if err := writeExclusive(filepath.Join(serviceDir, "varkiv-agent.service"), 0o600, []byte(unit)); err != nil {
		return nil, err
	}
	readme := "Varkiv SteamOS/Bazzite private device package\n\nSENSITIVE: home/.config/varkiv/agent.json contains a device access token. Keep this folder private.\n\n1. Review every file and stop if an equivalent destination already exists; this package never merges or overwrites it.\n2. Copy the contents inside this package's home directory into the intended non-root user's home directory. Do not copy the top-level home directory itself.\n3. Review the exact paths in agent.json, then optionally run 'systemctl --user daemon-reload' and explicitly enable/start varkiv-agent.service. This package does not run those commands.\n4. Stop and disable the user service before replacing the package. ROMs, saves, media, emulator files, and unrelated user configuration are never removed by this package.\n5. The unit uses fixed argv, no shell, no root privileges, and only the roots explicitly recorded in agent.json.\n\nThis layout is Package-tested only. SteamOS/Bazzite suspend, Wi-Fi recovery, Flatpak paths, emulator exit behavior, and upgrades remain unverified on real hardware.\n"
	if err := writeExclusive(filepath.Join(stage, "README.txt"), 0o600, []byte(readme)); err != nil {
		return nil, err
	}
	return files, nil
}

func buildWindowsStage(stage, binaryPath, configPath, user, installDir string) ([]string, error) {
	installDir = strings.TrimRight(strings.TrimSpace(installDir), `\\/`)
	if _, err := windowsPathDirectory(installDir + `\varkiv.exe`); err != nil {
		return nil, fmt.Errorf("Windows install directory: %w", err)
	}
	binaryTarget := installDir + `\varkiv.exe`
	configTarget := installDir + `\agent.json`
	_, task, err := RenderServiceTemplate(ServiceTemplateInput{Kind: "windows-task", BinaryPath: binaryTarget, ConfigPath: configTarget, User: user, Interval: time.Minute})
	if err != nil {
		return nil, err
	}
	_, trayTask, err := RenderServiceTemplate(ServiceTemplateInput{Kind: "windows-tray-task", BinaryPath: binaryTarget, ConfigPath: configTarget, User: user, Interval: time.Minute})
	if err != nil {
		return nil, err
	}
	base := filepath.Join(stage, "Varkiv")
	files := []string{
		"Varkiv/varkiv.exe",
		"Varkiv/agent.json",
		"Varkiv/varkiv-agent-task.xml",
		"Varkiv/varkiv-agent-tray-task.xml",
		"README.txt",
	}
	if err = copyPackageExclusive(binaryPath, filepath.Join(base, "varkiv.exe"), 0o700); err != nil {
		return nil, err
	}
	if err = copyPackageExclusive(configPath, filepath.Join(base, "agent.json"), 0o600); err != nil {
		return nil, err
	}
	if err = writeExclusive(filepath.Join(base, "varkiv-agent-task.xml"), 0o600, []byte(task)); err != nil {
		return nil, err
	}
	if err = writeExclusive(filepath.Join(base, "varkiv-agent-tray-task.xml"), 0o600, []byte(trayTask)); err != nil {
		return nil, err
	}
	readme := "Varkiv Windows handheld private device package\n\nSENSITIVE: Varkiv\\agent.json contains a device access token. Keep this folder private.\n\n1. Review every file. This package was rendered for the explicit install directory " + installDir + ".\n2. Stop if that destination already contains any same-named file; this package never installs, merges, overwrites, or removes it.\n3. Copy the files inside this package's Varkiv directory into the reviewed install directory without changing their names.\n4. As the intended non-administrator user, choose exactly one reviewed task: import varkiv-agent-tray-task.xml for a localized status icon and manual Sync Now action, or varkiv-agent-task.xml for headless polling. Do not import both. Both use InteractiveToken and LeastPrivilege.\n5. Run varkiv.exe agent sync --config agent.json once before enabling either task. Stop the selected task before replacing the package.\n\nThe tray shows only state and counts, never server addresses, tokens, paths, device IDs, ROM names, or save names. The generator does not connect to the handheld, invoke Task Scheduler, modify the registry, request elevation, or delete ROMs, saves, media, emulator files, or previous backups. This layout remains Package-tested until verified on Windows hardware.\n"
	if err = writeExclusive(filepath.Join(stage, "README.txt"), 0o600, []byte(readme)); err != nil {
		return nil, err
	}
	return files, nil
}

func targetAcceptanceCommand(kind string) string {
	switch kind {
	case "windows-handheld":
		return `varkiv.exe agent acceptance --config agent.json --out hardware-acceptance.json`
	case "rocknix":
		return `/storage/.config/varkiv/varkiv agent acceptance --config /storage/.config/varkiv/agent.json --out /storage/.config/varkiv/hardware-acceptance.json`
	case "knulli":
		return `/userdata/system/varkiv/varkiv agent acceptance --config /userdata/system/varkiv/agent.json --out /userdata/system/varkiv/hardware-acceptance.json`
	case "arkos":
		return `/roms/tools/.varkiv/varkiv agent acceptance --config /roms/tools/.varkiv/agent.json --out /roms/tools/.varkiv/hardware-acceptance.json`
	case "darkos":
		return `/roms/tools/.varkiv/varkiv agent acceptance --config /roms/tools/.varkiv/agent.json --out /roms/tools/.varkiv/hardware-acceptance.json`
	case "muos":
		return `/run/muos/storage/application/Varkiv/varkiv agent acceptance --config /run/muos/storage/application/Varkiv/agent.json --out /run/muos/storage/application/Varkiv/hardware-acceptance.json`
	case "onionos":
		return `/mnt/SDCARD/App/Varkiv/varkiv agent acceptance --config /mnt/SDCARD/App/Varkiv/agent.json --out /mnt/SDCARD/App/Varkiv/hardware-acceptance.json`
	default:
		return `$HOME/.local/share/varkiv/varkiv agent acceptance --config $HOME/.config/varkiv/agent.json --out $HOME/.config/varkiv/hardware-acceptance.json`
	}
}

func writeTargetAcceptanceGuide(stage, kind string) error {
	command := targetAcceptanceCommand(kind)
	targetChecks := ""
	if kind == "steamos-bazzite" || kind == "rocknix" || kind == "knulli" || kind == "arkos" || kind == "darkos" || kind == "muos" || kind == "onionos" {
		targetChecks = " For this handheld Linux target, hardware-tested also requires a real network recovery and client upgrade, recorded as --observe network-recovery --observe upgrade."
	}
	guide := "Varkiv real-device acceptance\n\n" +
		"1. First complete the documented frontend launch, emulator exit, save creation, upload, download, offline, sleep/resume, conflict recovery, token revocation, and upgrade checks that apply to this device." + targetChecks + "\n" +
		"2. Stop background synchronization before collecting the report.\n" +
		"3. Run this preflight command on the device:\n\n    " + command + "\n\n" +
		"4. Add only observations you personally completed, repeating for example: --observe frontend-launch --observe sync-upload --observe sync-download. Use a new output filename because reports are never overwritten.\n" +
		"5. The JSON omits the server URL, token, device/profile/stream identifiers, local paths, ROM/save names, and underlying errors. Review it locally before sharing it. It is only candidate evidence and always requires maintainer review; the command never upgrades a support claim or uploads the report.\n"
	return writeExclusive(filepath.Join(stage, "HARDWARE-ACCEPTANCE.txt"), 0o600, []byte(guide))
}

// BuildTargetPackage writes a new, private, reviewable device package. It
// never connects to the device, installs a service, or replaces an existing
// path. Each layout uses a documented persistent user or Tools folder.
func BuildTargetPackage(input TargetPackageInput) (TargetPackageResult, error) {
	kind := strings.ToLower(strings.TrimSpace(input.Kind))
	if !validTargetPackageKind(kind) {
		return TargetPackageResult{}, errors.New("target package kind must be windows-handheld, rocknix, knulli, arkos, darkos, muos, onionos, or steamos-bazzite")
	}
	var binaryErr error
	if kind == "windows-handheld" {
		binaryErr = validateWindowsAgent(input.BinaryPath)
	} else {
		binaryErr = validateLinuxAgent(input.BinaryPath, kind)
	}
	if binaryErr != nil {
		return TargetPackageResult{}, binaryErr
	}
	configInfo, err := os.Lstat(input.ConfigPath)
	if err != nil {
		return TargetPackageResult{}, err
	}
	if !configInfo.Mode().IsRegular() {
		return TargetPackageResult{}, errors.New("private agent config must be an exact regular file, not a symlink")
	}
	config, err := LoadConfig(input.ConfigPath)
	if err != nil {
		return TargetPackageResult{}, fmt.Errorf("private agent config: %w", err)
	}
	expectedProfileID := "builtin-device-" + kind
	if strings.HasPrefix(config.DeviceProfileID, "builtin-device-") && config.DeviceProfileID != expectedProfileID {
		return TargetPackageResult{}, fmt.Errorf("built-in device profile %q does not match %s target package; expected %q", config.DeviceProfileID, kind, expectedProfileID)
	}
	expectedTarget := targetPackageDeviceTarget(kind)
	if config.DeviceTarget == "" {
		return TargetPackageResult{}, errors.New("private agent config has no paired device target; run one authenticated sync with this config or pair the device again before packaging")
	}
	if config.DeviceTarget != expectedTarget {
		return TargetPackageResult{}, fmt.Errorf("private agent config target %q does not match %s target package; expected %q", config.DeviceTarget, kind, expectedTarget)
	}
	output, err := filepath.Abs(strings.TrimSpace(input.OutputPath))
	if err != nil || strings.TrimSpace(input.OutputPath) == "" {
		return TargetPackageResult{}, errors.New("output path is required")
	}
	if _, err = os.Lstat(output); err == nil {
		return TargetPackageResult{}, errors.New("target package output already exists; refusing to merge or overwrite")
	} else if !errors.Is(err, os.ErrNotExist) {
		return TargetPackageResult{}, err
	}
	if err = os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
		return TargetPackageResult{}, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(output), ".varkiv-"+kind+"-*")
	if err != nil {
		return TargetPackageResult{}, err
	}
	defer os.RemoveAll(stage)
	var files []string
	switch kind {
	case "windows-handheld":
		files, err = buildWindowsStage(stage, input.BinaryPath, input.ConfigPath, input.WindowsUser, input.WindowsInstallDir)
	case "rocknix":
		files, err = buildROCKNIXStage(stage, input.BinaryPath, input.ConfigPath)
	case "knulli":
		files, err = buildKNULLIStage(stage, input.BinaryPath, input.ConfigPath)
	case "arkos":
		files, err = buildArkOSStage(stage, input.BinaryPath, input.ConfigPath)
	case "darkos":
		files, err = buildDArkOSStage(stage, input.BinaryPath, input.ConfigPath)
	case "muos":
		files, err = buildMuOSStage(stage, input.BinaryPath, input.ConfigPath)
	case "onionos":
		files, err = buildOnionOSStage(stage, input.BinaryPath, input.ConfigPath)
	case "steamos-bazzite":
		files, err = buildSteamOSStage(stage, input.BinaryPath, input.ConfigPath)
	}
	if err != nil {
		return TargetPackageResult{}, err
	}
	if err = writeTargetAcceptanceGuide(stage, kind); err != nil {
		return TargetPackageResult{}, err
	}
	files = append(files, "HARDWARE-ACCEPTANCE.txt")
	manifestFiles := make([]targetManifestFile, 0, len(files))
	for _, relative := range files {
		path := filepath.Join(stage, filepath.FromSlash(relative))
		digest, digestErr := fileDigest(path)
		if digestErr != nil {
			return TargetPackageResult{}, digestErr
		}
		expectedMode := targetPackageFileMode(relative)
		if runtime.GOOS != "windows" {
			info, statErr := os.Stat(path)
			if statErr != nil || info.Mode().Perm() != expectedMode {
				return TargetPackageResult{}, fmt.Errorf("target package file mode drifted for %s", relative)
			}
		}
		// Windows cannot represent POSIX execute bits. Record the destination
		// contract rather than the permissions of the build host so a package
		// generated on Windows still verifies safely after transfer.
		manifestFiles = append(manifestFiles, targetManifestFile{Path: relative, SHA256: digest, Mode: uint32(expectedMode)})
	}
	manifest, err := json.MarshalIndent(map[string]any{"format_version": 1, "kind": kind, "sensitive": true, "files": manifestFiles}, "", "  ")
	if err != nil {
		return TargetPackageResult{}, err
	}
	manifest = append(manifest, '\n')
	if err = writeExclusive(filepath.Join(stage, "varkiv-target-manifest.json"), 0o600, manifest); err != nil {
		return TargetPackageResult{}, err
	}
	files = append(files, "varkiv-target-manifest.json")
	if err = os.Rename(stage, output); err != nil {
		return TargetPackageResult{}, err
	}
	return TargetPackageResult{Kind: kind, Files: files, Sensitive: true}, nil
}
