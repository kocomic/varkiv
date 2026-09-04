package server

import (
	"errors"
	"net/http"
	"strings"

	"varkiv/internal/catalog"
)

// listGames is intentionally limited to HTTP query translation. Filtering,
// ordering, pagination, and hydration belong to the catalog read model.
func (s *Server) listGames(w http.ResponseWriter, r *http.Request) {
	page := catalog.PageRequest{}
	if isV1(r) {
		var err error
		page, err = collectionPageRequest(r)
		if err != nil {
			writeError(w, err)
			return
		}
	}
	platform, err := s.canonicalPlatform(r.Context(), r.URL.Query().Get("platform"))
	if err != nil {
		writeError(w, err)
		return
	}
	query := catalog.GameReadQuery{
		Locale: r.URL.Query().Get("locale"), Search: r.URL.Query().Get("q"), Platform: platform, Page: page,
	}
	projection, err := collectionProjection(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if projection == "summary" {
		result, readErr := s.store.ReadGameSummaries(r.Context(), query)
		if readErr != nil {
			writeError(w, readErr)
			return
		}
		writeReadPage(w, r, result)
		return
	}
	result, err := s.store.ReadGames(r.Context(), query)
	if err != nil {
		writeError(w, err)
		return
	}
	writeReadPage(w, r, result)
}

func (s *Server) listSeries(w http.ResponseWriter, r *http.Request) {
	page := catalog.PageRequest{}
	if isV1(r) {
		var err error
		page, err = collectionPageRequest(r)
		if err != nil {
			writeError(w, err)
			return
		}
	}
	query := catalog.SeriesReadQuery{
		Locale: r.URL.Query().Get("locale"), Search: r.URL.Query().Get("q"), Page: page,
	}
	projection, err := collectionProjection(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if projection == "summary" {
		result, readErr := s.store.ReadSeriesSummaries(r.Context(), query)
		if readErr != nil {
			writeError(w, readErr)
			return
		}
		writeReadPage(w, r, result)
		return
	}
	result, err := s.store.ReadSeries(r.Context(), query)
	if err != nil {
		writeError(w, err)
		return
	}
	writeReadPage(w, r, result)
}

func collectionProjection(r *http.Request) (string, error) {
	projection := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("projection")))
	if projection == "" {
		return "full", nil
	}
	if projection != "full" && projection != "summary" {
		return "", errors.New("projection must be full or summary")
	}
	return projection, nil
}

func writeReadPage[T any](w http.ResponseWriter, r *http.Request, result catalog.Page[T]) {
	if isV1(r) {
		writeCatalogPage(w, result)
		return
	}
	writeJSON(w, http.StatusOK, result.Items)
}
