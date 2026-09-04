package deviceagent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"varkiv/internal/catalog"
	"varkiv/internal/filehash"
)

type RuntimeProbeItem struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Match  string `json:"match,omitempty"`
}

type RuntimeProbeResult struct {
	Target                string                             `json:"target"`
	EmulatorDirConfigured bool                               `json:"emulator_dir_configured"`
	CoreDirConfigured     bool                               `json:"core_dir_configured"`
	Drivers               []RuntimeProbeItem                 `json:"drivers"`
	Cores                 []RuntimeProbeItem                 `json:"retroarch_cores"`
	InstalledDrivers      int                                `json:"installed_drivers"`
	InstalledCores        int                                `json:"installed_cores"`
	Attestations          []catalog.RuntimeAttestationReport `json:"runtime_attestations"`
}

const maxRuntimeAttestationBytes int64 = 512 << 20

func attestRuntimeCandidate(root, match, kind, runtimeID string, contractVersion int) (catalog.RuntimeAttestationReport, error) {
	target := filepath.Join(root, filepath.FromSlash(match))
	if err := rejectSymlinkTraversal(root, target); err != nil {
		return catalog.RuntimeAttestationReport{}, err
	}
	before, err := os.Lstat(target)
	if err != nil || !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return catalog.RuntimeAttestationReport{}, errors.New("runtime attestation candidate must be a regular non-symlink file")
	}
	if before.Size() <= 0 || before.Size() > maxRuntimeAttestationBytes {
		return catalog.RuntimeAttestationReport{}, errors.New("runtime attestation candidate is outside the 1 byte to 512 MiB limit")
	}
	digest, size, err := filehash.File(target)
	if err != nil {
		return catalog.RuntimeAttestationReport{}, err
	}
	after, err := os.Lstat(target)
	if err != nil || !after.Mode().IsRegular() || after.Mode()&os.ModeSymlink != 0 || size != before.Size() || after.Size() != before.Size() || after.ModTime() != before.ModTime() {
		return catalog.RuntimeAttestationReport{}, errors.New("runtime attestation candidate changed while hashing")
	}
	return catalog.RuntimeAttestationReport{Kind: kind, RuntimeID: runtimeID, ContractVersion: contractVersion, SHA256: digest, Size: size}, nil
}

func configuredProbeDir(config Config, profile catalog.DeviceProfile, key string) (string, bool, error) {
	value := strings.TrimSpace(config.PathOverrides[key])
	if value == "" {
		value = strings.TrimSpace(profile.Paths[key])
	}
	if value == "" {
		return "", false, nil
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(config.RootDir, filepath.FromSlash(value))
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", false, err
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return absolute, true, nil
		}
		return "", false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", false, errors.New("runtime probe directory must be a real directory, not a symbolic link")
	}
	return absolute, true, nil
}

func probeCandidate(root, candidate string, caseSensitive bool) (bool, string) {
	candidate = filepath.Clean(filepath.FromSlash(strings.TrimSpace(candidate)))
	if candidate == "." || filepath.IsAbs(candidate) || candidate == ".." || strings.HasPrefix(candidate, ".."+string(filepath.Separator)) {
		return false, ""
	}
	target := filepath.Join(root, candidate)
	if err := rejectSymlinkTraversal(root, target); err != nil {
		return false, ""
	}
	if info, err := os.Lstat(target); err == nil && info.Mode().IsRegular() {
		return true, filepath.ToSlash(candidate)
	}
	if caseSensitive || filepath.Dir(candidate) != "." {
		return false, ""
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) > 2048 {
		return false, ""
	}
	for _, entry := range entries {
		if strings.EqualFold(entry.Name(), candidate) {
			info, infoErr := entry.Info()
			if infoErr == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
				return true, entry.Name()
			}
		}
	}
	return false, ""
}

func probeRuntime(config Config, remote deviceConfigResponse) (RuntimeProbeResult, error) {
	emulatorDir, emulatorConfigured, err := configuredProbeDir(config, remote.DeviceProfile, "emulator_dir")
	if err != nil {
		return RuntimeProbeResult{}, err
	}
	coreDir, coreConfigured, err := configuredProbeDir(config, remote.DeviceProfile, "core_dir")
	if err != nil {
		return RuntimeProbeResult{}, err
	}
	result := RuntimeProbeResult{Target: remote.DeviceProfile.Target, EmulatorDirConfigured: emulatorConfigured, CoreDirConfigured: coreConfigured, Drivers: []RuntimeProbeItem{}, Cores: []RuntimeProbeItem{}, Attestations: []catalog.RuntimeAttestationReport{}}
	requirements := map[string]int{}
	for _, requirement := range remote.RuntimeAttestationRequirements {
		requirements[requirement.Kind+"\x00"+requirement.RuntimeID] = requirement.ContractVersion
	}
	for _, driver := range remote.Drivers {
		if !driver.Enabled || !containsString(driver.Targets, remote.DeviceProfile.Target) {
			continue
		}
		item := RuntimeProbeItem{ID: driver.ID, Name: driver.Name, Status: "missing"}
		if remote.DeviceProfile.Target == "android" {
			item.Status = "android-companion-required"
		} else if !emulatorConfigured {
			item.Status = "not-configured"
		} else {
			candidates := append([]string{}, driver.Launch.Executables[remote.DeviceProfile.Target]...)
			candidates = append(candidates, driver.Launch.Executables["default"]...)
			for _, candidate := range candidates {
				if ok, match := probeCandidate(emulatorDir, candidate, remote.DeviceProfile.CaseSensitive); ok {
					item.Status, item.Match = "installed", match
					result.InstalledDrivers++
					if contract, required := requirements["driver\x00"+driver.ID]; required && contract == driver.ContractVersion {
						attestation, attestErr := attestRuntimeCandidate(emulatorDir, match, "driver", driver.ID, driver.ContractVersion)
						if attestErr != nil {
							return RuntimeProbeResult{}, attestErr
						}
						result.Attestations = append(result.Attestations, attestation)
					}
					break
				}
			}
		}
		result.Drivers = append(result.Drivers, item)
	}
	for _, core := range remote.Cores {
		if !core.Enabled {
			continue
		}
		item := RuntimeProbeItem{ID: core.ID, Name: core.Name, Status: "missing"}
		if !coreConfigured {
			item.Status = "not-configured"
		} else {
			for _, library := range core.LibraryNames {
				for _, candidate := range []string{library + ".dll", library + ".so", "lib" + library + ".so", library + ".dylib"} {
					if ok, match := probeCandidate(coreDir, candidate, remote.DeviceProfile.CaseSensitive); ok {
						item.Status, item.Match = "installed", match
						result.InstalledCores++
						if contract, required := requirements["core\x00"+core.ID]; required && contract == core.ContractVersion {
							attestation, attestErr := attestRuntimeCandidate(coreDir, match, "core", core.ID, core.ContractVersion)
							if attestErr != nil {
								return RuntimeProbeResult{}, attestErr
							}
							result.Attestations = append(result.Attestations, attestation)
						}
						break
					}
				}
				if item.Status == "installed" {
					break
				}
			}
		}
		result.Cores = append(result.Cores, item)
	}
	sort.Slice(result.Drivers, func(i, j int) bool { return result.Drivers[i].ID < result.Drivers[j].ID })
	sort.Slice(result.Cores, func(i, j int) bool { return result.Cores[i].ID < result.Cores[j].ID })
	sort.Slice(result.Attestations, func(i, j int) bool {
		if result.Attestations[i].Kind != result.Attestations[j].Kind {
			return result.Attestations[i].Kind < result.Attestations[j].Kind
		}
		return result.Attestations[i].RuntimeID < result.Attestations[j].RuntimeID
	})
	return result, nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// ProbeRuntime checks only directories explicitly configured for this Agent.
// It returns catalog IDs and curated candidate names, never host paths.
func ProbeRuntime(ctx context.Context, configPath string) (RuntimeProbeResult, error) {
	config, err := LoadConfig(configPath)
	if err != nil {
		return RuntimeProbeResult{}, err
	}
	remote, err := fetchDeviceConfig(ctx, defaultClient(), config)
	if err != nil {
		return RuntimeProbeResult{}, err
	}
	return probeRuntime(config, remote)
}
