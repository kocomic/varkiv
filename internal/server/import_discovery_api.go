package server

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type importSource struct {
	Format     string `json:"format"`
	Path       string `json:"path"`
	Platform   string `json:"platform"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modified_at"`
}

func (s *Server) listImportSources(w http.ResponseWriter, r *http.Request) {
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	wanted := ""
	switch format {
	case "pegasus":
		wanted = "metadata.pegasus.txt"
	case "es-de":
		wanted = "gamelist.xml"
	case "varkiv":
		wanted = "library-manifest.json"
	default:
		writeError(w, errors.New("format must be pegasus, es-de, or varkiv"))
		return
	}
	items := make([]importSource, 0)
	err := filepath.WalkDir(s.libraryRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path != s.libraryRoot && entry.IsDir() && strings.HasPrefix(entry.Name(), ".") {
			return filepath.SkipDir
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.EqualFold(entry.Name(), wanted) {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		rel, relErr := filepath.Rel(s.libraryRoot, path)
		if relErr != nil {
			return relErr
		}
		platform := ""
		if format != "varkiv" {
			platform = filepath.Base(filepath.Dir(path))
			registry, registryErr := s.store.PlatformRegistry(r.Context())
			if registryErr != nil {
				return registryErr
			}
			if preset, ok := registry.ResolveCollectionDirectory(platform); ok {
				platform = preset.ID
			} else {
				platform = strings.ToLower(platform)
			}
		}
		items = append(items, importSource{Format: format, Path: filepath.ToSlash(rel), Platform: platform, Size: info.Size(), ModifiedAt: info.ModTime().UTC().Format("2006-01-02T15:04:05Z")})
		return nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	if !isV1(r) && len(items) > 200 {
		items = items[:200]
	}
	writeCollection(w, r, items)
}
