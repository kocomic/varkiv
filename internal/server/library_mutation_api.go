package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"varkiv/internal/catalog"
	"varkiv/internal/filehash"
)

func (s *Server) getGame(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.GetGame(r.Context(), r.PathValue("id"), r.URL.Query().Get("locale"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, item)
}

func (s *Server) createGame(w http.ResponseWriter, r *http.Request) {
	var in catalog.NewGame
	if !decode(w, r, &in) {
		return
	}
	var err error
	in.Platform, err = s.canonicalPlatform(r.Context(), in.Platform)
	if err != nil {
		writeError(w, err)
		return
	}
	item, err := s.store.CreateGame(r.Context(), in)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Location", resourceLocation(r, "games", item.ID))
	writeJSON(w, 201, item)
}
func (s *Server) updateGame(w http.ResponseWriter, r *http.Request) {
	var in catalog.NewGame
	if !decode(w, r, &in) {
		return
	}
	var err error
	in.Platform, err = s.canonicalPlatform(r.Context(), in.Platform)
	if err != nil {
		writeError(w, err)
		return
	}
	item, err := s.store.UpdateGame(r.Context(), r.PathValue("id"), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, item)
}

func (s *Server) canonicalPlatform(ctx context.Context, value string) (string, error) {
	value = strings.TrimSpace(value)
	registry, err := s.store.PlatformRegistry(ctx)
	if err != nil {
		return "", err
	}
	if preset, ok := registry.Resolve(value); ok {
		return preset.ID, nil
	}
	return value, nil
}

func (s *Server) deleteGame(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteGame(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) setPrimaryEdition(w http.ResponseWriter, r *http.Request) {
	var in struct {
		EditionID string `json:"edition_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.store.SetPrimaryEdition(r.Context(), r.PathValue("id"), in.EditionID); err != nil {
		writeError(w, err)
		return
	}
	item, err := s.store.GetGame(r.Context(), r.PathValue("id"), r.URL.Query().Get("locale"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, item)
}

type editionRequest struct {
	catalog.NewEdition
	ArtifactPath string `json:"artifact_path"`
	ArtifactRole string `json:"artifact_role"`
	DiscIndex    int    `json:"disc_index"`
}

func (s *Server) createEdition(w http.ResponseWriter, r *http.Request) {
	var in editionRequest
	if !decode(w, r, &in) {
		return
	}
	var firstArtifact *catalog.NewArtifact
	if strings.TrimSpace(in.ArtifactPath) != "" {
		if in.NewEdition.ID == "" {
			in.NewEdition.ID = catalog.NewID()
		}
		a, err := s.artifactInput(in.NewEdition.ID, in.ArtifactPath, in.ArtifactRole, in.DiscIndex)
		if err != nil {
			writeError(w, err)
			return
		}
		firstArtifact = &a
	}
	var e catalog.Edition
	var err error
	if firstArtifact == nil {
		e, err = s.store.AddEdition(r.Context(), in.NewEdition)
	} else {
		e, err = s.store.AddEditionWithArtifact(r.Context(), in.NewEdition, *firstArtifact)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Location", resourceLocation(r, "editions", e.ID))
	games, err := s.store.ListGames(r.Context(), r.URL.Query().Get("locale"))
	if err != nil {
		writeError(w, err)
		return
	}
	for _, game := range games {
		if game.ID == e.GameID {
			writeJSON(w, 201, game)
			return
		}
	}
	writeJSON(w, 201, e)
}

func (s *Server) updateEdition(w http.ResponseWriter, r *http.Request) {
	var in editionRequest
	if !decode(w, r, &in) {
		return
	}
	item, err := s.store.UpdateEdition(r.Context(), r.PathValue("id"), in.NewEdition)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, item)
}

func (s *Server) getEdition(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.GetEdition(r.Context(), r.PathValue("id"), r.URL.Query().Get("locale"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteEdition(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteEdition(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) moveEdition(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TargetGameID string `json:"target_game_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	item, err := s.store.MoveEdition(r.Context(), r.PathValue("id"), in.TargetGameID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, item)
}
func (s *Server) createArtifact(w http.ResponseWriter, r *http.Request) {
	var in catalog.NewArtifact
	if !decode(w, r, &in) {
		return
	}
	a, err := s.artifactInput(in.EditionID, in.Path, in.Role, in.DiscIndex)
	if err != nil {
		writeError(w, err)
		return
	}
	if a.Missing {
		writeError(w, errArtifactMissing)
		return
	}
	item, err := s.store.AddArtifact(r.Context(), a)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Location", resourceLocation(r, "artifacts", item.ID))
	writeJSON(w, 201, item)
}

func (s *Server) getArtifact(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.GetArtifact(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) updateArtifact(w http.ResponseWriter, r *http.Request) {
	var in catalog.NewArtifact
	if !decode(w, r, &in) {
		return
	}
	existing, err := s.store.GetArtifact(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	if existing.StorageKind == "managed" && strings.TrimSpace(in.Path) != "" && strings.TrimSpace(in.Path) != existing.Path {
		writeError(w, errors.New("managed artifact path cannot be changed; import or relink a new artifact instead"))
		return
	}
	var a catalog.NewArtifact
	if existing.StorageKind == "managed" {
		a, err = s.recheckArtifact(existing, in.Role, in.DiscIndex)
	} else {
		a, err = s.artifactInput(existing.EditionID, in.Path, in.Role, in.DiscIndex)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	item, err := s.store.UpdateArtifact(r.Context(), existing.ID, a)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, item)
}

func (s *Server) deleteArtifact(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteArtifact(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) recheckArtifacts(w http.ResponseWriter, r *http.Request) {
	games, err := s.store.ListGames(r.Context(), "")
	if err != nil {
		writeError(w, err)
		return
	}
	result := struct {
		Checked int `json:"checked"`
		Missing int `json:"missing"`
	}{}
	for _, game := range games {
		for _, edition := range game.Editions {
			for _, existing := range edition.Artifacts {
				input, inputErr := s.recheckArtifact(existing, existing.Role, existing.DiscIndex)
				if inputErr != nil {
					writeError(w, inputErr)
					return
				}
				if _, err = s.store.UpdateArtifact(r.Context(), existing.ID, input); err != nil {
					writeError(w, err)
					return
				}
				result.Checked++
				if input.Missing {
					result.Missing++
				}
			}
		}
	}
	writeJSON(w, 200, result)
}

func (s *Server) recheckArtifact(existing catalog.Artifact, role string, disc int) (catalog.NewArtifact, error) {
	if existing.StorageKind != "managed" {
		return s.artifactInput(existing.EditionID, existing.Path, role, disc)
	}
	path, err := s.storage.ResolveArtifact(existing)
	if err != nil {
		return catalog.NewArtifact{}, err
	}
	info, statErr := os.Lstat(path)
	input := catalog.NewArtifact{EditionID: existing.EditionID, Path: existing.Path, StorageKind: "managed", SourcePath: existing.SourcePath, OriginalName: existing.OriginalName, Role: role, DiscIndex: disc, Missing: statErr != nil}
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return input, nil
		}
		return catalog.NewArtifact{}, statErr
	}
	if info.IsDir() {
		input.SHA256, input.Size, err = hashDirectory(path)
	} else {
		input.Size = info.Size()
		input.SHA256, err = hashFile(path)
	}
	return input, err
}

func (s *Server) artifactInput(editionID, value, role string, disc int) (catalog.NewArtifact, error) {
	root, err := filepath.Abs(s.libraryRoot)
	if err != nil {
		return catalog.NewArtifact{}, err
	}
	path := filepath.FromSlash(strings.TrimSpace(value))
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path, err = filepath.Abs(filepath.Clean(path))
	if err != nil {
		return catalog.NewArtifact{}, err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return catalog.NewArtifact{}, err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return catalog.NewArtifact{}, errArtifactOutsideLibrary
	}
	stored := filepath.ToSlash(rel)
	path, err = s.storage.ResolveArtifact(catalog.Artifact{Path: stored, StorageKind: "library"})
	if err != nil {
		return catalog.NewArtifact{}, errArtifactUnreadable
	}
	info, statErr := os.Lstat(path)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return catalog.NewArtifact{}, errArtifactMissing
		}
		return catalog.NewArtifact{}, errArtifactUnreadable
	}
	var size int64
	var hash string
	if info.IsDir() {
		hash, size, err = hashDirectory(path)
	} else {
		size = info.Size()
		hash, err = hashFile(path)
	}
	if err != nil {
		return catalog.NewArtifact{}, errArtifactUnreadable
	}
	if role == "" {
		role = "rom"
	}
	return catalog.NewArtifact{EditionID: editionID, Path: stored, StorageKind: "library", SourcePath: stored, OriginalName: filepath.Base(path), Role: role, DiscIndex: disc, Size: size, SHA256: hash}, nil
}

func hashDirectory(root string) (string, int64, error) {
	return filehash.Directory(root)
}

func hashFile(path string) (string, error) {
	digest, _, err := filehash.File(path)
	return digest, err
}
