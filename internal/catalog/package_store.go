package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

func normalizePackageProfile(in NewPackageProfile) (NewPackageProfile, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Frontend = strings.ToLower(strings.TrimSpace(in.Frontend))
	in.Target = strings.ToLower(strings.TrimSpace(in.Target))
	in.Locale = strings.TrimSpace(in.Locale)
	in.FileMode = strings.ToLower(strings.TrimSpace(in.FileMode))
	in.OutputSlug = strings.ToLower(strings.TrimSpace(in.OutputSlug))
	in.DeviceProfileID = strings.TrimSpace(in.DeviceProfileID)
	in.FrontendAdapterID = strings.TrimSpace(in.FrontendAdapterID)
	if in.Locale == "" {
		in.Locale = "zh-CN"
	}
	if in.FileMode == "" {
		in.FileMode = "copy"
	}
	if in.Name == "" || in.Target == "" {
		return in, errors.New("name and target are required")
	}
	if in.Frontend != "pegasus" && in.Frontend != "es-de" {
		return in, errors.New("frontend must be pegasus or es-de")
	}
	if in.FileMode != "copy" && in.FileMode != "hardlink" && in.FileMode != "reference" {
		return in, errors.New("file_mode must be copy, hardlink, or reference")
	}
	if in.OutputSlug == "" {
		in.OutputSlug = strings.ToLower(strings.TrimSpace(in.Name))
	}
	for _, r := range in.OutputSlug {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return in, errors.New("output_slug must contain only lowercase letters, numbers, hyphens, or underscores")
		}
	}
	if len(in.OutputSlug) > 100 {
		return in, errors.New("output_slug must not exceed 100 characters")
	}
	for index := range in.Templates {
		template := &in.Templates[index]
		template.Name = strings.TrimSpace(template.Name)
		template.Scope = strings.ToLower(strings.TrimSpace(template.Scope))
		template.OutputPath = filepath.ToSlash(strings.TrimSpace(template.OutputPath))
		if template.Name == "" || template.OutputPath == "" {
			return in, fmt.Errorf("template %d name and output_path are required", index)
		}
		if template.Scope != "package" && template.Scope != "platform" && template.Scope != "edition" {
			return in, fmt.Errorf("template %d scope must be package, platform, or edition", index)
		}
		if len(template.Body) > 64*1024 || len(template.OutputPath) > 512 {
			return in, fmt.Errorf("template %d exceeds the safe size limit", index)
		}
	}
	return in, nil
}

func (s *Store) CreatePackageProfile(ctx context.Context, in NewPackageProfile) (PackageProfile, error) {
	in, err := normalizePackageProfile(in)
	if err != nil {
		return PackageProfile{}, err
	}
	if in.ID == "" {
		in.ID = NewID()
	}
	if err = validateBuiltinID(in.ID, in.Builtin); err != nil {
		return PackageProfile{}, err
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PackageProfile{}, err
	}
	defer tx.Rollback()
	now := nowText()
	if _, err = tx.ExecContext(ctx, `INSERT INTO package_profiles(id,name,frontend,target,locale,file_mode,output_slug,enabled,builtin,created_at,updated_at,device_profile_id,frontend_adapter_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, in.ID, in.Name, in.Frontend, in.Target, in.Locale, in.FileMode, in.OutputSlug, boolInt(enabled), boolInt(in.Builtin), now, now, in.DeviceProfileID, in.FrontendAdapterID); err != nil {
		return PackageProfile{}, err
	}
	if err = replacePackageTemplates(ctx, tx, in.ID, in.Templates); err != nil {
		return PackageProfile{}, err
	}
	if err = tx.Commit(); err != nil {
		return PackageProfile{}, err
	}
	return s.GetPackageProfile(ctx, in.ID)
}

func replacePackageTemplates(ctx context.Context, tx *sql.Tx, profileID string, templates []NewPackageConfigTemplate) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM package_config_templates WHERE profile_id=?`, profileID); err != nil {
		return err
	}
	for index, template := range templates {
		if template.ID == "" {
			template.ID = NewID()
		}
		if template.SortOrder == 0 {
			template.SortOrder = (index + 1) * 10
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO package_config_templates(id,profile_id,name,scope,output_path,body,sort_order) VALUES(?,?,?,?,?,?,?)`, template.ID, profileID, template.Name, template.Scope, template.OutputPath, template.Body, template.SortOrder); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) GetPackageProfile(ctx context.Context, id string) (PackageProfile, error) {
	var item PackageProfile
	var enabled, builtin int
	var created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT id,name,frontend,target,device_profile_id,frontend_adapter_id,locale,file_mode,output_slug,enabled,builtin,created_at,updated_at FROM package_profiles WHERE id=?`, id).Scan(&item.ID, &item.Name, &item.Frontend, &item.Target, &item.DeviceProfileID, &item.FrontendAdapterID, &item.Locale, &item.FileMode, &item.OutputSlug, &enabled, &builtin, &created, &updated)
	if err != nil {
		return PackageProfile{}, err
	}
	item.Enabled = enabled != 0
	item.Builtin = builtin != 0
	item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	rows, err := s.db.QueryContext(ctx, `SELECT id,profile_id,name,scope,output_path,body,sort_order FROM package_config_templates WHERE profile_id=? ORDER BY sort_order,id`, id)
	if err != nil {
		return PackageProfile{}, err
	}
	defer rows.Close()
	item.Templates = []PackageConfigTemplate{}
	for rows.Next() {
		var template PackageConfigTemplate
		if err = rows.Scan(&template.ID, &template.ProfileID, &template.Name, &template.Scope, &template.OutputPath, &template.Body, &template.SortOrder); err != nil {
			return PackageProfile{}, err
		}
		item.Templates = append(item.Templates, template)
	}
	return item, rows.Err()
}

func (s *Store) ListPackageProfiles(ctx context.Context) ([]PackageProfile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM package_profiles ORDER BY enabled DESC,lower(name),id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	items := make([]PackageProfile, 0, len(ids))
	for _, id := range ids {
		item, getErr := s.GetPackageProfile(ctx, id)
		if getErr != nil {
			return nil, getErr
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Store) UpdatePackageProfile(ctx context.Context, id string, in NewPackageProfile) (PackageProfile, error) {
	current, err := s.GetPackageProfile(ctx, id)
	if err != nil {
		return PackageProfile{}, err
	}
	if current.Builtin && !in.Builtin {
		return PackageProfile{}, ErrBuiltinImmutable
	}
	in, err = normalizePackageProfile(in)
	if err != nil {
		return PackageProfile{}, err
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PackageProfile{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE package_profiles SET name=?,frontend=?,target=?,device_profile_id=?,frontend_adapter_id=?,locale=?,file_mode=?,output_slug=?,enabled=?,updated_at=? WHERE id=?`, in.Name, in.Frontend, in.Target, in.DeviceProfileID, in.FrontendAdapterID, in.Locale, in.FileMode, in.OutputSlug, boolInt(enabled), nowText(), id)
	if err != nil {
		return PackageProfile{}, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return PackageProfile{}, sql.ErrNoRows
	}
	if err = replacePackageTemplates(ctx, tx, id, in.Templates); err != nil {
		return PackageProfile{}, err
	}
	if err = tx.Commit(); err != nil {
		return PackageProfile{}, err
	}
	return s.GetPackageProfile(ctx, id)
}

func (s *Store) DeletePackageProfile(ctx context.Context, id string) error {
	profile, err := s.GetPackageProfile(ctx, id)
	if err != nil {
		return err
	}
	if profile.Builtin {
		return ErrBuiltinImmutable
	}
	var history int
	if err = s.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM package_plans WHERE profile_id=?) + (SELECT COUNT(*) FROM package_releases WHERE profile_id=?)`, id, id).Scan(&history); err != nil {
		return err
	}
	if history > 0 {
		return ErrPackageProfileHasHistory
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM package_profiles WHERE id=?`, id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) CreatePackagePlan(ctx context.Context, in NewPackagePlanRecord) (PackagePlanRecord, error) {
	if in.ProfileID == "" || in.Fingerprint == "" || in.PlanJSON == "" || in.ExpiresAt.IsZero() {
		return PackagePlanRecord{}, errors.New("profile_id, fingerprint, plan_json, and expires_at are required")
	}
	if in.ID == "" {
		in.ID = NewID()
	}
	if in.Status == "" {
		in.Status = "ready"
	}
	now := nowText()
	_, err := s.db.ExecContext(ctx, `INSERT INTO package_plans(id,profile_id,fingerprint,status,plan_json,expires_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, in.ID, in.ProfileID, in.Fingerprint, in.Status, in.PlanJSON, timeText(in.ExpiresAt), now, now)
	if err != nil {
		return PackagePlanRecord{}, err
	}
	return s.GetPackagePlan(ctx, in.ID)
}

func scanPackagePlan(scanner interface{ Scan(...any) error }) (PackagePlanRecord, error) {
	var item PackagePlanRecord
	var expires, created, updated string
	err := scanner.Scan(&item.ID, &item.ProfileID, &item.Fingerprint, &item.Status, &item.PlanJSON, &expires, &created, &updated)
	item.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expires)
	item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return item, err
}

const packagePlanColumns = `id,profile_id,fingerprint,status,plan_json,expires_at,created_at,updated_at`

func (s *Store) GetPackagePlan(ctx context.Context, id string) (PackagePlanRecord, error) {
	return scanPackagePlan(s.db.QueryRowContext(ctx, `SELECT `+packagePlanColumns+` FROM package_plans WHERE id=?`, id))
}

func (s *Store) ListPackagePlans(ctx context.Context, profileID string) ([]PackagePlanRecord, error) {
	query, args := `SELECT `+packagePlanColumns+` FROM package_plans`, []any{}
	if profileID != "" {
		query += ` WHERE profile_id=?`
		args = append(args, profileID)
	}
	query += ` ORDER BY created_at DESC,id DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PackagePlanRecord{}
	for rows.Next() {
		item, scanErr := scanPackagePlan(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) UpdatePackagePlanStatus(ctx context.Context, id, status string) (PackagePlanRecord, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE package_plans SET status=?,updated_at=? WHERE id=?`, status, nowText(), id)
	if err != nil {
		return PackagePlanRecord{}, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return PackagePlanRecord{}, sql.ErrNoRows
	}
	return s.GetPackagePlan(ctx, id)
}

func (s *Store) CreatePackageRelease(ctx context.Context, in NewPackageReleaseRecord) (PackageReleaseRecord, error) {
	if in.ProfileID == "" || in.PlanID == "" || in.OutputSlug == "" || in.ResultJSON == "" {
		return PackageReleaseRecord{}, errors.New("profile_id, plan_id, output_slug, and result_json are required")
	}
	if in.ID == "" {
		in.ID = NewID()
	}
	if in.Status == "" {
		in.Status = "succeeded"
	}
	created := nowText()
	_, err := s.db.ExecContext(ctx, `INSERT INTO package_releases(id,profile_id,plan_id,status,output_slug,result_json,created_at) VALUES(?,?,?,?,?,?,?)`, in.ID, in.ProfileID, in.PlanID, in.Status, in.OutputSlug, in.ResultJSON, created)
	if err != nil {
		return PackageReleaseRecord{}, err
	}
	return s.GetPackageRelease(ctx, in.ID)
}

// RecordPackageRelease records the immutable release audit entry and moves its
// plan to the terminal status in one transaction. Callers never observe a
// successful release with a still-ready plan, or a terminal plan without its
// corresponding audit record.
func (s *Store) RecordPackageRelease(ctx context.Context, in NewPackageReleaseRecord, planStatus string) (PackageReleaseRecord, error) {
	if in.ProfileID == "" || in.PlanID == "" || in.OutputSlug == "" || in.ResultJSON == "" {
		return PackageReleaseRecord{}, errors.New("profile_id, plan_id, output_slug, and result_json are required")
	}
	if planStatus != "built" && planStatus != "failed" {
		return PackageReleaseRecord{}, errors.New("plan status must be built or failed")
	}
	if in.ID == "" {
		in.ID = NewID()
	}
	if in.Status == "" {
		in.Status = "succeeded"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PackageReleaseRecord{}, err
	}
	defer tx.Rollback()
	created := nowText()
	result, err := tx.ExecContext(ctx, `UPDATE package_plans SET status=?,updated_at=? WHERE id=? AND profile_id=?`, planStatus, created, in.PlanID, in.ProfileID)
	if err != nil {
		return PackageReleaseRecord{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return PackageReleaseRecord{}, sql.ErrNoRows
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO package_releases(id,profile_id,plan_id,status,output_slug,result_json,created_at) VALUES(?,?,?,?,?,?,?)`, in.ID, in.ProfileID, in.PlanID, in.Status, in.OutputSlug, in.ResultJSON, created); err != nil {
		return PackageReleaseRecord{}, err
	}
	if err = tx.Commit(); err != nil {
		return PackageReleaseRecord{}, err
	}
	return s.GetPackageRelease(ctx, in.ID)
}

func scanPackageRelease(scanner interface{ Scan(...any) error }) (PackageReleaseRecord, error) {
	var item PackageReleaseRecord
	var created string
	err := scanner.Scan(&item.ID, &item.ProfileID, &item.PlanID, &item.Status, &item.OutputSlug, &item.ResultJSON, &created)
	item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return item, err
}

func (s *Store) GetPackageRelease(ctx context.Context, id string) (PackageReleaseRecord, error) {
	return scanPackageRelease(s.db.QueryRowContext(ctx, `SELECT id,profile_id,plan_id,status,output_slug,result_json,created_at FROM package_releases WHERE id=?`, id))
}

func (s *Store) ListPackageReleases(ctx context.Context, profileID string) ([]PackageReleaseRecord, error) {
	query, args := `SELECT id,profile_id,plan_id,status,output_slug,result_json,created_at FROM package_releases`, []any{}
	if profileID != "" {
		query += ` WHERE profile_id=?`
		args = append(args, profileID)
	}
	query += ` ORDER BY created_at DESC,id DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PackageReleaseRecord{}
	for rows.Next() {
		item, scanErr := scanPackageRelease(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
