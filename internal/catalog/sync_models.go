package catalog

import (
	"errors"
	"time"
)

var ErrSaveRevisionNotBound = errors.New("save revision is not bound to this device profile")
var ErrSaveBindingIdentityRequired = errors.New("save binding edition identity is required")

type SaveBindingIdentityError struct {
	Field       string
	Requirement string
}

func (e *SaveBindingIdentityError) Error() string {
	if e.Requirement != "" {
		return "save binding requires " + e.Requirement + " " + e.Field
	}
	return "save binding requires " + e.Field
}

func (e *SaveBindingIdentityError) Unwrap() error { return ErrSaveBindingIdentityRequired }

type SaveBinding struct {
	ID              string         `json:"id"`
	StreamID        string         `json:"stream_id"`
	EditionID       string         `json:"edition_id"`
	DeviceProfileID string         `json:"device_profile_id,omitempty"`
	DriverID        string         `json:"driver_id"`
	CoreID          string         `json:"core_id,omitempty"`
	LocalPaths      []string       `json:"local_paths"`
	Discovery       map[string]any `json:"discovery"`
	Enabled         bool           `json:"enabled"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

type NewSaveBinding struct {
	ID              string         `json:"id,omitempty"`
	StreamID        string         `json:"stream_id"`
	EditionID       string         `json:"edition_id"`
	DeviceProfileID string         `json:"device_profile_id,omitempty"`
	DriverID        string         `json:"driver_id"`
	CoreID          string         `json:"core_id,omitempty"`
	LocalPaths      []string       `json:"local_paths"`
	Discovery       map[string]any `json:"discovery,omitempty"`
	Enabled         *bool          `json:"enabled,omitempty"`
}

type PairingCode struct {
	ID              string         `json:"id"`
	RequestedDevice map[string]any `json:"requested_device"`
	ExpiresAt       time.Time      `json:"expires_at"`
	RedeemedAt      time.Time      `json:"redeemed_at,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	CodeHash        string         `json:"-"`
}

type NewPairingCode struct {
	ID              string
	CodeHash        string
	RequestedDevice map[string]any
	ExpiresAt       time.Time
}

type ClientToken struct {
	ID        string    `json:"id"`
	DeviceID  string    `json:"device_id"`
	Scopes    []string  `json:"scopes"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
	RevokedAt time.Time `json:"revoked_at,omitempty"`
	TokenHash string    `json:"-"`
}

type ClientIdentity struct {
	TokenID  string
	DeviceID string
	Scopes   []string
}

type SyncSession struct {
	ID                string          `json:"id"`
	DeviceID          string          `json:"device_id"`
	IdempotencyKey    string          `json:"idempotency_key"`
	Status            string          `json:"status"`
	BaseManifestHash  string          `json:"base_manifest_hash,omitempty"`
	OperationPlanHash string          `json:"operation_plan_hash,omitempty"`
	UploadedCount     int             `json:"uploaded_count"`
	DownloadedCount   int             `json:"downloaded_count"`
	ConflictCount     int             `json:"conflict_count"`
	FailureCode       string          `json:"failure_code,omitempty"`
	Operations        []SyncOperation `json:"operations"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
	FinishedAt        time.Time       `json:"finished_at,omitempty"`
}

type NewSyncSession struct {
	ID                string
	DeviceID          string
	IdempotencyKey    string
	Status            string
	BaseManifestHash  string
	OperationPlanHash string
	Operations        []NewSyncOperation
	Inventory         []NewInventoryItem
}

type SyncOperation struct {
	ID               string         `json:"id"`
	SessionID        string         `json:"session_id"`
	StreamID         string         `json:"stream_id"`
	Action           string         `json:"action"`
	Status           string         `json:"status"`
	BaseRevisionID   string         `json:"base_revision_id,omitempty"`
	TargetRevisionID string         `json:"target_revision_id,omitempty"`
	ExpectedHash     string         `json:"expected_hash,omitempty"`
	ActualHash       string         `json:"actual_hash,omitempty"`
	Bytes            int64          `json:"bytes"`
	FailureCode      string         `json:"failure_code,omitempty"`
	Detail           map[string]any `json:"detail"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

type NewSyncOperation struct {
	ID               string
	StreamID         string
	Action           string
	Status           string
	BaseRevisionID   string
	TargetRevisionID string
	ExpectedHash     string
	Bytes            int64
	Detail           map[string]any
}

type InventoryItem struct {
	ID               string    `json:"id"`
	SessionID        string    `json:"session_id"`
	ClientItemID     string    `json:"client_item_id"`
	PlatformID       string    `json:"platform_id"`
	SHA256           string    `json:"sha256,omitempty"`
	Serial           string    `json:"serial,omitempty"`
	ProductCode      string    `json:"product_code,omitempty"`
	TitleID          string    `json:"title_id,omitempty"`
	Size             int64     `json:"size"`
	MatchStatus      string    `json:"match_status"`
	MatchedEditionID string    `json:"matched_edition_id,omitempty"`
	MatchMethod      string    `json:"match_method,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

type NewInventoryItem struct {
	ID               string
	ClientItemID     string
	PlatformID       string
	SHA256           string
	Serial           string
	ProductCode      string
	TitleID          string
	Size             int64
	MatchStatus      string
	MatchedEditionID string
	MatchMethod      string
}

// InventoryMatchOverride is a device-local, privacy-minimized confirmation for
// one ambiguous inventory identity. ClientItemID is an opaque digest generated
// by the Agent; no ROM name or path is stored here.
type InventoryMatchOverride struct {
	ID                    string    `json:"id"`
	DeviceID              string    `json:"device_id"`
	ClientItemID          string    `json:"-"`
	PlatformID            string    `json:"platform_id"`
	IdentityHash          string    `json:"-"`
	CandidateHash         string    `json:"-"`
	EditionID             string    `json:"edition_id"`
	MatchMethod           string    `json:"match_method"`
	SourceSessionID       string    `json:"source_session_id"`
	SourceInventoryItemID string    `json:"source_inventory_item_id"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

type NewInventoryMatchOverride struct {
	ID                    string
	DeviceID              string
	ClientItemID          string
	PlatformID            string
	IdentityHash          string
	CandidateIDs          []string
	EditionID             string
	MatchMethod           string
	SourceSessionID       string
	SourceInventoryItemID string
}

type InventoryReviewItem struct {
	InventoryItem
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
}
