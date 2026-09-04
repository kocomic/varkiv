package server

import (
	"crypto/rand"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"varkiv/internal/buildinfo"
	"varkiv/internal/bundler"
	"varkiv/internal/catalog"
	"varkiv/internal/platforms"
	"varkiv/internal/saves"
	storagex "varkiv/internal/storage"
)

const apiVersion = "v1"

//go:embed openapi.yaml
var openAPI []byte

type apiErrorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

type apiErrorEnvelope struct {
	Error apiErrorBody `json:"error"`
}

type pagination struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
	Total  int `json:"total"`
}

type collectionEnvelope[T any] struct {
	Data       []T        `json:"data"`
	Pagination pagination `json:"pagination"`
}

func apiContract(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api") {
			next.ServeHTTP(w, r)
			return
		}
		requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if !validRequestID(requestID) {
			var data [16]byte
			if _, err := rand.Read(data[:]); err == nil {
				requestID = hex.EncodeToString(data[:])
			} else {
				requestID = "request-id-unavailable"
			}
		}
		w.Header().Set("X-Request-ID", requestID)
		w.Header().Set("X-Varkiv-API-Version", apiVersion)
		if r.URL.Path == "/api/multiplayer/v1" || strings.HasPrefix(r.URL.Path, "/api/multiplayer/v1/") {
			w.Header().Set("X-Varkiv-Multiplayer-Version", "v1")
		}
		w.Header().Set("Cache-Control", "no-store")
		if (r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/")) && r.URL.Path != "/api/v1" && !strings.HasPrefix(r.URL.Path, "/api/v1/") && r.URL.Path != "/api/multiplayer/v1" && !strings.HasPrefix(r.URL.Path, "/api/multiplayer/v1/") {
			w.Header().Set("Deprecation", "true")
			w.Header().Set("Link", `</api/v1>; rel="successor-version"`)
		}
		next.ServeHTTP(w, r)
	})
}

func validRequestID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if !(r == '-' || r == '_' || r == '.' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}

func isV1(r *http.Request) bool {
	return r.URL.Path == "/api/v1" || strings.HasPrefix(r.URL.Path, "/api/v1/")
}

func resourceLocation(r *http.Request, resource, id string) string {
	prefix := "/api"
	if isV1(r) {
		prefix = "/api/v1"
	}
	return prefix + "/" + resource + "/" + id
}

func publicAPIPath(path string) bool {
	return path == "/api" || path == "/api/v1" || path == "/api/health" || path == "/api/v1/health" || path == "/api/v1/health/live" || path == "/api/v1/health/ready" || path == "/api/v1/capabilities" || path == "/api/v1/openapi.yaml" || path == "/api/web-emulation/readiness" || path == "/api/v1/web-emulation/readiness" || path == "/api/web-netplay/readiness" || path == "/api/v1/web-netplay/readiness" || path == "/api/web-netplay/sessions/join" || path == "/api/v1/web-netplay/sessions/join" || path == "/api/multiplayer/v1" || path == "/api/multiplayer/v1/capabilities" || path == "/api/multiplayer/v1/openapi.yaml" || multiplayerJoinPath(path) || strings.HasPrefix(path, "/api/v1/web-emulation/content/") || strings.HasPrefix(path, "/api/v1/web-emulation/saves/") || pairingRedeemPath(path)
}

func multiplayerJoinPath(path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	return len(parts) == 6 && parts[0] == "api" && parts[1] == "multiplayer" && parts[2] == "v1" && parts[3] == "sessions" && parts[4] != "" && parts[5] == "join"
}

func allowedAPIMethods(path string) []string {
	if path == "/api/multiplayer/v1" || path == "/api/multiplayer/v1/capabilities" || path == "/api/multiplayer/v1/openapi.yaml" {
		return []string{http.MethodGet}
	}
	if path == "/api/multiplayer/v1/sessions" {
		return []string{http.MethodPost}
	}
	if strings.HasPrefix(path, "/api/multiplayer/v1/sessions/") {
		parts := strings.Split(strings.TrimPrefix(path, "/api/multiplayer/v1/sessions/"), "/")
		if len(parts) == 1 && parts[0] != "" {
			return []string{http.MethodGet}
		}
		if len(parts) == 2 && parts[0] != "" && (parts[1] == "join" || parts[1] == "close") {
			return []string{http.MethodPost}
		}
	}
	if strings.HasPrefix(path, "/api/v1") {
		path = strings.TrimPrefix(path, "/api/v1")
	} else {
		path = strings.TrimPrefix(path, "/api")
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 1 {
		switch parts[0] {
		case "":
			return []string{http.MethodGet}
		case "health", "capabilities", "openapi.yaml", "platforms", "import-sources", "saves", "media", "source-scans", "package-plans", "package-releases", "runtime-import-hints", "support-readiness", "config-template-presets", "save-compatibility-groups", "runtime-attestations", "hash-sources":
			return []string{http.MethodGet}
		case "games", "devices", "series", "sources", "package-profiles", "custom-platforms", "source-adapters", "frontend-adapters", "device-profiles", "emulator-drivers", "retroarch-cores", "core-mappings", "launch-bindings", "save-bindings", "save-streams":
			return []string{http.MethodGet, http.MethodPost}
		case "pairing-codes":
			return []string{http.MethodPost}
		case "hardware-acceptance":
			return nil
		case "storage-cleanup":
			return nil
		case "editions", "artifacts", "packages":
			return []string{http.MethodPost}
		}
	}
	if len(parts) == 2 {
		switch parts[0] {
		case "web-netplay":
			if parts[1] == "readiness" {
				return []string{http.MethodGet}
			}
			if parts[1] == "sessions" {
				return []string{http.MethodPost}
			}
		case "web-emulation":
			if parts[1] == "readiness" {
				return []string{http.MethodGet}
			}
			if parts[1] == "sessions" {
				return []string{http.MethodPost}
			}
		case "sync":
			if parts[1] == "manifest" || parts[1] == "config" {
				return []string{http.MethodGet}
			}
			if parts[1] == "sessions" {
				return []string{http.MethodGet, http.MethodPost}
			}
			if parts[1] == "inventory-matches" {
				return []string{http.MethodGet}
			}
		case "platforms":
			return []string{http.MethodGet}
		case "pairing-codes":
			if parts[1] == "redeem" {
				return []string{http.MethodPost}
			}
		case "save-bindings":
			if parts[1] == "setup" {
				return []string{http.MethodPost}
			}
			return []string{http.MethodGet, http.MethodPut, http.MethodDelete}
		case "save-streams", "save-revisions":
			return []string{http.MethodGet}
		case "editions", "devices", "series", "custom-platforms", "source-adapters", "frontend-adapters", "device-profiles", "emulator-drivers", "retroarch-cores", "core-mappings", "launch-bindings", "runtime-import-hints":
			if parts[0] == "runtime-import-hints" {
				return []string{http.MethodGet}
			}
			if (parts[0] == "core-mappings" || parts[0] == "launch-bindings") && parts[1] == "resolve" {
				return []string{http.MethodGet}
			}
			return []string{http.MethodGet, http.MethodPut, http.MethodDelete}
		case "artifacts":
			if parts[1] == "recheck" {
				return []string{http.MethodPost}
			}
			return []string{http.MethodGet, http.MethodPut, http.MethodDelete}
		case "games":
			return []string{http.MethodGet, http.MethodPut, http.MethodDelete}
		case "sources":
			return []string{http.MethodGet, http.MethodPut, http.MethodDelete}
		case "source-scans":
			return []string{http.MethodGet}
		case "package-profiles":
			return []string{http.MethodGet, http.MethodPut, http.MethodDelete}
		case "package-plans", "package-releases":
			return []string{http.MethodGet}
		case "imports":
			if parts[1] == "preview" || parts[1] == "commit" {
				return []string{http.MethodPost}
			}
		case "hash-packs":
			if parts[1] == "preview" || parts[1] == "import" || parts[1] == "export" {
				return []string{http.MethodPost}
			}
		case "hash-identities":
			return []string{http.MethodGet}
		case "hardware-acceptance":
			if parts[1] == "preview" || parts[1] == "commit" {
				return []string{http.MethodPost}
			}
		case "storage-cleanup":
			if parts[1] == "preview" || parts[1] == "commit" {
				return []string{http.MethodPost}
			}
			if parts[1] == "runs" {
				return []string{http.MethodGet}
			}
		case "saves":
			if parts[1] == "upload" {
				return []string{http.MethodPost}
			}
			return []string{http.MethodGet}
		case "media":
			if parts[1] == "upload" || parts[1] == "recheck" {
				return []string{http.MethodPost}
			}
			return []string{http.MethodGet, http.MethodPut, http.MethodDelete}
		case "health":
			if parts[1] == "live" || parts[1] == "ready" {
				return []string{http.MethodGet}
			}
		}
	}
	if len(parts) == 3 {
		if parts[0] == "web-netplay" && parts[1] == "sessions" && parts[2] == "join" {
			return []string{http.MethodPost}
		}
		if parts[0] == "runtime-import-hints" && parts[1] == "batch" && (parts[2] == "preview" || parts[2] == "commit") {
			return []string{http.MethodPost}
		}
		if parts[0] == "web-emulation" && parts[1] == "saves" {
			return []string{http.MethodGet, http.MethodPost}
		}
		if parts[0] == "web-emulation" && parts[1] == "editions" {
			return []string{http.MethodGet}
		}
		if parts[0] == "sync" && parts[1] == "inventory-matches" && (parts[2] == "preview" || parts[2] == "commit") {
			return []string{http.MethodPost}
		}
		if parts[0] == "save-revisions" && parts[2] == "archive" {
			return []string{http.MethodGet}
		}
		if parts[0] == "runtime-import-hints" && (parts[2] == "apply" || parts[2] == "dismiss") {
			return []string{http.MethodPost}
		}
		if (parts[0] == "sources" && parts[2] == "scans") || (parts[0] == "source-scans" && parts[2] == "commit") {
			return []string{http.MethodPost}
		}
		if (parts[0] == "package-profiles" && parts[2] == "plans") || (parts[0] == "package-plans" && parts[2] == "build") {
			return []string{http.MethodPost}
		}
		if parts[0] == "imports" && parts[1] == "roms" && (parts[2] == "preview" || parts[2] == "commit") {
			return []string{http.MethodPost}
		}
		if parts[0] == "games" && (parts[2] == "merge" || parts[2] == "primary") {
			if parts[2] == "primary" {
				return []string{http.MethodPut}
			}
			return []string{http.MethodPost}
		}
		if (parts[0] == "editions" && parts[2] == "move") || (parts[0] == "saves" && parts[2] == "download") || (parts[0] == "media" && (parts[2] == "content" || parts[2] == "thumbnail")) {
			if parts[0] == "saves" || parts[0] == "media" {
				return []string{http.MethodGet}
			}
			return []string{http.MethodPost}
		}
		if parts[0] == "devices" && (parts[2] == "revoke" || parts[2] == "heartbeat") {
			return []string{http.MethodPost}
		}
		if parts[0] == "save-streams" && parts[2] == "revisions" {
			return []string{http.MethodGet, http.MethodPost}
		}
		if parts[0] == "sync" && parts[1] == "sessions" {
			return []string{http.MethodGet}
		}
	}
	if len(parts) == 4 && parts[0] == "sync" && parts[1] == "sessions" && parts[3] == "inventory" {
		return []string{http.MethodGet}
	}
	if len(parts) == 4 && parts[0] == "games" && parts[2] == "merge" && parts[3] == "preview" {
		return []string{http.MethodPost}
	}
	if len(parts) == 4 && parts[0] == "web-emulation" && parts[1] == "content" {
		return []string{http.MethodGet}
	}
	if len(parts) == 5 && parts[0] == "save-revisions" && parts[2] == "files" && parts[4] == "content" {
		return []string{http.MethodGet}
	}
	if len(parts) == 6 && parts[0] == "sync" && parts[1] == "sessions" && parts[3] == "operations" && (parts[5] == "upload" || parts[5] == "ack") {
		return []string{http.MethodPost}
	}
	if len(parts) == 8 && parts[0] == "sync" && parts[1] == "sessions" && parts[3] == "operations" && parts[5] == "files" && parts[7] == "content" {
		return []string{http.MethodGet}
	}
	if len(parts) == 4 && parts[0] == "series" && parts[2] == "members" {
		return []string{http.MethodPut, http.MethodDelete}
	}
	if len(parts) == 4 && parts[0] == "storage-cleanup" && parts[1] == "runs" && parts[3] == "restore" {
		return []string{http.MethodPost}
	}
	return nil
}

func (s *Server) apiRoot(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"name":            "Varkiv API",
		"api_version":     apiVersion,
		"service_version": buildinfo.Version,
		"links": map[string]string{
			"capabilities":           "/api/v1/capabilities",
			"multiplayer":            "/api/multiplayer/v1",
			"web_emulator_readiness": "/api/v1/web-emulation/readiness",
			"health":                 "/api/v1/health",
			"liveness":               "/api/v1/health/live",
			"readiness":              "/api/v1/health/ready",
			"openapi":                "/api/v1/openapi.yaml",
			"media":                  "/api/v1/media",
			"games":                  "/api/v1/games",
			"series":                 "/api/v1/series",
			"sources":                "/api/v1/sources",
			"source_scans":           "/api/v1/source-scans",
			"hash_sources":           "/api/v1/hash-sources",
			"hash_pack_preview":      "/api/v1/hash-packs/preview",
			"hash_pack_export":       "/api/v1/hash-packs/export",
			"package_profiles":       "/api/v1/package-profiles",
			"package_plans":          "/api/v1/package-plans",
			"package_releases":       "/api/v1/package-releases",
			"source_adapters":        "/api/v1/source-adapters",
			"frontend_adapters":      "/api/v1/frontend-adapters",
			"device_profiles":        "/api/v1/device-profiles",
			"emulator_drivers":       "/api/v1/emulator-drivers",
			"retroarch_cores":        "/api/v1/retroarch-cores",
			"core_mappings":          "/api/v1/core-mappings",
			"launch_bindings":        "/api/v1/launch-bindings",
			"web_emulation":          "/api/v1/web-emulation/sessions",
			"web_netplay":            "/api/v1/web-netplay/sessions",
			"runtime_hints":          "/api/v1/runtime-import-hints",
			"sync_manifest":          "/api/v1/sync/manifest",
			"pairing_codes":          "/api/v1/pairing-codes",
			"save_bindings":          "/api/v1/save-bindings",
			"save_compatibility":     "/api/v1/save-compatibility-groups",
			"runtime_attestations":   "/api/v1/runtime-attestations",
			"sync_sessions":          "/api/v1/sync/sessions",
			"storage_cleanup":        "/api/v1/storage-cleanup/preview",
		},
	})
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "name": "Varkiv", "version": buildinfo.Version, "api_version": apiVersion, "auth_required": s.token != ""})
}

func (s *Server) healthLive(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) healthReady(w http.ResponseWriter, r *http.Request) {
	version, err := s.store.SchemaVersion(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "not_ready", "database is not ready")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready", "schema_version": version, "supported_schema_version": catalog.CurrentSchemaVersion})
}

const capabilitiesContractVersion = 1

type capabilityDocument struct {
	ContractVersion    int             `json:"contract_version"`
	APIVersion         string          `json:"api_version"`
	Imports            []string        `json:"imports"`
	Exports            []string        `json:"exports"`
	DeviceTargets      []string        `json:"device_targets"`
	FileModes          []string        `json:"file_modes"`
	ROMImportStorage   []string        `json:"rom_import_storage"`
	MediaImportStorage []string        `json:"media_import_storage"`
	MediaUploadMaxMB   int             `json:"media_upload_max_mb"`
	Locales            []string        `json:"locales"`
	PlatformPresets    int             `json:"platform_presets"`
	CustomPlatforms    int             `json:"custom_platforms"`
	SaveUploadMaxMB    int             `json:"save_upload_max_mb"`
	Features           map[string]bool `json:"features"`
}

func capabilityFeatures() map[string]bool {
	return map[string]bool{
		"rom_scan_preview":             true,
		"import_preview_tokens":        true,
		"atomic_import_batches":        true,
		"missing_rom_detection":        true,
		"inventory_match_confirmation": true,
		"persistent_sources":           true,
		"source_scan_audit":            true,
		"source_adapters":              true,
		"bounded_source_preview":       true,
		"package_plans":                true,
		"package_release_history":      true,
		"safe_config_templates":        true,
		"unmanaged_output_guard":       true,
		"frontend_adapters":            true,
		"device_profiles":              true,
		"emulator_drivers":             true,
		"retroarch_core_mappings":      true,
		"launch_bindings":              true,
		"runtime_import_review":        true,
		"hardware_acceptance_review":   true,
		"structured_launch_roundtrip":  true,
		"portable_runtime_catalog_v2":  true,
		"portable_builtin_snapshots":   true,
		"reserved_builtin_namespace":   true,
		"neutral_manifest_v5":          true,
		"neutral_manifest_v6":          true,
		"portable_custom_platforms":    true,
		"custom_platforms":             true,
		"edition_grouping":             true,
		"game_merge_preview":           true,
		"cross_platform_series":        true,
		"multilingual_names":           true,
		"save_revisions":               true,
		"multi_file_save_streams":      true,
		"device_pairing":               true,
		"save_bindings":                true,
		"runtime_identity_attestation": true,
		"verified_cross_driver_saves":  true,
		"sync_negotiation":             true,
		"device_sync_manifest":         true,
		"directory_rom_inventory":      true,
		"managed_roms":                 true,
		"media_assets":                 true,
		"media_thumbnails":             true,
		"managed_storage_quarantine":   true,
		"managed_storage_restore":      true,
		"hash_pack_import":             true,
		"hash_pack_export":             true,
		"hash_identity_provenance":     true,
		"web_emulation":                false,
		"web_netplay":                  false,
		"device_agent":                 true,
	}
}

func (s *Server) capabilities(w http.ResponseWriter, r *http.Request) {
	customPlatforms, err := s.store.ListCustomPlatforms(r.Context(), true)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, capabilityDocument{
		ContractVersion: capabilitiesContractVersion,
		APIVersion:      apiVersion,
		Imports:         []string{"rom-file", "rom-directory", "pegasus", "es-de", "varkiv-v6", "varkiv-v5-compatible", "varkiv-v4-compatible", "varkiv-hashpack-v1"},
		Exports:         []string{"pegasus", "es-de", "varkiv-hashpack-v1"},
		DeviceTargets: []string{
			"windows", "steamos-bazzite", "android", "rocknix", "darkos", "arkos", "knulli", "muos", "onionos", "portable",
		},
		FileModes:        []string{"copy", "hardlink", "reference"},
		ROMImportStorage: []string{"reference", "copy"},
		MediaImportStorage: []string{
			"copy", "reference", "ignore",
		},
		MediaUploadMaxMB: 64,
		Locales:          []string{"zh-CN", "zh-TW", "ja", "en"},
		PlatformPresets:  len(platforms.All()),
		CustomPlatforms:  len(customPlatforms),
		SaveUploadMaxMB:  64,
		Features: func() map[string]bool {
			features := capabilityFeatures()
			features["web_emulation"] = s.webEmulatorAssets != ""
			features["web_netplay"] = s.webNetplayProxy != nil && s.webNetplayAssets != ""
			return features
		}(),
	})
}

func (s *Server) openAPISpec(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(openAPI)
}

func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeAPIError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(value); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_json", "invalid JSON: "+err.Error())
		return false
	}
	if err = decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeAPIError(w, http.StatusBadRequest, "invalid_json", "request body must contain exactly one JSON value")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "encoding_failed", "response encoding failed")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(append(data, '\n'))
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, apiErrorEnvelope{Error: apiErrorBody{Code: code, Message: message, RequestID: w.Header().Get("X-Request-ID")}})
}

func writeError(w http.ResponseWriter, err error) {
	status, code, message := http.StatusInternalServerError, "internal_error", "internal server error"
	var maxBytes *http.MaxBytesError
	switch {
	case errors.As(err, &maxBytes):
		status, code, message = http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds the allowed size"
	case errors.Is(err, errImportPreviewStale):
		status, code, message = http.StatusConflict, "import_preview_stale", "the import source or library changed after preview; generate a new preview"
	case errors.Is(err, errHashPackPreviewStale):
		status, code, message = http.StatusConflict, "hash_pack_preview_stale", "the hash pack changed after preview; review the exact file again"
	case errors.Is(err, catalog.ErrHashReleaseConflict):
		status, code, message = http.StatusConflict, "hash_release_conflict", "the same source release already exists with different content; publish a new release identifier"
	case errors.Is(err, catalog.ErrGameMergeStale):
		status, code, message = http.StatusConflict, "game_merge_stale", "the game, editions, media, or series changed after preview; review the merge again"
	case errors.Is(err, catalog.ErrInvalidGameMerge):
		status, code, message = http.StatusBadRequest, "invalid_argument", err.Error()
	case errors.Is(err, catalog.ErrSaveBindingIdentityRequired):
		status, code, message = http.StatusBadRequest, "save_binding_identity_required", err.Error()
	case errors.Is(err, errHardwareAcceptanceStale):
		status, code, message = http.StatusConflict, "hardware_acceptance_stale", "the report selection or runtime catalog changed after preview; review the report again"
	case errors.Is(err, errManagedCleanupStale):
		status, code, message = http.StatusConflict, "managed_cleanup_stale", "managed storage or catalog references changed after preview; generate a new cleanup preview"
	case errors.Is(err, catalog.ErrInventoryMatchStale):
		status, code, message = http.StatusConflict, "inventory_match_stale", "the inventory identity or candidate set changed after review; review the match again"
	case errors.Is(err, catalog.ErrInventoryMatchNotAmbiguous):
		status, code, message = http.StatusConflict, "inventory_match_not_ambiguous", "the inventory item no longer requires manual confirmation"
	case errors.Is(err, catalog.ErrRuntimeHintBatchStale):
		status, code, message = http.StatusConflict, "runtime_hint_batch_stale", "a runtime hint, edition, or catalog definition changed after preview; review the batch again"
	case errors.Is(err, catalog.ErrRuntimeHintBatchConflict):
		status, code, message = http.StatusConflict, "runtime_hint_batch_conflict", "one selected edition already has a launch binding for this device; no hints were applied"
	case errors.Is(err, storagex.ErrManagedStorageChanged):
		status, code, message = http.StatusConflict, "managed_cleanup_stale", "managed storage changed after preview; generate a new cleanup preview"
	case errors.Is(err, storagex.ErrManagedStorageUnsafe):
		status, code, message = http.StatusConflict, "managed_storage_unsafe", "managed storage contains a symbolic link or unsupported file type; no files were moved"
	case errors.Is(err, storagex.ErrCleanupRunNotFound):
		status, code, message = http.StatusNotFound, "cleanup_run_not_found", "cleanup recovery operation not found"
	case errors.Is(err, storagex.ErrCleanupRestoreConflict):
		status, code, message = http.StatusConflict, "cleanup_restore_conflict", "a managed path is occupied; no files were restored"
	case errors.Is(err, storagex.ErrCleanupRecoveryDamaged):
		status, code, message = http.StatusConflict, "cleanup_recovery_damaged", "cleanup recovery data is unavailable or changed; no files were restored"
	case errors.Is(err, storagex.ErrMediaUnavailable):
		status, code, message = http.StatusNotFound, "media_content_unavailable", "media content is unavailable; restore the source or managed blob"
	case errors.Is(err, storagex.ErrMediaIntegrity):
		status, code, message = http.StatusConflict, "media_content_integrity_failed", "media content does not match its recorded identity; no bytes were returned"
	case errors.Is(err, errMediaThumbnailUnsupported):
		status, code, message = http.StatusUnsupportedMediaType, "media_thumbnail_unsupported", "media cannot be safely decoded as a PNG, JPEG, or GIF thumbnail"
	case errors.Is(err, errMediaThumbnailUnverified):
		status, code, message = http.StatusUnprocessableEntity, "media_thumbnail_unverified", "media needs a verified SHA-256 identity before a thumbnail can be cached"
	case errors.Is(err, errMediaThumbnailTooLarge):
		status, code, message = http.StatusUnprocessableEntity, "media_thumbnail_too_large", "media dimensions exceed thumbnail safety limits"
	case errors.Is(err, catalog.ErrImportDuplicate):
		status, code, message = http.StatusConflict, "import_batch_conflict", "the import batch conflicts with existing content; no selected item was imported"
	case errors.Is(err, sql.ErrNoRows):
		status, code, message = http.StatusNotFound, "not_found", "resource not found"
	case errors.Is(err, catalog.ErrPlatformMismatch):
		status, code, message = http.StatusConflict, "platform_mismatch", err.Error()
	case errors.Is(err, catalog.ErrDeviceHasSaveRevisions):
		status, code, message = http.StatusConflict, "device_has_save_revisions", err.Error()
	case errors.Is(err, catalog.ErrPairedDeviceIdentityInUse):
		status, code, message = http.StatusConflict, "paired_device_identity_in_use", err.Error()
	case errors.Is(err, catalog.ErrDeviceRevocationRequired):
		status, code, message = http.StatusConflict, "device_revocation_required", err.Error()
	case errors.Is(err, catalog.ErrSourceHasScans):
		status, code, message = http.StatusConflict, "source_has_scan_history", err.Error()
	case errors.Is(err, catalog.ErrPackageProfileHasHistory):
		status, code, message = http.StatusConflict, "package_profile_has_history", err.Error()
	case errors.Is(err, catalog.ErrBuiltinImmutable):
		status, code, message = http.StatusConflict, "builtin_immutable", err.Error()
	case errors.Is(err, catalog.ErrBuiltinNamespaceReserved):
		status, code, message = http.StatusConflict, "builtin_namespace_reserved", err.Error()
	case errors.Is(err, catalog.ErrRuntimeObjectInUse):
		status, code, message = http.StatusConflict, "runtime_object_in_use", err.Error()
	case errors.Is(err, catalog.ErrCustomPlatformInUse):
		status, code, message = http.StatusConflict, "custom_platform_in_use", err.Error()
	case errors.Is(err, catalog.ErrPlatformDefinitionConflict):
		status, code, message = http.StatusConflict, "platform_definition_conflict", err.Error()
	case errors.Is(err, catalog.ErrPlatformDefinitionDisabled):
		status, code, message = http.StatusConflict, "platform_definition_disabled", err.Error()
	case errors.Is(err, catalog.ErrRuntimeDefinitionConflict):
		status, code, message = http.StatusConflict, "runtime_definition_conflict", err.Error()
	case errors.Is(err, catalog.ErrRuntimeDefinitionDisabled):
		status, code, message = http.StatusConflict, "runtime_definition_disabled", err.Error()
	case errors.Is(err, platforms.ErrRegistryConflict):
		status, code, message = http.StatusConflict, "platform_key_conflict", err.Error()
	case errors.Is(err, catalog.ErrPairingCodeInvalid):
		status, code, message = http.StatusBadRequest, "pairing_code_invalid", err.Error()
	case errors.Is(err, catalog.ErrPairingCodeExpired):
		status, code, message = http.StatusGone, "pairing_code_expired", err.Error()
	case errors.Is(err, catalog.ErrPairingCodeRedeemed):
		status, code, message = http.StatusConflict, "pairing_code_redeemed", err.Error()
	case errors.Is(err, catalog.ErrPairingDeviceProfileMismatch):
		status, code, message = http.StatusConflict, "pairing_device_profile_mismatch", err.Error()
	case errors.Is(err, catalog.ErrPairingDeviceProfileUnavailable):
		status, code, message = http.StatusConflict, "pairing_device_profile_unavailable", err.Error()
	case errors.Is(err, catalog.ErrPairingDeviceProfileIncompatible):
		status, code, message = http.StatusConflict, "pairing_device_profile_incompatible", err.Error()
	case errors.Is(err, catalog.ErrClientTokenInvalid):
		status, code, message = http.StatusUnauthorized, "client_token_invalid", err.Error()
	case errors.Is(err, catalog.ErrDeviceRevoked):
		status, code, message = http.StatusForbidden, "device_revoked", err.Error()
	case errors.Is(err, catalog.ErrSaveRevisionNotBound):
		status, code, message = http.StatusForbidden, "save_revision_not_bound", "save revision is not bound to this device profile"
	case errors.Is(err, catalog.ErrSaveRuntimeNotAttested):
		status, code, message = http.StatusConflict, "save_runtime_not_attested", "the device has not attested the exact runtime identities required by this save stream"
	case errors.Is(err, catalog.ErrRuntimeAttestationNotRequested):
		status, code, message = http.StatusUnprocessableEntity, "runtime_attestation_not_requested", "the runtime identity was not requested for this device"
	case errors.Is(err, catalog.ErrIdempotencyConflict):
		status, code, message = http.StatusConflict, "idempotency_conflict", err.Error()
	case errors.Is(err, saves.ErrContentHashMismatch):
		status, code, message = http.StatusUnprocessableEntity, "save_content_hash_mismatch", err.Error()
	case errors.Is(err, catalog.ErrOperationHashMismatch):
		status, code, message = http.StatusUnprocessableEntity, "sync_operation_hash_mismatch", err.Error()
	case errors.Is(err, bundler.ErrUnmanagedTargetConflict):
		status, code, message = http.StatusConflict, "unmanaged_target_conflict", err.Error()
	case errors.Is(err, errPackagePlanStale):
		status, code, message = http.StatusConflict, "package_plan_stale", "the package profile, catalog, source files, or plan lifetime changed; create a new plan"
	case errors.Is(err, errArtifactOutsideLibrary):
		status, code, message = http.StatusBadRequest, "artifact_outside_library", errArtifactOutsideLibrary.Error()
	case errors.Is(err, errArtifactMissing):
		status, code, message = http.StatusBadRequest, "artifact_missing", errArtifactMissing.Error()
	case errors.Is(err, errArtifactUnreadable):
		status, code, message = http.StatusUnprocessableEntity, "artifact_unreadable", errArtifactUnreadable.Error()
	case strings.Contains(err.Error(), "UNIQUE constraint"):
		status, code, message = http.StatusConflict, "already_exists", "resource already exists"
	case strings.Contains(err.Error(), "required"), strings.Contains(err.Error(), " must "), strings.Contains(err.Error(), "inside library"), strings.Contains(err.Error(), "out of range"), strings.Contains(err.Error(), "invalid"), strings.Contains(err.Error(), "unknown"), strings.Contains(err.Error(), "malformed"), strings.Contains(err.Error(), "exactly one"):
		status, code, message = http.StatusBadRequest, "invalid_argument", err.Error()
	}
	writeAPIError(w, status, code, message)
}

func pageBounds(r *http.Request, total int) (int, int, pagination, error) {
	request, err := collectionPageRequest(r)
	if err != nil {
		return 0, 0, pagination{}, err
	}
	limit, offset := request.Limit, request.Offset
	start := min(offset, total)
	end := min(start+limit, total)
	return start, end, pagination{Limit: limit, Offset: offset, Total: total}, nil
}

func collectionPageRequest(r *http.Request) (catalog.PageRequest, error) {
	limit, offset := 100, 0
	var err error
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 200 {
			return catalog.PageRequest{}, fmt.Errorf("limit must be between 1 and 200")
		}
	}
	if value := r.URL.Query().Get("offset"); value != "" {
		offset, err = strconv.Atoi(value)
		if err != nil || offset < 0 {
			return catalog.PageRequest{}, fmt.Errorf("offset must be zero or greater")
		}
	}
	return catalog.PageRequest{Limit: limit, Offset: offset}, nil
}

func writeCatalogPage[T any](w http.ResponseWriter, page catalog.Page[T]) {
	writeJSON(w, http.StatusOK, collectionEnvelope[T]{
		Data:       page.Items,
		Pagination: pagination{Limit: page.Limit, Offset: page.Offset, Total: page.Total},
	})
}

func writeCollection[T any](w http.ResponseWriter, r *http.Request, items []T) {
	if !isV1(r) {
		writeJSON(w, http.StatusOK, items)
		return
	}
	start, end, page, err := pageBounds(r, len(items))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, collectionEnvelope[T]{Data: items[start:end], Pagination: page})
}
