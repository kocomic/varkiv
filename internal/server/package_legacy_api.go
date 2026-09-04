package server

import (
	"errors"
	"net/http"
	"path/filepath"

	"varkiv/internal/bundler"
)

func defaultPackageProfiles() []bundler.Profile {
	return []bundler.Profile{
		{Name: "rocknix-esde-zh", Frontend: "es-de", Target: "rocknix", DeviceProfileID: "builtin-device-rocknix", FrontendAdapterID: esdeAdapterID, Locale: "zh-CN", FileMode: "copy", Enabled: true},
		{Name: "rocknix-pegasus-zh", Frontend: "pegasus", Target: "rocknix", DeviceProfileID: "builtin-device-rocknix", FrontendAdapterID: pegasusAdapterID, Locale: "zh-CN", FileMode: "copy", Enabled: true},
		{Name: "android-pegasus-zh", Frontend: "pegasus", Target: "android", DeviceProfileID: "builtin-device-android-handheld", FrontendAdapterID: pegasusAdapterID, Locale: "zh-CN", FileMode: "copy", Enabled: true},
		{Name: "android-esde-zh", Frontend: "es-de", Target: "android", DeviceProfileID: "builtin-device-android-handheld", FrontendAdapterID: esdeAdapterID, Locale: "zh-CN", FileMode: "copy", Enabled: true},
		{Name: "windows-pegasus-zh", Frontend: "pegasus", Target: "windows", DeviceProfileID: "builtin-device-windows-handheld", FrontendAdapterID: pegasusAdapterID, Locale: "zh-CN", FileMode: "hardlink", Enabled: true},
		{Name: "windows-esde-zh", Frontend: "es-de", Target: "windows", DeviceProfileID: "builtin-device-windows-handheld", FrontendAdapterID: esdeAdapterID, Locale: "zh-CN", FileMode: "hardlink", Enabled: true},
		{Name: "steamos-bazzite-esde-zh", Frontend: "es-de", Target: "steamos-bazzite", DeviceProfileID: "builtin-device-steamos-bazzite", FrontendAdapterID: esdeAdapterID, Locale: "zh-CN", FileMode: "copy", Enabled: true},
		{Name: "darkos-esde-zh", Frontend: "es-de", Target: "darkos", DeviceProfileID: "builtin-device-darkos", FrontendAdapterID: esdeAdapterID, Locale: "zh-CN", FileMode: "copy", Enabled: true},
		{Name: "arkos-esde-zh", Frontend: "es-de", Target: "arkos", DeviceProfileID: "builtin-device-arkos", FrontendAdapterID: esdeAdapterID, Locale: "zh-CN", FileMode: "copy", Enabled: true},
		{Name: "knulli-esde-zh", Frontend: "es-de", Target: "knulli", DeviceProfileID: "builtin-device-knulli", FrontendAdapterID: esdeAdapterID, Locale: "zh-CN", FileMode: "copy", Enabled: true},
		{Name: "portable-esde-en", Frontend: "es-de", Target: "portable", DeviceProfileID: "builtin-device-portable", FrontendAdapterID: esdeAdapterID, Locale: "en", FileMode: "copy", Enabled: true},
		{Name: "portable-pegasus-en", Frontend: "pegasus", Target: "portable", DeviceProfileID: "builtin-device-portable", FrontendAdapterID: pegasusAdapterID, Locale: "en", FileMode: "copy", Enabled: true},
	}
}

func (s *Server) listPackageProfiles(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListPackageProfiles(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeCollection(w, r, items)
}

func (s *Server) buildPackage(w http.ResponseWriter, r *http.Request) {
	var profile bundler.Profile
	if !decode(w, r, &profile) {
		return
	}
	segment := safeSegment(profile.Name)
	if segment == "" {
		writeError(w, errors.New("profile name is required"))
		return
	}
	out := filepath.Join(s.stateRoot, "exports", segment)
	recoveryRoot := filepath.Join(s.stateRoot, "recovery", "packages", segment)
	recoveryLocator := filepath.ToSlash(filepath.Join("state", "recovery", "packages", segment))
	result, err := bundler.BuildWithStorageAndRecovery(r.Context(), s.store, s.libraryRoot, s.storage.ROMRoot, s.storage.MediaRoot, out, recoveryRoot, recoveryLocator, profile)
	if err != nil {
		writeError(w, err)
		return
	}
	// API responses expose a state-relative locator, never the server's absolute
	// filesystem layout. The lifecycle API should be preferred for new clients.
	result.Output = filepath.ToSlash(filepath.Join("state", "exports", segment))
	w.Header().Set("Deprecation", "true")
	w.Header().Set("Link", `</api/v1/package-profiles>; rel="successor-version"`)
	writeJSON(w, 201, result)
}
