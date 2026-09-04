package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"varkiv/internal/bundler"
	"varkiv/internal/catalog"
)

var errPackagePlanStale = errors.New("package plan is stale")

const packagePlanLifetime = 30 * time.Minute

type packagePlanResponse struct {
	ID          string       `json:"id"`
	ProfileID   string       `json:"profile_id"`
	Fingerprint string       `json:"fingerprint"`
	Status      string       `json:"status"`
	ExpiresAt   time.Time    `json:"expires_at"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
	Plan        bundler.Plan `json:"plan"`
}

type packageReleaseResponse struct {
	ID         string         `json:"id"`
	ProfileID  string         `json:"profile_id"`
	PlanID     string         `json:"plan_id"`
	Status     string         `json:"status"`
	OutputSlug string         `json:"output_slug"`
	CreatedAt  time.Time      `json:"created_at"`
	Result     map[string]any `json:"result"`
}

func (s *Server) ensureDefaultPackageProfiles(ctx context.Context) error {
	for _, profile := range defaultPackageProfiles() {
		id := "builtin-" + safeSegment(profile.Name)
		if existing, err := s.store.GetPackageProfile(ctx, id); err == nil {
			if existing.DeviceProfileID == "" || existing.FrontendAdapterID == "" {
				templates := make([]catalog.NewPackageConfigTemplate, len(existing.Templates))
				for index, template := range existing.Templates {
					templates[index] = catalog.NewPackageConfigTemplate{ID: template.ID, Name: template.Name, Scope: template.Scope, OutputPath: template.OutputPath, Body: template.Body, SortOrder: template.SortOrder}
				}
				enabled := existing.Enabled
				_, err = s.store.UpdatePackageProfile(ctx, id, catalog.NewPackageProfile{Name: existing.Name, Frontend: existing.Frontend, Target: existing.Target, DeviceProfileID: profile.DeviceProfileID, FrontendAdapterID: profile.FrontendAdapterID, Locale: existing.Locale, FileMode: existing.FileMode, OutputSlug: existing.OutputSlug, Enabled: &enabled, Templates: templates, Builtin: true})
				if err != nil {
					return err
				}
			}
			continue
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		enabled := true
		_, err := s.store.CreatePackageProfile(ctx, catalog.NewPackageProfile{ID: id, Name: profile.Name, Frontend: profile.Frontend, Target: profile.Target, DeviceProfileID: profile.DeviceProfileID, FrontendAdapterID: profile.FrontendAdapterID, Locale: profile.Locale, FileMode: profile.FileMode, OutputSlug: profile.Name, Enabled: &enabled, Builtin: true})
		if err != nil {
			return err
		}
	}
	return nil
}

func packageProfileToBundler(profile catalog.PackageProfile) bundler.Profile {
	return bundler.ProfileFromCatalog(profile)
}

func newPackageProfileToBundler(profile catalog.NewPackageProfile) bundler.Profile {
	templates := make([]bundler.ConfigTemplate, len(profile.Templates))
	for index, template := range profile.Templates {
		templates[index] = bundler.ConfigTemplate{Name: template.Name, Scope: template.Scope, OutputPath: template.OutputPath, Body: template.Body}
	}
	return bundler.Profile{Name: profile.Name, Frontend: profile.Frontend, Target: profile.Target, DeviceProfileID: profile.DeviceProfileID, FrontendAdapterID: profile.FrontendAdapterID, Locale: profile.Locale, FileMode: profile.FileMode, OutputSlug: profile.OutputSlug, Enabled: profile.Enabled == nil || *profile.Enabled, Templates: templates}
}

func validatePackageProfile(in catalog.NewPackageProfile) error {
	_, err := bundler.ValidateProfile(newPackageProfileToBundler(in))
	return err
}

func (s *Server) normalizePackageRuntimeRefs(ctx context.Context, in *catalog.NewPackageProfile) error {
	if strings.TrimSpace(in.DeviceProfileID) == "" {
		profiles, err := s.store.ListDeviceProfiles(ctx)
		if err != nil {
			return err
		}
		for _, profile := range profiles {
			if profile.Enabled && profile.Target == strings.ToLower(strings.TrimSpace(in.Target)) {
				in.DeviceProfileID = profile.ID
				break
			}
		}
	}
	if strings.TrimSpace(in.FrontendAdapterID) == "" {
		adapters, err := s.store.ListFrontendAdapters(ctx)
		if err != nil {
			return err
		}
		for _, adapter := range adapters {
			if adapter.Enabled && adapter.Handler == strings.ToLower(strings.TrimSpace(in.Frontend)) {
				in.FrontendAdapterID = adapter.ID
				break
			}
		}
	}
	if in.DeviceProfileID == "" || in.FrontendAdapterID == "" {
		return errors.New("package profile requires a matching device_profile_id and frontend_adapter_id")
	}
	device, err := s.store.GetDeviceProfile(ctx, in.DeviceProfileID)
	if err != nil {
		return err
	}
	adapter, err := s.store.GetFrontendAdapter(ctx, in.FrontendAdapterID)
	if err != nil {
		return err
	}
	if device.Target != strings.ToLower(strings.TrimSpace(in.Target)) || !adapter.Enabled || adapter.Handler == "" || adapter.Handler != strings.ToLower(strings.TrimSpace(in.Frontend)) {
		return errors.New("device_profile_id or frontend_adapter_id does not match target/frontend")
	}
	return nil
}

func (s *Server) createPackageProfile(w http.ResponseWriter, r *http.Request) {
	var in catalog.NewPackageProfile
	if !decode(w, r, &in) {
		return
	}
	if err := s.normalizePackageRuntimeRefs(r.Context(), &in); err != nil {
		writeError(w, err)
		return
	}
	if err := validatePackageProfile(in); err != nil {
		writeError(w, err)
		return
	}
	item, err := s.store.CreatePackageProfile(r.Context(), in)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Location", resourceLocation(r, "package-profiles", item.ID))
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) getPackageProfile(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.GetPackageProfile(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) updatePackageProfile(w http.ResponseWriter, r *http.Request) {
	var in catalog.NewPackageProfile
	if !decode(w, r, &in) {
		return
	}
	if err := s.normalizePackageRuntimeRefs(r.Context(), &in); err != nil {
		writeError(w, err)
		return
	}
	if err := validatePackageProfile(in); err != nil {
		writeError(w, err)
		return
	}
	item, err := s.store.UpdatePackageProfile(r.Context(), r.PathValue("id"), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deletePackageProfile(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeletePackageProfile(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func publicPackagePlan(plan bundler.Plan, slug string) bundler.Plan {
	plan.Output = filepath.ToSlash(filepath.Join("state", "exports", slug))
	return plan
}

func (s *Server) createPackagePlan(w http.ResponseWriter, r *http.Request) {
	profile, err := s.store.GetPackageProfile(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	if !profile.Enabled {
		writeError(w, errors.New("package profile must be enabled before planning"))
		return
	}
	out := filepath.Join(s.stateRoot, "exports", profile.OutputSlug)
	plan, err := bundler.PlanWithStorage(r.Context(), s.store, s.libraryRoot, s.storage.ROMRoot, s.storage.MediaRoot, out, packageProfileToBundler(profile))
	if err != nil {
		writeError(w, err)
		return
	}
	plan = publicPackagePlan(plan, profile.OutputSlug)
	data, err := json.Marshal(plan)
	if err != nil {
		writeError(w, err)
		return
	}
	record, err := s.store.CreatePackagePlan(r.Context(), catalog.NewPackagePlanRecord{ProfileID: profile.ID, Fingerprint: plan.Fingerprint, Status: "ready", PlanJSON: string(data), ExpiresAt: time.Now().UTC().Add(packagePlanLifetime)})
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Location", resourceLocation(r, "package-plans", record.ID))
	writeJSON(w, http.StatusCreated, packagePlanEnvelope(record, plan))
}

func packagePlanEnvelope(record catalog.PackagePlanRecord, plan bundler.Plan) packagePlanResponse {
	return packagePlanResponse{record.ID, record.ProfileID, record.Fingerprint, record.Status, record.ExpiresAt, record.CreatedAt, record.UpdatedAt, plan}
}

func decodePackagePlan(record catalog.PackagePlanRecord) (packagePlanResponse, error) {
	var plan bundler.Plan
	if err := json.Unmarshal([]byte(record.PlanJSON), &plan); err != nil {
		return packagePlanResponse{}, err
	}
	return packagePlanEnvelope(record, plan), nil
}

func (s *Server) listPackagePlans(w http.ResponseWriter, r *http.Request) {
	records, err := s.store.ListPackagePlans(r.Context(), strings.TrimSpace(r.URL.Query().Get("profile_id")))
	if err != nil {
		writeError(w, err)
		return
	}
	items := make([]packagePlanResponse, 0, len(records))
	for _, record := range records {
		item, decodeErr := decodePackagePlan(record)
		if decodeErr != nil {
			writeError(w, decodeErr)
			return
		}
		items = append(items, item)
	}
	writeCollection(w, r, items)
}

func (s *Server) getPackagePlan(w http.ResponseWriter, r *http.Request) {
	record, err := s.store.GetPackagePlan(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	item, err := decodePackagePlan(record)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) buildPackagePlan(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	record, err := s.store.GetPackagePlan(ctx, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	if record.Status != "ready" || time.Now().UTC().After(record.ExpiresAt) {
		if record.Status == "ready" {
			_, _ = s.store.UpdatePackagePlanStatus(ctx, record.ID, "stale")
		}
		writeError(w, errPackagePlanStale)
		return
	}
	profile, err := s.store.GetPackageProfile(ctx, record.ProfileID)
	if err != nil {
		writeError(w, err)
		return
	}
	out := filepath.Join(s.stateRoot, "exports", profile.OutputSlug)
	current, err := bundler.PlanWithStorage(ctx, s.store, s.libraryRoot, s.storage.ROMRoot, s.storage.MediaRoot, out, packageProfileToBundler(profile))
	if err != nil {
		writeError(w, err)
		return
	}
	if current.Fingerprint != record.Fingerprint {
		_, _ = s.store.UpdatePackagePlanStatus(ctx, record.ID, "stale")
		writeError(w, errPackagePlanStale)
		return
	}
	if len(current.Conflicts) > 0 {
		err = fmt.Errorf("%w: %s", bundler.ErrUnmanagedTargetConflict, strings.Join(current.Conflicts, ", "))
		s.recordFailedPackageRelease(ctx, profile, record, err)
		writeError(w, err)
		return
	}
	recoveryRoot := filepath.Join(s.stateRoot, "recovery", "packages", profile.OutputSlug)
	recoveryLocator := filepath.ToSlash(filepath.Join("state", "recovery", "packages", profile.OutputSlug))
	result, err := bundler.BuildWithStorageAndRecovery(ctx, s.store, s.libraryRoot, s.storage.ROMRoot, s.storage.MediaRoot, out, recoveryRoot, recoveryLocator, packageProfileToBundler(profile))
	if err != nil {
		s.recordFailedPackageRelease(ctx, profile, record, err)
		writeError(w, err)
		return
	}
	result.Output = filepath.ToSlash(filepath.Join("state", "exports", profile.OutputSlug))
	data, _ := json.Marshal(result)
	release, err := s.store.RecordPackageRelease(ctx, catalog.NewPackageReleaseRecord{ProfileID: profile.ID, PlanID: record.ID, Status: "succeeded", OutputSlug: profile.OutputSlug, ResultJSON: string(data)}, "built")
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, packageReleaseEnvelope(release))
}

func (s *Server) recordFailedPackageRelease(ctx context.Context, profile catalog.PackageProfile, plan catalog.PackagePlanRecord, buildErr error) {
	code, message := "package_build_failed", "package build failed; existing managed files were kept or restored"
	if errors.Is(buildErr, bundler.ErrUnmanagedTargetConflict) {
		code, message = "unmanaged_target_conflict", "package output contains a conflicting or modified target"
	}
	data, _ := json.Marshal(map[string]string{"code": code, "error": message})
	_, _ = s.store.RecordPackageRelease(ctx, catalog.NewPackageReleaseRecord{ProfileID: profile.ID, PlanID: plan.ID, Status: "failed", OutputSlug: profile.OutputSlug, ResultJSON: string(data)}, "failed")
}

func packageReleaseEnvelope(record catalog.PackageReleaseRecord) packageReleaseResponse {
	result := map[string]any{}
	_ = json.Unmarshal([]byte(record.ResultJSON), &result)
	return packageReleaseResponse{record.ID, record.ProfileID, record.PlanID, record.Status, record.OutputSlug, record.CreatedAt, result}
}

func (s *Server) listPackageReleases(w http.ResponseWriter, r *http.Request) {
	records, err := s.store.ListPackageReleases(r.Context(), strings.TrimSpace(r.URL.Query().Get("profile_id")))
	if err != nil {
		writeError(w, err)
		return
	}
	items := make([]packageReleaseResponse, len(records))
	for index, record := range records {
		items[index] = packageReleaseEnvelope(record)
	}
	writeCollection(w, r, items)
}

func (s *Server) getPackageRelease(w http.ResponseWriter, r *http.Request) {
	record, err := s.store.GetPackageRelease(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, packageReleaseEnvelope(record))
}
