package catalog

import "time"

// SaveCompatibilityGroup is a versioned, evidence-backed agreement that two
// exact runtime members use the same on-disk save representation. A shared
// display name or core ID alone never creates membership.
type SaveCompatibilityGroup struct {
	ID              string                    `json:"id"`
	Name            string                    `json:"name"`
	Format          string                    `json:"format"`
	ContractVersion int                       `json:"contract_version"`
	Evidence        map[string]any            `json:"evidence"`
	Members         []SaveCompatibilityMember `json:"members"`
	Builtin         bool                      `json:"builtin"`
	Enabled         bool                      `json:"enabled"`
	CreatedAt       time.Time                 `json:"created_at"`
	UpdatedAt       time.Time                 `json:"updated_at"`
}

type SaveCompatibilityMember struct {
	GroupID               string `json:"group_id"`
	DriverID              string `json:"driver_id"`
	CoreID                string `json:"core_id,omitempty"`
	RuntimeKind           string `json:"runtime_kind"`
	DriverContractVersion int    `json:"driver_contract_version"`
	CoreContractVersion   int    `json:"core_contract_version,omitempty"`
	OSFamily              string `json:"os_family,omitempty"`
	Architecture          string `json:"architecture,omitempty"`
	DriverSHA256          string `json:"driver_sha256,omitempty"`
	DriverSize            int64  `json:"driver_size,omitempty"`
	CoreSHA256            string `json:"core_sha256,omitempty"`
	CoreSize              int64  `json:"core_size,omitempty"`
}

type NewSaveCompatibilityGroup struct {
	ID              string                    `json:"id,omitempty"`
	Name            string                    `json:"name"`
	Format          string                    `json:"format"`
	ContractVersion int                       `json:"contract_version,omitempty"`
	Evidence        map[string]any            `json:"evidence,omitempty"`
	Members         []SaveCompatibilityMember `json:"members"`
	Builtin         bool                      `json:"-"`
	Enabled         *bool                     `json:"enabled,omitempty"`
}

// RuntimeAttestation contains only a public runtime object identity and exact
// file digest. Host paths and matched basenames are never sent or persisted.
type RuntimeAttestation struct {
	DeviceID        string    `json:"device_id"`
	Kind            string    `json:"kind"`
	RuntimeID       string    `json:"runtime_id"`
	ContractVersion int       `json:"contract_version"`
	SHA256          string    `json:"sha256"`
	Size            int64     `json:"size"`
	ObservedAt      time.Time `json:"observed_at"`
}

type RuntimeAttestationReport struct {
	Kind            string `json:"kind"`
	RuntimeID       string `json:"runtime_id"`
	ContractVersion int    `json:"contract_version"`
	SHA256          string `json:"sha256"`
	Size            int64  `json:"size"`
}

type RuntimeAttestationRequirement struct {
	Kind            string `json:"kind"`
	RuntimeID       string `json:"runtime_id"`
	ContractVersion int    `json:"contract_version"`
}
