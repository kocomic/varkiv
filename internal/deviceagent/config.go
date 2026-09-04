package deviceagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// SanitizeError removes configured host paths before an error reaches a
// service log or terminal. The agent may still return a useful operation
// description, but never needs to disclose where ROMs or saves live.
func SanitizeError(configPath string, source error) error {
	if source == nil {
		return nil
	}
	message := source.Error()
	replacements := map[string]string{}
	if absolute, err := filepath.Abs(configPath); err == nil {
		replacements[filepath.Clean(absolute)] = "<agent-config>"
	}
	if config, err := LoadConfig(configPath); err == nil {
		replacements[filepath.Clean(config.RootDir)] = "<agent-root>"
		for _, value := range config.PathOverrides {
			if absolute, absoluteErr := filepath.Abs(value); absoluteErr == nil {
				replacements[filepath.Clean(absolute)] = "<configured-path>"
			}
		}
		for _, value := range config.DriverRoots {
			if absolute, absoluteErr := filepath.Abs(value); absoluteErr == nil {
				replacements[filepath.Clean(absolute)] = "<driver-root>"
			}
		}
		for _, value := range config.ROMRoots {
			if absolute, absoluteErr := filepath.Abs(value); absoluteErr == nil {
				replacements[filepath.Clean(absolute)] = "<rom-root>"
			}
		}
	}
	paths := make([]string, 0, len(replacements))
	for value := range replacements {
		if value != "." && value != string(filepath.Separator) {
			paths = append(paths, value)
		}
	}
	sort.Slice(paths, func(i, j int) bool { return len(paths[i]) > len(paths[j]) })
	for _, value := range paths {
		message = strings.ReplaceAll(message, value, replacements[value])
		if filepath.Separator == '\\' {
			message = strings.ReplaceAll(message, filepath.ToSlash(value), replacements[value])
		}
	}
	return errors.New(message)
}

type StreamState struct {
	RevisionID  string `json:"revision_id,omitempty"`
	ContentHash string `json:"content_hash,omitempty"`
}

type PendingSync struct {
	IdempotencyKey string `json:"idempotency_key"`
	Fingerprint    string `json:"fingerprint"`
}

type ROMCacheEntry struct {
	Kind       string `json:"kind,omitempty"`
	Size       int64  `json:"size"`
	MTimeNS    int64  `json:"mtime_ns"`
	Signal     string `json:"signal,omitempty"`
	SHA256     string `json:"sha256"`
	VerifiedAt int64  `json:"verified_at,omitempty"`
}

type AgentSyncStatus struct {
	State           string `json:"state"`
	AttemptedAt     string `json:"attempted_at"`
	FinishedAt      string `json:"finished_at,omitempty"`
	LastSuccessAt   string `json:"last_success_at,omitempty"`
	SessionRecorded bool   `json:"session_recorded"`
	Uploaded        int    `json:"uploaded"`
	Downloaded      int    `json:"downloaded"`
	Conflicts       int    `json:"conflicts"`
	ErrorCode       string `json:"error_code,omitempty"`
}

func (status AgentSyncStatus) validate() error {
	switch status.State {
	case "running", "complete", "conflict", "failed":
	default:
		return errors.New("agent sync status has an invalid state")
	}
	if _, err := time.Parse(time.RFC3339Nano, status.AttemptedAt); err != nil {
		return errors.New("agent sync status has an invalid attempted_at")
	}
	if status.State == "running" {
		if status.FinishedAt != "" || status.ErrorCode != "" || status.SessionRecorded || status.Uploaded != 0 || status.Downloaded != 0 || status.Conflicts != 0 {
			return errors.New("running agent sync status must not be finished")
		}
	} else if _, err := time.Parse(time.RFC3339Nano, status.FinishedAt); err != nil {
		return errors.New("finished agent sync status has an invalid finished_at")
	}
	if status.LastSuccessAt != "" {
		if _, err := time.Parse(time.RFC3339Nano, status.LastSuccessAt); err != nil {
			return errors.New("agent sync status has an invalid last_success_at")
		}
	}
	if status.Uploaded < 0 || status.Downloaded < 0 || status.Conflicts < 0 {
		return errors.New("agent sync status counts must not be negative")
	}
	switch status.State {
	case "complete":
		if status.ErrorCode != "" || status.Conflicts != 0 {
			return errors.New("complete agent sync status must not contain an error")
		}
	case "conflict":
		if status.ErrorCode != "sync_conflict" || status.Conflicts == 0 {
			return errors.New("conflict agent sync status requires a conflict result")
		}
	case "failed":
		if status.ErrorCode != "sync_failed" {
			return errors.New("failed agent sync status requires sync_failed")
		}
	}
	return nil
}

type Config struct {
	ServerURL       string                   `json:"server_url"`
	DeviceID        string                   `json:"device_id"`
	AccessToken     string                   `json:"access_token"`
	DeviceProfileID string                   `json:"device_profile_id,omitempty"`
	DeviceTarget    string                   `json:"device_target,omitempty"`
	RootDir         string                   `json:"root_dir"`
	PathOverrides   map[string]string        `json:"path_overrides,omitempty"`
	DriverRoots     map[string]string        `json:"driver_roots,omitempty"`
	ROMRoots        map[string]string        `json:"rom_roots,omitempty"`
	ROMCache        map[string]ROMCacheEntry `json:"rom_cache,omitempty"`
	Streams         map[string]StreamState   `json:"streams,omitempty"`
	Pending         *PendingSync             `json:"pending_sync,omitempty"`
	LastSync        *AgentSyncStatus         `json:"last_sync,omitempty"`
}

func (config *Config) normalize() error {
	parsed, err := url.Parse(strings.TrimSpace(config.ServerURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return errors.New("server URL must be an http(s) origin without credentials, path, query, or fragment")
	}
	parsed.Path = ""
	config.ServerURL = parsed.String()
	config.DeviceID = strings.TrimSpace(config.DeviceID)
	config.AccessToken = strings.TrimSpace(config.AccessToken)
	config.DeviceTarget = strings.ToLower(strings.TrimSpace(config.DeviceTarget))
	if config.DeviceID == "" || config.AccessToken == "" {
		return errors.New("device ID and access token are required")
	}
	if len(config.DeviceTarget) > 128 || strings.ContainsAny(config.DeviceTarget, "\x00\r\n") {
		return errors.New("device target is invalid")
	}
	if strings.TrimSpace(config.RootDir) == "" {
		return errors.New("root directory is required")
	}
	config.RootDir, err = filepath.Abs(config.RootDir)
	if err != nil {
		return err
	}
	if config.PathOverrides == nil {
		config.PathOverrides = map[string]string{}
	}
	if config.DriverRoots == nil {
		config.DriverRoots = map[string]string{}
	}
	normalizedDriverRoots := map[string]string{}
	for id, value := range config.DriverRoots {
		id, value = strings.TrimSpace(id), strings.TrimSpace(value)
		if id == "" || value == "" {
			return errors.New("driver roots require non-empty driver IDs and paths")
		}
		for _, char := range id {
			if !(char >= 'a' && char <= 'z') && !(char >= 'A' && char <= 'Z') && !(char >= '0' && char <= '9') && char != '-' && char != '_' && char != '.' {
				return errors.New("driver root IDs may contain only letters, numbers, dot, dash, and underscore")
			}
		}
		absolute, absoluteErr := filepath.Abs(value)
		if absoluteErr != nil {
			return absoluteErr
		}
		normalizedDriverRoots[id] = absolute
	}
	config.DriverRoots = normalizedDriverRoots
	if config.Streams == nil {
		config.Streams = map[string]StreamState{}
	}
	if config.ROMRoots == nil {
		config.ROMRoots = map[string]string{}
	}
	if config.ROMCache == nil {
		config.ROMCache = map[string]ROMCacheEntry{}
	}
	if config.LastSync != nil {
		if err = config.LastSync.validate(); err != nil {
			return err
		}
	}
	return nil
}

func LoadConfig(path string) (Config, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Config{}, err
	}
	if runtime.GOOS == "windows" {
		if err = secureConfigFile(path); err != nil {
			return Config{}, fmt.Errorf("secure private agent config: %w", err)
		}
	} else if info.Mode().Perm()&0o077 != 0 {
		return Config{}, fmt.Errorf("agent config permissions must not allow group or other access (current %04o)", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var config Config
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&config); err != nil {
		return Config{}, err
	}
	if err = config.normalize(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func SaveConfig(path string, config Config) error {
	if err := config.normalize(); err != nil {
		return err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temp, err := os.CreateTemp(filepath.Dir(abs), ".varkiv-agent-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err = temp.Chmod(0o600); err == nil {
		_, err = temp.Write(data)
	}
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = secureConfigFile(tempPath); err != nil {
		return fmt.Errorf("secure private agent config: %w", err)
	}
	if _, err = os.Lstat(abs); err == nil {
		return errors.New("agent config already exists; refusing to overwrite without an explicit update")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err = os.Link(tempPath, abs); err != nil {
		return err
	}
	return os.Remove(tempPath)
}

func UpdateConfig(path string, config Config) error {
	if err := config.normalize(); err != nil {
		return err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temp, err := os.CreateTemp(filepath.Dir(abs), ".varkiv-agent-update-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err = temp.Chmod(0o600); err == nil {
		_, err = temp.Write(data)
	}
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = secureConfigFile(tempPath); err != nil {
		return fmt.Errorf("secure private agent config update: %w", err)
	}
	return replaceFile(tempPath, abs)
}
