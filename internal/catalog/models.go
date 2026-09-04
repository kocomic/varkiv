package catalog

import (
	"path/filepath"
	"strings"
	"time"
)

// Series is a presentation-level grouping across platforms. A member Game
// remains the ownership boundary for editions, ROMs, media, and saves.
type Series struct {
	ID           string            `json:"id"`
	DefaultTitle string            `json:"default_title"`
	DisplayTitle string            `json:"display_title"`
	Description  string            `json:"description,omitempty"`
	Titles       map[string]string `json:"titles"`
	Members      []SeriesMember    `json:"members"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

type SeriesMember struct {
	SeriesID     string `json:"series_id"`
	GameID       string `json:"game_id"`
	RelationType string `json:"relation_type"`
	SortOrder    int    `json:"sort_order"`
	Game         Game   `json:"game"`
}

type NewSeries struct {
	ID           string            `json:"id,omitempty"`
	DefaultTitle string            `json:"default_title"`
	Description  string            `json:"description,omitempty"`
	Titles       map[string]string `json:"titles"`
}

type NewSeriesMember struct {
	RelationType string `json:"relation_type"`
	SortOrder    int    `json:"sort_order"`
}

// SeriesMemberMutation identifies one Game inside an atomic Series mutation.
// Game, Edition, ROM, media, and save ownership never move through this model.
type SeriesMemberMutation struct {
	GameID       string `json:"game_id"`
	RelationType string `json:"relation_type"`
	SortOrder    int    `json:"sort_order"`
}

// SeriesMutation updates presentation metadata and, when Members is present,
// replaces the complete member set in the same database transaction. A nil
// Members field keeps existing memberships for backwards-compatible clients.
type SeriesMutation struct {
	ID           string                  `json:"id,omitempty"`
	DefaultTitle string                  `json:"default_title"`
	Description  string                  `json:"description,omitempty"`
	Titles       map[string]string       `json:"titles"`
	Members      *[]SeriesMemberMutation `json:"members,omitempty"`
}

type Game struct {
	ID               string            `json:"id"`
	DefaultTitle     string            `json:"default_title"`
	DisplayTitle     string            `json:"display_title"`
	Platform         string            `json:"platform"`
	PrimaryEditionID string            `json:"primary_edition_id,omitempty"`
	Titles           map[string]string `json:"titles"`
	Editions         []Edition         `json:"editions"`
	Media            []MediaAsset      `json:"media"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

type Edition struct {
	ID            string            `json:"id"`
	GameID        string            `json:"game_id"`
	DefaultTitle  string            `json:"default_title"`
	DisplayTitle  string            `json:"display_title"`
	EditionType   string            `json:"edition_type"`
	Version       string            `json:"version,omitempty"`
	Languages     []string          `json:"languages"`
	Author        string            `json:"author,omitempty"`
	SaveNamespace string            `json:"save_namespace"`
	Serial        string            `json:"serial,omitempty"`
	ProductCode   string            `json:"product_code,omitempty"`
	TitleID       string            `json:"title_id,omitempty"`
	SortOrder     int               `json:"sort_order"`
	Titles        map[string]string `json:"titles"`
	Artifacts     []Artifact        `json:"artifacts"`
	Media         []MediaAsset      `json:"media"`
}

type Artifact struct {
	ID           string `json:"id"`
	EditionID    string `json:"edition_id"`
	Path         string `json:"path"`
	StorageKind  string `json:"storage_kind"`
	SourcePath   string `json:"source_path,omitempty"`
	OriginalName string `json:"original_name,omitempty"`
	Role         string `json:"role"`
	DiscIndex    int    `json:"disc_index,omitempty"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sha256,omitempty"`
	Missing      bool   `json:"missing"`
}

type MediaAsset struct {
	ID               string    `json:"id"`
	GameID           string    `json:"game_id,omitempty"`
	EditionID        string    `json:"edition_id,omitempty"`
	Kind             string    `json:"kind"`
	StorageKind      string    `json:"storage_kind"`
	Path             string    `json:"path"`
	SourcePath       string    `json:"source_path,omitempty"`
	OriginalName     string    `json:"original_name"`
	MIMEType         string    `json:"mime_type"`
	Size             int64     `json:"size"`
	SHA256           string    `json:"sha256"`
	Locale           string    `json:"locale,omitempty"`
	SourceType       string    `json:"source_type"`
	SortOrder        int       `json:"sort_order"`
	ContentStatus    string    `json:"content_status"`
	ContentCheckedAt string    `json:"content_checked_at,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

type NewGame struct {
	ID           string            `json:"id,omitempty"`
	DefaultTitle string            `json:"default_title"`
	Platform     string            `json:"platform"`
	Titles       map[string]string `json:"titles"`
}

type NewEdition struct {
	ID           string            `json:"id,omitempty"`
	GameID       string            `json:"game_id"`
	DefaultTitle string            `json:"default_title"`
	EditionType  string            `json:"edition_type"`
	Version      string            `json:"version,omitempty"`
	Languages    []string          `json:"languages"`
	Author       string            `json:"author,omitempty"`
	Serial       string            `json:"serial,omitempty"`
	ProductCode  string            `json:"product_code,omitempty"`
	TitleID      string            `json:"title_id,omitempty"`
	SortOrder    int               `json:"sort_order"`
	Titles       map[string]string `json:"titles"`
}

type NewArtifact struct {
	ID           string `json:"id,omitempty"`
	EditionID    string `json:"edition_id"`
	Path         string `json:"path"`
	StorageKind  string `json:"storage_kind,omitempty"`
	SourcePath   string `json:"source_path,omitempty"`
	OriginalName string `json:"original_name,omitempty"`
	Role         string `json:"role"`
	DiscIndex    int    `json:"disc_index,omitempty"`
	Size         int64  `json:"size,omitempty"`
	SHA256       string `json:"sha256,omitempty"`
	Missing      bool   `json:"missing,omitempty"`
}

// IsLaunchArtifactRole separates playable entry points from auxiliary package
// resources. Patches, DLC, updates, and documentation may travel with an
// Edition but must never silently become frontend or emulator launch targets.
func IsLaunchArtifactRole(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "", "rom", "executable", "disc":
		return true
	default:
		return false
	}
}

func ValidArtifactRole(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "rom", "disc", "executable", "patch", "dlc", "update", "other":
		return true
	default:
		return false
	}
}

// SelectLaunchArtifact applies one deterministic rule everywhere a frontend,
// driver, template, or device descriptor needs the Edition's primary entry.
func SelectLaunchArtifact(artifacts []Artifact) *Artifact {
	bestIndex, bestScore := -1, int(^uint(0)>>1)
	for index := range artifacts {
		artifact := &artifacts[index]
		if artifact.Missing || !IsLaunchArtifactRole(artifact.Role) {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(artifact.Role))
		score := 1000 + index
		switch role {
		case "", "rom":
			score = 100 + index
			switch strings.ToLower(filepath.Ext(artifact.Path)) {
			case ".m3u":
				score = index
			case ".cue":
				score = 50 + index
			}
		case "executable":
			score = 200 + index
		case "disc":
			score = 300 + index
			if artifact.DiscIndex > 0 {
				score += artifact.DiscIndex
			}
		}
		if score < bestScore {
			bestIndex, bestScore = index, score
		}
	}
	if bestIndex < 0 {
		return nil
	}
	return &artifacts[bestIndex]
}

type NewMediaAsset struct {
	ID               string `json:"id,omitempty"`
	GameID           string `json:"game_id,omitempty"`
	EditionID        string `json:"edition_id,omitempty"`
	Kind             string `json:"kind"`
	StorageKind      string `json:"storage_kind,omitempty"`
	Path             string `json:"path"`
	SourcePath       string `json:"source_path,omitempty"`
	OriginalName     string `json:"original_name,omitempty"`
	MIMEType         string `json:"mime_type,omitempty"`
	Size             int64  `json:"size,omitempty"`
	SHA256           string `json:"sha256,omitempty"`
	Locale           string `json:"locale,omitempty"`
	SourceType       string `json:"source_type,omitempty"`
	SortOrder        int    `json:"sort_order,omitempty"`
	ContentStatus    string `json:"content_status,omitempty"`
	ContentCheckedAt string `json:"content_checked_at,omitempty"`
}

// MediaMetadataUpdate deliberately excludes ownership, paths, hashes, and
// content attributes. Updating library semantics must never move or rewrite a
// media blob.
type MediaMetadataUpdate struct {
	Kind      string `json:"kind"`
	Locale    string `json:"locale,omitempty"`
	SortOrder int    `json:"sort_order"`
}

type MediaContentStatusUpdate struct {
	ID               string `json:"id"`
	ContentStatus    string `json:"content_status"`
	ContentCheckedAt string `json:"content_checked_at"`
}

// LibrarySource is a durable, read-only description of content below the
// configured library root. Paths are always portable relative paths; the
// catalog never owns or deletes files merely because a source is removed.
type LibrarySource struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	Kind                string    `json:"kind"`
	SourceAdapterID     string    `json:"source_adapter_id"`
	RootPath            string    `json:"root_path,omitempty"`
	MetadataPath        string    `json:"metadata_path,omitempty"`
	RuntimeMetadataPath string    `json:"runtime_metadata_path,omitempty"`
	Platform            string    `json:"platform"`
	MetadataLocale      string    `json:"metadata_locale,omitempty"`
	ROMStoragePolicy    string    `json:"rom_storage_policy"`
	MediaStoragePolicy  string    `json:"media_storage_policy"`
	Enabled             bool      `json:"enabled"`
	LastScanAt          time.Time `json:"last_scan_at,omitempty"`
	LastScanStatus      string    `json:"last_scan_status"`
	LastError           string    `json:"last_error,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type NewLibrarySource struct {
	ID                  string `json:"id,omitempty"`
	Name                string `json:"name"`
	Kind                string `json:"kind"`
	SourceAdapterID     string `json:"source_adapter_id,omitempty"`
	RootPath            string `json:"root_path,omitempty"`
	MetadataPath        string `json:"metadata_path,omitempty"`
	RuntimeMetadataPath string `json:"runtime_metadata_path,omitempty"`
	Platform            string `json:"platform"`
	MetadataLocale      string `json:"metadata_locale,omitempty"`
	ROMStoragePolicy    string `json:"rom_storage_policy,omitempty"`
	MediaStoragePolicy  string `json:"media_storage_policy,omitempty"`
	Enabled             *bool  `json:"enabled,omitempty"`
}

type SourceScan struct {
	ID              string    `json:"id"`
	SourceID        string    `json:"source_id"`
	Status          string    `json:"status"`
	RequestedAt     time.Time `json:"requested_at"`
	StartedAt       time.Time `json:"started_at,omitempty"`
	FinishedAt      time.Time `json:"finished_at,omitempty"`
	ExpiresAt       time.Time `json:"expires_at,omitempty"`
	CandidateCount  int       `json:"candidate_count"`
	ImportableCount int       `json:"importable_count"`
	MissingCount    int       `json:"missing_count"`
	DuplicateCount  int       `json:"duplicate_count"`
	ConflictCount   int       `json:"conflict_count"`
	FailureCode     string    `json:"failure_code,omitempty"`
	FailureDetail   string    `json:"failure_detail,omitempty"`
}

type NewSourceScan struct {
	ID               string
	SourceID         string
	Status           string
	StartedAt        time.Time
	FinishedAt       time.Time
	ExpiresAt        time.Time
	CandidateCount   int
	ImportableCount  int
	MissingCount     int
	DuplicateCount   int
	ConflictCount    int
	PreviewTokenHash string
	FailureCode      string
	FailureDetail    string
}

type PackageConfigTemplate struct {
	ID         string `json:"id"`
	ProfileID  string `json:"profile_id"`
	Name       string `json:"name"`
	Scope      string `json:"scope"`
	OutputPath string `json:"output_path"`
	Body       string `json:"body"`
	SortOrder  int    `json:"sort_order"`
}

type NewPackageConfigTemplate struct {
	ID         string `json:"id,omitempty"`
	Name       string `json:"name"`
	Scope      string `json:"scope"`
	OutputPath string `json:"output_path"`
	Body       string `json:"body"`
	SortOrder  int    `json:"sort_order,omitempty"`
}

type PackageProfile struct {
	ID                string                  `json:"id"`
	Name              string                  `json:"name"`
	Frontend          string                  `json:"frontend"`
	Target            string                  `json:"target"`
	DeviceProfileID   string                  `json:"device_profile_id,omitempty"`
	FrontendAdapterID string                  `json:"frontend_adapter_id,omitempty"`
	Locale            string                  `json:"locale"`
	FileMode          string                  `json:"file_mode"`
	OutputSlug        string                  `json:"output_slug"`
	Enabled           bool                    `json:"enabled"`
	Builtin           bool                    `json:"builtin"`
	Templates         []PackageConfigTemplate `json:"templates"`
	CreatedAt         time.Time               `json:"created_at"`
	UpdatedAt         time.Time               `json:"updated_at"`
}

type NewPackageProfile struct {
	ID                string                     `json:"id,omitempty"`
	Name              string                     `json:"name"`
	Frontend          string                     `json:"frontend"`
	Target            string                     `json:"target"`
	DeviceProfileID   string                     `json:"device_profile_id,omitempty"`
	FrontendAdapterID string                     `json:"frontend_adapter_id,omitempty"`
	Locale            string                     `json:"locale,omitempty"`
	FileMode          string                     `json:"file_mode,omitempty"`
	OutputSlug        string                     `json:"output_slug,omitempty"`
	Enabled           *bool                      `json:"enabled,omitempty"`
	Templates         []NewPackageConfigTemplate `json:"templates,omitempty"`
	Builtin           bool                       `json:"-"`
}

type PackagePlanRecord struct {
	ID          string    `json:"id"`
	ProfileID   string    `json:"profile_id"`
	Fingerprint string    `json:"fingerprint"`
	Status      string    `json:"status"`
	PlanJSON    string    `json:"-"`
	ExpiresAt   time.Time `json:"expires_at"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type NewPackagePlanRecord struct {
	ID          string
	ProfileID   string
	Fingerprint string
	Status      string
	PlanJSON    string
	ExpiresAt   time.Time
}

type PackageReleaseRecord struct {
	ID         string    `json:"id"`
	ProfileID  string    `json:"profile_id"`
	PlanID     string    `json:"plan_id"`
	Status     string    `json:"status"`
	OutputSlug string    `json:"output_slug"`
	ResultJSON string    `json:"-"`
	CreatedAt  time.Time `json:"created_at"`
}

type NewPackageReleaseRecord struct {
	ID         string
	ProfileID  string
	PlanID     string
	Status     string
	OutputSlug string
	ResultJSON string
}

type ImportedGame struct {
	GameID             string                     `json:"game_id,omitempty"`
	EditionID          string                     `json:"edition_id,omitempty"`
	Platform           string                     `json:"platform"`
	DefaultTitle       string                     `json:"default_title"`
	Titles             map[string]string          `json:"titles"`
	EditionTitle       string                     `json:"edition_title"`
	EditionTitles      map[string]string          `json:"edition_titles"`
	EditionType        string                     `json:"edition_type"`
	Version            string                     `json:"version,omitempty"`
	Languages          []string                   `json:"languages"`
	Author             string                     `json:"author,omitempty"`
	Serial             string                     `json:"serial,omitempty"`
	ProductCode        string                     `json:"product_code,omitempty"`
	TitleID            string                     `json:"title_id,omitempty"`
	Artifacts          []NewArtifact              `json:"artifacts"`
	Media              []NewMediaAsset            `json:"media"`
	RuntimeHints       []NewRuntimeImportHint     `json:"runtime_hints,omitempty"`
	RuntimeCatalog     *PortableRuntimeCatalog    `json:"runtime_catalog,omitempty"`
	SeriesMemberships  []ImportedSeriesMembership `json:"series_memberships,omitempty"`
	PlatformDefinition *NewCustomPlatform         `json:"platform_definition,omitempty"`
}

type ImportedSeriesMembership struct {
	Series       NewSeries `json:"series"`
	RelationType string    `json:"relation_type"`
	SortOrder    int       `json:"sort_order"`
}

type MoveEdition struct {
	TargetGameID string `json:"target_game_id"`
}

type MergeGames struct {
	SourceGameID        string `json:"source_game_id"`
	PreviewToken        string `json:"preview_token,omitempty"`
	SnapshotFingerprint string `json:"snapshot_fingerprint,omitempty"`
}

// GameMergePlan is a privacy-minimized, point-in-time description of a
// same-platform Game merge. SnapshotFingerprint binds the complete affected
// catalog graph (games, localized titles, editions, artifacts, media, and
// series memberships) without returning ROM paths or hashes to the client.
type GameMergePlan struct {
	SnapshotFingerprint      string `json:"snapshot_fingerprint"`
	TargetGameID             string `json:"target_game_id"`
	SourceGameID             string `json:"source_game_id"`
	TargetTitle              string `json:"target_title"`
	SourceTitle              string `json:"source_title"`
	Platform                 string `json:"platform"`
	TargetEditions           int    `json:"target_editions"`
	SourceEditions           int    `json:"source_editions"`
	ResultEditions           int    `json:"result_editions"`
	SourceArtifacts          int    `json:"source_artifacts"`
	SourceEditionMedia       int    `json:"source_edition_media"`
	SourceGameMedia          int    `json:"source_game_media"`
	SourceLocalizedTitles    int    `json:"source_localized_titles"`
	AddedLocalizedTitles     int    `json:"added_localized_titles"`
	CollidingLocalizedTitles int    `json:"colliding_localized_titles"`
	SourceSeriesMemberships  int    `json:"source_series_memberships"`
	SeriesCollisions         int    `json:"series_collisions"`
}

type Device struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	DeviceProfileID string          `json:"device_profile_id,omitempty"`
	OSFamily        string          `json:"os_family"`
	Distribution    string          `json:"distribution"`
	Architecture    string          `json:"architecture"`
	AgentVersion    string          `json:"agent_version,omitempty"`
	Status          string          `json:"status"`
	Capabilities    map[string]bool `json:"capabilities"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	LastSeenAt      time.Time       `json:"last_seen_at"`
	RevokedAt       time.Time       `json:"revoked_at,omitempty"`
}

type NewDevice struct {
	ID              string          `json:"id,omitempty"`
	Name            string          `json:"name"`
	DeviceProfileID string          `json:"device_profile_id,omitempty"`
	OSFamily        string          `json:"os_family"`
	Distribution    string          `json:"distribution"`
	Architecture    string          `json:"architecture"`
	AgentVersion    string          `json:"agent_version,omitempty"`
	Status          string          `json:"status,omitempty"`
	Capabilities    map[string]bool `json:"capabilities"`
}

type SaveStreamEdition struct {
	EditionID     string    `json:"edition_id"`
	Compatibility string    `json:"compatibility"`
	CreatedAt     time.Time `json:"created_at"`
}

type SaveStream struct {
	ID                   string              `json:"id"`
	Namespace            string              `json:"namespace"`
	OwnerType            string              `json:"owner_type"`
	OwnerKey             string              `json:"owner_key"`
	DriverID             string              `json:"driver_id"`
	Portability          string              `json:"portability"`
	CompatibilityGroupID string              `json:"compatibility_group_id,omitempty"`
	Editions             []SaveStreamEdition `json:"editions"`
	CreatedAt            time.Time           `json:"created_at"`
	UpdatedAt            time.Time           `json:"updated_at"`
}

type NewSaveStream struct {
	ID                   string   `json:"id,omitempty"`
	Namespace            string   `json:"namespace,omitempty"`
	OwnerType            string   `json:"owner_type"`
	OwnerKey             string   `json:"owner_key"`
	DriverID             string   `json:"driver_id"`
	Portability          string   `json:"portability,omitempty"`
	CompatibilityGroupID string   `json:"compatibility_group_id,omitempty"`
	EditionIDs           []string `json:"edition_ids"`
	Compatibility        string   `json:"compatibility,omitempty"`
}

type NewSaveSetup struct {
	Stream  NewSaveStream  `json:"stream"`
	Binding NewSaveBinding `json:"binding"`
}

type SaveSetup struct {
	Stream  SaveStream  `json:"stream"`
	Binding SaveBinding `json:"binding"`
}

type SaveFile struct {
	ID          string `json:"id"`
	RevisionID  string `json:"revision_id"`
	LogicalPath string `json:"logical_path"`
	Checksum    string `json:"checksum"`
	Size        int64  `json:"size"`
	BlobPath    string `json:"-"`
	MTimeNS     int64  `json:"mtime_ns,omitempty"`
	Mode        int64  `json:"mode,omitempty"`
}

type NewSaveFile struct {
	ID          string
	LogicalPath string
	Checksum    string
	Size        int64
	BlobPath    string
	MTimeNS     int64
	Mode        int64
}

type SaveRevision struct {
	ID               string     `json:"id"`
	StreamID         string     `json:"stream_id"`
	ParentRevisionID string     `json:"parent_revision_id,omitempty"`
	ContentHash      string     `json:"content_hash"`
	TotalSize        int64      `json:"total_size"`
	FileCount        int        `json:"file_count"`
	Status           string     `json:"status"`
	Files            []SaveFile `json:"files"`

	// Compatibility fields keep the preview single-file API readable while
	// clients migrate to SaveStream and SaveFile.
	EditionID      string    `json:"edition_id"`
	DeviceID       string    `json:"device_id"`
	DriverID       string    `json:"driver_id"`
	RelativePath   string    `json:"relative_path"`
	ScopeType      string    `json:"scope_type"`
	ScopeKey       string    `json:"scope_key"`
	Checksum       string    `json:"checksum"`
	Size           int64     `json:"size"`
	BlobPath       string    `json:"-"`
	BaseRevisionID string    `json:"base_revision_id,omitempty"`
	Conflict       bool      `json:"conflict"`
	CreatedAt      time.Time `json:"created_at"`
}

type NewSaveRevision struct {
	ID               string
	StreamID         string
	ParentRevisionID string
	ContentHash      string
	Status           string
	Files            []NewSaveFile
	EditionID        string
	DeviceID         string
	DriverID         string
	RelativePath     string
	ScopeType        string
	ScopeKey         string
	Checksum         string
	Size             int64
	BlobPath         string
	BaseRevisionID   string
	Conflict         bool
}
