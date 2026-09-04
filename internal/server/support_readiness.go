package server

import (
	"net/http"
)

// getSupportReadiness exposes the same reviewed-evidence audit used by the
// release CLI. The catalog implementation is deliberately read-only and does
// not probe devices or open ROM, media, save, or configured source paths.
func (s *Server) getSupportReadiness(w http.ResponseWriter, r *http.Request) {
	report, err := s.store.HardwareReadiness(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}
