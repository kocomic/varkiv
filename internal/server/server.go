package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"varkiv/internal/catalog"
	"varkiv/internal/multiplayer"
	"varkiv/internal/saves"
	storagex "varkiv/internal/storage"
)

//go:embed web/*
var webFiles embed.FS

var (
	errArtifactOutsideLibrary = errors.New("artifact path must be inside library root")
	errArtifactMissing        = errors.New("artifact path must point to an existing file or directory before it can be added")
	errArtifactUnreadable     = errors.New("artifact file or directory cannot be read and hashed")
)

type Server struct {
	store                   *catalog.Store
	libraryRoot             string
	stateRoot               string
	webEmulatorAssets       string
	webEmulatorDir          string
	webEmulatorManifest     []webEmulatorAssetIdentity
	webEmulatorReport       webEmulatorAssetReport
	webNetplayAssets        string
	webNetplayDir           string
	webNetplayReport        webEmulatorAssetReport
	webNetplaySignalRaw     string
	webNetplayICEServersRaw string
	webNetplaySignal        *url.URL
	webNetplayProxy         *httputil.ReverseProxy
	webNetplayICEServers    []webNetplayICEServer
	saves                   *saves.Repository
	storage                 *storagex.Repository
	multiplayer             *multiplayer.Broker
	token                   string
	importKey               [32]byte
	thumbnailMu             sync.Mutex
	mux                     *http.ServeMux
}

type Option func(*Server)

func WithToken(token string) Option {
	return func(s *Server) { s.token = strings.TrimSpace(token) }
}

func WithStateRoot(path string) Option {
	return func(s *Server) {
		if value := strings.TrimSpace(path); value != "" {
			s.stateRoot = value
		}
	}
}

// WithWebEmulatorAssets enables the optional browser player. The value is the
// EmulatorJS data directory (for example /emulatorjs/ or a pinned HTTPS URL).
// EmulatorJS itself is deliberately not embedded in the Apache-2.0 binary.
func WithWebEmulatorAssets(value string) Option {
	return func(s *Server) { s.webEmulatorAssets = strings.TrimSpace(value) }
}

// WithWebEmulatorDirectory serves an operator-provided EmulatorJS data/
// directory from /emulatorjs/. The third-party files remain outside the
// Varkiv repository and container image.
func WithWebEmulatorDirectory(value string) Option {
	return func(s *Server) { s.webEmulatorDir = strings.TrimSpace(value) }
}

// WithWebNetplay configures the isolated EmulatorJS/WebRTC experiment. The
// browser receives only the same-origin Varkiv endpoint; signalUpstream is
// an operator-controlled internal HTTP endpoint and is never exposed in API
// responses or generated player markup.
func WithWebNetplay(assets, directory, signalUpstream, iceServersJSON string) Option {
	return func(s *Server) {
		s.webNetplayAssets = strings.TrimSpace(assets)
		s.webNetplayDir = strings.TrimSpace(directory)
		s.webNetplaySignalRaw = strings.TrimSpace(signalUpstream)
		s.webNetplayICEServersRaw = strings.TrimSpace(iceServersJSON)
	}
}

func New(store *catalog.Store, libraryRoot string, options ...Option) (*Server, error) {
	s := &Server{store: store, libraryRoot: libraryRoot, stateRoot: filepath.Join(libraryRoot, ".library-data"), webEmulatorManifest: webEmulatorAssetManifest, multiplayer: multiplayer.NewBroker(), mux: http.NewServeMux()}
	if _, err := rand.Read(s.importKey[:]); err != nil {
		return nil, fmt.Errorf("create import signing key: %w", err)
	}
	for _, option := range options {
		option(s)
	}
	if s.webEmulatorAssets != "" && s.webEmulatorDir != "" {
		return nil, errors.New("web emulator assets URL and local directory are mutually exclusive")
	}
	if s.webEmulatorDir != "" {
		var err error
		s.webEmulatorDir, s.webEmulatorReport, err = validateWebEmulatorDirectory(s.webEmulatorDir, s.webEmulatorManifest)
		if err != nil {
			return nil, err
		}
		s.webEmulatorAssets = "/emulatorjs/"
	}
	if err := validateWebEmulatorAssets(s.webEmulatorAssets); err != nil {
		return nil, err
	}
	if err := s.configureWebNetplay(); err != nil {
		return nil, err
	}
	stateRoot, err := filepath.Abs(s.stateRoot)
	if err != nil {
		return nil, err
	}
	s.stateRoot = stateRoot
	if err = os.MkdirAll(s.stateRoot, 0o755); err != nil {
		return nil, err
	}
	saveRepo, err := saves.New(store, filepath.Join(s.stateRoot, "saves"))
	if err != nil {
		return nil, err
	}
	s.saves = saveRepo
	storageRepo, err := storagex.New(s.libraryRoot, s.stateRoot)
	if err != nil {
		return nil, err
	}
	s.storage = storageRepo
	if err = EnsureDefaults(context.Background(), store); err != nil {
		return nil, err
	}
	s.routes()
	return s, nil
}

func (s *Server) Handler() http.Handler { return securityHeaders(apiContract(s.authorize(s.mux))) }

// EnsureDefaults initializes and reconciles the built-in runtime catalog and
// package profiles for every product entrypoint. It intentionally has no file
// storage side effects, so CLI imports and exports can share the same database
// contract as the Web service without constructing a Server.
func EnsureDefaults(ctx context.Context, store *catalog.Store) error {
	seed := &Server{store: store}
	if err := seed.ensureRuntimeCatalog(ctx); err != nil {
		return fmt.Errorf("initialize runtime catalog: %w", err)
	}
	if err := seed.ensureDefaultPackageProfiles(ctx); err != nil {
		return fmt.Errorf("initialize package profiles: %w", err)
	}
	return nil
}

func (s *Server) routes() {
	s.multiplayerRoutes()
	s.apiRoutes("/api")
	s.apiRoutes("/api/v1")
	s.mux.HandleFunc("GET /api/v1/health/live", s.healthLive)
	s.mux.HandleFunc("GET /api/v1/health/ready", s.healthReady)
	s.mux.HandleFunc("GET /api/v1/capabilities", s.capabilities)
	s.mux.HandleFunc("GET /api/v1/openapi.yaml", s.openAPISpec)
	s.mux.HandleFunc("GET /play/{token}", s.webEmulationPlayer)
	s.mux.HandleFunc("GET /play-netplay/{token}", s.webNetplayPlayer)
	if s.webEmulatorDir != "" {
		s.mux.HandleFunc("GET /emulatorjs/{path...}", s.webEmulatorAsset)
	}
	if s.webNetplayDir != "" {
		s.mux.HandleFunc("GET /emulatorjs-netplay/{path...}", s.webNetplayEmulatorAsset)
	}
	if s.webNetplayProxy != nil {
		s.mux.HandleFunc("GET /list", s.webNetplaySignalProxy)
		s.mux.HandleFunc("GET /games", s.webNetplaySignalProxy)
		s.mux.HandleFunc("GET /socket.io/{path...}", s.webNetplaySignalProxy)
		s.mux.HandleFunc("POST /socket.io/{path...}", s.webNetplaySignalProxy)
	}
	s.mux.HandleFunc("/api", s.apiNotFound)
	s.mux.HandleFunc("/api/", s.apiNotFound)
	root, _ := fs.Sub(webFiles, "web")
	s.mux.Handle("/", http.FileServer(http.FS(root)))
}

func (s *Server) apiNotFound(w http.ResponseWriter, r *http.Request) {
	if methods := allowedAPIMethods(r.URL.Path); len(methods) > 0 {
		w.Header().Set("Allow", strings.Join(methods, ", "))
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	writeAPIError(w, http.StatusNotFound, "api_route_not_found", "API route not found")
}

func (s *Server) apiRoutes(prefix string) {
	s.mux.HandleFunc("GET "+prefix, s.apiRoot)
	s.mux.HandleFunc("GET "+prefix+"/health", s.health)
	s.mux.HandleFunc("GET "+prefix+"/platforms", s.listPlatforms)
	s.mux.HandleFunc("GET "+prefix+"/platforms/{id}", s.getPlatform)
	s.mux.HandleFunc("GET "+prefix+"/custom-platforms", s.listCustomPlatforms)
	s.mux.HandleFunc("POST "+prefix+"/custom-platforms", s.createCustomPlatform)
	s.mux.HandleFunc("GET "+prefix+"/custom-platforms/{id}", s.getCustomPlatform)
	s.mux.HandleFunc("PUT "+prefix+"/custom-platforms/{id}", s.updateCustomPlatform)
	s.mux.HandleFunc("DELETE "+prefix+"/custom-platforms/{id}", s.deleteCustomPlatform)
	s.mux.HandleFunc("GET "+prefix+"/source-adapters", s.listSourceAdapters)
	s.mux.HandleFunc("POST "+prefix+"/source-adapters", s.createSourceAdapter)
	s.mux.HandleFunc("GET "+prefix+"/source-adapters/{id}", s.getSourceAdapter)
	s.mux.HandleFunc("PUT "+prefix+"/source-adapters/{id}", s.updateSourceAdapter)
	s.mux.HandleFunc("DELETE "+prefix+"/source-adapters/{id}", s.deleteSourceAdapter)
	s.mux.HandleFunc("GET "+prefix+"/frontend-adapters", s.listFrontendAdapters)
	s.mux.HandleFunc("POST "+prefix+"/frontend-adapters", s.createFrontendAdapter)
	s.mux.HandleFunc("GET "+prefix+"/frontend-adapters/{id}", s.getFrontendAdapter)
	s.mux.HandleFunc("PUT "+prefix+"/frontend-adapters/{id}", s.updateFrontendAdapter)
	s.mux.HandleFunc("DELETE "+prefix+"/frontend-adapters/{id}", s.deleteFrontendAdapter)
	s.mux.HandleFunc("GET "+prefix+"/device-profiles", s.listDeviceProfiles)
	s.mux.HandleFunc("POST "+prefix+"/device-profiles", s.createDeviceProfile)
	s.mux.HandleFunc("GET "+prefix+"/device-profiles/{id}", s.getDeviceProfile)
	s.mux.HandleFunc("PUT "+prefix+"/device-profiles/{id}", s.updateDeviceProfile)
	s.mux.HandleFunc("DELETE "+prefix+"/device-profiles/{id}", s.deleteDeviceProfile)
	s.mux.HandleFunc("GET "+prefix+"/emulator-drivers", s.listEmulatorDrivers)
	s.mux.HandleFunc("POST "+prefix+"/emulator-drivers", s.createEmulatorDriver)
	s.mux.HandleFunc("GET "+prefix+"/emulator-drivers/{id}", s.getEmulatorDriver)
	s.mux.HandleFunc("PUT "+prefix+"/emulator-drivers/{id}", s.updateEmulatorDriver)
	s.mux.HandleFunc("DELETE "+prefix+"/emulator-drivers/{id}", s.deleteEmulatorDriver)
	s.mux.HandleFunc("GET "+prefix+"/retroarch-cores", s.listRetroArchCores)
	s.mux.HandleFunc("POST "+prefix+"/retroarch-cores", s.createRetroArchCore)
	s.mux.HandleFunc("GET "+prefix+"/retroarch-cores/{id}", s.getRetroArchCore)
	s.mux.HandleFunc("PUT "+prefix+"/retroarch-cores/{id}", s.updateRetroArchCore)
	s.mux.HandleFunc("DELETE "+prefix+"/retroarch-cores/{id}", s.deleteRetroArchCore)
	s.mux.HandleFunc("GET "+prefix+"/core-mappings", s.listCoreMappings)
	s.mux.HandleFunc("POST "+prefix+"/core-mappings", s.createCoreMapping)
	s.mux.HandleFunc("GET "+prefix+"/core-mappings/resolve", s.resolveCoreMapping)
	s.mux.HandleFunc("GET "+prefix+"/core-mappings/{id}", s.getCoreMapping)
	s.mux.HandleFunc("PUT "+prefix+"/core-mappings/{id}", s.updateCoreMapping)
	s.mux.HandleFunc("DELETE "+prefix+"/core-mappings/{id}", s.deleteCoreMapping)
	s.mux.HandleFunc("GET "+prefix+"/launch-bindings", s.listLaunchBindings)
	s.mux.HandleFunc("POST "+prefix+"/launch-bindings", s.createLaunchBinding)
	s.mux.HandleFunc("GET "+prefix+"/launch-bindings/resolve", s.resolveLaunchBinding)
	s.mux.HandleFunc("GET "+prefix+"/launch-bindings/{id}", s.getLaunchBinding)
	s.mux.HandleFunc("PUT "+prefix+"/launch-bindings/{id}", s.updateLaunchBinding)
	s.mux.HandleFunc("DELETE "+prefix+"/launch-bindings/{id}", s.deleteLaunchBinding)
	s.mux.HandleFunc("GET "+prefix+"/web-emulation/editions/{id}", s.webEmulationEditionStatus)
	s.mux.HandleFunc("GET "+prefix+"/web-emulation/readiness", s.webEmulatorReadiness)
	s.mux.HandleFunc("POST "+prefix+"/web-emulation/sessions", s.createWebEmulationSession)
	s.mux.HandleFunc("GET "+prefix+"/web-emulation/content/{token}/{name}", s.webEmulationContent)
	s.mux.HandleFunc("GET "+prefix+"/web-emulation/saves/{token}", s.webEmulationSave)
	s.mux.HandleFunc("POST "+prefix+"/web-emulation/saves/{token}", s.webEmulationSave)
	s.mux.HandleFunc("GET "+prefix+"/web-netplay/readiness", s.webNetplayReadiness)
	s.mux.HandleFunc("POST "+prefix+"/web-netplay/sessions", s.createWebNetplaySession)
	s.mux.HandleFunc("POST "+prefix+"/web-netplay/sessions/join", s.joinWebNetplaySession)
	s.mux.HandleFunc("GET "+prefix+"/runtime-import-hints", s.listRuntimeImportHints)
	s.mux.HandleFunc("POST "+prefix+"/runtime-import-hints/batch/preview", s.previewRuntimeImportHintBatch)
	s.mux.HandleFunc("POST "+prefix+"/runtime-import-hints/batch/commit", s.commitRuntimeImportHintBatch)
	s.mux.HandleFunc("GET "+prefix+"/runtime-import-hints/{id}", s.getRuntimeImportHint)
	s.mux.HandleFunc("POST "+prefix+"/runtime-import-hints/{id}/apply", s.applyRuntimeImportHint)
	s.mux.HandleFunc("POST "+prefix+"/runtime-import-hints/{id}/dismiss", s.dismissRuntimeImportHint)
	s.mux.HandleFunc("POST "+prefix+"/hardware-acceptance/preview", s.previewHardwareAcceptance)
	s.mux.HandleFunc("POST "+prefix+"/hardware-acceptance/commit", s.commitHardwareAcceptance)
	s.mux.HandleFunc("GET "+prefix+"/support-readiness", s.getSupportReadiness)
	s.mux.HandleFunc("POST "+prefix+"/storage-cleanup/preview", s.previewManagedStorageCleanup)
	s.mux.HandleFunc("POST "+prefix+"/storage-cleanup/commit", s.commitManagedStorageCleanup)
	s.mux.HandleFunc("GET "+prefix+"/storage-cleanup/runs", s.listManagedStorageCleanupRuns)
	s.mux.HandleFunc("POST "+prefix+"/storage-cleanup/runs/{id}/restore", s.restoreManagedStorageCleanupRun)
	s.mux.HandleFunc("GET "+prefix+"/series", s.listSeries)
	s.mux.HandleFunc("POST "+prefix+"/series", s.createSeries)
	s.mux.HandleFunc("GET "+prefix+"/series/{id}", s.getSeries)
	s.mux.HandleFunc("PUT "+prefix+"/series/{id}", s.updateSeries)
	s.mux.HandleFunc("DELETE "+prefix+"/series/{id}", s.deleteSeries)
	s.mux.HandleFunc("PUT "+prefix+"/series/{id}/members/{game_id}", s.putSeriesMember)
	s.mux.HandleFunc("DELETE "+prefix+"/series/{id}/members/{game_id}", s.deleteSeriesMember)
	s.mux.HandleFunc("GET "+prefix+"/games", s.listGames)
	s.mux.HandleFunc("GET "+prefix+"/games/{id}", s.getGame)
	s.mux.HandleFunc("POST "+prefix+"/games", s.createGame)
	s.mux.HandleFunc("PUT "+prefix+"/games/{id}", s.updateGame)
	s.mux.HandleFunc("DELETE "+prefix+"/games/{id}", s.deleteGame)
	s.mux.HandleFunc("POST "+prefix+"/games/{id}/merge/preview", s.previewGameMerge)
	s.mux.HandleFunc("POST "+prefix+"/games/{id}/merge", s.mergeGame)
	s.mux.HandleFunc("PUT "+prefix+"/games/{id}/primary", s.setPrimaryEdition)
	s.mux.HandleFunc("POST "+prefix+"/editions", s.createEdition)
	s.mux.HandleFunc("GET "+prefix+"/editions/{id}", s.getEdition)
	s.mux.HandleFunc("PUT "+prefix+"/editions/{id}", s.updateEdition)
	s.mux.HandleFunc("DELETE "+prefix+"/editions/{id}", s.deleteEdition)
	s.mux.HandleFunc("POST "+prefix+"/editions/{id}/move", s.moveEdition)
	s.mux.HandleFunc("POST "+prefix+"/artifacts", s.createArtifact)
	s.mux.HandleFunc("GET "+prefix+"/artifacts/{id}", s.getArtifact)
	s.mux.HandleFunc("PUT "+prefix+"/artifacts/{id}", s.updateArtifact)
	s.mux.HandleFunc("DELETE "+prefix+"/artifacts/{id}", s.deleteArtifact)
	s.mux.HandleFunc("POST "+prefix+"/artifacts/recheck", s.recheckArtifacts)
	s.mux.HandleFunc("GET "+prefix+"/import-sources", s.listImportSources)
	s.mux.HandleFunc("GET "+prefix+"/sources", s.listLibrarySources)
	s.mux.HandleFunc("POST "+prefix+"/sources", s.createLibrarySource)
	s.mux.HandleFunc("GET "+prefix+"/sources/{id}", s.getLibrarySource)
	s.mux.HandleFunc("PUT "+prefix+"/sources/{id}", s.updateLibrarySource)
	s.mux.HandleFunc("DELETE "+prefix+"/sources/{id}", s.deleteLibrarySource)
	s.mux.HandleFunc("POST "+prefix+"/sources/{id}/scans", s.createSourceScan)
	s.mux.HandleFunc("GET "+prefix+"/source-scans", s.listSourceScans)
	s.mux.HandleFunc("GET "+prefix+"/source-scans/{id}", s.getSourceScan)
	s.mux.HandleFunc("POST "+prefix+"/source-scans/{id}/commit", s.commitSourceScan)
	s.mux.HandleFunc("POST "+prefix+"/imports/preview", s.previewImport)
	s.mux.HandleFunc("GET "+prefix+"/hash-sources", s.listHashSources)
	s.mux.HandleFunc("GET "+prefix+"/hash-identities/{sha256}", s.resolveHashIdentity)
	s.mux.HandleFunc("POST "+prefix+"/hash-packs/preview", s.previewHashPack)
	s.mux.HandleFunc("POST "+prefix+"/hash-packs/import", s.importHashPack)
	s.mux.HandleFunc("POST "+prefix+"/hash-packs/export", s.exportHashPack)
	s.mux.HandleFunc("POST "+prefix+"/imports/commit", s.commitImport)
	s.mux.HandleFunc("POST "+prefix+"/imports/roms/preview", s.previewROMImport)
	s.mux.HandleFunc("POST "+prefix+"/imports/roms/commit", s.commitROMImport)
	s.mux.HandleFunc("GET "+prefix+"/package-profiles", s.listPackageProfiles)
	s.mux.HandleFunc("POST "+prefix+"/package-profiles", s.createPackageProfile)
	s.mux.HandleFunc("GET "+prefix+"/config-template-presets", s.listConfigTemplatePresets)
	s.mux.HandleFunc("GET "+prefix+"/package-profiles/{id}", s.getPackageProfile)
	s.mux.HandleFunc("PUT "+prefix+"/package-profiles/{id}", s.updatePackageProfile)
	s.mux.HandleFunc("DELETE "+prefix+"/package-profiles/{id}", s.deletePackageProfile)
	s.mux.HandleFunc("POST "+prefix+"/package-profiles/{id}/plans", s.createPackagePlan)
	s.mux.HandleFunc("GET "+prefix+"/package-plans", s.listPackagePlans)
	s.mux.HandleFunc("GET "+prefix+"/package-plans/{id}", s.getPackagePlan)
	s.mux.HandleFunc("POST "+prefix+"/package-plans/{id}/build", s.buildPackagePlan)
	s.mux.HandleFunc("GET "+prefix+"/package-releases", s.listPackageReleases)
	s.mux.HandleFunc("GET "+prefix+"/package-releases/{id}", s.getPackageRelease)
	s.mux.HandleFunc("POST "+prefix+"/packages", s.buildPackage)
	s.mux.HandleFunc("GET "+prefix+"/devices", s.listDevices)
	s.mux.HandleFunc("POST "+prefix+"/devices", s.createDevice)
	s.mux.HandleFunc("GET "+prefix+"/devices/{id}", s.getDevice)
	s.mux.HandleFunc("PUT "+prefix+"/devices/{id}", s.updateDevice)
	s.mux.HandleFunc("DELETE "+prefix+"/devices/{id}", s.deleteDevice)
	s.mux.HandleFunc("POST "+prefix+"/devices/{id}/revoke", s.revokeDevice)
	s.mux.HandleFunc("POST "+prefix+"/devices/{id}/heartbeat", s.heartbeatDevice)
	s.mux.HandleFunc("POST "+prefix+"/pairing-codes", s.createPairingCode)
	s.mux.HandleFunc("POST "+prefix+"/pairing-codes/redeem", s.redeemPairingCode)
	s.mux.HandleFunc("GET "+prefix+"/saves", s.listSaveRevisions)
	s.mux.HandleFunc("GET "+prefix+"/saves/{id}", s.getSaveRevision)
	s.mux.HandleFunc("POST "+prefix+"/saves/upload", s.uploadSaveRevision)
	s.mux.HandleFunc("GET "+prefix+"/saves/{id}/download", s.downloadSaveRevision)
	s.mux.HandleFunc("GET "+prefix+"/save-streams", s.listSaveStreams)
	s.mux.HandleFunc("POST "+prefix+"/save-streams", s.createSaveStream)
	s.mux.HandleFunc("GET "+prefix+"/save-streams/{id}", s.getSaveStream)
	s.mux.HandleFunc("GET "+prefix+"/save-streams/{id}/revisions", s.listStreamRevisions)
	s.mux.HandleFunc("POST "+prefix+"/save-streams/{id}/revisions", s.uploadStreamRevision)
	s.mux.HandleFunc("GET "+prefix+"/save-compatibility-groups", s.listSaveCompatibilityGroups)
	s.mux.HandleFunc("GET "+prefix+"/runtime-attestations", s.listRuntimeAttestations)
	s.mux.HandleFunc("GET "+prefix+"/save-revisions/{id}", s.getSaveRevision)
	s.mux.HandleFunc("GET "+prefix+"/save-revisions/{id}/archive", s.downloadSaveRevisionArchive)
	s.mux.HandleFunc("GET "+prefix+"/save-revisions/{id}/files/{file_id}/content", s.downloadSaveRevisionFile)
	s.mux.HandleFunc("GET "+prefix+"/save-bindings", s.listSaveBindings)
	s.mux.HandleFunc("POST "+prefix+"/save-bindings", s.createSaveBinding)
	s.mux.HandleFunc("POST "+prefix+"/save-bindings/setup", s.createSaveSetup)
	s.mux.HandleFunc("GET "+prefix+"/save-bindings/{id}", s.getSaveBinding)
	s.mux.HandleFunc("PUT "+prefix+"/save-bindings/{id}", s.updateSaveBinding)
	s.mux.HandleFunc("DELETE "+prefix+"/save-bindings/{id}", s.deleteSaveBinding)
	s.mux.HandleFunc("GET "+prefix+"/sync/manifest", s.syncManifest)
	s.mux.HandleFunc("GET "+prefix+"/sync/config", s.syncDeviceConfig)
	s.mux.HandleFunc("POST "+prefix+"/sync/sessions", s.createSyncSession)
	s.mux.HandleFunc("GET "+prefix+"/sync/sessions", s.listSyncSessions)
	s.mux.HandleFunc("GET "+prefix+"/sync/sessions/{id}", s.getSyncSession)
	s.mux.HandleFunc("GET "+prefix+"/sync/sessions/{id}/inventory", s.listSyncInventory)
	s.mux.HandleFunc("GET "+prefix+"/sync/inventory-matches", s.listInventoryMatchReviews)
	s.mux.HandleFunc("POST "+prefix+"/sync/inventory-matches/preview", s.previewInventoryMatch)
	s.mux.HandleFunc("POST "+prefix+"/sync/inventory-matches/commit", s.commitInventoryMatch)
	s.mux.HandleFunc("POST "+prefix+"/sync/sessions/{id}/operations/{operation_id}/upload", s.uploadSyncOperation)
	s.mux.HandleFunc("POST "+prefix+"/sync/sessions/{id}/operations/{operation_id}/ack", s.ackSyncOperation)
	s.mux.HandleFunc("GET "+prefix+"/sync/sessions/{id}/operations/{operation_id}/files/{file_id}/content", s.downloadSyncOperationFile)
	s.mux.HandleFunc("GET "+prefix+"/media", s.listMedia)
	s.mux.HandleFunc("POST "+prefix+"/media/upload", s.uploadMedia)
	s.mux.HandleFunc("POST "+prefix+"/media/recheck", s.recheckMedia)
	s.mux.HandleFunc("GET "+prefix+"/media/{id}", s.getMedia)
	s.mux.HandleFunc("PUT "+prefix+"/media/{id}", s.updateMedia)
	s.mux.HandleFunc("DELETE "+prefix+"/media/{id}", s.deleteMedia)
	s.mux.HandleFunc("GET "+prefix+"/media/{id}/content", s.downloadMedia)
	s.mux.HandleFunc("GET "+prefix+"/media/{id}/thumbnail", s.downloadMediaThumbnail)
}

func (s *Server) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api") || publicAPIPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		header := r.Header.Get("Authorization")
		value := ""
		if strings.HasPrefix(header, "Bearer ") {
			value = strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
		}
		if s.token != "" && subtle.ConstantTimeCompare([]byte(value), []byte(s.token)) == 1 {
			next.ServeHTTP(w, r)
			return
		}
		if s.token == "" && value == "" {
			next.ServeHTTP(w, r)
			return
		}
		identity, err := s.store.AuthenticateClientToken(r.Context(), hashClientSecret(value))
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="game-library"`)
			writeAPIError(w, http.StatusUnauthorized, "authentication_required", "authentication required")
			return
		}
		if !clientPathAllowed(r, identity) {
			writeAPIError(w, http.StatusForbidden, "insufficient_scope", "client token cannot access this resource")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), clientIdentityKey{}, identity)))
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), gamepad=(self)")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; img-src 'self' data: blob:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}
