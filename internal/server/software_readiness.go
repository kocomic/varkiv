package server

import (
	"context"
	"reflect"
	"sort"

	"varkiv/internal/catalog"
	"varkiv/internal/platforms"
)

// SoftwareReadinessGate reports only public built-in component IDs. It never
// includes user-created objects, evidence payloads, paths, devices, or tokens.
type SoftwareReadinessGate struct {
	ID       string   `json:"id"`
	Status   string   `json:"status"`
	Missing  []string `json:"missing"`
	Disabled []string `json:"disabled"`
	Drifted  []string `json:"drifted"`
}

type SoftwareReadinessReport struct {
	Ready bool                    `json:"ready"`
	Gates []SoftwareReadinessGate `json:"gates"`
}

func readinessGate(id string, missing, disabled, drifted []string) SoftwareReadinessGate {
	if missing == nil {
		missing = []string{}
	}
	if disabled == nil {
		disabled = []string{}
	}
	if drifted == nil {
		drifted = []string{}
	}
	for _, values := range [][]string{missing, disabled, drifted} {
		sort.Strings(values)
	}
	status := "ready"
	if len(missing)+len(disabled)+len(drifted) != 0 {
		status = "pending"
	}
	return SoftwareReadinessGate{ID: id, Status: status, Missing: missing, Disabled: disabled, Drifted: drifted}
}

func SoftwareReadiness(ctx context.Context, store *catalog.Store) (SoftwareReadinessReport, error) {
	gates := make([]SoftwareReadinessGate, 0, 7)

	platformItems := platforms.All()
	platformByID := make(map[string]platforms.Platform, len(platformItems))
	for _, item := range platformItems {
		platformByID[item.ID] = item
	}
	platformMissing, platformDrifted := []string{}, []string{}
	for _, id := range []string{"arcade", "gba", "nds", "3ds", "gamecube", "wii", "wiiu", "psx", "ps2", "ps3", "psp", "psvita", "dreamcast"} {
		item, ok := platformByID[id]
		if !ok {
			platformMissing = append(platformMissing, id)
			continue
		}
		if !item.Builtin || !item.Enabled || len(item.Extensions) == 0 || len(item.ESDESystems) == 0 {
			platformDrifted = append(platformDrifted, id)
		}
	}
	gates = append(gates, readinessGate("platform-registry", platformMissing, nil, platformDrifted))

	sources, err := store.ListSourceAdapters(ctx)
	if err != nil {
		return SoftwareReadinessReport{}, err
	}
	sourceByID := make(map[string]catalog.SourceAdapter, len(sources))
	for _, item := range sources {
		sourceByID[item.ID] = item
	}
	missing, disabled, drifted := []string{}, []string{}, []string{}
	for _, expected := range builtinSourceAdapters() {
		actual, ok := sourceByID[expected.ID]
		if !ok {
			missing = append(missing, expected.ID)
			continue
		}
		if !actual.Enabled {
			disabled = append(disabled, expected.ID)
		}
		if !actual.Builtin || actual.ContractVersion != expected.ContractVersion || actual.Format != expected.Format || actual.Handler != expected.Handler || !reflect.DeepEqual(actual.Capabilities, expected.Capabilities) {
			drifted = append(drifted, expected.ID)
		}
	}
	gates = append(gates, readinessGate("source-adapters", missing, disabled, drifted))

	frontends, err := store.ListFrontendAdapters(ctx)
	if err != nil {
		return SoftwareReadinessReport{}, err
	}
	frontendByID := make(map[string]catalog.FrontendAdapter, len(frontends))
	for _, item := range frontends {
		frontendByID[item.ID] = item
	}
	missing, disabled, drifted = []string{}, []string{}, []string{}
	for _, expected := range builtinFrontendAdapters() {
		actual, ok := frontendByID[expected.ID]
		if !ok {
			missing = append(missing, expected.ID)
			continue
		}
		if !actual.Enabled {
			disabled = append(disabled, expected.ID)
		}
		if !actual.Builtin || actual.ContractVersion != expected.ContractVersion || actual.Format != expected.Format || actual.Handler != expected.Handler || !reflect.DeepEqual(actual.Capabilities, expected.Capabilities) {
			drifted = append(drifted, expected.ID)
		}
	}
	gates = append(gates, readinessGate("frontend-adapters", missing, disabled, drifted))

	devices, err := store.ListDeviceProfiles(ctx)
	if err != nil {
		return SoftwareReadinessReport{}, err
	}
	deviceByID := make(map[string]catalog.DeviceProfile, len(devices))
	for _, item := range devices {
		deviceByID[item.ID] = item
	}
	missing, disabled, drifted = []string{}, []string{}, []string{}
	for _, expected := range builtinDeviceProfiles() {
		actual, ok := deviceByID[expected.ID]
		if !ok {
			missing = append(missing, expected.ID)
			continue
		}
		if !actual.Enabled {
			disabled = append(disabled, expected.ID)
		}
		if !actual.Builtin || actual.ContractVersion != expected.ContractVersion || actual.Target != expected.Target || actual.OSFamily != expected.OSFamily || actual.Distribution != expected.Distribution || actual.Architecture != expected.Architecture || actual.PathStyle != expected.PathStyle || actual.DefaultFrontendID != expected.DefaultFrontendID || !reflect.DeepEqual(actual.Paths, expected.Paths) {
			drifted = append(drifted, expected.ID)
		}
	}
	gates = append(gates, readinessGate("device-profiles", missing, disabled, drifted))

	drivers, err := store.ListEmulatorDrivers(ctx)
	if err != nil {
		return SoftwareReadinessReport{}, err
	}
	driverByID := make(map[string]catalog.EmulatorDriver, len(drivers))
	for _, item := range drivers {
		driverByID[item.ID] = item
	}
	missing, disabled, drifted = []string{}, []string{}, []string{}
	for _, expected := range builtinEmulatorDrivers() {
		actual, ok := driverByID[expected.ID]
		if !ok {
			missing = append(missing, expected.ID)
			continue
		}
		if !actual.Enabled {
			disabled = append(disabled, expected.ID)
		}
		expectedLaunch := expected.Launch
		if expectedLaunch.AndroidIntent != nil {
			expectedLaunch.AndroidPackage = expectedLaunch.AndroidIntent.Package
			expectedLaunch.AndroidActivity = expectedLaunch.AndroidIntent.Activity
		}
		expectedConfigPaths := expected.ConfigPaths
		if expectedConfigPaths == nil {
			expectedConfigPaths = map[string]string{}
		}
		if !actual.Builtin || actual.ContractVersion != expected.ContractVersion || actual.Family != expected.Family || !reflect.DeepEqual(actual.Platforms, expected.Platforms) || !reflect.DeepEqual(actual.Targets, expected.Targets) || !reflect.DeepEqual(actual.Launch, expectedLaunch) || !reflect.DeepEqual(actual.Save, expected.Save) || !reflect.DeepEqual(actual.ConfigPaths, expectedConfigPaths) {
			drifted = append(drifted, expected.ID)
		}
	}
	gates = append(gates, readinessGate("emulator-drivers", missing, disabled, drifted))

	cores, err := store.ListRetroArchCores(ctx)
	if err != nil {
		return SoftwareReadinessReport{}, err
	}
	coreByID := make(map[string]catalog.RetroArchCore, len(cores))
	for _, item := range cores {
		coreByID[item.ID] = item
	}
	mappings, err := store.ListCoreMappings(ctx, "")
	if err != nil {
		return SoftwareReadinessReport{}, err
	}
	mappingByID := make(map[string]catalog.CoreMapping, len(mappings))
	for _, item := range mappings {
		mappingByID[item.ID] = item
	}
	missing, disabled, drifted = []string{}, []string{}, []string{}
	for _, expected := range builtinRetroArchCores() {
		actual, ok := coreByID[expected.ID]
		if !ok {
			missing = append(missing, expected.ID)
			continue
		}
		if !actual.Enabled {
			disabled = append(disabled, expected.ID)
		}
		if !actual.Builtin || actual.ContractVersion != expected.ContractVersion || !reflect.DeepEqual(actual.LibraryNames, expected.LibraryNames) || !reflect.DeepEqual(actual.Platforms, expected.Platforms) {
			drifted = append(drifted, expected.ID)
		}
	}
	for _, expected := range builtinCoreMappings() {
		actual, ok := mappingByID[expected.ID]
		if !ok {
			missing = append(missing, expected.ID)
			continue
		}
		if !actual.Builtin || actual.ScopeType != expected.ScopeType || actual.ScopeKey != expected.ScopeKey || actual.PlatformID != expected.PlatformID || actual.CoreID != expected.CoreID || actual.Priority != expected.Priority {
			drifted = append(drifted, expected.ID)
		}
	}
	gates = append(gates, readinessGate("retroarch-catalog", missing, disabled, drifted))

	profiles, err := store.ListPackageProfiles(ctx)
	if err != nil {
		return SoftwareReadinessReport{}, err
	}
	profileByID := make(map[string]catalog.PackageProfile, len(profiles))
	for _, item := range profiles {
		profileByID[item.ID] = item
	}
	missing, disabled, drifted = []string{}, []string{}, []string{}
	for _, expected := range defaultPackageProfiles() {
		id := "builtin-" + safeSegment(expected.Name)
		actual, ok := profileByID[id]
		if !ok {
			missing = append(missing, id)
			continue
		}
		if !actual.Enabled {
			disabled = append(disabled, id)
		}
		if !actual.Builtin || actual.Frontend != expected.Frontend || actual.Target != expected.Target || actual.DeviceProfileID != expected.DeviceProfileID || actual.FrontendAdapterID != expected.FrontendAdapterID || actual.Locale != expected.Locale || actual.FileMode != expected.FileMode || actual.OutputSlug != expected.Name {
			drifted = append(drifted, id)
		}
	}
	gates = append(gates, readinessGate("package-profiles", missing, disabled, drifted))

	report := SoftwareReadinessReport{Ready: true, Gates: gates}
	for _, gate := range gates {
		if gate.Status != "ready" {
			report.Ready = false
			break
		}
	}
	return report, nil
}
