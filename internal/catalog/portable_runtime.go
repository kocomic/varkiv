package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	ErrRuntimeDefinitionConflict = errors.New("portable runtime definition conflicts with the existing catalog")
	ErrRuntimeDefinitionDisabled = errors.New("portable runtime definition already exists but is disabled")
)

const (
	maxPortableRuntimeDefinitions = 128
	maxPortablePackageTemplates   = 64
)

func portableEnabled() *bool { value := true; return &value }

func validatePortableDefinitionID(value string) error {
	if value == "" || len(value) > 128 {
		return errors.New("portable definition id must contain between 1 and 128 characters")
	}
	for index, char := range value {
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || (index > 0 && strings.ContainsRune("._:-", char))) {
			return errors.New("portable definition id may contain only letters, numbers, dot, dash, underscore, and colon")
		}
	}
	return nil
}

func requirePortableEnabled(enabled *bool) error {
	if enabled != nil && !*enabled {
		return errors.New("portable definitions must be enabled")
	}
	return nil
}

func PortableFrontendAdapter(item FrontendAdapter) NewFrontendAdapter {
	return NewFrontendAdapter{ID: item.ID, Name: item.Name, Format: item.Format, Handler: item.Handler, ContractVersion: item.ContractVersion, Capabilities: item.Capabilities, SupportLevel: "catalogued", Evidence: map[string]any{}, Enabled: portableEnabled()}
}

func PortableDeviceProfile(item DeviceProfile) NewDeviceProfile {
	caseSensitive := item.CaseSensitive
	return NewDeviceProfile{ID: item.ID, Name: item.Name, ContractVersion: item.ContractVersion, Target: item.Target, OSFamily: item.OSFamily, Distribution: item.Distribution, Architecture: item.Architecture, PathStyle: item.PathStyle, CaseSensitive: &caseSensitive, MaxPath: item.MaxPath, IllegalCharacters: item.IllegalCharacters, SupportsHardlink: item.SupportsHardlink, SupportsHooks: item.SupportsHooks, DefaultFrontendID: item.DefaultFrontendID, Paths: item.Paths, SupportLevel: "catalogued", Evidence: map[string]any{}, Enabled: portableEnabled()}
}

func PortableEmulatorDriver(item EmulatorDriver) NewEmulatorDriver {
	return NewEmulatorDriver{ID: item.ID, Name: item.Name, Family: item.Family, ContractVersion: item.ContractVersion, Platforms: item.Platforms, Targets: item.Targets, Launch: item.Launch, Save: item.Save, ConfigPaths: item.ConfigPaths, SupportLevel: "catalogued", Evidence: map[string]any{}, Enabled: portableEnabled()}
}

func PortableRetroArchCore(item RetroArchCore) NewRetroArchCore {
	return NewRetroArchCore{ID: item.ID, Name: item.Name, ContractVersion: item.ContractVersion, LibraryNames: item.LibraryNames, Platforms: item.Platforms, SupportLevel: "catalogued", Evidence: map[string]any{}, Enabled: portableEnabled()}
}

func PortablePackageProfile(item PackageProfile) NewPackageProfile {
	templates := make([]NewPackageConfigTemplate, len(item.Templates))
	for index, template := range item.Templates {
		templates[index] = NewPackageConfigTemplate{Name: template.Name, Scope: template.Scope, OutputPath: template.OutputPath, Body: template.Body, SortOrder: template.SortOrder}
	}
	return NewPackageProfile{ID: item.ID, Name: item.Name, Frontend: item.Frontend, Target: item.Target, DeviceProfileID: item.DeviceProfileID, FrontendAdapterID: item.FrontendAdapterID, Locale: item.Locale, FileMode: item.FileMode, OutputSlug: item.OutputSlug, Enabled: portableEnabled(), Templates: templates}
}

func canonicalJSON(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func normalizePortableFrontend(in NewFrontendAdapter) (NewFrontendAdapter, error) {
	if err := requirePortableEnabled(in.Enabled); err != nil {
		return in, err
	}
	in.Builtin, in.Enabled, in.SupportLevel, in.Evidence = false, portableEnabled(), "catalogued", map[string]any{}
	if strings.TrimSpace(in.ID) == "" {
		return in, errors.New("portable frontend adapter id is required")
	}
	in.ID = strings.TrimSpace(in.ID)
	if err := validatePortableDefinitionID(in.ID); err != nil {
		return in, err
	}
	return normalizeFrontendAdapter(in)
}

func normalizePortableDevice(in NewDeviceProfile) (NewDeviceProfile, error) {
	if err := requirePortableEnabled(in.Enabled); err != nil {
		return in, err
	}
	in.Builtin, in.Enabled, in.SupportLevel, in.Evidence = false, portableEnabled(), "catalogued", map[string]any{}
	if strings.TrimSpace(in.ID) == "" {
		return in, errors.New("portable device profile id is required")
	}
	in.ID = strings.TrimSpace(in.ID)
	if err := validatePortableDefinitionID(in.ID); err != nil {
		return in, err
	}
	return normalizeDeviceProfile(in)
}

func normalizePortableDriver(in NewEmulatorDriver) (NewEmulatorDriver, error) {
	if err := requirePortableEnabled(in.Enabled); err != nil {
		return in, err
	}
	in.Builtin, in.Enabled, in.SupportLevel, in.Evidence = false, portableEnabled(), "catalogued", map[string]any{}
	if strings.TrimSpace(in.ID) == "" {
		return in, errors.New("portable emulator driver id is required")
	}
	in.ID = strings.TrimSpace(in.ID)
	if err := validatePortableDefinitionID(in.ID); err != nil {
		return in, err
	}
	return normalizeEmulatorDriver(in)
}

func normalizePortableCore(in NewRetroArchCore) (NewRetroArchCore, error) {
	if err := requirePortableEnabled(in.Enabled); err != nil {
		return in, err
	}
	in.Builtin, in.Enabled, in.SupportLevel, in.Evidence = false, portableEnabled(), "catalogued", map[string]any{}
	if strings.TrimSpace(in.ID) == "" {
		return in, errors.New("portable RetroArch core id is required")
	}
	in.ID = strings.TrimSpace(in.ID)
	if err := validatePortableDefinitionID(in.ID); err != nil {
		return in, err
	}
	return normalizeRetroArchCore(in)
}

func normalizePortableProfile(in NewPackageProfile) (NewPackageProfile, error) {
	if err := requirePortableEnabled(in.Enabled); err != nil {
		return in, err
	}
	if strings.TrimSpace(in.ID) == "" {
		return in, errors.New("portable package profile id is required")
	}
	if len(in.Templates) > maxPortablePackageTemplates {
		return in, fmt.Errorf("portable package profile exceeds %d templates", maxPortablePackageTemplates)
	}
	in.ID, in.Enabled, in.Builtin = strings.TrimSpace(in.ID), portableEnabled(), false
	if err := validatePortableDefinitionID(in.ID); err != nil {
		return in, err
	}
	for index := range in.Templates {
		in.Templates[index].ID = ""
	}
	return normalizePackageProfile(in)
}

type normalizedPortableRuntimeCatalog struct {
	PortableRuntimeCatalog
	ExistingFrontend map[string]bool
	ExistingDevice   map[string]bool
	ExistingDriver   map[string]bool
	ExistingCore     map[string]bool
	ExistingProfile  bool
}

func addUnique[T any](kind, id string, item T, values *[]T, seen map[string]string) error {
	encoded := canonicalJSON(item)
	if previous, ok := seen[id]; ok {
		if previous != encoded {
			return fmt.Errorf("%w: repeated %s %s has different definitions", ErrRuntimeDefinitionConflict, kind, id)
		}
		return nil
	}
	seen[id] = encoded
	*values = append(*values, item)
	return nil
}

// MergePortableRuntimeCatalogs de-duplicates definitions repeated on selected
// import candidates and rejects divergent copies before any database write.
func MergePortableRuntimeCatalogs(games []ImportedGame) (PortableRuntimeCatalog, error) {
	result := PortableRuntimeCatalog{}
	frontends, devices, drivers, cores := map[string]string{}, map[string]string{}, map[string]string{}, map[string]string{}
	var profileJSON string
	for _, game := range games {
		if game.RuntimeCatalog == nil {
			continue
		}
		for _, item := range game.RuntimeCatalog.FrontendAdapters {
			if err := addUnique("frontend adapter", item.ID, item, &result.FrontendAdapters, frontends); err != nil {
				return result, err
			}
		}
		for _, item := range game.RuntimeCatalog.DeviceProfiles {
			if err := addUnique("device profile", item.ID, item, &result.DeviceProfiles, devices); err != nil {
				return result, err
			}
		}
		for _, item := range game.RuntimeCatalog.EmulatorDrivers {
			if err := addUnique("emulator driver", item.ID, item, &result.EmulatorDrivers, drivers); err != nil {
				return result, err
			}
		}
		for _, item := range game.RuntimeCatalog.RetroArchCores {
			if err := addUnique("RetroArch core", item.ID, item, &result.RetroArchCores, cores); err != nil {
				return result, err
			}
		}
		if profile := game.RuntimeCatalog.PackageProfile; profile != nil {
			encoded := canonicalJSON(profile)
			if profileJSON != "" && profileJSON != encoded {
				return result, fmt.Errorf("%w: repeated package profile differs", ErrRuntimeDefinitionConflict)
			}
			copy := *profile
			result.PackageProfile, profileJSON = &copy, encoded
		}
	}
	return result, nil
}

func portableDefinitionCount(in PortableRuntimeCatalog) int {
	count := len(in.FrontendAdapters) + len(in.DeviceProfiles) + len(in.EmulatorDrivers) + len(in.RetroArchCores)
	if in.PackageProfile != nil {
		count++
	}
	return count
}

func enabledOrConflict(kind, id string, enabled bool) error {
	if !enabled {
		return fmt.Errorf("%w: %s %s", ErrRuntimeDefinitionDisabled, kind, id)
	}
	return nil
}

// ValidatePortableRuntimeCatalogImports normalizes portable definitions and
// checks exact reusable identity without changing local catalog state.
func (s *Store) ValidatePortableRuntimeCatalogImports(ctx context.Context, input PortableRuntimeCatalog) (normalizedPortableRuntimeCatalog, error) {
	result := normalizedPortableRuntimeCatalog{ExistingFrontend: map[string]bool{}, ExistingDevice: map[string]bool{}, ExistingDriver: map[string]bool{}, ExistingCore: map[string]bool{}}
	if portableDefinitionCount(input) > maxPortableRuntimeDefinitions {
		return result, fmt.Errorf("portable runtime catalog exceeds %d definitions", maxPortableRuntimeDefinitions)
	}
	existingFrontends, err := s.ListFrontendAdapters(ctx)
	if err != nil {
		return result, err
	}
	frontendFormatOwner := map[string]string{}
	for _, item := range existingFrontends {
		if item.Enabled {
			frontendFormatOwner[item.Format] = item.ID
		}
	}
	for _, raw := range input.FrontendAdapters {
		item, err := normalizePortableFrontend(raw)
		if err != nil {
			return result, fmt.Errorf("portable frontend adapter: %w", err)
		}
		if current, getErr := s.GetFrontendAdapter(ctx, item.ID); getErr == nil {
			if reservedBuiltinID(item.ID) && !current.Builtin {
				return result, fmt.Errorf("%w: frontend adapter %s does not resolve to a built-in definition", ErrRuntimeDefinitionConflict, item.ID)
			}
			if err = enabledOrConflict("frontend adapter", item.ID, current.Enabled); err != nil {
				return result, err
			}
			if canonicalJSON(PortableFrontendAdapter(current)) != canonicalJSON(item) {
				return result, fmt.Errorf("%w: frontend adapter %s", ErrRuntimeDefinitionConflict, item.ID)
			}
			result.ExistingFrontend[item.ID] = true
		} else if !errors.Is(getErr, sql.ErrNoRows) {
			return result, getErr
		} else if reservedBuiltinID(item.ID) {
			return result, fmt.Errorf("%w: built-in frontend adapter %s is unavailable", ErrRuntimeDefinitionConflict, item.ID)
		}
		if owner := frontendFormatOwner[item.Format]; owner != "" && owner != item.ID {
			return result, fmt.Errorf("%w: frontend format %s is already owned by %s", ErrRuntimeDefinitionConflict, item.Format, owner)
		}
		frontendFormatOwner[item.Format] = item.ID
		result.FrontendAdapters = append(result.FrontendAdapters, item)
	}
	for _, raw := range input.DeviceProfiles {
		item, err := normalizePortableDevice(raw)
		if err != nil {
			return result, fmt.Errorf("portable device profile: %w", err)
		}
		if current, getErr := s.GetDeviceProfile(ctx, item.ID); getErr == nil {
			if reservedBuiltinID(item.ID) && !current.Builtin {
				return result, fmt.Errorf("%w: device profile %s does not resolve to a built-in definition", ErrRuntimeDefinitionConflict, item.ID)
			}
			if err = enabledOrConflict("device profile", item.ID, current.Enabled); err != nil {
				return result, err
			}
			if canonicalJSON(PortableDeviceProfile(current)) != canonicalJSON(item) {
				return result, fmt.Errorf("%w: device profile %s", ErrRuntimeDefinitionConflict, item.ID)
			}
			result.ExistingDevice[item.ID] = true
		} else if !errors.Is(getErr, sql.ErrNoRows) {
			return result, getErr
		} else if reservedBuiltinID(item.ID) {
			return result, fmt.Errorf("%w: built-in device profile %s is unavailable", ErrRuntimeDefinitionConflict, item.ID)
		}
		result.DeviceProfiles = append(result.DeviceProfiles, item)
	}
	for _, raw := range input.EmulatorDrivers {
		item, err := normalizePortableDriver(raw)
		if err != nil {
			return result, fmt.Errorf("portable emulator driver: %w", err)
		}
		if current, getErr := s.GetEmulatorDriver(ctx, item.ID); getErr == nil {
			if reservedBuiltinID(item.ID) && !current.Builtin {
				return result, fmt.Errorf("%w: emulator driver %s does not resolve to a built-in definition", ErrRuntimeDefinitionConflict, item.ID)
			}
			if err = enabledOrConflict("emulator driver", item.ID, current.Enabled); err != nil {
				return result, err
			}
			if canonicalJSON(PortableEmulatorDriver(current)) != canonicalJSON(item) {
				return result, fmt.Errorf("%w: emulator driver %s", ErrRuntimeDefinitionConflict, item.ID)
			}
			result.ExistingDriver[item.ID] = true
		} else if !errors.Is(getErr, sql.ErrNoRows) {
			return result, getErr
		} else if reservedBuiltinID(item.ID) {
			return result, fmt.Errorf("%w: built-in emulator driver %s is unavailable", ErrRuntimeDefinitionConflict, item.ID)
		}
		result.EmulatorDrivers = append(result.EmulatorDrivers, item)
	}
	for _, raw := range input.RetroArchCores {
		item, err := normalizePortableCore(raw)
		if err != nil {
			return result, fmt.Errorf("portable RetroArch core: %w", err)
		}
		if current, getErr := s.GetRetroArchCore(ctx, item.ID); getErr == nil {
			if reservedBuiltinID(item.ID) && !current.Builtin {
				return result, fmt.Errorf("%w: RetroArch core %s does not resolve to a built-in definition", ErrRuntimeDefinitionConflict, item.ID)
			}
			if err = enabledOrConflict("RetroArch core", item.ID, current.Enabled); err != nil {
				return result, err
			}
			if canonicalJSON(PortableRetroArchCore(current)) != canonicalJSON(item) {
				return result, fmt.Errorf("%w: RetroArch core %s", ErrRuntimeDefinitionConflict, item.ID)
			}
			result.ExistingCore[item.ID] = true
		} else if !errors.Is(getErr, sql.ErrNoRows) {
			return result, getErr
		} else if reservedBuiltinID(item.ID) {
			return result, fmt.Errorf("%w: built-in RetroArch core %s is unavailable", ErrRuntimeDefinitionConflict, item.ID)
		}
		result.RetroArchCores = append(result.RetroArchCores, item)
	}
	if input.PackageProfile != nil {
		profile, err := normalizePortableProfile(*input.PackageProfile)
		if err != nil {
			return result, fmt.Errorf("portable package profile: %w", err)
		}
		if current, getErr := s.GetPackageProfile(ctx, profile.ID); getErr == nil {
			if reservedBuiltinID(profile.ID) && !current.Builtin {
				return result, fmt.Errorf("%w: package profile %s does not resolve to a built-in definition", ErrRuntimeDefinitionConflict, profile.ID)
			}
			if err = enabledOrConflict("package profile", profile.ID, current.Enabled); err != nil {
				return result, err
			}
			if canonicalJSON(PortablePackageProfile(current)) != canonicalJSON(profile) {
				return result, fmt.Errorf("%w: package profile %s", ErrRuntimeDefinitionConflict, profile.ID)
			}
			result.ExistingProfile = true
		} else if !errors.Is(getErr, sql.ErrNoRows) {
			return result, getErr
		} else if reservedBuiltinID(profile.ID) {
			return result, fmt.Errorf("%w: built-in package profile %s is unavailable", ErrRuntimeDefinitionConflict, profile.ID)
		}
		profiles, listErr := s.ListPackageProfiles(ctx)
		if listErr != nil {
			return result, listErr
		}
		for _, current := range profiles {
			if current.ID != profile.ID && current.OutputSlug == profile.OutputSlug {
				return result, fmt.Errorf("%w: package output slug %s is already owned by %s", ErrRuntimeDefinitionConflict, profile.OutputSlug, current.ID)
			}
		}
		result.PackageProfile = &profile
	}
	availableFrontend, availableDevice := map[string]bool{}, map[string]bool{}
	for _, item := range result.FrontendAdapters {
		availableFrontend[item.ID] = true
	}
	for _, item := range result.DeviceProfiles {
		availableDevice[item.ID] = true
	}
	for _, item := range result.DeviceProfiles {
		if item.DefaultFrontendID != "" && !availableFrontend[item.DefaultFrontendID] {
			if current, err := s.GetFrontendAdapter(ctx, item.DefaultFrontendID); err != nil || !current.Enabled {
				return result, fmt.Errorf("portable device profile %s references unavailable frontend adapter %s", item.ID, item.DefaultFrontendID)
			}
		}
	}
	if profile := result.PackageProfile; profile != nil {
		var deviceTarget, frontendHandler string
		for _, item := range result.DeviceProfiles {
			if item.ID == profile.DeviceProfileID {
				deviceTarget = item.Target
			}
		}
		for _, item := range result.FrontendAdapters {
			if item.ID == profile.FrontendAdapterID {
				frontendHandler = item.Handler
			}
		}
		if profile.DeviceProfileID != "" && !availableDevice[profile.DeviceProfileID] {
			if current, err := s.GetDeviceProfile(ctx, profile.DeviceProfileID); err != nil || !current.Enabled {
				return result, fmt.Errorf("portable package profile references unavailable device profile %s", profile.DeviceProfileID)
			} else {
				deviceTarget = current.Target
			}
		}
		if profile.FrontendAdapterID != "" && !availableFrontend[profile.FrontendAdapterID] {
			if current, err := s.GetFrontendAdapter(ctx, profile.FrontendAdapterID); err != nil || !current.Enabled {
				return result, fmt.Errorf("portable package profile references unavailable frontend adapter %s", profile.FrontendAdapterID)
			} else {
				frontendHandler = current.Handler
			}
		}
		if profile.DeviceProfileID != "" && deviceTarget != profile.Target {
			return result, errors.New("portable package profile target does not match its device profile")
		}
		if profile.FrontendAdapterID != "" && (frontendHandler == "" || frontendHandler != profile.Frontend) {
			return result, errors.New("portable package profile frontend does not match its frontend adapter")
		}
	}
	sort.Slice(result.FrontendAdapters, func(i, j int) bool { return result.FrontendAdapters[i].ID < result.FrontendAdapters[j].ID })
	sort.Slice(result.DeviceProfiles, func(i, j int) bool { return result.DeviceProfiles[i].ID < result.DeviceProfiles[j].ID })
	sort.Slice(result.EmulatorDrivers, func(i, j int) bool { return result.EmulatorDrivers[i].ID < result.EmulatorDrivers[j].ID })
	sort.Slice(result.RetroArchCores, func(i, j int) bool { return result.RetroArchCores[i].ID < result.RetroArchCores[j].ID })
	return result, nil
}

func insertPortableRuntimeCatalogTx(ctx context.Context, tx *sql.Tx, in normalizedPortableRuntimeCatalog) error {
	now := nowText()
	for _, item := range in.FrontendAdapters {
		if !in.ExistingFrontend[item.ID] {
			if _, err := tx.ExecContext(ctx, `INSERT INTO frontend_adapters(id,name,format,handler,contract_version,capabilities_json,support_level,evidence_json,builtin,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, item.ID, item.Name, item.Format, item.Handler, item.ContractVersion, jsonText(item.Capabilities, "{}"), item.SupportLevel, "{}", 0, 1, now, now); err != nil {
				return err
			}
		}
	}
	for _, item := range in.DeviceProfiles {
		if !in.ExistingDevice[item.ID] {
			caseSensitive := item.PathStyle != "windows"
			if item.CaseSensitive != nil {
				caseSensitive = *item.CaseSensitive
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO device_profiles(id,name,contract_version,target,os_family,distribution,architecture,path_style,case_sensitive,max_path,illegal_characters,supports_hardlink,supports_hooks,default_frontend_id,paths_json,support_level,evidence_json,builtin,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,NULLIF(?,''),?,?,?,?,?,?,?)`, item.ID, item.Name, item.ContractVersion, item.Target, item.OSFamily, item.Distribution, item.Architecture, item.PathStyle, boolInt(caseSensitive), item.MaxPath, item.IllegalCharacters, boolInt(item.SupportsHardlink), boolInt(item.SupportsHooks), item.DefaultFrontendID, jsonText(item.Paths, "{}"), item.SupportLevel, "{}", 0, 1, now, now); err != nil {
				return err
			}
		}
	}
	for _, item := range in.EmulatorDrivers {
		if !in.ExistingDriver[item.ID] {
			if _, err := tx.ExecContext(ctx, `INSERT INTO emulator_drivers(id,name,family,contract_version,platforms_json,targets_json,launch_json,save_json,config_paths_json,support_level,evidence_json,builtin,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, item.ID, item.Name, item.Family, item.ContractVersion, jsonText(item.Platforms, "[]"), jsonText(item.Targets, "[]"), jsonText(item.Launch, "{}"), jsonText(item.Save, "{}"), jsonText(item.ConfigPaths, "{}"), item.SupportLevel, "{}", 0, 1, now, now); err != nil {
				return err
			}
		}
	}
	for _, item := range in.RetroArchCores {
		if !in.ExistingCore[item.ID] {
			if _, err := tx.ExecContext(ctx, `INSERT INTO retroarch_cores(id,name,contract_version,library_names_json,platforms_json,support_level,evidence_json,builtin,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, item.ID, item.Name, item.ContractVersion, jsonText(item.LibraryNames, "[]"), jsonText(item.Platforms, "[]"), item.SupportLevel, "{}", 0, 1, now, now); err != nil {
				return err
			}
		}
	}
	if profile := in.PackageProfile; profile != nil && !in.ExistingProfile {
		if _, err := tx.ExecContext(ctx, `INSERT INTO package_profiles(id,name,frontend,target,locale,file_mode,output_slug,enabled,builtin,created_at,updated_at,device_profile_id,frontend_adapter_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, profile.ID, profile.Name, profile.Frontend, profile.Target, profile.Locale, profile.FileMode, profile.OutputSlug, 1, 0, now, now, profile.DeviceProfileID, profile.FrontendAdapterID); err != nil {
			return fmt.Errorf("portable package profile: %w", err)
		}
		if err := replacePackageTemplates(ctx, tx, profile.ID, profile.Templates); err != nil {
			return err
		}
	}
	return nil
}
