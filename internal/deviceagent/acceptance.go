package deviceagent

import (
	"context"
	"errors"
	"os"
	"runtime"
	"sort"
	"time"
)

var acceptedHardwareObservations = map[string]bool{
	"frontend-launch": true, "rom-launch": true, "emulator-exit": true, "save-created": true,
	"sync-upload": true, "sync-download": true, "conflict-recovery": true, "offline-play": true,
	"sleep-resume": true, "token-revocation": true, "upgrade": true,
	"network-recovery": true, "saf-rom-root": true, "saf-save-tree": true, "keystore-token": true,
	"retroarch-intent": true, "ppsspp-intent": true, "background-recovery": true,
}

type AcceptanceRootSummary struct {
	AgentRootReal         bool `json:"agent_root_real"`
	ROMRootsConfigured    int  `json:"rom_roots_configured"`
	ROMRootsReal          bool `json:"rom_roots_real"`
	DriverRootsConfigured int  `json:"driver_roots_configured"`
	DriverRootsReal       bool `json:"driver_roots_real"`
	PathOverrides         int  `json:"path_overrides"`
}

type HardwareAcceptanceReport struct {
	Format              string                `json:"format"`
	GeneratedAt         string                `json:"generated_at"`
	AgentVersion        string                `json:"agent_version"`
	HostOS              string                `json:"host_os"`
	HostArchitecture    string                `json:"host_architecture"`
	Target              string                `json:"target"`
	ConfigProtected     bool                  `json:"config_protected"`
	Roots               AcceptanceRootSummary `json:"roots"`
	Runtime             RuntimeProbeResult    `json:"runtime"`
	LastSync            *AgentSyncStatus      `json:"last_sync,omitempty"`
	ObservedOnHardware  []string              `json:"observed_on_hardware"`
	SoftwarePreflight   bool                  `json:"software_preflight_passed"`
	EvidenceLevel       string                `json:"evidence_level"`
	RequiresReview      bool                  `json:"requires_maintainer_review"`
	ContainsPrivateData bool                  `json:"contains_private_data"`
}

func realDirectory(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
}

func BuildHardwareAcceptanceReport(ctx context.Context, configPath, agentVersion string, observations []string) (HardwareAcceptanceReport, error) {
	unique := map[string]bool{}
	for _, observation := range observations {
		if !acceptedHardwareObservations[observation] {
			return HardwareAcceptanceReport{}, errors.New("hardware observation is not a supported evidence key")
		}
		unique[observation] = true
	}
	observed := make([]string, 0, len(unique))
	for observation := range unique {
		observed = append(observed, observation)
	}
	sort.Strings(observed)

	config, err := LoadConfig(configPath)
	if err != nil {
		return HardwareAcceptanceReport{}, err
	}
	remote, err := fetchDeviceConfig(ctx, defaultClient(), config)
	if err != nil {
		return HardwareAcceptanceReport{}, err
	}
	probe, err := probeRuntime(config, remote)
	if err != nil {
		return HardwareAcceptanceReport{}, err
	}
	roots := AcceptanceRootSummary{
		AgentRootReal: realDirectory(config.RootDir), ROMRootsConfigured: len(config.ROMRoots), ROMRootsReal: true,
		DriverRootsConfigured: len(config.DriverRoots), DriverRootsReal: true, PathOverrides: len(config.PathOverrides),
	}
	for _, root := range config.ROMRoots {
		roots.ROMRootsReal = roots.ROMRootsReal && realDirectory(root)
	}
	for _, root := range config.DriverRoots {
		roots.DriverRootsReal = roots.DriverRootsReal && realDirectory(root)
	}
	report := HardwareAcceptanceReport{
		Format: "varkiv-hardware-acceptance-v1", GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
		AgentVersion: agentVersion, HostOS: runtime.GOOS, HostArchitecture: runtime.GOARCH, Target: probe.Target,
		ConfigProtected: true, Roots: roots, Runtime: probe, LastSync: config.LastSync, ObservedOnHardware: observed,
		EvidenceLevel: "candidate", RequiresReview: true, ContainsPrivateData: false,
	}
	report.SoftwarePreflight = roots.AgentRootReal && roots.ROMRootsReal && roots.DriverRootsReal && report.Target != ""
	return report, nil
}
