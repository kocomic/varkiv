package catalog

import "time"

// SourceAdapter is a versioned, declarative import contract. Handler selects
// one of the audited parsers compiled into the service; custom adapters can
// specialize a safe handler without loading code or gaining filesystem access.
type SourceAdapter struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Format          string          `json:"format"`
	Handler         string          `json:"handler"`
	ContractVersion int             `json:"contract_version"`
	Capabilities    map[string]bool `json:"capabilities"`
	SupportLevel    string          `json:"support_level"`
	Evidence        map[string]any  `json:"evidence"`
	Builtin         bool            `json:"builtin"`
	Enabled         bool            `json:"enabled"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type NewSourceAdapter struct {
	ID              string          `json:"id,omitempty"`
	Name            string          `json:"name"`
	Format          string          `json:"format"`
	Handler         string          `json:"handler"`
	ContractVersion int             `json:"contract_version,omitempty"`
	Capabilities    map[string]bool `json:"capabilities,omitempty"`
	SupportLevel    string          `json:"support_level,omitempty"`
	Evidence        map[string]any  `json:"evidence,omitempty"`
	Builtin         bool            `json:"-"`
	Enabled         *bool           `json:"enabled,omitempty"`
}

type FrontendAdapter struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Format          string          `json:"format"`
	Handler         string          `json:"handler"`
	ContractVersion int             `json:"contract_version"`
	Capabilities    map[string]bool `json:"capabilities"`
	SupportLevel    string          `json:"support_level"`
	Evidence        map[string]any  `json:"evidence"`
	Builtin         bool            `json:"builtin"`
	Enabled         bool            `json:"enabled"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type NewFrontendAdapter struct {
	ID              string          `json:"id,omitempty"`
	Name            string          `json:"name"`
	Format          string          `json:"format"`
	Handler         string          `json:"handler"`
	ContractVersion int             `json:"contract_version,omitempty"`
	Capabilities    map[string]bool `json:"capabilities,omitempty"`
	SupportLevel    string          `json:"support_level,omitempty"`
	Evidence        map[string]any  `json:"evidence,omitempty"`
	Builtin         bool            `json:"-"`
	Enabled         *bool           `json:"enabled,omitempty"`
}

type DeviceProfile struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	ContractVersion   int               `json:"contract_version"`
	Target            string            `json:"target"`
	OSFamily          string            `json:"os_family"`
	Distribution      string            `json:"distribution,omitempty"`
	Architecture      string            `json:"architecture,omitempty"`
	PathStyle         string            `json:"path_style"`
	CaseSensitive     bool              `json:"case_sensitive"`
	MaxPath           int               `json:"max_path"`
	IllegalCharacters string            `json:"illegal_characters,omitempty"`
	SupportsHardlink  bool              `json:"supports_hardlink"`
	SupportsHooks     bool              `json:"supports_hooks"`
	DefaultFrontendID string            `json:"default_frontend_id,omitempty"`
	Paths             map[string]string `json:"paths"`
	SupportLevel      string            `json:"support_level"`
	Evidence          map[string]any    `json:"evidence"`
	Builtin           bool              `json:"builtin"`
	Enabled           bool              `json:"enabled"`
	CreatedAt         time.Time         `json:"created_at"`
	UpdatedAt         time.Time         `json:"updated_at"`
}

type NewDeviceProfile struct {
	ID                string            `json:"id,omitempty"`
	Name              string            `json:"name"`
	ContractVersion   int               `json:"contract_version,omitempty"`
	Target            string            `json:"target"`
	OSFamily          string            `json:"os_family"`
	Distribution      string            `json:"distribution,omitempty"`
	Architecture      string            `json:"architecture,omitempty"`
	PathStyle         string            `json:"path_style,omitempty"`
	CaseSensitive     *bool             `json:"case_sensitive,omitempty"`
	MaxPath           int               `json:"max_path,omitempty"`
	IllegalCharacters string            `json:"illegal_characters,omitempty"`
	SupportsHardlink  bool              `json:"supports_hardlink,omitempty"`
	SupportsHooks     bool              `json:"supports_hooks,omitempty"`
	DefaultFrontendID string            `json:"default_frontend_id,omitempty"`
	Paths             map[string]string `json:"paths,omitempty"`
	SupportLevel      string            `json:"support_level,omitempty"`
	Evidence          map[string]any    `json:"evidence,omitempty"`
	Builtin           bool              `json:"-"`
	Enabled           *bool             `json:"enabled,omitempty"`
}

type EmulatorDriver struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Family          string            `json:"family"`
	ContractVersion int               `json:"contract_version"`
	Platforms       []string          `json:"platforms"`
	Targets         []string          `json:"targets"`
	Launch          DriverLaunchSpec  `json:"launch"`
	Save            DriverSaveSpec    `json:"save"`
	ConfigPaths     map[string]string `json:"config_paths"`
	SupportLevel    string            `json:"support_level"`
	Evidence        map[string]any    `json:"evidence"`
	Builtin         bool              `json:"builtin"`
	Enabled         bool              `json:"enabled"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

type DriverLaunchSpec struct {
	RequiresCore    bool                `json:"requires_core,omitempty"`
	Executables     map[string][]string `json:"executables,omitempty"`
	AndroidPackage  string              `json:"android_package,omitempty"`
	AndroidActivity string              `json:"android_activity,omitempty"`
	AndroidIntent   *AndroidIntentSpec  `json:"android_intent,omitempty"`
	Arguments       []string            `json:"arguments"`
}

// AndroidIntentSpec is a declarative, shell-free explicit Intent contract.
// Templates are rendered by the paired Android client from allowlisted local
// values such as {{rom.uri}}; arbitrary commands and environment access are
// deliberately not representable.
type AndroidIntentSpec struct {
	Action            string            `json:"action,omitempty"`
	Package           string            `json:"package"`
	PackageCandidates []string          `json:"package_candidates,omitempty"`
	Activity          string            `json:"activity,omitempty"`
	Data              string            `json:"data,omitempty"`
	MIMEType          string            `json:"mime_type,omitempty"`
	Categories        []string          `json:"categories,omitempty"`
	StringExtras      map[string]string `json:"string_extras,omitempty"`
	BooleanExtras     map[string]bool   `json:"boolean_extras,omitempty"`
	Flags             []string          `json:"flags,omitempty"`
}

type DriverSaveSpec struct {
	Scope              string              `json:"scope"`
	Layout             string              `json:"layout"`
	Patterns           []string            `json:"patterns"`
	ScopeByPlatform    map[string]string   `json:"scope_by_platform,omitempty"`
	LayoutByPlatform   map[string]string   `json:"layout_by_platform,omitempty"`
	PatternsByPlatform map[string][]string `json:"patterns_by_platform,omitempty"`
	Refresh            string              `json:"refresh"`
	Portability        string              `json:"portability"`
}

type NewEmulatorDriver struct {
	ID              string            `json:"id,omitempty"`
	Name            string            `json:"name"`
	Family          string            `json:"family"`
	ContractVersion int               `json:"contract_version,omitempty"`
	Platforms       []string          `json:"platforms"`
	Targets         []string          `json:"targets"`
	Launch          DriverLaunchSpec  `json:"launch"`
	Save            DriverSaveSpec    `json:"save"`
	ConfigPaths     map[string]string `json:"config_paths,omitempty"`
	SupportLevel    string            `json:"support_level,omitempty"`
	Evidence        map[string]any    `json:"evidence,omitempty"`
	Builtin         bool              `json:"-"`
	Enabled         *bool             `json:"enabled,omitempty"`
}

type RetroArchCore struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	ContractVersion int            `json:"contract_version"`
	LibraryNames    []string       `json:"library_names"`
	Platforms       []string       `json:"platforms"`
	SupportLevel    string         `json:"support_level"`
	Evidence        map[string]any `json:"evidence"`
	Builtin         bool           `json:"builtin"`
	Enabled         bool           `json:"enabled"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

type NewRetroArchCore struct {
	ID              string         `json:"id,omitempty"`
	Name            string         `json:"name"`
	ContractVersion int            `json:"contract_version,omitempty"`
	LibraryNames    []string       `json:"library_names"`
	Platforms       []string       `json:"platforms"`
	SupportLevel    string         `json:"support_level,omitempty"`
	Evidence        map[string]any `json:"evidence,omitempty"`
	Builtin         bool           `json:"-"`
	Enabled         *bool          `json:"enabled,omitempty"`
}

// PortableRuntimeCatalog contains only declarative definitions required to
// rebuild an exported package on another Varkiv server. Runtime evidence,
// timestamps, built-in ownership, local enablement, and Agent-owned absolute
// paths are deliberately outside this contract.
type PortableRuntimeCatalog struct {
	FrontendAdapters []NewFrontendAdapter `json:"frontend_adapters,omitempty"`
	DeviceProfiles   []NewDeviceProfile   `json:"device_profiles,omitempty"`
	EmulatorDrivers  []NewEmulatorDriver  `json:"emulator_drivers,omitempty"`
	RetroArchCores   []NewRetroArchCore   `json:"retroarch_cores,omitempty"`
	PackageProfile   *NewPackageProfile   `json:"package_profile,omitempty"`
}

type CoreMapping struct {
	ID         string    `json:"id"`
	ScopeType  string    `json:"scope_type"`
	ScopeKey   string    `json:"scope_key,omitempty"`
	PlatformID string    `json:"platform_id"`
	CoreID     string    `json:"core_id"`
	Priority   int       `json:"priority"`
	Notes      string    `json:"notes,omitempty"`
	Builtin    bool      `json:"builtin"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type NewCoreMapping struct {
	ID         string `json:"id,omitempty"`
	ScopeType  string `json:"scope_type"`
	ScopeKey   string `json:"scope_key,omitempty"`
	PlatformID string `json:"platform_id"`
	CoreID     string `json:"core_id"`
	Priority   int    `json:"priority,omitempty"`
	Notes      string `json:"notes,omitempty"`
	Builtin    bool   `json:"-"`
}

type LaunchBinding struct {
	ID                string    `json:"id"`
	EditionID         string    `json:"edition_id"`
	DeviceProfileID   string    `json:"device_profile_id,omitempty"`
	DriverID          string    `json:"driver_id"`
	FrontendAdapterID string    `json:"frontend_adapter_id,omitempty"`
	CoreID            string    `json:"core_id,omitempty"`
	Arguments         []string  `json:"arguments"`
	Enabled           bool      `json:"enabled"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type NewLaunchBinding struct {
	ID                string   `json:"id,omitempty"`
	EditionID         string   `json:"edition_id"`
	DeviceProfileID   string   `json:"device_profile_id,omitempty"`
	DriverID          string   `json:"driver_id"`
	FrontendAdapterID string   `json:"frontend_adapter_id,omitempty"`
	CoreID            string   `json:"core_id,omitempty"`
	Arguments         []string `json:"arguments,omitempty"`
	Enabled           *bool    `json:"enabled,omitempty"`
}

// RuntimeImportHint is inert metadata recovered from an integration package or
// frontend configuration. It is never executed and never becomes a launch
// binding until a user explicitly reviews and applies it.
type RuntimeImportHint struct {
	ID                string    `json:"id"`
	EditionID         string    `json:"edition_id"`
	SourceKind        string    `json:"source_kind"`
	SourceFormat      string    `json:"source_format"`
	DeviceProfileID   string    `json:"device_profile_id,omitempty"`
	FrontendAdapterID string    `json:"frontend_adapter_id,omitempty"`
	DriverID          string    `json:"driver_id,omitempty"`
	CoreID            string    `json:"core_id,omitempty"`
	Arguments         []string  `json:"arguments"`
	RawCommand        string    `json:"raw_command,omitempty"`
	Trust             string    `json:"trust"`
	Status            string    `json:"status"`
	SourceRef         string    `json:"source_ref,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type NewRuntimeImportHint struct {
	ID                string   `json:"id,omitempty"`
	EditionID         string   `json:"edition_id,omitempty"`
	SourceKind        string   `json:"source_kind"`
	SourceFormat      string   `json:"source_format"`
	DeviceProfileID   string   `json:"device_profile_id,omitempty"`
	FrontendAdapterID string   `json:"frontend_adapter_id,omitempty"`
	DriverID          string   `json:"driver_id,omitempty"`
	CoreID            string   `json:"core_id,omitempty"`
	Arguments         []string `json:"arguments,omitempty"`
	RawCommand        string   `json:"raw_command,omitempty"`
	Trust             string   `json:"trust"`
	SourceRef         string   `json:"source_ref,omitempty"`
}

type RuntimeHintApplication struct {
	Hint    RuntimeImportHint `json:"hint"`
	Binding LaunchBinding     `json:"binding"`
}

// RuntimeHintBatchReview is an explicit request to apply one reviewed,
// declarative launch configuration to a bounded set of pending import hints.
// Raw imported commands are never part of the binding template.
type RuntimeHintBatchReview struct {
	HintIDs           []string `json:"hint_ids"`
	DeviceProfileID   string   `json:"device_profile_id"`
	DriverID          string   `json:"driver_id"`
	FrontendAdapterID string   `json:"frontend_adapter_id,omitempty"`
	CoreID            string   `json:"core_id,omitempty"`
	Arguments         []string `json:"arguments,omitempty"`
}

type RuntimeHintBatchHintSnapshot struct {
	HintID      string `json:"hint_id"`
	EditionID   string `json:"edition_id"`
	PlatformID  string `json:"platform_id"`
	Fingerprint string `json:"fingerprint"`
}

// RuntimeHintBatchSnapshot binds the exact hints, editions, runtime catalog
// definitions, and declarative argv that a user reviewed. It contains hashes,
// never raw frontend commands.
type RuntimeHintBatchSnapshot struct {
	Review                RuntimeHintBatchReview         `json:"review"`
	Hints                 []RuntimeHintBatchHintSnapshot `json:"hints"`
	PlatformID            string                         `json:"platform_id"`
	DefinitionFingerprint string                         `json:"definition_fingerprint"`
}

type RuntimeHintBatchResult struct {
	Applied      int                      `json:"applied"`
	Applications []RuntimeHintApplication `json:"applications"`
}

type CoreResolution struct {
	PlatformID      string         `json:"platform_id"`
	EditionID       string         `json:"edition_id,omitempty"`
	DeviceProfileID string         `json:"device_profile_id,omitempty"`
	Mapping         *CoreMapping   `json:"mapping,omitempty"`
	Core            *RetroArchCore `json:"core,omitempty"`
	Resolution      string         `json:"resolution"`
}
