package platforms

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type Platform struct {
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
}

type Registry struct {
	items  []Platform
	lookup map[string]int
}

var ErrRegistryConflict = errors.New("platform registry key conflict")

//go:embed platforms.json
var data []byte

var builtin Registry

func init() {
	var items []Platform
	if err := json.Unmarshal(data, &items); err != nil {
		panic("invalid embedded platform registry: " + err.Error())
	}
	for index := range items {
		items[index].Builtin = true
		items[index].Enabled = true
	}
	var err error
	builtin, err = NewRegistry(items)
	if err != nil {
		panic("invalid embedded platform registry: " + err.Error())
	}
}

func All() []Platform {
	return builtin.All()
}

func Resolve(value string) (Platform, bool) {
	return builtin.Resolve(value)
}

func NewRegistry(items []Platform) (Registry, error) {
	result := Registry{items: make([]Platform, 0, len(items)), lookup: make(map[string]int)}
	for _, item := range items {
		item = clonePlatform(item)
		if !item.Enabled && !item.Builtin {
			continue
		}
		if item.Builtin {
			item.Enabled = true
		} else if err := ValidateCustom(item); err != nil {
			return Registry{}, fmt.Errorf("custom platform %q: %w", item.ID, err)
		}
		result.items = append(result.items, item)
	}
	sort.Slice(result.items, func(i, j int) bool {
		if result.items[i].Vendor == result.items[j].Vendor {
			return result.items[i].Name < result.items[j].Name
		}
		return result.items[i].Vendor < result.items[j].Vendor
	})
	for index, item := range result.items {
		keys := append([]string{item.ID}, item.Aliases...)
		keys = append(keys, item.ESDESystems...)
		for _, raw := range keys {
			key := normalizeKey(raw)
			if key == "" {
				continue
			}
			if existing, ok := result.lookup[key]; ok && result.items[existing].ID != item.ID {
				return Registry{}, fmt.Errorf("%w: lookup key %q is shared by %q and %q", ErrRegistryConflict, key, result.items[existing].ID, item.ID)
			}
			result.lookup[key] = index
		}
	}
	return result, nil
}

func (r Registry) All() []Platform {
	result := make([]Platform, len(r.items))
	for index := range r.items {
		result[index] = clonePlatform(r.items[index])
	}
	return result
}

func (r Registry) Resolve(value string) (Platform, bool) {
	index, ok := r.lookup[normalizeKey(value)]
	if !ok {
		return Platform{}, false
	}
	return clonePlatform(r.items[index]), true
}

// ResolveCollectionDirectory maps frontend collection folders to a hardware
// platform without turning curated sets such as "FC hack" into new platforms.
func ResolveCollectionDirectory(value string) (Platform, bool) {
	return builtin.ResolveCollectionDirectory(value)
}

func (r Registry) ResolveCollectionDirectory(value string) (Platform, bool) {
	if platform, ok := r.Resolve(value); ok {
		return platform, true
	}
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.NewReplacer("_", " ", "-", " ").Replace(normalized)
	normalized = strings.Join(strings.Fields(normalized), " ")
	for _, prefix := range []string{"fbneo", "mame", "hbmame", "light gun", "teknoparrot", "model2", "model3"} {
		if normalized == prefix || strings.HasPrefix(normalized, prefix+" ") {
			return r.Resolve("arcade")
		}
	}
	for _, marker := range []string{" hack", " plus", " msu1", " hd", " 18x"} {
		if index := strings.Index(normalized, marker); index > 0 {
			if platform, ok := r.Resolve(strings.TrimSpace(normalized[:index])); ok {
				return platform, true
			}
		}
	}
	return Platform{}, false
}

var customID = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

func ValidateCustom(item Platform) error {
	if !customID.MatchString(item.ID) || item.ID == "." || item.ID == ".." {
		return errors.New("id must be a lowercase portable slug of 1 to 64 characters")
	}
	if strings.TrimSpace(item.Name) == "" || len(item.Name) > 120 || len(item.NameZH) > 120 || len(item.Vendor) > 120 {
		return errors.New("name is required and names and vendor must be at most 120 characters")
	}
	if !oneOf(item.Category, "console", "handheld", "arcade", "computer") {
		return errors.New("category must be console, handheld, arcade, or computer")
	}
	if !oneOf(item.BIOS, "none", "optional", "required", "varies") {
		return errors.New("bios must be none, optional, required, or varies")
	}
	if !oneOf(item.Runtime, "web", "web_experimental", "native") {
		return errors.New("runtime must be web, web_experimental, or native")
	}
	if len(item.Aliases) > 64 || len(item.Extensions) > 64 || len(item.ESDESystems) > 64 {
		return errors.New("aliases, extensions, and ES-DE systems are limited to 64 entries each")
	}
	for _, values := range [][]string{item.Aliases, item.ESDESystems} {
		seen := map[string]bool{}
		for _, value := range values {
			key := normalizeKey(value)
			if key == "" || len(key) > 64 || !customID.MatchString(key) || seen[key] {
				return errors.New("aliases and ES-DE systems must be unique lowercase portable slugs")
			}
			seen[key] = true
		}
	}
	seen := map[string]bool{}
	for _, value := range item.Extensions {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "directory" && (!strings.HasPrefix(value, ".") || len(value) < 2 || len(value) > 16 || strings.ContainsAny(value, `/\\\x00`)) {
			return errors.New("extensions must be short dot-prefixed suffixes or directory")
		}
		if seen[value] {
			return errors.New("extensions must be unique")
		}
		seen[value] = true
	}
	for target, names := range item.SuggestedEmulators {
		if !oneOf(target, "windows", "android", "handheld_linux") || len(names) > 16 {
			return errors.New("suggested emulator targets must be windows, android, or handheld_linux and contain at most 16 names")
		}
		for _, name := range names {
			if strings.TrimSpace(name) == "" || len(name) > 120 {
				return errors.New("suggested emulator names must be non-empty and at most 120 characters")
			}
		}
	}
	return nil
}

func clonePlatform(item Platform) Platform {
	item.Aliases = append([]string{}, item.Aliases...)
	item.Extensions = append([]string{}, item.Extensions...)
	item.ESDESystems = append([]string{}, item.ESDESystems...)
	suggested := make(map[string][]string, len(item.SuggestedEmulators))
	for key, values := range item.SuggestedEmulators {
		suggested[key] = append([]string{}, values...)
	}
	item.SuggestedEmulators = suggested
	return item
}

func normalizeKey(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

func oneOf(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}
