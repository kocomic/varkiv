package bundler

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"varkiv/internal/catalog"
	"varkiv/internal/filehash"
	"varkiv/internal/platforms"
	"varkiv/internal/runtimecfg"
)

var ErrUnmanagedTargetConflict = errors.New("package output contains an unmanaged target")

type PlanItem struct {
	Kind      string `json:"kind"`
	EditionID string `json:"edition_id,omitempty"`
	Source    string `json:"source,omitempty"`
	Target    string `json:"target"`
	Action    string `json:"action"`
	Size      int64  `json:"size,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

type Plan struct {
	GeneratedAt         time.Time  `json:"generated_at"`
	Output              string     `json:"output"`
	Profile             Profile    `json:"profile"`
	Fingerprint         string     `json:"fingerprint"`
	Items               []PlanItem `json:"items"`
	ManagedPaths        []string   `json:"managed_paths"`
	Conflicts           []string   `json:"conflicts"`
	Warnings            []string   `json:"warnings"`
	EstimatedWriteBytes int64      `json:"estimated_write_bytes"`
	AvailableBytes      int64      `json:"available_bytes,omitempty"`
	SpaceChecked        bool       `json:"space_checked"`
}

type renderedConfig struct {
	Path string
	Body string
}

type portableLaunchBinding struct {
	DeviceProfileID   string   `json:"device_profile_id,omitempty"`
	FrontendAdapterID string   `json:"frontend_adapter_id,omitempty"`
	DriverID          string   `json:"driver_id"`
	CoreID            string   `json:"core_id,omitempty"`
	Arguments         []string `json:"arguments"`
}

type portableLaunchResolution struct {
	EditionID       string                `json:"edition_id"`
	PlatformID      string                `json:"platform_id"`
	ROMPath         string                `json:"rom_path"`
	Binding         portableLaunchBinding `json:"binding"`
	Arguments       []string              `json:"arguments"`
	ExecutableHints []string              `json:"executable_hints"`
	AndroidPackage  string                `json:"android_package,omitempty"`
	AndroidActivity string                `json:"android_activity,omitempty"`
	Warnings        []string              `json:"warnings"`
}

var placeholderPattern = regexp.MustCompile(`\{\{\s*([a-z][a-z0-9_.]*)\s*\}\}`)
var anyTemplateAction = regexp.MustCompile(`\{\{[^}]*\}\}`)

var allowedTemplateVariables = map[string]bool{
	"profile.name": true, "profile.frontend": true, "profile.target": true, "profile.locale": true, "profile.file_mode": true,
	"platform.id": true, "game.id": true, "game.title": true, "edition.id": true, "edition.title": true, "edition.type": true,
	"edition.save_namespace": true, "edition.serial": true, "edition.product_code": true, "edition.title_id": true,
	"rom.path": true, "rom.source_path": true, "rom.stem": true,
	"device.id": true, "device.target": true, "device.config_dir": true, "device.save_dir": true, "device.rom_dir": true, "device.core_dir": true, "device.emulator_dir": true,
	"driver.id": true, "driver.family": true, "core.id": true, "core.library": true,
	"launch.android_package": true, "launch.android_activity": true, "launch.arguments_json": true, "launch.executable_hints_json": true,
}

// ProfileFromCatalog keeps CLI, API, and package workers on the exact same
// persisted profile contract, including runtime references and reviewed
// configuration templates.
func ProfileFromCatalog(profile catalog.PackageProfile) Profile {
	templates := make([]ConfigTemplate, len(profile.Templates))
	for index, template := range profile.Templates {
		templates[index] = ConfigTemplate{Name: template.Name, Scope: template.Scope, OutputPath: template.OutputPath, Body: template.Body}
	}
	return Profile{
		ID:                profile.ID,
		Name:              profile.Name,
		Frontend:          profile.Frontend,
		Target:            profile.Target,
		DeviceProfileID:   profile.DeviceProfileID,
		FrontendAdapterID: profile.FrontendAdapterID,
		Locale:            profile.Locale,
		FileMode:          profile.FileMode,
		OutputSlug:        profile.OutputSlug,
		Enabled:           profile.Enabled,
		Templates:         templates,
	}
}

func ValidateProfile(profile Profile) (Profile, error) { return normalizeProfile(profile) }

func PlanWithStorage(ctx context.Context, store *catalog.Store, libraryRoot, managedROMRoot, managedMediaRoot, outRoot string, profile Profile) (Plan, error) {
	plan, _, err := planWithStorage(ctx, store, libraryRoot, managedROMRoot, managedMediaRoot, outRoot, profile)
	return plan, err
}

func planWithStorage(ctx context.Context, store *catalog.Store, libraryRoot, managedROMRoot, managedMediaRoot, outRoot string, profile Profile) (Plan, []renderedConfig, error) {
	profile, err := normalizeProfile(profile)
	if err != nil {
		return Plan{}, nil, err
	}
	if profile.FrontendAdapterID != "" {
		adapter, adapterErr := store.GetFrontendAdapter(ctx, profile.FrontendAdapterID)
		if adapterErr != nil {
			return Plan{}, nil, adapterErr
		}
		if !adapter.Enabled || adapter.Handler == "" {
			return Plan{}, nil, errors.New("frontend adapter is disabled or has no audited export handler")
		}
		if adapter.Handler != profile.Frontend {
			return Plan{}, nil, errors.New("frontend adapter handler does not match package frontend")
		}
	}
	platformRegistry, err := store.PlatformRegistry(ctx)
	if err != nil {
		return Plan{}, nil, err
	}
	root, err := filepath.Abs(libraryRoot)
	if err != nil {
		return Plan{}, nil, err
	}
	managedROM, err := filepath.Abs(managedROMRoot)
	if err != nil {
		return Plan{}, nil, err
	}
	managedMedia, err := filepath.Abs(managedMediaRoot)
	if err != nil {
		return Plan{}, nil, err
	}
	out, err := filepath.Abs(outRoot)
	if err != nil {
		return Plan{}, nil, err
	}
	if out == root {
		return Plan{}, nil, errors.New("output must differ from library root")
	}
	owned, hadManifest, err := loadManagedTargets(out)
	if err != nil {
		return Plan{}, nil, err
	}
	plan := Plan{GeneratedAt: time.Now().UTC(), Output: out, Profile: profile, Items: []PlanItem{}, ManagedPaths: []string{}, Conflicts: []string{}, Warnings: []string{}}
	if profile.FileMode == "reference" {
		plan.Warnings = append(plan.Warnings, "reference package contains metadata only; ROM and media paths are relative to the target package root and no content bytes were copied")
	}
	var selectedDevice *catalog.DeviceProfile
	if profile.DeviceProfileID != "" {
		device, deviceErr := store.GetDeviceProfile(ctx, profile.DeviceProfileID)
		if deviceErr != nil {
			return Plan{}, nil, fmt.Errorf("device profile %s: %w", profile.DeviceProfileID, deviceErr)
		}
		selectedDevice = &device
	}
	games, err := store.ListGames(ctx, profile.Locale)
	if err != nil {
		return Plan{}, nil, err
	}
	platforms := map[string]bool{}
	configs := []renderedConfig{}
	launches := []runtimecfg.LaunchResolution{}
	plannedTargets := map[string]PlanItem{}
	addTarget := func(item PlanItem) {
		item.Target = filepath.ToSlash(item.Target)
		if item.Target == recoveryDirectory || strings.HasPrefix(item.Target, recoveryDirectory+"/") || item.Target == legacyRecoveryDirectory || strings.HasPrefix(item.Target, legacyRecoveryDirectory+"/") {
			item.Action = "conflict"
			item.Detail = "target uses the reserved recovery directory"
			plan.Conflicts = append(plan.Conflicts, item.Target)
			plan.Items = append(plan.Items, item)
			return
		}
		if item.Action == "conflict" {
			plan.Conflicts = append(plan.Conflicts, item.Target)
			plan.Items = append(plan.Items, item)
			return
		}
		if item.Action != "missing" {
			if previous, exists := plannedTargets[item.Target]; exists {
				if previous.SHA256 == "" || item.SHA256 == "" || previous.SHA256 != item.SHA256 {
					item.Action = "conflict"
					item.Detail = "multiple planned writers target the same path"
					plan.Conflicts = append(plan.Conflicts, item.Target)
				} else {
					item.Action = "deduplicated"
				}
			} else {
				plannedTargets[item.Target] = item
			}
		}
		if item.Action != "reference" && item.Action != "missing" && item.Action != "conflict" {
			if info, statErr := os.Lstat(filepath.Join(out, filepath.FromSlash(item.Target))); statErr == nil && info != nil {
				expectedHash, isOwned := owned[item.Target]
				switch {
				case !isOwned:
					item.Action = "conflict"
					item.Detail = "target exists but is not owned by a previous Varkiv manifest"
					plan.Conflicts = append(plan.Conflicts, item.Target)
				case expectedHash != "":
					currentHash, _, hashErr := hashFile(filepath.Join(out, filepath.FromSlash(item.Target)))
					if hashErr != nil || currentHash != expectedHash {
						item.Action = "conflict"
						item.Detail = "managed target was modified after the previous release"
						plan.Conflicts = append(plan.Conflicts, item.Target)
					}
				}
			}
			if item.Action != "conflict" {
				plan.ManagedPaths = append(plan.ManagedPaths, item.Target)
			}
		}
		plan.Items = append(plan.Items, item)
	}
	for _, game := range games {
		platforms[game.Platform] = true
		for _, edition := range game.Editions {
			romPath, romSource := "", ""
			launchArtifact := catalog.SelectLaunchArtifact(edition.Artifacts)
			for _, artifact := range edition.Artifacts {
				artifactRoot := root
				if artifact.StorageKind == "managed" {
					artifactRoot = managedROM
				}
				rel, cleanErr := cleanRelative(artifact.Path)
				if cleanErr != nil {
					return Plan{}, nil, cleanErr
				}
				if launchArtifact != nil && artifact.Path == launchArtifact.Path && artifact.Role == launchArtifact.Role && artifact.DiscIndex == launchArtifact.DiscIndex {
					romPath, romSource = filepath.ToSlash(rel), artifact.SourcePath
				}
				source := filepath.Join(artifactRoot, rel)
				info, statErr := os.Lstat(source)
				if statErr != nil || info.Mode()&os.ModeSymlink != 0 {
					addTarget(PlanItem{Kind: "rom", EditionID: edition.ID, Source: filepath.ToSlash(rel), Target: filepath.ToSlash(rel), Action: "missing", Detail: "source is unavailable or is a symbolic link"})
				} else if info.IsDir() {
					if validCatalogSHA256(artifact.SHA256) {
						currentSHA, _, hashErr := filehash.Directory(source)
						if hashErr != nil {
							addTarget(PlanItem{Kind: "rom", EditionID: edition.ID, Source: filepath.ToSlash(rel), Target: filepath.ToSlash(rel), Action: "missing", Detail: "directory artifact cannot be read safely"})
							continue
						}
						if !strings.EqualFold(currentSHA, artifact.SHA256) {
							addTarget(PlanItem{Kind: "rom", EditionID: edition.ID, Source: filepath.ToSlash(rel), Target: filepath.ToSlash(rel), Action: "conflict", Detail: "source content no longer matches the catalog fingerprint; recheck the artifact before exporting"})
							continue
						}
					}
					walkErr := filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
						if walkErr != nil {
							return walkErr
						}
						if entry.IsDir() {
							return nil
						}
						child, relErr := filepath.Rel(artifactRoot, path)
						if relErr != nil {
							return relErr
						}
						addTarget(planFileItem(edition.ID, artifactRoot, out, child, profile.FileMode, owned))
						return nil
					})
					if walkErr != nil {
						return Plan{}, nil, walkErr
					}
				} else {
					item := planFileItem(edition.ID, artifactRoot, out, rel, profile.FileMode, owned)
					if item.Action != "missing" && catalogFingerprintChanged(artifact.SHA256, item.SHA256) {
						item.Action = "conflict"
						item.Detail = "source content no longer matches the catalog fingerprint; recheck the artifact before exporting"
					}
					addTarget(item)
				}
			}
			var launch *runtimecfg.LaunchResolution
			if profile.DeviceProfileID != "" {
				resolution, resolveErr := runtimecfg.Resolve(ctx, store, edition.ID, profile.DeviceProfileID)
				switch {
				case resolveErr == nil:
					launch = &resolution
					launches = append(launches, resolution)
				case errors.Is(resolveErr, sql.ErrNoRows):
					plan.Warnings = append(plan.Warnings, fmt.Sprintf("edition %s has no launch binding for device profile %s", edition.ID, profile.DeviceProfileID))
				default:
					plan.Conflicts = append(plan.Conflicts, "launch:"+edition.ID)
					plan.Items = append(plan.Items, PlanItem{Kind: "launch", EditionID: edition.ID, Target: "varkiv-launches.json", Action: "conflict", Detail: resolveErr.Error()})
				}
			}
			values := templateValues(profile, game, edition, romPath, romSource, selectedDevice, launch)
			for _, template := range profile.Templates {
				if template.Scope != "edition" {
					continue
				}
				config, renderErr := renderConfigTemplate(template, values)
				if renderErr != nil {
					return Plan{}, nil, renderErr
				}
				configs = append(configs, config)
				addTarget(planConfigItem(config, out, owned))
			}
			for _, asset := range append(append([]catalog.MediaAsset{}, game.Media...), edition.Media...) {
				mediaRoot := root
				if asset.StorageKind == "managed" {
					mediaRoot = managedMedia
				}
				rel, cleanErr := cleanRelative(asset.Path)
				if cleanErr != nil {
					return Plan{}, nil, cleanErr
				}
				item := planFileItem(edition.ID, mediaRoot, out, rel, profile.FileMode, owned)
				item.Kind = "media"
				if item.Action != "missing" && catalogFingerprintChanged(asset.SHA256, item.SHA256) {
					item.Action = "conflict"
					item.Detail = "source content no longer matches the catalog fingerprint; recheck the media before exporting"
				}
				addTarget(item)
			}
		}
	}
	platformIDs := make([]string, 0, len(platforms))
	for platform := range platforms {
		platformIDs = append(platformIDs, platform)
	}
	sort.Strings(platformIDs)
	for _, template := range profile.Templates {
		switch template.Scope {
		case "package":
			config, renderErr := renderConfigTemplate(template, templateValues(profile, catalog.Game{}, catalog.Edition{}, "", "", selectedDevice, nil))
			if renderErr != nil {
				return Plan{}, nil, renderErr
			}
			configs = append(configs, config)
			addTarget(planConfigItem(config, out, owned))
		case "platform":
			for _, platform := range platformIDs {
				values := templateValues(profile, catalog.Game{Platform: platform}, catalog.Edition{}, "", "", selectedDevice, nil)
				config, renderErr := renderConfigTemplate(template, values)
				if renderErr != nil {
					return Plan{}, nil, renderErr
				}
				configs = append(configs, config)
				addTarget(planConfigItem(config, out, owned))
			}
		}
	}
	{
		portableRuntime, portableErr := portableRuntimeCatalog(ctx, store, profile, selectedDevice, launches)
		if portableErr != nil {
			return Plan{}, nil, portableErr
		}
		launchManifest := struct {
			FormatVersion     int                            `json:"format_version"`
			DeviceProfileID   string                         `json:"device_profile_id"`
			FrontendAdapterID string                         `json:"frontend_adapter_id,omitempty"`
			Bindings          []portableLaunchResolution     `json:"bindings"`
			RuntimeCatalog    catalog.PortableRuntimeCatalog `json:"runtime_catalog"`
		}{2, profile.DeviceProfileID, profile.FrontendAdapterID, portableLaunches(launches), portableRuntime}
		data, marshalErr := json.MarshalIndent(launchManifest, "", "  ")
		if marshalErr != nil {
			return Plan{}, nil, marshalErr
		}
		config := renderedConfig{Path: "varkiv-launches.json", Body: string(data) + "\n"}
		configs = append(configs, config)
		addTarget(planConfigItem(config, out, owned))
	}
	for _, path := range generatedFrontendPaths(profile.Frontend, platformIDs, platformRegistry) {
		item := PlanItem{Kind: "metadata", Target: path, Action: "generate"}
		if _, exists := owned[path]; hadManifest && !exists {
			// v1 manifests did not list generated metadata, but their presence is
			// safely attributable when a valid prior manifest exists.
			owned[path] = ""
		}
		addTarget(item)
	}
	if selectedDevice != nil {
		applyDevicePreflight(&plan, *selectedDevice, out)
	} else {
		plan.Warnings = append(plan.Warnings, "no device profile selected; target filesystem constraints were not checked")
		applySpacePreflight(&plan, out)
	}
	sort.Strings(plan.ManagedPaths)
	plan.ManagedPaths = uniqueStrings(plan.ManagedPaths)
	sort.Strings(plan.Conflicts)
	plan.Conflicts = uniqueStrings(plan.Conflicts)
	type fingerprintItem struct {
		Kind      string `json:"kind"`
		EditionID string `json:"edition_id,omitempty"`
		Source    string `json:"source,omitempty"`
		Target    string `json:"target"`
		Size      int64  `json:"size,omitempty"`
		SHA256    string `json:"sha256,omitempty"`
	}
	fingerprintItems := make([]fingerprintItem, len(plan.Items))
	for index, item := range plan.Items {
		fingerprintItems[index] = fingerprintItem{item.Kind, item.EditionID, item.Source, item.Target, item.Size, item.SHA256}
	}
	fingerprintInput := struct {
		Profile Profile           `json:"profile"`
		Items   []fingerprintItem `json:"items"`
	}{profile, fingerprintItems}
	data, _ := json.Marshal(fingerprintInput)
	sum := sha256.Sum256(data)
	plan.Fingerprint = hex.EncodeToString(sum[:])
	return plan, configs, nil
}

func portableLaunches(launches []runtimecfg.LaunchResolution) []portableLaunchResolution {
	result := make([]portableLaunchResolution, 0, len(launches))
	for _, item := range launches {
		result = append(result, portableLaunchResolution{
			EditionID: item.EditionID, PlatformID: item.PlatformID, ROMPath: item.ROMPath,
			Binding:   portableLaunchBinding{DeviceProfileID: item.Binding.DeviceProfileID, FrontendAdapterID: item.Binding.FrontendAdapterID, DriverID: item.Binding.DriverID, CoreID: item.Binding.CoreID, Arguments: append([]string{}, item.Binding.Arguments...)},
			Arguments: append([]string{}, item.Arguments...), ExecutableHints: append([]string{}, item.ExecutableHints...), AndroidPackage: item.AndroidPackage, AndroidActivity: item.AndroidActivity, Warnings: append([]string{}, item.Warnings...),
		})
	}
	return result
}

func portablePackageProfile(profile Profile) catalog.NewPackageProfile {
	templates := make([]catalog.NewPackageConfigTemplate, len(profile.Templates))
	for index, item := range profile.Templates {
		templates[index] = catalog.NewPackageConfigTemplate{Name: item.Name, Scope: item.Scope, OutputPath: item.OutputPath, Body: item.Body, SortOrder: (index + 1) * 10}
	}
	id := strings.TrimSpace(profile.ID)
	slug := strings.ToLower(strings.TrimSpace(profile.OutputSlug))
	if id == "" {
		copy := profile
		copy.ID = ""
		data, _ := json.Marshal(copy)
		sum := sha256.Sum256(data)
		id = "portable-package-" + hex.EncodeToString(sum[:8])
	}
	if slug == "" {
		slug = id
	}
	enabled := true
	return catalog.NewPackageProfile{ID: id, Name: profile.Name, Frontend: profile.Frontend, Target: profile.Target, DeviceProfileID: profile.DeviceProfileID, FrontendAdapterID: profile.FrontendAdapterID, Locale: profile.Locale, FileMode: profile.FileMode, OutputSlug: slug, Enabled: &enabled, Templates: templates}
}

func portableRuntimeCatalog(ctx context.Context, store *catalog.Store, profile Profile, selectedDevice *catalog.DeviceProfile, launches []runtimecfg.LaunchResolution) (catalog.PortableRuntimeCatalog, error) {
	result := catalog.PortableRuntimeCatalog{}
	if strings.TrimSpace(profile.ID) != "" {
		stored, err := store.GetPackageProfile(ctx, profile.ID)
		if err != nil {
			return result, fmt.Errorf("portable package profile %s: %w", profile.ID, err)
		}
		portable := catalog.PortablePackageProfile(stored)
		result.PackageProfile = &portable
	} else {
		portable := portablePackageProfile(profile)
		result.PackageProfile = &portable
	}
	frontends, devices, drivers, cores := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	addFrontend := func(item *catalog.FrontendAdapter) {
		if item != nil && !frontends[item.ID] {
			result.FrontendAdapters = append(result.FrontendAdapters, catalog.PortableFrontendAdapter(*item))
			frontends[item.ID] = true
		}
	}
	addDevice := func(item catalog.DeviceProfile) {
		if !devices[item.ID] {
			result.DeviceProfiles = append(result.DeviceProfiles, catalog.PortableDeviceProfile(item))
			devices[item.ID] = true
		}
	}
	addDriver := func(item catalog.EmulatorDriver) {
		if !drivers[item.ID] {
			result.EmulatorDrivers = append(result.EmulatorDrivers, catalog.PortableEmulatorDriver(item))
			drivers[item.ID] = true
		}
	}
	addCore := func(item *catalog.RetroArchCore) {
		if item != nil && !cores[item.ID] {
			result.RetroArchCores = append(result.RetroArchCores, catalog.PortableRetroArchCore(*item))
			cores[item.ID] = true
		}
	}
	if selectedDevice != nil {
		addDevice(*selectedDevice)
		if selectedDevice.DefaultFrontendID != "" {
			adapter, err := store.GetFrontendAdapter(ctx, selectedDevice.DefaultFrontendID)
			if err != nil {
				return result, err
			}
			addFrontend(&adapter)
		}
	}
	if profile.FrontendAdapterID != "" {
		adapter, err := store.GetFrontendAdapter(ctx, profile.FrontendAdapterID)
		if err != nil {
			return result, err
		}
		addFrontend(&adapter)
	}
	for _, launch := range launches {
		addDevice(launch.DeviceProfile)
		addFrontend(launch.FrontendAdapter)
		addDriver(launch.Driver)
		if launch.CoreResolution != nil {
			addCore(launch.CoreResolution.Core)
		}
	}
	sort.Slice(result.FrontendAdapters, func(i, j int) bool { return result.FrontendAdapters[i].ID < result.FrontendAdapters[j].ID })
	sort.Slice(result.DeviceProfiles, func(i, j int) bool { return result.DeviceProfiles[i].ID < result.DeviceProfiles[j].ID })
	sort.Slice(result.EmulatorDrivers, func(i, j int) bool { return result.EmulatorDrivers[i].ID < result.EmulatorDrivers[j].ID })
	sort.Slice(result.RetroArchCores, func(i, j int) bool { return result.RetroArchCores[i].ID < result.RetroArchCores[j].ID })
	return result, nil
}

func normalizeProfile(profile Profile) (Profile, error) {
	profile.Name = strings.TrimSpace(profile.Name)
	profile.Frontend = strings.ToLower(strings.TrimSpace(profile.Frontend))
	profile.Target = strings.ToLower(strings.TrimSpace(profile.Target))
	profile.Locale = strings.TrimSpace(profile.Locale)
	profile.FileMode = strings.ToLower(strings.TrimSpace(profile.FileMode))
	profile.DeviceProfileID = strings.TrimSpace(profile.DeviceProfileID)
	profile.FrontendAdapterID = strings.TrimSpace(profile.FrontendAdapterID)
	if profile.Name == "" {
		return profile, errors.New("profile name is required")
	}
	if profile.Frontend != "pegasus" && profile.Frontend != "es-de" {
		return profile, errors.New("frontend must be pegasus or es-de")
	}
	if profile.FileMode == "" {
		profile.FileMode = "copy"
	}
	if profile.FileMode != "copy" && profile.FileMode != "hardlink" && profile.FileMode != "reference" {
		return profile, errors.New("file_mode must be copy, hardlink, or reference")
	}
	if profile.Locale == "" {
		profile.Locale = "zh-CN"
	}
	for index := range profile.Templates {
		profile.Templates[index].Name = strings.TrimSpace(profile.Templates[index].Name)
		profile.Templates[index].Scope = strings.ToLower(strings.TrimSpace(profile.Templates[index].Scope))
		profile.Templates[index].OutputPath = filepath.ToSlash(strings.TrimSpace(profile.Templates[index].OutputPath))
		if err := validateConfigTemplate(profile.Templates[index]); err != nil {
			return profile, fmt.Errorf("template %d: %w", index, err)
		}
	}
	return profile, nil
}

func validateConfigTemplate(template ConfigTemplate) error {
	if template.Name == "" || template.OutputPath == "" {
		return errors.New("name and output_path are required")
	}
	if template.Scope != "package" && template.Scope != "platform" && template.Scope != "edition" {
		return errors.New("scope must be package, platform, or edition")
	}
	if len(template.Body) > 64*1024 || len(template.OutputPath) > 512 {
		return errors.New("template exceeds the safe size limit")
	}
	if strings.ContainsRune(template.Body, 0) || strings.ContainsRune(template.OutputPath, 0) {
		return errors.New("template must not contain NUL")
	}
	if strings.Contains(template.OutputPath, "\\") || strings.Contains(template.OutputPath, ":") {
		return errors.New("output_path must use a portable relative path")
	}
	if _, err := cleanRelative(template.OutputPath); err != nil {
		return fmt.Errorf("output_path: %w", err)
	}
	allowedExtensions := map[string]bool{".json": true, ".xml": true, ".ini": true, ".cfg": true, ".conf": true, ".toml": true, ".yaml": true, ".yml": true, ".txt": true, ".properties": true, ".opt": true}
	if extension := strings.ToLower(filepath.Ext(template.OutputPath)); !allowedExtensions[extension] {
		return fmt.Errorf("output_path extension %q is not an allowed configuration type", extension)
	}
	for _, value := range []string{template.Body, template.OutputPath} {
		for _, match := range placeholderPattern.FindAllStringSubmatch(value, -1) {
			if !allowedTemplateVariables[match[1]] {
				return fmt.Errorf("unknown template variable %s", match[1])
			}
			if template.Scope == "package" && !strings.HasPrefix(match[1], "profile.") && !strings.HasPrefix(match[1], "device.") {
				return fmt.Errorf("variable %s is unavailable in package scope", match[1])
			}
			if template.Scope == "platform" && !strings.HasPrefix(match[1], "profile.") && !strings.HasPrefix(match[1], "device.") && match[1] != "platform.id" {
				return fmt.Errorf("variable %s is unavailable in platform scope", match[1])
			}
		}
		remaining := placeholderPattern.ReplaceAllString(value, "")
		if anyTemplateAction.MatchString(remaining) {
			return errors.New("only simple {{variable.name}} placeholders are allowed")
		}
	}
	return nil
}

func renderConfigTemplate(template ConfigTemplate, values map[string]string) (renderedConfig, error) {
	render := func(value string) (string, error) {
		var unavailable string
		rendered := placeholderPattern.ReplaceAllStringFunc(value, func(match string) string {
			parts := placeholderPattern.FindStringSubmatch(match)
			resolved := values[parts[1]]
			if strings.TrimSpace(resolved) == "" && unavailable == "" {
				unavailable = parts[1]
			}
			return resolved
		})
		if unavailable != "" {
			return "", fmt.Errorf("template %q requires unavailable %s", template.Name, unavailable)
		}
		return rendered, nil
	}
	path, err := render(template.OutputPath)
	if err != nil {
		return renderedConfig{}, err
	}
	if strings.Contains(path, "\\") || strings.Contains(path, ":") {
		return renderedConfig{}, fmt.Errorf("template %q output must use a portable relative path", template.Name)
	}
	rel, err := cleanRelative(path)
	if err != nil {
		return renderedConfig{}, fmt.Errorf("template %q output: %w", template.Name, err)
	}
	extension := strings.ToLower(filepath.Ext(rel))
	allowedExtensions := map[string]bool{".json": true, ".xml": true, ".ini": true, ".cfg": true, ".conf": true, ".toml": true, ".yaml": true, ".yml": true, ".txt": true, ".properties": true, ".opt": true}
	if !allowedExtensions[extension] {
		return renderedConfig{}, fmt.Errorf("template %q output extension %q is not an allowed configuration type", template.Name, extension)
	}
	body, err := render(template.Body)
	if err != nil {
		return renderedConfig{}, err
	}
	return renderedConfig{Path: filepath.ToSlash(rel), Body: body}, nil
}

func templateValues(profile Profile, game catalog.Game, edition catalog.Edition, romPath, romSource string, device *catalog.DeviceProfile, launch *runtimecfg.LaunchResolution) map[string]string {
	values := map[string]string{
		"profile.name": profile.Name, "profile.frontend": profile.Frontend, "profile.target": profile.Target, "profile.locale": profile.Locale, "profile.file_mode": profile.FileMode,
		"platform.id": game.Platform, "game.id": game.ID, "game.title": game.DisplayTitle, "edition.id": edition.ID, "edition.title": edition.DisplayTitle, "edition.type": edition.EditionType,
		"edition.save_namespace": edition.SaveNamespace, "edition.serial": edition.Serial, "edition.product_code": edition.ProductCode, "edition.title_id": edition.TitleID,
		"rom.path": romPath, "rom.source_path": safeTemplateSourcePath(romSource), "rom.stem": strings.TrimSuffix(filepath.Base(filepath.FromSlash(romPath)), filepath.Ext(filepath.FromSlash(romPath))),
	}
	if device != nil {
		values["device.id"], values["device.target"] = device.ID, device.Target
		for _, key := range []string{"config_dir", "save_dir", "rom_dir", "core_dir", "emulator_dir"} {
			values["device."+key] = device.Paths[key]
		}
	}
	if launch != nil {
		values["driver.id"], values["driver.family"] = launch.Driver.ID, launch.Driver.Family
		argumentsJSON, _ := json.Marshal(launch.Arguments)
		executablesJSON, _ := json.Marshal(launch.ExecutableHints)
		values["launch.arguments_json"], values["launch.executable_hints_json"] = string(argumentsJSON), string(executablesJSON)
		if launch.CoreResolution != nil && launch.CoreResolution.Core != nil {
			values["core.id"] = launch.CoreResolution.Core.ID
			if len(launch.CoreResolution.Core.LibraryNames) > 0 {
				values["core.library"] = launch.CoreResolution.Core.LibraryNames[0]
			}
		}
		values["launch.android_package"], values["launch.android_activity"] = launch.AndroidPackage, launch.AndroidActivity
	}
	return values
}

func safeTemplateSourcePath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, `\`) || strings.Contains(value, ":") {
		return ""
	}
	rel, err := cleanRelative(value)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(rel)
}

func validCatalogSHA256(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func catalogFingerprintChanged(expected, current string) bool {
	return validCatalogSHA256(expected) && !strings.EqualFold(strings.TrimSpace(expected), strings.TrimSpace(current))
}

func planFileItem(editionID, root, out, rel, mode string, owned map[string]string) PlanItem {
	target := filepath.ToSlash(rel)
	item := PlanItem{Kind: "rom", EditionID: editionID, Source: target, Target: target, Action: mode}
	source := filepath.Join(root, rel)
	info, err := os.Lstat(source)
	if err != nil || info.IsDir() {
		item.Action, item.Detail = "missing", "source file is unavailable or is a directory"
		return item
	}
	if info.Mode()&os.ModeSymlink != 0 {
		item.Action, item.Detail = "missing", "symbolic links are not exported"
		return item
	}
	item.SHA256, item.Size, err = hashFile(source)
	if err != nil {
		item.Action, item.Detail = "missing", err.Error()
		return item
	}
	if mode == "reference" {
		return item
	}
	_, isOwned := owned[target]
	if same, _ := sameFile(filepath.Join(out, rel), item.Size, item.SHA256); same && isOwned {
		item.Action = "unchanged"
	}
	return item
}

func planConfigItem(config renderedConfig, out string, owned map[string]string) PlanItem {
	data := []byte(config.Body)
	sum := sha256.Sum256(data)
	item := PlanItem{Kind: "config", Target: config.Path, Action: "generate", Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}
	_, isOwned := owned[config.Path]
	if same, _ := sameFile(filepath.Join(out, filepath.FromSlash(config.Path)), item.Size, item.SHA256); same && isOwned {
		item.Action = "unchanged"
	}
	return item
}

func generatedFrontendPaths(frontend string, platformIDs []string, registry platforms.Registry) []string {
	paths := []string{"library-manifest.json", "package-manifest.json"}
	for _, platform := range platformIDs {
		if frontend == "pegasus" {
			paths = append(paths, filepath.ToSlash(filepath.Join(platform, "metadata.pegasus.txt")))
		} else {
			frontendSystem := platform
			if preset, ok := registry.Resolve(platform); ok && len(preset.ESDESystems) > 0 {
				frontendSystem = preset.ESDESystems[0]
			}
			paths = append(paths, filepath.ToSlash(filepath.Join("gamelists", frontendSystem, "gamelist.xml")))
		}
	}
	return paths
}

func loadManagedTargets(out string) (map[string]string, bool, error) {
	owned := map[string]string{}
	path := filepath.Join(out, "package-manifest.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return owned, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var manifest Manifest
	if err = json.Unmarshal(data, &manifest); err != nil {
		return nil, false, fmt.Errorf("existing package manifest is invalid; refusing to overwrite unmanaged output: %w", err)
	}
	// Reference records are audit evidence only. They never grant ownership of
	// a path because the package did not create or manage those content bytes.
	// This makes a later copy build refuse to overwrite a target tree that the
	// user supplied after generating reference metadata.
	if manifest.Profile.FileMode != "reference" {
		for _, record := range manifest.Files {
			owned[filepath.ToSlash(record.Target)] = record.SHA256
		}
	}
	for _, target := range manifest.ManagedPaths {
		path := filepath.ToSlash(target)
		if _, exists := owned[path]; !exists {
			owned[path] = ""
		}
	}
	for _, record := range manifest.ManagedRecords {
		owned[filepath.ToSlash(record.Path)] = record.SHA256
	}
	return owned, true, nil
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return values
	}
	out := values[:0]
	for index, value := range values {
		if index == 0 || value != values[index-1] {
			out = append(out, value)
		}
	}
	return out
}
