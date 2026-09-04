package server

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"

	"varkiv/internal/catalog"
	"varkiv/internal/saves"
)

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListDevices(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeCollection(w, r, items)
}

func (s *Server) getDevice(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.GetDevice(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) createDevice(w http.ResponseWriter, r *http.Request) {
	var in catalog.NewDevice
	if !decode(w, r, &in) {
		return
	}
	item, err := s.store.CreateDevice(r.Context(), in)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Location", resourceLocation(r, "devices", item.ID))
	writeJSON(w, 201, item)
}

func (s *Server) updateDevice(w http.ResponseWriter, r *http.Request) {
	var in catalog.NewDevice
	if !decode(w, r, &in) {
		return
	}
	item, err := s.store.UpdateDevice(r.Context(), r.PathValue("id"), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteDevice(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteDevice(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listSaveRevisions(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListSaveRevisions(r.Context(), r.URL.Query().Get("edition_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeCollection(w, r, items)
}

type syncArtifact struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Role      string `json:"role"`
	DiscIndex int    `json:"disc_index,omitempty"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256,omitempty"`
	Missing   bool   `json:"missing"`
}

type syncEdition struct {
	GameID         string                `json:"game_id"`
	GameTitle      string                `json:"game_title"`
	Platform       string                `json:"platform"`
	EditionID      string                `json:"edition_id"`
	EditionTitle   string                `json:"edition_title"`
	SaveNamespace  string                `json:"save_namespace"`
	Serial         string                `json:"serial,omitempty"`
	ProductCode    string                `json:"product_code,omitempty"`
	TitleID        string                `json:"title_id,omitempty"`
	Artifacts      []syncArtifact        `json:"artifacts"`
	RevisionCount  int                   `json:"revision_count"`
	LatestRevision *catalog.SaveRevision `json:"latest_revision,omitempty"`
}

// syncManifest is the compact identity document consumed by the device
// agent. The agent matches a local ROM to an Edition, then uses that Edition's
// stable save namespace when pushing emulator-managed save files.
func (s *Server) syncManifest(w http.ResponseWriter, r *http.Request) {
	games, err := s.store.ListGames(r.Context(), r.URL.Query().Get("locale"))
	if err != nil {
		writeError(w, err)
		return
	}
	revisions, err := s.store.ListSaveRevisions(r.Context(), "")
	if err != nil {
		writeError(w, err)
		return
	}
	byEdition := make(map[string][]catalog.SaveRevision)
	for _, revision := range revisions {
		byEdition[revision.EditionID] = append(byEdition[revision.EditionID], revision)
	}
	editions := make([]syncEdition, 0)
	for _, game := range games {
		for _, edition := range game.Editions {
			entry := syncEdition{
				GameID: game.ID, GameTitle: game.DisplayTitle, Platform: game.Platform,
				EditionID: edition.ID, EditionTitle: edition.DisplayTitle,
				SaveNamespace: edition.SaveNamespace, Serial: edition.Serial,
				ProductCode: edition.ProductCode, TitleID: edition.TitleID,
				Artifacts:     make([]syncArtifact, 0, len(edition.Artifacts)),
				RevisionCount: len(byEdition[edition.ID]),
			}
			for _, artifact := range edition.Artifacts {
				if !catalog.IsLaunchArtifactRole(artifact.Role) {
					continue
				}
				entry.Artifacts = append(entry.Artifacts, syncArtifact{
					ID: artifact.ID, Path: artifact.Path, Role: artifact.Role,
					DiscIndex: artifact.DiscIndex, Size: artifact.Size,
					SHA256: artifact.SHA256, Missing: artifact.Missing,
				})
			}
			if history := byEdition[edition.ID]; len(history) > 0 {
				latest := history[0]
				entry.LatestRevision = &latest
			}
			editions = append(editions, entry)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"api_version":    apiVersion,
		"matching_order": []string{"sha256", "serial", "product_code", "title_id"},
		"editions":       editions,
	})
}

func (s *Server) getSaveRevision(w http.ResponseWriter, r *http.Request) {
	item, err := s.saveRevisionForRequest(r, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) uploadSaveRevision(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeError(w, fmt.Errorf("invalid save upload: %w", err))
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, errors.New("save file is required"))
		return
	}
	defer file.Close()
	deviceID := r.FormValue("device_id")
	if identity, ok := clientIdentity(r); ok {
		deviceID = identity.DeviceID
	}
	result, err := s.saves.Push(r.Context(), saves.PushInput{
		EditionID: r.FormValue("edition_id"), DeviceID: deviceID, DriverID: r.FormValue("driver_id"), RelativePath: r.FormValue("relative_path"), ScopeType: r.FormValue("scope_type"), ScopeKey: r.FormValue("scope_key"), BaseRevisionID: r.FormValue("base_revision_id"),
	}, file)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Location", resourceLocation(r, "saves", result.Revision.ID))
	writeJSON(w, 201, result)
}

func (s *Server) downloadSaveRevision(w http.ResponseWriter, r *http.Request) {
	file, revision, err := s.saves.OpenRevision(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	defer file.Close()
	name := filepath.Base(filepath.FromSlash(revision.RelativePath))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, name))
	w.Header().Set("Content-Length", fmt.Sprint(revision.Size))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, file)
}
