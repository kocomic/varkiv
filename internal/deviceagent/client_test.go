package deviceagent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPairDoesNotSubmitAdministratorDeviceProfile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/pairing-codes/redeem" {
			t.Errorf("unexpected pairing request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		var request struct {
			Device map[string]any `json:"device"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if _, exists := request.Device["device_profile_id"]; exists {
			t.Errorf("agent attempted to select the administrator-bound profile: %#v", request.Device)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"device":{"id":"fixture-device","device_profile_id":"fixture-profile"},"device_target":"windows","access_token":"fixture-token"}`))
	}))
	defer server.Close()

	config, err := Pair(context.Background(), PairInput{ServerURL: server.URL, Code: "ABCDE-FGHIJ", Name: "Fixture handheld", OSFamily: "windows", Architecture: "amd64", RootDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if config.DeviceProfileID != "fixture-profile" || config.DeviceTarget != "windows" {
		t.Fatalf("server-bound profile was not persisted: %#v", config)
	}
}

func TestPairRejectsMissingServerProfileBinding(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"device":{"id":"fixture-device"},"device_target":"windows","access_token":"fixture-token"}`))
	}))
	defer server.Close()

	if _, err := Pair(context.Background(), PairInput{ServerURL: server.URL, Code: "ABCDE-FGHIJ", Name: "Fixture handheld", OSFamily: "windows", RootDir: t.TempDir()}); err == nil {
		t.Fatal("agent accepted a pairing response without the administrator-bound profile")
	}
}

func TestAgentClientRefusesRedirectsWithoutForwardingSecrets(t *testing.T) {
	var sinkHits atomic.Int32
	var sinkAuthorization atomic.Value
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sinkHits.Add(1)
		sinkAuthorization.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer sink.Close()

	var originAuthorization string
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originAuthorization = r.Header.Get("Authorization")
		w.Header().Set("Location", sink.URL+"/must-not-run")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer origin.Close()

	var output map[string]any
	err := doJSON(context.Background(), defaultClient(), http.MethodPost, origin.URL+"/sync", "fixture-token-must-not-move", "", map[string]string{"private": "save-payload"}, &output)
	if err == nil || !strings.Contains(err.Error(), "HTTP 307") {
		t.Fatalf("redirect was not rejected: %v", err)
	}
	if originAuthorization != "Bearer fixture-token-must-not-move" || sinkHits.Load() != 0 {
		t.Fatalf("secret crossed redirect boundary: originAuth=%t sinkHits=%d", originAuthorization != "", sinkHits.Load())
	}
	if value := sinkAuthorization.Load(); value != nil {
		t.Fatalf("redirect sink received authorization: %q", value)
	}
	if strings.Contains(err.Error(), "fixture-token") || strings.Contains(err.Error(), sink.URL) {
		t.Fatalf("redirect error disclosed private transport data: %v", err)
	}
}

func TestAgentJSONResponseLimitRejectsOversizeInsteadOfTruncating(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
		_, _ = w.Write([]byte(strings.Repeat(" ", maxAgentJSONResponse)))
	}))
	defer server.Close()

	var output map[string]any
	err := doJSON(context.Background(), defaultClient(), http.MethodGet, server.URL, "", "", nil, &output)
	if err == nil || err.Error() != "server response exceeded the size limit" {
		t.Fatalf("oversized JSON response was accepted or misreported: %v", err)
	}
}

func TestProtocolIdentifiersAndHashesAreOpaque(t *testing.T) {
	if value, err := protocolPathSegment("session-0123"); err != nil || value != "session-0123" {
		t.Fatalf("safe protocol id rejected: %q %v", value, err)
	}
	for _, value := range []string{"", "../session", "session/operation", "session?token=secret", "%2e%2e", " identifier"} {
		if _, err := protocolPathSegment(value); err == nil {
			t.Fatalf("unsafe protocol id accepted: %q", value)
		}
	}
	digest := strings.Repeat("A", 64)
	if value, err := protocolSHA256(digest); err != nil || value != strings.Repeat("a", 64) {
		t.Fatalf("valid content hash rejected: %q %v", value, err)
	}
	for _, value := range []string{"", strings.Repeat("a", 63), strings.Repeat("g", 64), strings.Repeat("a", 65), "secret?hash=" + strings.Repeat("a", 64)} {
		if _, err := protocolSHA256(value); err == nil {
			t.Fatalf("unsafe content hash accepted: %.40q", value)
		}
	}
}
