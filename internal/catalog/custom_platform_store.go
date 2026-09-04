package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"varkiv/internal/platforms"
)

type CustomPlatform struct {
	ID                 string              `json:"id"`
	Name               string              `json:"name"`
	NameZH             string              `json:"name_zh"`
	Vendor             string              `json:"vendor"`
	Category           string              `json:"category"`
	Aliases            []string            `json:"aliases"`
	Extensions         []string            `json:"extensions"`
	ESDESystems        []string            `json:"esde_systems"`
	BIOS               string              `json:"bios"`
	Runtime            string              `json:"runtime"`
	SuggestedEmulators map[string][]string `json:"suggested_emulators"`
	Builtin            bool                `json:"builtin"`
	Enabled            bool                `json:"enabled"`
	CreatedAt          time.Time           `json:"created_at"`
	UpdatedAt          time.Time           `json:"updated_at"`
}

type NewCustomPlatform struct {
	ID                 string              `json:"id,omitempty"`
	Name               string              `json:"name"`
	NameZH             string              `json:"name_zh,omitempty"`
	Vendor             string              `json:"vendor,omitempty"`
	Category           string              `json:"category"`
	Aliases            []string            `json:"aliases,omitempty"`
	Extensions         []string            `json:"extensions,omitempty"`
	ESDESystems        []string            `json:"esde_systems,omitempty"`
	BIOS               string              `json:"bios,omitempty"`
	Runtime            string              `json:"runtime,omitempty"`
	SuggestedEmulators map[string][]string `json:"suggested_emulators,omitempty"`
	Enabled            *bool               `json:"enabled,omitempty"`
}

func (item CustomPlatform) Platform() platforms.Platform {
	return platforms.Platform{
		ID: item.ID, Name: item.Name, NameZH: item.NameZH, Vendor: item.Vendor, Category: item.Category,
		Aliases: item.Aliases, Extensions: item.Extensions, ESDESystems: item.ESDESystems, BIOS: item.BIOS,
		Runtime: item.Runtime, SuggestedEmulators: item.SuggestedEmulators, Builtin: false, Enabled: item.Enabled,
	}
}

func (item CustomPlatform) PortableDefinition() NewCustomPlatform {
	return NewCustomPlatform{
		ID: item.ID, Name: item.Name, NameZH: item.NameZH, Vendor: item.Vendor, Category: item.Category,
		Aliases: append([]string{}, item.Aliases...), Extensions: append([]string{}, item.Extensions...),
		ESDESystems: append([]string{}, item.ESDESystems...), BIOS: item.BIOS, Runtime: item.Runtime,
		SuggestedEmulators: item.SuggestedEmulators,
	}
}

func normalizeCustomPlatform(in NewCustomPlatform, id string) (CustomPlatform, error) {
	if strings.TrimSpace(id) == "" {
		id = in.ID
	}
	item := CustomPlatform{
		ID: strings.ToLower(strings.TrimSpace(id)), Name: strings.TrimSpace(in.Name), NameZH: strings.TrimSpace(in.NameZH),
		Vendor: strings.TrimSpace(in.Vendor), Category: strings.ToLower(strings.TrimSpace(in.Category)),
		Aliases: normalizePlatformValues(in.Aliases, false), Extensions: normalizePlatformValues(in.Extensions, true),
		ESDESystems: normalizePlatformValues(in.ESDESystems, false), BIOS: strings.ToLower(strings.TrimSpace(in.BIOS)),
		Runtime: strings.ToLower(strings.TrimSpace(in.Runtime)), SuggestedEmulators: map[string][]string{}, Enabled: true,
	}
	if item.Vendor == "" {
		item.Vendor = "Custom"
	}
	if item.BIOS == "" {
		item.BIOS = "varies"
	}
	if item.Runtime == "" {
		item.Runtime = "native"
	}
	if in.Enabled != nil {
		item.Enabled = *in.Enabled
	}
	for target, names := range in.SuggestedEmulators {
		normalizedNames := make([]string, 0, len(names))
		for _, name := range names {
			if name = strings.TrimSpace(name); name != "" {
				normalizedNames = append(normalizedNames, name)
			}
		}
		item.SuggestedEmulators[strings.ToLower(strings.TrimSpace(target))] = normalizedNames
	}
	if err := platforms.ValidateCustom(item.Platform()); err != nil {
		return CustomPlatform{}, err
	}
	return item, nil
}

// NormalizeCustomPlatform applies the same defaults and validation used by
// persisted custom platforms without writing anything. Import previews use it
// to reject malformed portable definitions before a commit is attempted.
func NormalizeCustomPlatform(in NewCustomPlatform) (CustomPlatform, error) {
	return normalizeCustomPlatform(in, "")
}

func customPlatformDefinitionEqual(left, right CustomPlatform) bool {
	left.CreatedAt, left.UpdatedAt, left.Builtin, left.Enabled = time.Time{}, time.Time{}, false, false
	right.CreatedAt, right.UpdatedAt, right.Builtin, right.Enabled = time.Time{}, time.Time{}, false, false
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

// ValidateCustomPlatformImports returns normalized definitions and checks them
// against built-ins, existing custom definitions, and every registry key. An
// existing definition is reusable only when its portable fields are identical
// and it remains enabled; imports never overwrite or re-enable local settings.
func (s *Store) ValidateCustomPlatformImports(ctx context.Context, definitions []NewCustomPlatform) ([]CustomPlatform, error) {
	if len(definitions) > 256 {
		return nil, errors.New("portable manifest exceeds 256 custom platforms")
	}
	existing, err := s.ListCustomPlatforms(ctx, true)
	if err != nil {
		return nil, err
	}
	existingByID := make(map[string]CustomPlatform, len(existing))
	registryItems := platforms.All()
	for _, item := range existing {
		existingByID[item.ID] = item
		if item.Enabled {
			registryItems = append(registryItems, item.Platform())
		}
	}
	normalized := make([]CustomPlatform, 0, len(definitions))
	seen := map[string]bool{}
	for _, definition := range definitions {
		if definition.Enabled != nil && !*definition.Enabled {
			return nil, errors.New("portable custom platforms must be enabled")
		}
		item, normalizeErr := normalizeCustomPlatform(definition, "")
		if normalizeErr != nil {
			return nil, normalizeErr
		}
		if seen[item.ID] {
			return nil, fmt.Errorf("portable manifest repeats custom platform %q", item.ID)
		}
		seen[item.ID] = true
		if _, builtin := platforms.Resolve(item.ID); builtin {
			return nil, fmt.Errorf("%w: %s is a built-in platform", ErrPlatformDefinitionConflict, item.ID)
		}
		if current, ok := existingByID[item.ID]; ok {
			if !customPlatformDefinitionEqual(current, item) {
				return nil, fmt.Errorf("%w: %s", ErrPlatformDefinitionConflict, item.ID)
			}
			if !current.Enabled {
				return nil, fmt.Errorf("%w: %s", ErrPlatformDefinitionDisabled, item.ID)
			}
			normalized = append(normalized, item)
			continue
		}
		registryItems = append(registryItems, item.Platform())
		normalized = append(normalized, item)
	}
	if _, err = platforms.NewRegistry(registryItems); err != nil {
		return nil, err
	}
	return normalized, nil
}

// ImportGamesAndCustomPlatformsAtomic creates any missing portable platform
// definitions and all selected games in one transaction. Existing compatible
// definitions are reused. A duplicate game or failed platform insert rolls the
// complete metadata batch back.
func (s *Store) ImportGamesAndCustomPlatformsAtomic(ctx context.Context, games []ImportedGame, definitions []NewCustomPlatform) error {
	if len(games) == 0 {
		return errors.New("import batch must contain at least one game")
	}
	normalized, err := s.ValidateCustomPlatformImports(ctx, definitions)
	if err != nil {
		return err
	}
	portableRuntime, err := MergePortableRuntimeCatalogs(games)
	if err != nil {
		return err
	}
	normalizedRuntime, err := s.ValidatePortableRuntimeCatalogImports(ctx, portableRuntime)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, item := range normalized {
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM custom_platforms WHERE id=?`, item.ID).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		aliases, _ := json.Marshal(item.Aliases)
		extensions, _ := json.Marshal(item.Extensions)
		esdeSystems, _ := json.Marshal(item.ESDESystems)
		emulators, _ := json.Marshal(item.SuggestedEmulators)
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err = tx.ExecContext(ctx, `INSERT INTO custom_platforms(
			id,name,name_zh,vendor,category,aliases_json,extensions_json,esde_systems_json,bios,runtime,suggested_emulators_json,enabled,created_at,updated_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, item.ID, item.Name, item.NameZH, item.Vendor, item.Category, string(aliases), string(extensions), string(esdeSystems), item.BIOS, item.Runtime, string(emulators), 1, now, now); err != nil {
			return err
		}
	}
	if err = insertPortableRuntimeCatalogTx(ctx, tx, normalizedRuntime); err != nil {
		return err
	}
	for _, game := range games {
		_, created, importErr := importGameTx(ctx, tx, game)
		if importErr != nil {
			return importErr
		}
		if !created {
			return ErrImportDuplicate
		}
	}
	return tx.Commit()
}

func normalizePlatformValues(values []string, extension bool) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if extension || value != "" {
			value = strings.ToLower(value)
		}
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

func (s *Store) PlatformRegistry(ctx context.Context) (platforms.Registry, error) {
	custom, err := s.ListCustomPlatforms(ctx, false)
	if err != nil {
		return platforms.Registry{}, err
	}
	items := platforms.All()
	for _, item := range custom {
		items = append(items, item.Platform())
	}
	return platforms.NewRegistry(items)
}

func (s *Store) CreateCustomPlatform(ctx context.Context, in NewCustomPlatform) (CustomPlatform, error) {
	item, err := normalizeCustomPlatform(in, "")
	if err != nil {
		return CustomPlatform{}, err
	}
	if _, builtin := platforms.Resolve(item.ID); builtin {
		return CustomPlatform{}, ErrBuiltinImmutable
	}
	if err = s.validateCustomPlatformRegistry(ctx, item, ""); err != nil {
		return CustomPlatform{}, err
	}
	now := time.Now().UTC()
	aliases, _ := json.Marshal(item.Aliases)
	extensions, _ := json.Marshal(item.Extensions)
	esdeSystems, _ := json.Marshal(item.ESDESystems)
	emulators, _ := json.Marshal(item.SuggestedEmulators)
	_, err = s.db.ExecContext(ctx, `INSERT INTO custom_platforms(
		id,name,name_zh,vendor,category,aliases_json,extensions_json,esde_systems_json,bios,runtime,suggested_emulators_json,enabled,created_at,updated_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, item.ID, item.Name, item.NameZH, item.Vendor, item.Category, string(aliases), string(extensions), string(esdeSystems), item.BIOS, item.Runtime, string(emulators), boolInt(item.Enabled), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return CustomPlatform{}, err
	}
	return s.GetCustomPlatform(ctx, item.ID)
}

func (s *Store) validateCustomPlatformRegistry(ctx context.Context, candidate CustomPlatform, replacedID string) error {
	items := platforms.All()
	custom, err := s.ListCustomPlatforms(ctx, true)
	if err != nil {
		return err
	}
	for _, item := range custom {
		if item.ID != replacedID && item.Enabled {
			items = append(items, item.Platform())
		}
	}
	if candidate.Enabled {
		items = append(items, candidate.Platform())
	}
	_, err = platforms.NewRegistry(items)
	return err
}

func (s *Store) GetCustomPlatform(ctx context.Context, id string) (CustomPlatform, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,name,name_zh,vendor,category,aliases_json,extensions_json,esde_systems_json,bios,runtime,suggested_emulators_json,enabled,created_at,updated_at FROM custom_platforms WHERE id=?`, strings.ToLower(strings.TrimSpace(id)))
	return scanCustomPlatform(row)
}

func (s *Store) ListCustomPlatforms(ctx context.Context, includeDisabled bool) ([]CustomPlatform, error) {
	query := `SELECT id,name,name_zh,vendor,category,aliases_json,extensions_json,esde_systems_json,bios,runtime,suggested_emulators_json,enabled,created_at,updated_at FROM custom_platforms`
	if !includeDisabled {
		query += ` WHERE enabled=1`
	}
	query += ` ORDER BY vendor,name,id`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []CustomPlatform{}
	for rows.Next() {
		item, scanErr := scanCustomPlatform(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) UpdateCustomPlatform(ctx context.Context, id string, in NewCustomPlatform) (CustomPlatform, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	if _, err := s.GetCustomPlatform(ctx, id); err != nil {
		return CustomPlatform{}, err
	}
	if strings.TrimSpace(in.ID) != "" && strings.ToLower(strings.TrimSpace(in.ID)) != id {
		return CustomPlatform{}, errors.New("custom platform id cannot be changed")
	}
	item, err := normalizeCustomPlatform(in, id)
	if err != nil {
		return CustomPlatform{}, err
	}
	if err = s.validateCustomPlatformRegistry(ctx, item, id); err != nil {
		return CustomPlatform{}, err
	}
	aliases, _ := json.Marshal(item.Aliases)
	extensions, _ := json.Marshal(item.Extensions)
	esdeSystems, _ := json.Marshal(item.ESDESystems)
	emulators, _ := json.Marshal(item.SuggestedEmulators)
	result, err := s.db.ExecContext(ctx, `UPDATE custom_platforms SET name=?,name_zh=?,vendor=?,category=?,aliases_json=?,extensions_json=?,esde_systems_json=?,bios=?,runtime=?,suggested_emulators_json=?,enabled=?,updated_at=? WHERE id=?`, item.Name, item.NameZH, item.Vendor, item.Category, string(aliases), string(extensions), string(esdeSystems), item.BIOS, item.Runtime, string(emulators), boolInt(item.Enabled), time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return CustomPlatform{}, err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return CustomPlatform{}, sql.ErrNoRows
	}
	return s.GetCustomPlatform(ctx, id)
}

func (s *Store) DeleteCustomPlatform(ctx context.Context, id string) error {
	id = strings.ToLower(strings.TrimSpace(id))
	item, err := s.GetCustomPlatform(ctx, id)
	if err != nil {
		return err
	}
	keys := append([]string{id}, item.Aliases...)
	keys = append(keys, item.ESDESystems...)
	for _, key := range keys {
		for _, query := range []string{
			`SELECT COUNT(*) FROM games WHERE lower(trim(platform))=?`,
			`SELECT COUNT(*) FROM library_sources WHERE lower(trim(platform))=?`,
			`SELECT COUNT(*) FROM core_mappings WHERE lower(trim(platform_id))=?`,
			`SELECT COUNT(*) FROM inventory_items WHERE lower(trim(platform_id))=?`,
		} {
			var count int
			if err := s.db.QueryRowContext(ctx, query, key).Scan(&count); err != nil {
				return err
			}
			if count > 0 {
				return ErrCustomPlatformInUse
			}
		}
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM custom_platforms WHERE id=?`, id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

type rowScanner interface{ Scan(...any) error }

func scanCustomPlatform(row rowScanner) (CustomPlatform, error) {
	var item CustomPlatform
	var aliases, extensions, esdeSystems, emulators, created, updated string
	var enabled int
	if err := row.Scan(&item.ID, &item.Name, &item.NameZH, &item.Vendor, &item.Category, &aliases, &extensions, &esdeSystems, &item.BIOS, &item.Runtime, &emulators, &enabled, &created, &updated); err != nil {
		return CustomPlatform{}, err
	}
	if err := json.Unmarshal([]byte(aliases), &item.Aliases); err != nil {
		return CustomPlatform{}, fmt.Errorf("decode custom platform aliases: %w", err)
	}
	if err := json.Unmarshal([]byte(extensions), &item.Extensions); err != nil {
		return CustomPlatform{}, fmt.Errorf("decode custom platform extensions: %w", err)
	}
	if err := json.Unmarshal([]byte(esdeSystems), &item.ESDESystems); err != nil {
		return CustomPlatform{}, fmt.Errorf("decode custom platform ES-DE systems: %w", err)
	}
	if err := json.Unmarshal([]byte(emulators), &item.SuggestedEmulators); err != nil {
		return CustomPlatform{}, fmt.Errorf("decode custom platform emulator suggestions: %w", err)
	}
	item.Enabled = enabled == 1
	item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return item, nil
}
