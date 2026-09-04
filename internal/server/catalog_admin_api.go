package server

import (
	"errors"
	"net/http"
	"strings"

	"varkiv/internal/catalog"
	"varkiv/internal/platforms"
)

func (s *Server) listPlatforms(w http.ResponseWriter, r *http.Request) {
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	category := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("category")))
	if category != "" && category != "console" && category != "handheld" && category != "arcade" && category != "computer" {
		writeError(w, errors.New("category must be console, handheld, arcade, or computer"))
		return
	}
	registry, err := s.store.PlatformRegistry(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	items := registry.All()
	filtered := make([]platforms.Platform, 0, len(items))
	for _, item := range items {
		terms := []string{item.ID, item.Name, item.NameZH, item.Vendor}
		terms = append(terms, item.Aliases...)
		terms = append(terms, item.ESDESystems...)
		searchable := strings.ToLower(strings.Join(terms, " "))
		if (category != "" && item.Category != category) || (query != "" && !strings.Contains(searchable, query)) {
			continue
		}
		filtered = append(filtered, item)
	}
	writeCollection(w, r, filtered)
}

func (s *Server) getPlatform(w http.ResponseWriter, r *http.Request) {
	registry, err := s.store.PlatformRegistry(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	item, ok := registry.Resolve(r.PathValue("id"))
	if !ok {
		writeAPIError(w, http.StatusNotFound, "platform_not_found", "platform preset not found")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) listCustomPlatforms(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListCustomPlatforms(r.Context(), true)
	if err != nil {
		writeError(w, err)
		return
	}
	writeCollection(w, r, items)
}

func (s *Server) createCustomPlatform(w http.ResponseWriter, r *http.Request) {
	var in catalog.NewCustomPlatform
	if !decode(w, r, &in) {
		return
	}
	item, err := s.store.CreateCustomPlatform(r.Context(), in)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Location", resourceLocation(r, "custom-platforms", item.ID))
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) getCustomPlatform(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.GetCustomPlatform(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) updateCustomPlatform(w http.ResponseWriter, r *http.Request) {
	var in catalog.NewCustomPlatform
	if !decode(w, r, &in) {
		return
	}
	item, err := s.store.UpdateCustomPlatform(r.Context(), r.PathValue("id"), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteCustomPlatform(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteCustomPlatform(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createSeries(w http.ResponseWriter, r *http.Request) {
	var in catalog.SeriesMutation
	if !decode(w, r, &in) {
		return
	}
	item, err := s.store.CreateSeriesMutation(r.Context(), in)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Location", resourceLocation(r, "series", item.ID))
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) getSeries(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.GetSeries(r.Context(), r.PathValue("id"), r.URL.Query().Get("locale"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) updateSeries(w http.ResponseWriter, r *http.Request) {
	var in catalog.SeriesMutation
	if !decode(w, r, &in) {
		return
	}
	item, err := s.store.UpdateSeriesMutation(r.Context(), r.PathValue("id"), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteSeries(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteSeries(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) putSeriesMember(w http.ResponseWriter, r *http.Request) {
	var in catalog.NewSeriesMember
	if !decode(w, r, &in) {
		return
	}
	item, err := s.store.PutSeriesMember(r.Context(), r.PathValue("id"), memberGameID(r), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteSeriesMember(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteSeriesMember(r.Context(), r.PathValue("id"), memberGameID(r)); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func memberGameID(r *http.Request) string {
	return r.PathValue("game_id")
}
