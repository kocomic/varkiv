package deviceagent

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeErrorRemovesConfiguredHostPaths(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "private saves")
	romRoot := filepath.Join(base, "private roms")
	configPath := filepath.Join(base, "agent.json")
	config := Config{ServerURL: "https://example.invalid", DeviceID: "device", AccessToken: "secret", RootDir: root, ROMRoots: map[string]string{"gba": romRoot}}
	if err := SaveConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	sanitized := SanitizeError(configPath, errors.New("read "+filepath.Join(root, "slot", "save.srm")+" after "+filepath.Join(romRoot, "game.gba")+" using "+configPath))
	if strings.Contains(sanitized.Error(), base) || !strings.Contains(sanitized.Error(), "<agent-root>") || !strings.Contains(sanitized.Error(), "<rom-root>") || !strings.Contains(sanitized.Error(), "<agent-config>") {
		t.Fatalf("path was not safely redacted: %q", sanitized)
	}
}

func TestAgentServerURLRequiresAnOrigin(t *testing.T) {
	for _, value := range []string{
		"https://user:secret@example.invalid",
		"https://example.invalid/api",
		"https://example.invalid/?token=secret",
		"https://example.invalid/#fragment",
		"file:///private/library",
	} {
		config := Config{ServerURL: value, DeviceID: "device", AccessToken: "secret", RootDir: t.TempDir()}
		if err := config.normalize(); err == nil {
			t.Fatalf("unsafe server URL was accepted: %s", value)
		}
		if _, err := safeHTTPOrigin(value, true); err == nil {
			t.Fatalf("unsafe pairing origin was accepted: %s", value)
		}
	}
	config := Config{ServerURL: "https://example.invalid/", DeviceID: "device", AccessToken: "secret", RootDir: t.TempDir()}
	if err := config.normalize(); err != nil || config.ServerURL != "https://example.invalid" {
		t.Fatalf("safe origin normalization failed: %q %v", config.ServerURL, err)
	}
	if origin, err := safeHTTPOrigin("http://127.0.0.1:8080/", false); err != nil || origin != "http://127.0.0.1:8080" {
		t.Fatalf("loopback origin normalization failed: %q %v", origin, err)
	}
	if _, err := safeHTTPOrigin("http://192.0.2.20:8080", false); err == nil {
		t.Fatal("non-loopback HTTP was accepted without explicit consent")
	}
}

func TestAgentSyncStatusRejectsSemanticDrift(t *testing.T) {
	base := Config{ServerURL: "https://example.invalid", DeviceID: "device", AccessToken: "secret", RootDir: t.TempDir()}
	for _, invalid := range []AgentSyncStatus{
		{State: "unknown", AttemptedAt: "2026-08-27T12:00:00Z"},
		{State: "running", AttemptedAt: "2026-08-27T12:00:00Z", Uploaded: 1},
		{State: "complete", AttemptedAt: "2026-08-27T12:00:00Z", FinishedAt: "bad"},
		{State: "complete", AttemptedAt: "2026-08-27T12:00:00Z", FinishedAt: "2026-08-27T12:00:01Z", ErrorCode: "sync_failed"},
		{State: "conflict", AttemptedAt: "2026-08-27T12:00:00Z", FinishedAt: "2026-08-27T12:00:01Z", ErrorCode: "sync_conflict"},
		{State: "failed", AttemptedAt: "2026-08-27T12:00:00Z", FinishedAt: "2026-08-27T12:00:01Z"},
	} {
		config := base
		config.LastSync = &invalid
		if err := config.normalize(); err == nil {
			t.Fatalf("invalid agent sync status accepted: %#v", invalid)
		}
	}
}
