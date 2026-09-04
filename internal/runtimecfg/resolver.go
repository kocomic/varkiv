package runtimecfg

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"varkiv/internal/catalog"
)

type LaunchResolution struct {
	EditionID       string                   `json:"edition_id"`
	PlatformID      string                   `json:"platform_id"`
	ROMPath         string                   `json:"rom_path"`
	Binding         catalog.LaunchBinding    `json:"binding"`
	DeviceProfile   catalog.DeviceProfile    `json:"device_profile"`
	FrontendAdapter *catalog.FrontendAdapter `json:"frontend_adapter,omitempty"`
	Driver          catalog.EmulatorDriver   `json:"driver"`
	CoreResolution  *catalog.CoreResolution  `json:"core_resolution,omitempty"`
	Arguments       []string                 `json:"arguments"`
	ExecutableHints []string                 `json:"executable_hints"`
	AndroidPackage  string                   `json:"android_package,omitempty"`
	AndroidActivity string                   `json:"android_activity,omitempty"`
	Warnings        []string                 `json:"warnings"`
}

func titleIDParts(value string) (string, string) {
	value = strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(value), "-", ""), " ", "")
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 8 {
		return "", ""
	}
	return value[:8], value[8:]
}

func containsString(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func portableSourcePath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, `\`) || strings.Contains(value, ":") || filepath.IsAbs(filepath.FromSlash(value)) {
		return ""
	}
	cleaned := filepath.Clean(filepath.FromSlash(value))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(cleaned)
}

func renderArgument(argument string, values map[string]string) (string, error) {
	if err := catalog.ValidateLaunchArguments([]string{argument}); err != nil {
		return "", err
	}
	for _, match := range regexpVariables(argument) {
		if strings.TrimSpace(values[match]) == "" {
			return "", fmt.Errorf("launch argument requires unavailable %s", match)
		}
	}
	for key, value := range values {
		argument = strings.ReplaceAll(argument, "{{"+key+"}}", value)
	}
	if strings.Contains(argument, "{{") || strings.Contains(argument, "}}") {
		return "", errors.New("launch argument could not be fully resolved")
	}
	return argument, nil
}

func regexpVariables(value string) []string {
	result := []string{}
	for start := 0; ; {
		left := strings.Index(value[start:], "{{")
		if left < 0 {
			return result
		}
		left += start
		right := strings.Index(value[left+2:], "}}")
		if right < 0 {
			return result
		}
		right += left + 2
		result = append(result, value[left+2:right])
		start = right + 2
	}
}

func Resolve(ctx context.Context, store *catalog.Store, editionID, deviceProfileID string) (LaunchResolution, error) {
	editionID, deviceProfileID = strings.TrimSpace(editionID), strings.TrimSpace(deviceProfileID)
	if editionID == "" || deviceProfileID == "" {
		return LaunchResolution{}, errors.New("edition_id and device_profile_id are required")
	}
	edition, err := store.GetEdition(ctx, editionID, "")
	if err != nil {
		return LaunchResolution{}, err
	}
	game, err := store.GetGame(ctx, edition.GameID, "")
	if err != nil {
		return LaunchResolution{}, err
	}
	device, err := store.GetDeviceProfile(ctx, deviceProfileID)
	if err != nil {
		return LaunchResolution{}, err
	}
	binding, err := store.ResolveLaunchBinding(ctx, editionID, deviceProfileID)
	if err != nil {
		return LaunchResolution{}, err
	}
	driver, err := store.GetEmulatorDriver(ctx, binding.DriverID)
	if err != nil {
		return LaunchResolution{}, err
	}
	if !driver.Enabled || !device.Enabled || !binding.Enabled {
		return LaunchResolution{}, errors.New("launch binding, driver, and device profile must be enabled")
	}
	if !containsString(driver.Platforms, game.Platform) {
		return LaunchResolution{}, fmt.Errorf("driver does not support platform %s", game.Platform)
	}
	if !containsString(driver.Targets, device.Target) {
		return LaunchResolution{}, fmt.Errorf("driver does not support device target %s", device.Target)
	}
	artifact := catalog.SelectLaunchArtifact(edition.Artifacts)
	if artifact == nil {
		return LaunchResolution{}, errors.New("edition has no available launch artifact")
	}
	result := LaunchResolution{EditionID: edition.ID, PlatformID: game.Platform, ROMPath: artifact.Path, Binding: binding, DeviceProfile: device, Driver: driver, Arguments: []string{}, Warnings: []string{}}
	adapterID := binding.FrontendAdapterID
	if adapterID == "" {
		adapterID = device.DefaultFrontendID
	}
	if adapterID != "" {
		adapter, adapterErr := store.GetFrontendAdapter(ctx, adapterID)
		if adapterErr != nil {
			return LaunchResolution{}, adapterErr
		}
		result.FrontendAdapter = &adapter
	}
	coreID, coreLibrary := "", ""
	if binding.CoreID != "" {
		core, coreErr := store.GetRetroArchCore(ctx, binding.CoreID)
		if coreErr != nil {
			return LaunchResolution{}, coreErr
		}
		resolution := catalog.CoreResolution{PlatformID: game.Platform, EditionID: edition.ID, DeviceProfileID: device.ID, Core: &core, Resolution: "launch_binding"}
		result.CoreResolution = &resolution
		coreID = core.ID
		if len(core.LibraryNames) > 0 {
			coreLibrary = core.LibraryNames[0]
		}
	} else if driver.Launch.RequiresCore {
		resolution, coreErr := store.ResolveCore(ctx, game.Platform, edition.ID, device.ID)
		if coreErr != nil {
			return LaunchResolution{}, coreErr
		}
		if resolution.Core == nil {
			return LaunchResolution{}, errors.New("RetroArch driver requires a core mapping")
		}
		result.CoreResolution = &resolution
		coreID = resolution.Core.ID
		if len(resolution.Core.LibraryNames) > 0 {
			coreLibrary = resolution.Core.LibraryNames[0]
		}
	}
	arguments := binding.Arguments
	if len(arguments) == 0 {
		arguments = driver.Launch.Arguments
	}
	titleIDHigh, titleIDLow := titleIDParts(edition.TitleID)
	romBase := filepath.Base(filepath.FromSlash(artifact.Path))
	romStem := strings.TrimSuffix(romBase, filepath.Ext(romBase))
	values := map[string]string{"edition.id": edition.ID, "edition.title": edition.DisplayTitle, "edition.save_namespace": edition.SaveNamespace, "edition.serial": edition.Serial, "edition.product_code": edition.ProductCode, "edition.title_id": edition.TitleID, "edition.title_id_high": titleIDHigh, "edition.title_id_low": titleIDLow, "platform.id": game.Platform, "rom.path": artifact.Path, "rom.source_path": portableSourcePath(artifact.SourcePath), "rom.stem": romStem, "core.id": coreID, "core.library": coreLibrary, "device.id": device.ID, "device.target": device.Target, "device.config_dir": device.Paths["config_dir"], "device.save_dir": device.Paths["save_dir"], "device.core_dir": device.Paths["core_dir"], "device.emulator_dir": device.Paths["emulator_dir"]}
	for _, argument := range arguments {
		rendered, renderErr := renderArgument(argument, values)
		if renderErr != nil {
			return LaunchResolution{}, renderErr
		}
		result.Arguments = append(result.Arguments, rendered)
	}
	result.ExecutableHints = append([]string{}, driver.Launch.Executables[device.Target]...)
	if device.OSFamily == "android" {
		result.AndroidPackage, result.AndroidActivity = driver.Launch.AndroidPackage, driver.Launch.AndroidActivity
		if driver.Launch.AndroidIntent != nil {
			result.AndroidPackage = driver.Launch.AndroidIntent.Package
			result.AndroidActivity = driver.Launch.AndroidIntent.Activity
		}
	}
	if len(result.ExecutableHints) == 0 && result.AndroidPackage == "" {
		result.Warnings = append(result.Warnings, "No executable or Android package is verified for this device; the Device Agent must discover or configure it.")
	}
	return result, nil
}
