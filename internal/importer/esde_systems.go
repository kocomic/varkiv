package importer

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"varkiv/internal/catalog"
	"varkiv/internal/platforms"
)

const maxESDESystemsSize = 4 << 20

type esdeSystemList struct {
	Systems []esdeSystem `xml:"system"`
}

type esdeSystem struct {
	Name     string        `xml:"name"`
	Commands []esdeCommand `xml:"command"`
}

type esdeCommand struct {
	Label string `xml:"label,attr"`
	Value string `xml:",chardata"`
}

// readExplicitLibraryFile accepts only an exact regular file explicitly
// configured below libraryRoot. It rejects a symlink at the selected path,
// verifies the resolved path remains under the resolved root, and enforces a
// hard byte limit before parsing.
func readExplicitLibraryFile(libraryRoot, selectedPath string, maxSize int64) ([]byte, string, error) {
	selectedPath = strings.TrimSpace(selectedPath)
	if selectedPath == "" {
		return nil, "", nil
	}
	root, err := filepath.Abs(libraryRoot)
	if err != nil {
		return nil, "", err
	}
	selected := filepath.FromSlash(selectedPath)
	if !filepath.IsAbs(selected) {
		selected = filepath.Join(root, selected)
	}
	selected, err = filepath.Abs(filepath.Clean(selected))
	if err != nil {
		return nil, "", err
	}
	if !pathInside(root, selected) {
		return nil, "", errors.New("runtime metadata must stay inside library root")
	}
	info, err := os.Lstat(selected)
	if err != nil {
		return nil, "", err
	}
	if !info.Mode().IsRegular() {
		return nil, "", errors.New("runtime metadata must be an exact regular file, not a directory or symlink")
	}
	if info.Size() > maxSize {
		return nil, "", fmt.Errorf("runtime metadata exceeds %d bytes", maxSize)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, "", err
	}
	resolvedSelected, err := filepath.EvalSymlinks(selected)
	if err != nil {
		return nil, "", err
	}
	if !pathInside(resolvedRoot, resolvedSelected) {
		return nil, "", errors.New("runtime metadata resolves outside library root")
	}
	file, err := os.Open(selected)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSize+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > maxSize {
		return nil, "", fmt.Errorf("runtime metadata exceeds %d bytes", maxSize)
	}
	rel, err := filepath.Rel(root, selected)
	if err != nil {
		return nil, "", err
	}
	return data, filepath.ToSlash(rel), nil
}

func samePlatform(left, right string, registry platforms.Registry) bool {
	canonical := func(value string) string {
		if item, ok := registry.Resolve(value); ok {
			return item.ID
		}
		return strings.ToLower(strings.TrimSpace(value))
	}
	return canonical(left) != "" && canonical(left) == canonical(right)
}

func attachESDESystemHints(libraryRoot, runtimePath, platform string, games []catalog.ImportedGame, registry platforms.Registry) ([]catalog.ImportedGame, error) {
	data, sourceRef, err := readExplicitLibraryFile(libraryRoot, runtimePath, maxESDESystemsSize)
	if err != nil || len(data) == 0 {
		return games, err
	}
	var list esdeSystemList
	if err = xml.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("parse explicit ES-DE runtime metadata: %w", err)
	}
	commands := make([]string, 0)
	seen := map[string]bool{}
	for _, system := range list.Systems {
		if !samePlatform(system.Name, platform, registry) {
			continue
		}
		for _, command := range system.Commands {
			raw := strings.TrimSpace(command.Value)
			if raw == "" || len(raw) > 8192 || strings.ContainsRune(raw, '\x00') || seen[raw] {
				continue
			}
			seen[raw] = true
			commands = append(commands, raw)
		}
	}
	for gameIndex := range games {
		for _, raw := range commands {
			games[gameIndex].RuntimeHints = append(games[gameIndex].RuntimeHints, catalog.NewRuntimeImportHint{
				SourceKind: "esde-system", SourceFormat: "es-de-es_systems-v1", RawCommand: raw, SourceRef: sourceRef,
			})
		}
	}
	return games, nil
}
