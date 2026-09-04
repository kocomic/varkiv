package server

import (
	"crypto/hmac"
	"errors"
	"net/http"
	"strings"

	"varkiv/internal/catalog"
	"varkiv/internal/runtimecfg"
)

func (s *Server) listSourceAdapters(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListSourceAdapters(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeCollection(w, r, items)
}
func (s *Server) createSourceAdapter(w http.ResponseWriter, r *http.Request) {
	var in catalog.NewSourceAdapter
	if !decode(w, r, &in) {
		return
	}
	item, err := s.store.CreateSourceAdapter(r.Context(), in)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Location", resourceLocation(r, "source-adapters", item.ID))
	writeJSON(w, http.StatusCreated, item)
}
func (s *Server) getSourceAdapter(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.GetSourceAdapter(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}
func (s *Server) updateSourceAdapter(w http.ResponseWriter, r *http.Request) {
	var in catalog.NewSourceAdapter
	if !decode(w, r, &in) {
		return
	}
	item, err := s.store.UpdateSourceAdapter(r.Context(), r.PathValue("id"), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}
func (s *Server) deleteSourceAdapter(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteSourceAdapter(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listFrontendAdapters(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListFrontendAdapters(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeCollection(w, r, items)
}
func (s *Server) createFrontendAdapter(w http.ResponseWriter, r *http.Request) {
	var in catalog.NewFrontendAdapter
	if !decode(w, r, &in) {
		return
	}
	item, err := s.store.CreateFrontendAdapter(r.Context(), in)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Location", resourceLocation(r, "frontend-adapters", item.ID))
	writeJSON(w, http.StatusCreated, item)
}
func (s *Server) getFrontendAdapter(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.GetFrontendAdapter(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}
func (s *Server) updateFrontendAdapter(w http.ResponseWriter, r *http.Request) {
	var in catalog.NewFrontendAdapter
	if !decode(w, r, &in) {
		return
	}
	item, err := s.store.UpdateFrontendAdapter(r.Context(), r.PathValue("id"), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}
func (s *Server) deleteFrontendAdapter(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteFrontendAdapter(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listRuntimeImportHints(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListRuntimeImportHints(r.Context(), r.URL.Query().Get("edition_id"), r.URL.Query().Get("status"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeCollection(w, r, items)
}

func (s *Server) getRuntimeImportHint(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.GetRuntimeImportHint(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) applyRuntimeImportHint(w http.ResponseWriter, r *http.Request) {
	var in catalog.NewLaunchBinding
	if !decode(w, r, &in) {
		return
	}
	binding, err := s.store.ApplyRuntimeImportHint(r.Context(), r.PathValue("id"), in)
	if err != nil {
		writeError(w, err)
		return
	}
	hint, err := s.store.GetRuntimeImportHint(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, catalog.RuntimeHintApplication{Hint: hint, Binding: binding})
}

func (s *Server) dismissRuntimeImportHint(w http.ResponseWriter, r *http.Request) {
	hint, err := s.store.DismissRuntimeImportHint(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, hint)
}

type runtimeHintBatchRequest struct {
	HintIDs           []string `json:"hint_ids"`
	DeviceProfileID   string   `json:"device_profile_id"`
	DriverID          string   `json:"driver_id"`
	FrontendAdapterID string   `json:"frontend_adapter_id,omitempty"`
	CoreID            string   `json:"core_id,omitempty"`
	Arguments         []string `json:"arguments,omitempty"`
	PreviewToken      string   `json:"preview_token,omitempty"`
}

func (in runtimeHintBatchRequest) review() catalog.RuntimeHintBatchReview {
	return catalog.RuntimeHintBatchReview{
		HintIDs: in.HintIDs, DeviceProfileID: in.DeviceProfileID, DriverID: in.DriverID,
		FrontendAdapterID: in.FrontendAdapterID, CoreID: in.CoreID, Arguments: in.Arguments,
	}
}

type runtimeHintBatchPreview struct {
	PreviewToken        string   `json:"preview_token"`
	Count               int      `json:"count"`
	PlatformID          string   `json:"platform_id"`
	DeviceProfileID     string   `json:"device_profile_id"`
	DriverID            string   `json:"driver_id"`
	FrontendAdapterID   string   `json:"frontend_adapter_id,omitempty"`
	CoreID              string   `json:"core_id,omitempty"`
	Arguments           []string `json:"arguments"`
	FailurePolicy       string   `json:"failure_policy"`
	RawCommandsExecuted bool     `json:"raw_commands_executed"`
}

func (s *Server) previewRuntimeImportHintBatch(w http.ResponseWriter, r *http.Request) {
	var in runtimeHintBatchRequest
	if !decode(w, r, &in) {
		return
	}
	snapshot, err := s.store.ReviewRuntimeImportHintBatch(r.Context(), in.review())
	if err != nil {
		writeError(w, err)
		return
	}
	token, err := s.signPreviewValue(previewTokenDomainRuntimeHintBatch, snapshot)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, runtimeHintBatchPreview{
		PreviewToken: token, Count: len(snapshot.Hints), PlatformID: snapshot.PlatformID,
		DeviceProfileID: snapshot.Review.DeviceProfileID, DriverID: snapshot.Review.DriverID,
		FrontendAdapterID: snapshot.Review.FrontendAdapterID, CoreID: snapshot.Review.CoreID,
		Arguments: snapshot.Review.Arguments, FailurePolicy: "atomic", RawCommandsExecuted: false,
	})
}

func (s *Server) commitRuntimeImportHintBatch(w http.ResponseWriter, r *http.Request) {
	var in runtimeHintBatchRequest
	if !decode(w, r, &in) {
		return
	}
	snapshot, err := s.store.ReviewRuntimeImportHintBatch(r.Context(), in.review())
	if err != nil {
		if errors.Is(err, catalog.ErrRuntimeHintBatchConflict) {
			writeError(w, err)
		} else {
			writeError(w, catalog.ErrRuntimeHintBatchStale)
		}
		return
	}
	expected, err := s.signPreviewValue(previewTokenDomainRuntimeHintBatch, snapshot)
	if err != nil {
		writeError(w, err)
		return
	}
	if in.PreviewToken == "" || !hmac.Equal([]byte(expected), []byte(in.PreviewToken)) {
		writeError(w, catalog.ErrRuntimeHintBatchStale)
		return
	}
	result, err := s.store.ApplyRuntimeImportHintBatchIfSnapshot(r.Context(), snapshot)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) listDeviceProfiles(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListDeviceProfiles(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeCollection(w, r, items)
}
func (s *Server) createDeviceProfile(w http.ResponseWriter, r *http.Request) {
	var in catalog.NewDeviceProfile
	if !decode(w, r, &in) {
		return
	}
	item, err := s.store.CreateDeviceProfile(r.Context(), in)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Location", resourceLocation(r, "device-profiles", item.ID))
	writeJSON(w, http.StatusCreated, item)
}
func (s *Server) getDeviceProfile(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.GetDeviceProfile(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}
func (s *Server) updateDeviceProfile(w http.ResponseWriter, r *http.Request) {
	var in catalog.NewDeviceProfile
	if !decode(w, r, &in) {
		return
	}
	item, err := s.store.UpdateDeviceProfile(r.Context(), r.PathValue("id"), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}
func (s *Server) deleteDeviceProfile(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteDeviceProfile(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listEmulatorDrivers(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListEmulatorDrivers(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeCollection(w, r, items)
}
func (s *Server) createEmulatorDriver(w http.ResponseWriter, r *http.Request) {
	var in catalog.NewEmulatorDriver
	if !decode(w, r, &in) {
		return
	}
	item, err := s.store.CreateEmulatorDriver(r.Context(), in)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Location", resourceLocation(r, "emulator-drivers", item.ID))
	writeJSON(w, http.StatusCreated, item)
}
func (s *Server) getEmulatorDriver(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.GetEmulatorDriver(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}
func (s *Server) updateEmulatorDriver(w http.ResponseWriter, r *http.Request) {
	var in catalog.NewEmulatorDriver
	if !decode(w, r, &in) {
		return
	}
	item, err := s.store.UpdateEmulatorDriver(r.Context(), r.PathValue("id"), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}
func (s *Server) deleteEmulatorDriver(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteEmulatorDriver(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listRetroArchCores(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListRetroArchCores(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeCollection(w, r, items)
}
func (s *Server) createRetroArchCore(w http.ResponseWriter, r *http.Request) {
	var in catalog.NewRetroArchCore
	if !decode(w, r, &in) {
		return
	}
	item, err := s.store.CreateRetroArchCore(r.Context(), in)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Location", resourceLocation(r, "retroarch-cores", item.ID))
	writeJSON(w, http.StatusCreated, item)
}
func (s *Server) getRetroArchCore(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.GetRetroArchCore(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}
func (s *Server) updateRetroArchCore(w http.ResponseWriter, r *http.Request) {
	var in catalog.NewRetroArchCore
	if !decode(w, r, &in) {
		return
	}
	item, err := s.store.UpdateRetroArchCore(r.Context(), r.PathValue("id"), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}
func (s *Server) deleteRetroArchCore(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteRetroArchCore(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listCoreMappings(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListCoreMappings(r.Context(), r.URL.Query().Get("platform_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeCollection(w, r, items)
}
func (s *Server) createCoreMapping(w http.ResponseWriter, r *http.Request) {
	var in catalog.NewCoreMapping
	if !decode(w, r, &in) {
		return
	}
	var err error
	in.PlatformID, err = s.canonicalPlatform(r.Context(), in.PlatformID)
	if err != nil {
		writeError(w, err)
		return
	}
	item, err := s.store.CreateCoreMapping(r.Context(), in)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Location", resourceLocation(r, "core-mappings", item.ID))
	writeJSON(w, http.StatusCreated, item)
}
func (s *Server) getCoreMapping(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.GetCoreMapping(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}
func (s *Server) updateCoreMapping(w http.ResponseWriter, r *http.Request) {
	var in catalog.NewCoreMapping
	if !decode(w, r, &in) {
		return
	}
	var err error
	in.PlatformID, err = s.canonicalPlatform(r.Context(), in.PlatformID)
	if err != nil {
		writeError(w, err)
		return
	}
	item, err := s.store.UpdateCoreMapping(r.Context(), r.PathValue("id"), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}
func (s *Server) deleteCoreMapping(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteCoreMapping(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) resolveCoreMapping(w http.ResponseWriter, r *http.Request) {
	platformID, err := s.canonicalPlatform(r.Context(), r.URL.Query().Get("platform_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	if strings.TrimSpace(platformID) == "" {
		writeError(w, errors.New("platform_id is required"))
		return
	}
	item, err := s.store.ResolveCore(r.Context(), platformID, r.URL.Query().Get("edition_id"), r.URL.Query().Get("device_profile_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) listLaunchBindings(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListLaunchBindings(r.Context(), r.URL.Query().Get("edition_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeCollection(w, r, items)
}
func (s *Server) createLaunchBinding(w http.ResponseWriter, r *http.Request) {
	var in catalog.NewLaunchBinding
	if !decode(w, r, &in) {
		return
	}
	item, err := s.store.CreateLaunchBinding(r.Context(), in)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Location", resourceLocation(r, "launch-bindings", item.ID))
	writeJSON(w, http.StatusCreated, item)
}
func (s *Server) getLaunchBinding(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.GetLaunchBinding(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}
func (s *Server) updateLaunchBinding(w http.ResponseWriter, r *http.Request) {
	var in catalog.NewLaunchBinding
	if !decode(w, r, &in) {
		return
	}
	item, err := s.store.UpdateLaunchBinding(r.Context(), r.PathValue("id"), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}
func (s *Server) deleteLaunchBinding(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteLaunchBinding(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) resolveLaunchBinding(w http.ResponseWriter, r *http.Request) {
	item, err := runtimecfg.Resolve(r.Context(), s.store, r.URL.Query().Get("edition_id"), r.URL.Query().Get("device_profile_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}
