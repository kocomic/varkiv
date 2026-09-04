package importer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"varkiv/internal/catalog"
)

const launchManifestName = "varkiv-launches.json"
const maxLaunchManifestSize = 4 << 20

type importedLaunchManifest struct {
	FormatVersion     int                            `json:"format_version"`
	DeviceProfileID   string                         `json:"device_profile_id"`
	FrontendAdapterID string                         `json:"frontend_adapter_id"`
	RuntimeCatalog    catalog.PortableRuntimeCatalog `json:"runtime_catalog"`
	Bindings          []struct {
		EditionID string `json:"edition_id"`
		ROMPath   string `json:"rom_path"`
		Binding   struct {
			DeviceProfileID   string   `json:"device_profile_id"`
			FrontendAdapterID string   `json:"frontend_adapter_id"`
			DriverID          string   `json:"driver_id"`
			CoreID            string   `json:"core_id"`
			Arguments         []string `json:"arguments"`
		} `json:"binding"`
		Driver struct {
			ID string `json:"id"`
		} `json:"driver"`
		CoreResolution *struct {
			Core *struct {
				ID string `json:"id"`
			} `json:"core"`
		} `json:"core_resolution"`
	} `json:"bindings"`
}

func pathInside(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// findLaunchManifest only inspects exact ancestor filenames up to the explicit
// library root. Symlinks and files resolving outside that root are ignored.
func findLaunchManifest(libraryRoot, metadataPath string) (string, error) {
	return findAncestorFile(libraryRoot, metadataPath, launchManifestName, maxLaunchManifestSize)
}

func findAncestorFile(libraryRoot, metadataPath, name string, maxSize int64) (string, error) {
	root, err := filepath.Abs(libraryRoot)
	if err != nil {
		return "", err
	}
	metadata, err := filepath.Abs(metadataPath)
	if err != nil {
		return "", err
	}
	if !pathInside(root, metadata) {
		// CLI callers may intentionally point at metadata outside the configured
		// library. Do not inspect its ancestors in that case.
		return "", nil
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	for current := filepath.Dir(metadata); pathInside(root, current); current = filepath.Dir(current) {
		candidate := filepath.Join(current, name)
		info, statErr := os.Lstat(candidate)
		switch {
		case statErr == nil && info.Mode().IsRegular():
			if info.Size() > maxSize {
				return "", fmt.Errorf("%s exceeds %d bytes", name, maxSize)
			}
			resolved, resolveErr := filepath.EvalSymlinks(candidate)
			if resolveErr == nil && pathInside(resolvedRoot, resolved) {
				return candidate, nil
			}
		case statErr != nil && !os.IsNotExist(statErr):
			return "", statErr
		}
		if current == root {
			break
		}
	}
	return "", nil
}

func cleanManifestROMPath(value string) (string, bool) {
	value = filepath.ToSlash(strings.TrimSpace(value))
	if value == "" || filepath.IsAbs(filepath.FromSlash(value)) || strings.HasPrefix(value, "/") {
		return "", false
	}
	value = filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	if value == "." || value == ".." || strings.HasPrefix(value, "../") || strings.ContainsAny(value, "\x00\r\n") {
		return "", false
	}
	return value, true
}

func attachStructuredLaunchHints(libraryRoot, metadataPath string, games []catalog.ImportedGame) ([]catalog.ImportedGame, error) {
	manifestPath, err := findLaunchManifest(libraryRoot, metadataPath)
	if err != nil || manifestPath == "" {
		return games, err
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, err
	}
	var manifest importedLaunchManifest
	if err = json.Unmarshal(data, &manifest); err != nil {
		// A foreign or damaged optional sidecar must not block ROM import.
		return games, nil
	}
	if manifest.FormatVersion != 1 && manifest.FormatVersion != 2 {
		return games, nil
	}
	if manifest.FormatVersion == 2 {
		if len(games) == 0 {
			return games, nil
		}
		// Repeat the small declarative catalog on each candidate so an arbitrary
		// user-selected subset remains self-contained and preview-token protected.
		for index := range games {
			copy := manifest.RuntimeCatalog
			games[index].RuntimeCatalog = &copy
		}
	}
	sourceRef, err := libraryPath(libraryRoot, filepath.Dir(manifestPath), filepath.Base(manifestPath))
	if err != nil {
		return nil, err
	}
	for _, binding := range manifest.Bindings {
		romPath, romOK := cleanManifestROMPath(binding.ROMPath)
		gameIndex := -1
		for index := range games {
			if binding.EditionID != "" && games[index].EditionID == binding.EditionID {
				gameIndex = index
				break
			}
			if romOK {
				for _, artifact := range games[index].Artifacts {
					if artifactPath, ok := cleanManifestROMPath(artifact.Path); ok && artifactPath == romPath {
						gameIndex = index
						break
					}
				}
			}
			if gameIndex >= 0 {
				break
			}
		}
		if gameIndex < 0 {
			continue
		}
		driverID := strings.TrimSpace(binding.Binding.DriverID)
		if driverID == "" {
			driverID = strings.TrimSpace(binding.Driver.ID)
		}
		coreID := strings.TrimSpace(binding.Binding.CoreID)
		if coreID == "" && binding.CoreResolution != nil && binding.CoreResolution.Core != nil {
			coreID = strings.TrimSpace(binding.CoreResolution.Core.ID)
		}
		deviceID := strings.TrimSpace(binding.Binding.DeviceProfileID)
		if deviceID == "" {
			deviceID = strings.TrimSpace(manifest.DeviceProfileID)
		}
		frontendID := strings.TrimSpace(binding.Binding.FrontendAdapterID)
		if frontendID == "" {
			frontendID = strings.TrimSpace(manifest.FrontendAdapterID)
		}
		// Reuse catalog validation as a strict portability gate. Unknown IDs or
		// unsafe argv are ignored rather than poisoning an otherwise valid import.
		hint := catalog.NewRuntimeImportHint{SourceKind: "structured-sidecar", SourceFormat: fmt.Sprintf("varkiv-launches-v%d", manifest.FormatVersion), DeviceProfileID: deviceID, FrontendAdapterID: frontendID, DriverID: driverID, CoreID: coreID, Arguments: binding.Binding.Arguments, SourceRef: sourceRef}
		if driverID == "" || !portableRuntimeHint(hint) {
			continue
		}
		games[gameIndex].RuntimeHints = append(games[gameIndex].RuntimeHints, hint)
	}
	return games, nil
}

func portableRuntimeHint(hint catalog.NewRuntimeImportHint) bool {
	// Normalize through a throwaway import transaction is intentionally avoided;
	// this mirrors the public identifier and argv contract before commit.
	for _, value := range []string{hint.DeviceProfileID, hint.FrontendAdapterID, hint.DriverID, hint.CoreID} {
		if value == "" {
			continue
		}
		if len(value) > 128 {
			return false
		}
		for index, r := range value {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || (index > 0 && strings.ContainsRune("._:-", r))) {
				return false
			}
		}
	}
	return catalog.ValidateLaunchArguments(hint.Arguments) == nil
}
