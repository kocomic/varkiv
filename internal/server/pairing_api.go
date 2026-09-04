package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"varkiv/internal/catalog"
)

type clientIdentityKey struct{}

func hashClientSecret(value string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(digest[:])
}

func randomHex(bytes int) (string, error) {
	data := make([]byte, bytes)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func randomPairingCode() (string, error) {
	const alphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"
	data := make([]byte, 10)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	for index := range data {
		data[index] = alphabet[int(data[index])%len(alphabet)]
	}
	return string(data[:5]) + "-" + string(data[5:]), nil
}

func normalizePairingCode(value string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(value), "-", ""))
}

func pairingRedeemPath(path string) bool {
	return path == "/api/v1/pairing-codes/redeem" || path == "/api/pairing-codes/redeem"
}

func hasScope(identity catalog.ClientIdentity, wanted string) bool {
	for _, scope := range identity.Scopes {
		if scope == wanted {
			return true
		}
	}
	return false
}

func clientPathAllowed(r *http.Request, identity catalog.ClientIdentity) bool {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	if path == r.URL.Path {
		path = strings.TrimPrefix(r.URL.Path, "/api")
	}
	if strings.HasPrefix(path, "/sync/") {
		if strings.HasPrefix(path, "/sync/inventory-matches") {
			return false
		}
		if path == "/sync/manifest" {
			return false
		}
		if r.Method == http.MethodGet {
			return hasScope(identity, "sync:read")
		}
		return hasScope(identity, "sync:write")
	}
	// Both shipped Agents only need metadata for the exact revision selected by
	// a negotiated sync operation. Collection enumeration, direct file/archive
	// downloads and diagnostic upload routes remain owner-only.
	if r.Method == http.MethodGet && strings.HasPrefix(path, "/save-revisions/") {
		tail := strings.TrimPrefix(path, "/save-revisions/")
		return tail != "" && !strings.Contains(tail, "/") && hasScope(identity, "sync:read")
	}
	if r.Method == http.MethodPost && path == "/devices/"+identity.DeviceID+"/heartbeat" {
		return hasScope(identity, "device:heartbeat")
	}
	return false
}

type pairingCodeRequest struct {
	ExpiresInSeconds int            `json:"expires_in_seconds"`
	RequestedDevice  map[string]any `json:"requested_device"`
}

func (s *Server) createPairingCode(w http.ResponseWriter, r *http.Request) {
	var in pairingCodeRequest
	if !decode(w, r, &in) {
		return
	}
	if in.ExpiresInSeconds == 0 {
		in.ExpiresInSeconds = 600
	}
	if in.ExpiresInSeconds < 120 || in.ExpiresInSeconds > 1800 {
		writeError(w, errors.New("expires_in_seconds must be between 120 and 1800"))
		return
	}
	profileID, ok := in.RequestedDevice["device_profile_id"].(string)
	profileID = strings.TrimSpace(profileID)
	if !ok || profileID == "" {
		writeAPIError(w, http.StatusBadRequest, "pairing_device_profile_required", "select an enabled device profile before creating a pairing code")
		return
	}
	profile, err := s.store.GetDeviceProfile(r.Context(), profileID)
	if err != nil || !profile.Enabled {
		writeError(w, catalog.ErrPairingDeviceProfileUnavailable)
		return
	}
	in.RequestedDevice = map[string]any{"device_profile_id": profile.ID}
	code, err := randomPairingCode()
	if err != nil {
		writeError(w, err)
		return
	}
	expires := time.Now().UTC().Add(time.Duration(in.ExpiresInSeconds) * time.Second)
	item, err := s.store.CreatePairingCode(r.Context(), catalog.NewPairingCode{CodeHash: hashClientSecret(normalizePairingCode(code)), RequestedDevice: in.RequestedDevice, ExpiresAt: expires})
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Location", resourceLocation(r, "pairing-codes", item.ID))
	writeJSON(w, http.StatusCreated, map[string]any{"id": item.ID, "code": code, "expires_at": item.ExpiresAt, "requested_device": item.RequestedDevice})
}

type pairingRedeemRequest struct {
	Code   string            `json:"code"`
	Device catalog.NewDevice `json:"device"`
}

func (s *Server) redeemPairingCode(w http.ResponseWriter, r *http.Request) {
	var in pairingRedeemRequest
	if !decode(w, r, &in) {
		return
	}
	normalized := normalizePairingCode(in.Code)
	if len(normalized) != 10 {
		writeAPIError(w, http.StatusBadRequest, "pairing_code_invalid", "pairing code is invalid")
		return
	}
	plainToken, err := randomHex(32)
	if err != nil {
		writeError(w, err)
		return
	}
	tokenExpires := time.Now().UTC().Add(90 * 24 * time.Hour)
	device, token, deviceTarget, err := s.store.RedeemPairingCode(r.Context(), hashClientSecret(normalized), hashClientSecret(plainToken), in.Device, []string{"sync:read", "sync:write", "device:heartbeat"}, tokenExpires)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"device": device, "device_target": deviceTarget, "access_token": plainToken, "token_type": "Bearer", "expires_at": token.ExpiresAt, "scopes": token.Scopes})
}

func (s *Server) revokeDevice(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.RevokeDevice(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) heartbeatDevice(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Capabilities        map[string]bool                    `json:"capabilities"`
		RuntimeAttestations []catalog.RuntimeAttestationReport `json:"runtime_attestations"`
	}
	if !decode(w, r, &input) {
		return
	}
	current, err := s.store.GetDevice(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	if current.Status == "revoked" {
		writeError(w, catalog.ErrDeviceRevoked)
		return
	}
	current.Status = "active"
	if current.Capabilities == nil {
		current.Capabilities = map[string]bool{}
	}
	allowed := map[string]bool{"runtime_probe": true, "runtime_file_grants_configured": true, "emulator_dir_configured": true, "core_dir_configured": true, "emulator_installed": true, "retroarch_core_installed": true}
	for key, value := range input.Capabilities {
		if allowed[key] {
			current.Capabilities[key] = value
		}
	}
	updated, err := s.store.RecordDeviceHeartbeat(r.Context(), current.ID, current.Capabilities, input.RuntimeAttestations)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}
