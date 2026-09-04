package server

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"varkiv/internal/catalog"
	"varkiv/internal/runtimecfg"
	"varkiv/internal/saves"
)

func (s *Server) listSaveBindings(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListSaveBindings(r.Context(), r.URL.Query().Get("edition_id"), r.URL.Query().Get("device_profile_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeCollection(w, r, items)
}

func (s *Server) createSaveBinding(w http.ResponseWriter, r *http.Request) {
	var in catalog.NewSaveBinding
	if !decode(w, r, &in) {
		return
	}
	item, err := s.store.CreateSaveBinding(r.Context(), in)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Location", resourceLocation(r, "save-bindings", item.ID))
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) createSaveSetup(w http.ResponseWriter, r *http.Request) {
	var in catalog.NewSaveSetup
	if !decode(w, r, &in) {
		return
	}
	item, err := s.store.CreateSaveSetup(r.Context(), in)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Location", resourceLocation(r, "save-bindings", item.Binding.ID))
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) getSaveBinding(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.GetSaveBinding(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) updateSaveBinding(w http.ResponseWriter, r *http.Request) {
	var in catalog.NewSaveBinding
	if !decode(w, r, &in) {
		return
	}
	item, err := s.store.UpdateSaveBinding(r.Context(), r.PathValue("id"), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteSaveBinding(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteSaveBinding(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type syncInventoryInput struct {
	ClientItemID string `json:"client_item_id"`
	PlatformID   string `json:"platform_id"`
	SHA256       string `json:"sha256,omitempty"`
	Serial       string `json:"serial,omitempty"`
	ProductCode  string `json:"product_code,omitempty"`
	TitleID      string `json:"title_id,omitempty"`
	Size         int64  `json:"size,omitempty"`
}

type syncSaveStateInput struct {
	StreamID       string `json:"stream_id"`
	BaseRevisionID string `json:"base_revision_id,omitempty"`
	ContentHash    string `json:"content_hash,omitempty"`
	HasLocalData   bool   `json:"has_local_data"`
}

type syncSessionRequest struct {
	DeviceID  string               `json:"device_id,omitempty"`
	Inventory []syncInventoryInput `json:"inventory"`
	Saves     []syncSaveStateInput `json:"saves"`
}

type syncSessionResponse struct {
	Session   catalog.SyncSession     `json:"session"`
	Inventory []catalog.InventoryItem `json:"inventory"`
}

type syncBindingDescriptor struct {
	Binding       catalog.SaveBinding `json:"binding"`
	Stream        catalog.SaveStream  `json:"stream"`
	EditionID     string              `json:"edition_id"`
	SaveNamespace string              `json:"save_namespace"`
	PlatformID    string              `json:"platform_id"`
	Serial        string              `json:"serial,omitempty"`
	ProductCode   string              `json:"product_code,omitempty"`
	TitleID       string              `json:"title_id,omitempty"`
	TitleIDHigh   string              `json:"title_id_high,omitempty"`
	TitleIDLow    string              `json:"title_id_low,omitempty"`
	// ROMMatchSHA256 is the selected launch artifact's content identity. The
	// Agent uses it to resolve the *device-local* basename without disclosing a
	// local file name or path to the server. It is deliberately omitted for
	// compressed content: current RetroArch derives its save basename from the
	// selected archive entry, which cannot be inferred safely from the outer
	// archive name.
	ROMMatchSHA256 string                 `json:"rom_match_sha256,omitempty"`
	ROMStem        string                 `json:"rom_stem,omitempty"`
	Driver         catalog.EmulatorDriver `json:"driver"`
}

func safeAutomaticROMMatchSHA256(artifact *catalog.Artifact) string {
	if artifact == nil || artifact.Missing {
		return ""
	}
	extension := strings.ToLower(filepath.Ext(filepath.FromSlash(artifact.Path)))
	if extension == ".zip" || extension == ".7z" {
		return ""
	}
	value := strings.ToLower(strings.TrimSpace(artifact.SHA256))
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return ""
	}
	return value
}

func splitRuntimeTitleID(value string) (string, string) {
	value = strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(value), "-", ""), " ", "")
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 8 {
		return "", ""
	}
	return value[:8], value[8:]
}

func (s *Server) syncDeviceConfig(w http.ResponseWriter, r *http.Request) {
	identity, paired := clientIdentity(r)
	deviceID := strings.TrimSpace(r.URL.Query().Get("device_id"))
	if paired {
		deviceID = identity.DeviceID
	}
	device, err := s.store.GetDevice(r.Context(), deviceID)
	if err != nil {
		writeError(w, err)
		return
	}
	if device.Status == "revoked" {
		writeError(w, catalog.ErrDeviceRevoked)
		return
	}
	var profile catalog.DeviceProfile
	if device.DeviceProfileID != "" {
		profile, err = s.store.GetDeviceProfile(r.Context(), device.DeviceProfileID)
		if err != nil {
			writeError(w, err)
			return
		}
	}
	bindings, err := s.store.ListSaveBindings(r.Context(), "", device.DeviceProfileID)
	if err != nil {
		writeError(w, err)
		return
	}
	descriptors := []syncBindingDescriptor{}
	selected := map[string]bool{}
	for _, binding := range bindings {
		if device.DeviceProfileID == "" && binding.DeviceProfileID != "" {
			continue
		}
		if !binding.Enabled {
			continue
		}
		authorized, authErr := s.store.SaveBindingRuntimeAuthorized(r.Context(), device, binding)
		if authErr != nil {
			writeError(w, authErr)
			return
		}
		if !authorized {
			continue
		}
		key := binding.StreamID + "\x00" + binding.EditionID + "\x00" + binding.DriverID
		if selected[key] {
			continue
		}
		selected[key] = true
		stream, streamErr := s.store.GetSaveStream(r.Context(), binding.StreamID)
		edition, editionErr := s.store.GetEdition(r.Context(), binding.EditionID, "")
		driver, driverErr := s.store.GetEmulatorDriver(r.Context(), binding.DriverID)
		if streamErr != nil || editionErr != nil || driverErr != nil {
			writeError(w, errors.New("save binding references unavailable runtime metadata"))
			return
		}
		game, gameErr := s.store.GetGame(r.Context(), edition.GameID, "")
		if gameErr != nil {
			writeError(w, gameErr)
			return
		}
		titleIDHigh, titleIDLow := splitRuntimeTitleID(edition.TitleID)
		romMatchSHA256 := safeAutomaticROMMatchSHA256(catalog.SelectLaunchArtifact(edition.Artifacts))
		descriptors = append(descriptors, syncBindingDescriptor{Binding: binding, Stream: stream, EditionID: edition.ID, SaveNamespace: edition.SaveNamespace, PlatformID: game.Platform, Serial: edition.Serial, ProductCode: edition.ProductCode, TitleID: edition.TitleID, TitleIDHigh: titleIDHigh, TitleIDLow: titleIDLow, ROMMatchSHA256: romMatchSHA256, Driver: driver})
	}
	drivers, err := s.store.ListEmulatorDrivers(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	cores, err := s.store.ListRetroArchCores(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	attestationRequirements, err := s.store.ListRuntimeAttestationRequirementsForDevice(r.Context(), device)
	if err != nil {
		writeError(w, err)
		return
	}
	launchBindings, err := s.store.ListLaunchBindings(r.Context(), "")
	if err != nil {
		writeError(w, err)
		return
	}
	launches := []runtimecfg.LaunchResolution{}
	seenEditions := map[string]bool{}
	for _, binding := range launchBindings {
		if !binding.Enabled || seenEditions[binding.EditionID] {
			continue
		}
		if binding.DeviceProfileID != "" && binding.DeviceProfileID != device.DeviceProfileID {
			continue
		}
		resolution, resolveErr := runtimecfg.Resolve(r.Context(), s.store, binding.EditionID, device.DeviceProfileID)
		if resolveErr != nil {
			continue
		}
		seenEditions[binding.EditionID] = true
		launches = append(launches, resolution)
	}
	registry, err := s.store.PlatformRegistry(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"device": device, "device_profile": profile, "bindings": descriptors, "drivers": drivers, "retroarch_cores": cores, "runtime_attestation_requirements": attestationRequirements, "launches": launches, "platforms": registry.All()})
}

func requestFingerprint(value any) string {
	data, _ := json.Marshal(value)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func clientIdentity(r *http.Request) (catalog.ClientIdentity, bool) {
	identity, ok := r.Context().Value(clientIdentityKey{}).(catalog.ClientIdentity)
	return identity, ok
}

func ensureSyncOwner(r *http.Request, deviceID string) error {
	if identity, ok := clientIdentity(r); ok && identity.DeviceID != deviceID {
		return catalog.ErrClientTokenInvalid
	}
	return nil
}

func (s *Server) createSyncSession(w http.ResponseWriter, r *http.Request) {
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" {
		writeAPIError(w, http.StatusBadRequest, "idempotency_key_required", "Idempotency-Key header is required")
		return
	}
	var in syncSessionRequest
	if !decode(w, r, &in) {
		return
	}
	if identity, ok := clientIdentity(r); ok {
		in.DeviceID = identity.DeviceID
	}
	in.DeviceID = strings.TrimSpace(in.DeviceID)
	device, err := s.store.GetDevice(r.Context(), in.DeviceID)
	if err != nil {
		writeError(w, err)
		return
	}
	if device.Status == "revoked" {
		writeError(w, catalog.ErrDeviceRevoked)
		return
	}
	authorizedStreams, err := s.store.DeviceAuthorizedSaveStreams(r.Context(), device)
	if err != nil {
		writeError(w, err)
		return
	}

	if len(in.Inventory) > 10000 || len(in.Saves) > 4096 {
		writeError(w, errors.New("inventory or save state count is out of range"))
		return
	}
	sort.Slice(in.Inventory, func(i, j int) bool { return in.Inventory[i].ClientItemID < in.Inventory[j].ClientItemID })
	sort.Slice(in.Saves, func(i, j int) bool { return in.Saves[i].StreamID < in.Saves[j].StreamID })
	seenInventory, seenStreams := map[string]bool{}, map[string]bool{}
	matched := make([]catalog.NewInventoryItem, 0, len(in.Inventory))
	for index := range in.Inventory {
		entry := &in.Inventory[index]
		entry.ClientItemID = strings.TrimSpace(entry.ClientItemID)
		if seenInventory[entry.ClientItemID] {
			writeError(w, errors.New("client_item_id must be unique within a sync session"))
			return
		}
		seenInventory[entry.ClientItemID] = true
		var canonicalErr error
		entry.PlatformID, canonicalErr = s.canonicalPlatform(r.Context(), entry.PlatformID)
		if canonicalErr != nil {
			writeError(w, canonicalErr)
			return
		}
		item, matchErr := s.store.MatchInventoryItemForDevice(r.Context(), in.DeviceID, catalog.NewInventoryItem{
			ClientItemID: entry.ClientItemID, PlatformID: entry.PlatformID, SHA256: entry.SHA256, Serial: entry.Serial,
			ProductCode: entry.ProductCode, TitleID: entry.TitleID, Size: entry.Size,
		})
		if matchErr != nil {
			writeError(w, matchErr)
			return
		}
		entry.PlatformID, entry.SHA256 = item.PlatformID, item.SHA256
		matched = append(matched, item)
	}

	operations := make([]catalog.NewSyncOperation, 0, len(in.Saves))
	for index := range in.Saves {
		state := &in.Saves[index]
		state.StreamID = strings.TrimSpace(state.StreamID)
		state.BaseRevisionID = strings.TrimSpace(state.BaseRevisionID)
		state.ContentHash = strings.ToLower(strings.TrimSpace(state.ContentHash))
		if state.StreamID == "" || seenStreams[state.StreamID] {
			writeError(w, errors.New("stream_id is required and must be unique within a sync session"))
			return
		}
		if !authorizedStreams[state.StreamID] {
			writeError(w, catalog.ErrSaveRuntimeNotAttested)
			return
		}
		seenStreams[state.StreamID] = true
		if state.HasLocalData {
			decoded, decodeErr := hex.DecodeString(state.ContentHash)
			if decodeErr != nil || len(decoded) != 32 {
				writeError(w, errors.New("content_hash must be 64 hexadecimal characters when has_local_data is true"))
				return
			}
		} else {
			state.ContentHash = ""
			state.BaseRevisionID = ""
		}
		if _, err = s.store.GetSaveStream(r.Context(), state.StreamID); err != nil {
			writeError(w, err)
			return
		}
		var baseRevision *catalog.SaveRevision
		if state.BaseRevisionID != "" {
			base, baseErr := s.store.GetSaveRevision(r.Context(), state.BaseRevisionID)
			if baseErr != nil || base.StreamID != state.StreamID {
				writeError(w, errors.New("base_revision_id must belong to stream_id"))
				return
			}
			baseRevision = &base
		}
		current, currentErr := s.store.CurrentStreamRevision(r.Context(), state.StreamID)
		if currentErr != nil && !errors.Is(currentErr, sql.ErrNoRows) {
			writeError(w, currentErr)
			return
		}
		op := catalog.NewSyncOperation{StreamID: state.StreamID, Status: "proposed", BaseRevisionID: state.BaseRevisionID, ExpectedHash: state.ContentHash, Detail: map[string]any{}}
		switch {
		case errors.Is(currentErr, sql.ErrNoRows) && state.HasLocalData:
			op.Action = "upload"
		case errors.Is(currentErr, sql.ErrNoRows):
			op.Action, op.Status = "noop", "complete"
		case !state.HasLocalData:
			op.Action, op.TargetRevisionID, op.ExpectedHash, op.Bytes = "download", current.ID, current.ContentHash, current.TotalSize
		case state.ContentHash == current.ContentHash:
			op.Action, op.Status, op.TargetRevisionID = "noop", "complete", current.ID
		case baseRevision != nil && state.ContentHash == baseRevision.ContentHash:
			op.Action, op.TargetRevisionID, op.ExpectedHash, op.Bytes = "download", current.ID, current.ContentHash, current.TotalSize
		case state.BaseRevisionID == current.ID:
			op.Action = "upload"
		default:
			op.Action, op.Status, op.TargetRevisionID = "conflict", "complete", current.ID
			op.Detail = map[string]any{"reason": "server_and_client_advanced", "server_content_hash": current.ContentHash}
		}
		operations = append(operations, op)
	}

	fingerprint := requestFingerprint(in)
	planHash := requestFingerprint(operations)
	session, replayed, err := s.store.CreateSyncSession(r.Context(), catalog.NewSyncSession{
		DeviceID: in.DeviceID, IdempotencyKey: idempotencyKey, Status: "proposed", BaseManifestHash: fingerprint,
		OperationPlanHash: planHash, Operations: operations, Inventory: matched,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	session, err = s.store.RecalculateSyncSession(r.Context(), session.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	inventory, err := s.store.ListInventoryItems(r.Context(), session.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
		w.Header().Set("Idempotent-Replayed", "true")
	}
	w.Header().Set("Location", resourceLocation(r, "sync/sessions", session.ID))
	writeJSON(w, status, syncSessionResponse{Session: session, Inventory: inventory})
}

func (s *Server) listSyncSessions(w http.ResponseWriter, r *http.Request) {
	deviceID := strings.TrimSpace(r.URL.Query().Get("device_id"))
	if identity, ok := clientIdentity(r); ok {
		deviceID = identity.DeviceID
	}
	items, err := s.store.ListSyncSessions(r.Context(), deviceID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeCollection(w, r, items)
}

func (s *Server) getSyncSession(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.GetSyncSession(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	if err = ensureSyncOwner(r, item.DeviceID); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) listSyncInventory(w http.ResponseWriter, r *http.Request) {
	session, err := s.store.GetSyncSession(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	if err = ensureSyncOwner(r, session.DeviceID); err != nil {
		writeError(w, err)
		return
	}
	items, err := s.store.ListInventoryItems(r.Context(), session.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeCollection(w, r, items)
}

type syncUploadManifest struct {
	EditionID string                   `json:"edition_id,omitempty"`
	Files     []saveUploadManifestFile `json:"files"`
}

type stagedSyncUpload struct {
	manifest syncUploadManifest
	files    []saves.IncomingFile
	handles  []*os.File
	paths    []string
}

func (upload *stagedSyncUpload) close() {
	for _, handle := range upload.handles {
		_ = handle.Close()
	}
	for _, path := range upload.paths {
		_ = os.Remove(path)
	}
}

func (s *Server) stageSyncUpload(w http.ResponseWriter, r *http.Request) (stagedSyncUpload, error) {
	r.Body = http.MaxBytesReader(w, r.Body, 256<<20)
	reader, err := r.MultipartReader()
	if err != nil {
		return stagedSyncUpload{}, fmt.Errorf("invalid sync upload: %w", err)
	}
	stagingRoot := filepath.Join(s.stateRoot, "staging")
	if err = os.MkdirAll(stagingRoot, 0o700); err != nil {
		return stagedSyncUpload{}, err
	}
	upload := stagedSyncUpload{}
	fileIndex := 0
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			upload.close()
			return stagedSyncUpload{}, fmt.Errorf("invalid sync upload: %w", nextErr)
		}
		switch part.FormName() {
		case "manifest":
			data, readErr := io.ReadAll(io.LimitReader(part, (1<<20)+1))
			_ = part.Close()
			if readErr != nil || len(data) > 1<<20 || json.Unmarshal(data, &upload.manifest) != nil || len(upload.manifest.Files) == 0 {
				upload.close()
				return stagedSyncUpload{}, errors.New("invalid sync upload manifest")
			}
		case "files":
			if len(upload.manifest.Files) == 0 || fileIndex >= len(upload.manifest.Files) {
				_ = part.Close()
				upload.close()
				return stagedSyncUpload{}, errors.New("sync upload manifest must precede and match file parts")
			}
			temp, createErr := os.CreateTemp(stagingRoot, ".sync-upload-*")
			if createErr != nil {
				_ = part.Close()
				upload.close()
				return stagedSyncUpload{}, createErr
			}
			path := temp.Name()
			_, copyErr := io.Copy(temp, part)
			closeErr := temp.Close()
			_ = part.Close()
			if copyErr != nil || closeErr != nil {
				_ = os.Remove(path)
				upload.close()
				if copyErr != nil {
					return stagedSyncUpload{}, copyErr
				}
				return stagedSyncUpload{}, closeErr
			}
			handle, openErr := os.Open(path)
			if openErr != nil {
				_ = os.Remove(path)
				upload.close()
				return stagedSyncUpload{}, openErr
			}
			metadata := upload.manifest.Files[fileIndex]
			upload.handles = append(upload.handles, handle)
			upload.paths = append(upload.paths, path)
			upload.files = append(upload.files, saves.IncomingFile{LogicalPath: metadata.LogicalPath, Reader: handle, MTimeNS: metadata.MTimeNS, Mode: metadata.Mode})
			fileIndex++
		default:
			_ = part.Close()
			upload.close()
			return stagedSyncUpload{}, errors.New("invalid sync upload part")
		}
	}
	if len(upload.files) != len(upload.manifest.Files) {
		upload.close()
		return stagedSyncUpload{}, errors.New("sync upload manifest and file parts must have the same item count")
	}
	return upload, nil
}

func (s *Server) syncOperationForRequest(r *http.Request) (catalog.SyncSession, catalog.SyncOperation, error) {
	session, err := s.store.GetSyncSession(r.Context(), r.PathValue("id"))
	if err != nil {
		return catalog.SyncSession{}, catalog.SyncOperation{}, err
	}
	if err = ensureSyncOwner(r, session.DeviceID); err != nil {
		return catalog.SyncSession{}, catalog.SyncOperation{}, err
	}
	op, err := s.store.GetSyncOperation(r.Context(), session.ID, r.PathValue("operation_id"))
	return session, op, err
}

func (s *Server) uploadSyncOperation(w http.ResponseWriter, r *http.Request) {
	session, op, err := s.syncOperationForRequest(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if op.Action != "upload" {
		writeError(w, errors.New("sync operation must be an upload"))
		return
	}
	upload, err := s.stageSyncUpload(w, r)
	if err != nil {
		writeError(w, err)
		return
	}
	defer upload.close()
	stream, err := s.store.GetSaveStream(r.Context(), op.StreamID)
	if err != nil {
		writeError(w, err)
		return
	}
	editionID := strings.TrimSpace(upload.manifest.EditionID)
	if editionID == "" && len(stream.Editions) == 1 {
		editionID = stream.Editions[0].EditionID
	}
	linked := false
	for _, relation := range stream.Editions {
		if relation.EditionID == editionID {
			linked = true
			break
		}
	}
	if !linked {
		writeError(w, errors.New("edition_id is required and must be linked to the save stream"))
		return
	}
	scopeType, scopeKey := stream.OwnerType, stream.OwnerKey
	if stream.OwnerType == "edition" {
		scopeType, scopeKey = "game", ""
	}
	result, err := s.saves.PushSet(r.Context(), saves.PushSetInput{
		EditionID: editionID, DeviceID: session.DeviceID, DriverID: stream.DriverID, ScopeType: scopeType, ScopeKey: scopeKey,
		BaseRevisionID: op.BaseRevisionID, ExpectedContentHash: op.ExpectedHash, Files: upload.files,
	})
	if err != nil {
		if errors.Is(err, saves.ErrContentHashMismatch) {
			_, _ = s.store.FailSyncOperation(r.Context(), session.ID, op.ID, "hash_mismatch", "")
			_, _ = s.store.RecalculateSyncSession(r.Context(), session.ID)
		}
		writeError(w, err)
		return
	}
	if result.Conflict {
		_, _ = s.store.FailSyncOperation(r.Context(), session.ID, op.ID, "server_advanced", result.Revision.ContentHash)
		_, _ = s.store.RecalculateSyncSession(r.Context(), session.ID)
		writeAPIError(w, http.StatusConflict, "sync_server_advanced", "server save advanced after negotiation; the uploaded revision was preserved as a conflict")
		return
	}
	completed, err := s.store.CompleteSyncOperation(r.Context(), session.ID, op.ID, result.Revision.ContentHash, result.Revision.ID, result.Revision.TotalSize)
	if err != nil {
		writeError(w, err)
		return
	}
	updated, err := s.store.RecalculateSyncSession(r.Context(), session.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"operation": completed, "revision": result.Revision, "session": updated, "created": result.Created})
}

func (s *Server) ackSyncOperation(w http.ResponseWriter, r *http.Request) {
	session, op, err := s.syncOperationForRequest(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if op.Action != "download" {
		writeError(w, errors.New("sync operation must be a download"))
		return
	}
	var in struct {
		ActualHash string `json:"actual_hash"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.ActualHash = strings.ToLower(strings.TrimSpace(in.ActualHash))
	completed, err := s.store.CompleteSyncOperation(r.Context(), session.ID, op.ID, in.ActualHash, op.TargetRevisionID, op.Bytes)
	if err != nil {
		_, _ = s.store.RecalculateSyncSession(r.Context(), session.ID)
		writeError(w, err)
		return
	}
	updated, err := s.store.RecalculateSyncSession(r.Context(), session.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"operation": completed, "session": updated})
}

func (s *Server) downloadSyncOperationFile(w http.ResponseWriter, r *http.Request) {
	_, op, err := s.syncOperationForRequest(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if op.Action != "download" || op.TargetRevisionID == "" {
		writeError(w, errors.New("sync operation must be a download with a target revision"))
		return
	}
	file, metadata, err := s.saves.OpenRevisionFile(r.Context(), op.TargetRevisionID, r.PathValue("file_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, filepath.Base(filepath.FromSlash(metadata.LogicalPath))))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", metadata.Size))
	w.Header().Set("ETag", `"`+metadata.Checksum+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, file)
}
