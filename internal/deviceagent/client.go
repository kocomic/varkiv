package deviceagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const maxAgentJSONResponse = 8 << 20

var protocolIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]{0,127}$`)
var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type PairInput struct {
	ServerURL     string
	Code          string
	Name          string
	OSFamily      string
	Distribution  string
	Architecture  string
	AgentVersion  string
	RootDir       string
	PathOverrides map[string]string
	DriverRoots   map[string]string
	ROMRoots      map[string]string
	AllowHTTP     bool
}

type pairResponse struct {
	Device struct {
		ID              string `json:"id"`
		DeviceProfileID string `json:"device_profile_id"`
	} `json:"device"`
	DeviceTarget string `json:"device_target"`
	AccessToken  string `json:"access_token"`
}

type apiFailure struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func endpoint(origin, path string) string { return strings.TrimRight(origin, "/") + path }

func protocolPathSegment(value string) (string, error) {
	if !protocolIDPattern.MatchString(value) {
		return "", errors.New("server returned an invalid protocol identifier")
	}
	return value, nil
}

func protocolSHA256(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if !sha256Pattern.MatchString(value) {
		return "", errors.New("server returned an invalid content hash")
	}
	return value, nil
}

func safeHTTPOrigin(value string, allowHTTP bool) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("server must be an http(s) origin without credentials, path, query, or fragment")
	}
	if parsed.Scheme == "http" && !allowHTTP {
		host := parsed.Hostname()
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return "", errors.New("non-loopback HTTP requires --allow-http because pairing credentials are otherwise unencrypted")
		}
	}
	parsed.Path = ""
	return parsed.String(), nil
}

func Pair(ctx context.Context, input PairInput) (Config, error) {
	origin, err := safeHTTPOrigin(input.ServerURL, input.AllowHTTP)
	if err != nil {
		return Config{}, err
	}
	if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Code) == "" {
		return Config{}, errors.New("device name and pairing code are required")
	}
	if input.OSFamily == "" {
		input.OSFamily = runtime.GOOS
	}
	if input.Architecture == "" {
		input.Architecture = runtime.GOARCH
	}
	payload := map[string]any{"code": input.Code, "device": map[string]any{
		"name": input.Name, "os_family": input.OSFamily,
		"distribution": input.Distribution, "architecture": input.Architecture, "agent_version": input.AgentVersion,
		"capabilities": map[string]bool{"save_streams": true, "multi_file_saves": true, "atomic_no_overwrite": true},
	}}
	var response pairResponse
	if err = doJSON(ctx, defaultClient(), http.MethodPost, endpoint(origin, "/api/v1/pairing-codes/redeem"), "", "", payload, &response); err != nil {
		return Config{}, err
	}
	if response.Device.ID, err = protocolPathSegment(response.Device.ID); err != nil {
		return Config{}, err
	}
	if response.Device.DeviceProfileID, err = protocolPathSegment(response.Device.DeviceProfileID); err != nil {
		return Config{}, err
	}
	if strings.TrimSpace(response.Device.DeviceProfileID) == "" || strings.TrimSpace(response.DeviceTarget) == "" {
		return Config{}, errors.New("pairing response did not include the administrator-selected device profile")
	}
	config := Config{ServerURL: origin, DeviceID: response.Device.ID, AccessToken: response.AccessToken, DeviceProfileID: response.Device.DeviceProfileID, DeviceTarget: response.DeviceTarget, RootDir: input.RootDir, PathOverrides: input.PathOverrides, DriverRoots: input.DriverRoots, ROMRoots: input.ROMRoots, ROMCache: map[string]ROMCacheEntry{}, Streams: map[string]StreamState{}}
	if err = config.normalize(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func doJSON(ctx context.Context, client *http.Client, method, target, token, idempotency string, input, output any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return err
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if idempotency != "" {
		req.Header.Set("Idempotency-Key", idempotency)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var failure apiFailure
		data, readErr := readAgentResponse(resp.Body, 1<<20)
		if readErr != nil {
			return errors.New("server error response exceeded the size limit")
		}
		_ = json.Unmarshal(data, &failure)
		if protocolIDPattern.MatchString(failure.Error.Code) {
			return fmt.Errorf("server rejected request: %s", failure.Error.Code)
		}
		return fmt.Errorf("server returned HTTP %d", resp.StatusCode)
	}
	data, err := readAgentResponse(resp.Body, maxAgentJSONResponse)
	if err != nil {
		return err
	}
	if output != nil {
		if len(bytes.TrimSpace(data)) == 0 {
			return errors.New("server returned an empty JSON response")
		}
		if err = json.Unmarshal(data, output); err != nil {
			return errors.New("server returned invalid JSON")
		}
	}
	return nil
}

func readAgentResponse(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("server response exceeded the size limit")
	}
	return data, nil
}

func defaultClient() *http.Client {
	return &http.Client{
		Timeout: 2 * time.Minute,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
